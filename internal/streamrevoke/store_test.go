package streamrevoke

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/redis/go-redis/v9"
)

// errFakeDurable is the error fakeDurable.ListActive returns while failNext > 0.
var errFakeDurable = errors.New("fake durable failure")

// newMemStore returns a memory-only Store (no Redis, no bus, no durable mirror).
func newMemStore() *Store {
	return New(Options{})
}

type cutDeadlineWriter struct {
	mu        sync.Mutex
	header    http.Header
	deadlines []time.Time
}

func (w *cutDeadlineWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *cutDeadlineWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *cutDeadlineWriter) WriteHeader(int)             {}
func (w *cutDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func (w *cutDeadlineWriter) deadlineCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.deadlines)
}

func TestImmediateCutLatchesBeforeRollingWriterConstruction(t *testing.T) {
	s := newMemStore()
	if err := s.RevokeSession(context.Background(), "already-cut", "test"); err != nil {
		t.Fatal(err)
	}
	latch := &httpstream.CutLatch{}
	ctx := httpstream.WithCutLatch(context.Background(), latch)
	base := &cutDeadlineWriter{}

	stop := s.WatchAndCutContext(ctx, base, "already-cut", 1, time.Now())
	defer stop()
	if !latch.IsCut() {
		t.Fatal("immediate revocation did not latch the terminal cut")
	}
	if got := base.deadlineCount(); got != 1 {
		t.Fatalf("cut deadlines = %d, want 1", got)
	}

	rolling := httpstream.NewRollingDeadlineWriterCtx(ctx, base)
	if _, err := rolling.Write([]byte("must not rearm")); err != nil {
		t.Fatal(err)
	}
	if got := base.deadlineCount(); got != 1 {
		t.Fatalf("deadlines after rolling writer = %d, want cut only", got)
	}
}

func TestWatchAndCutKeepsReapplyingUntilStopped(t *testing.T) {
	const watchInterval = 5 * time.Millisecond
	s := New(Options{WatchInterval: watchInterval})
	if err := s.RevokeSession(context.Background(), "keep-cut", "test"); err != nil {
		t.Fatal(err)
	}
	base := &cutDeadlineWriter{}
	stop := s.WatchAndCutContext(context.Background(), base, "keep-cut", 1, time.Now())

	deadline := time.Now().Add(time.Second)
	for base.deadlineCount() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := base.deadlineCount(); got < 3 {
		stop()
		t.Fatalf("deadline applications = %d, want immediate cut plus ticker reapplications", got)
	}
	stop()
	stoppedAt := base.deadlineCount()
	time.Sleep(3 * watchInterval)
	if got := base.deadlineCount(); got != stoppedAt {
		t.Fatalf("deadline applications after stop = %d, want %d", got, stoppedAt)
	}
}

func TestWatchAndCutLogsUnsupportedDeadlineOnlyOnce(t *testing.T) {
	const watchInterval = 5 * time.Millisecond
	s := New(Options{WatchInterval: watchInterval})
	if err := s.RevokeSession(context.Background(), "unsupported-cut", "test"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	stop := s.WatchAndCutContext(context.Background(), httptest.NewRecorder(), "unsupported-cut", 1, time.Now())
	time.Sleep(3 * watchInterval)
	stop()

	const message = "stream cut could not set write deadline; in-flight pour continues until its next request"
	if got := strings.Count(logs.String(), message); got != 1 {
		t.Fatalf("warning count = %d, want 1; logs: %s", got, logs.String())
	}
}

// fakeDurable is an in-memory DurableStore double for exercising the durable
// wiring (Upsert on revoke, warm on start, Prune on the poll tick) without a
// live Postgres. StartSync's warm is synchronous, but the poll goroutine calls
// ListActive/Prune concurrently with the test's own reads, so every field is
// mutex-guarded. failNext, when > 0, makes ListActive return an error that many
// times before succeeding, to exercise the bounded boot-warm retry.
type fakeDurable struct {
	mu            sync.Mutex
	rows          map[Key]Revocation
	upserts       int
	prunes        int
	deletes       int
	failNext      int
	lists         int
	upsertStarted chan struct{}
	unblockUpsert chan struct{}
}

type failingBus struct{}

func (failingBus) Publish(context.Context, string, cache.Event) error {
	return errors.New("publish failed")
}
func (failingBus) Subscribe(context.Context, string, cache.EventHandler) error { return nil }
func (failingBus) Close() error                                                { return nil }

func (f *fakeDurable) Delete(_ context.Context, kind Kind, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, Key{Kind: kind, ID: id})
	f.deletes++
	return nil
}

func newFakeDurable() *fakeDurable {
	return &fakeDurable{rows: make(map[Key]Revocation)}
}

func (f *fakeDurable) Upsert(_ context.Context, r Revocation) error {
	if f.upsertStarted != nil {
		close(f.upsertStarted)
		<-f.unblockUpsert
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[r.key()] = r
	f.upserts++
	return nil
}

func (f *fakeDurable) ListActive(_ context.Context) ([]Revocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	if f.failNext > 0 {
		f.failNext--
		return nil, errFakeDurable
	}
	now := time.Now()
	out := make([]Revocation, 0, len(f.rows))
	for _, r := range f.rows {
		if !r.expired(now) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDurable) Prune(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for k, r := range f.rows {
		if r.expired(now) {
			delete(f.rows, k)
		}
	}
	f.prunes++
	return nil
}

func (f *fakeDurable) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upserts
}

func (f *fakeDurable) pruneCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prunes
}

func (f *fakeDurable) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

// TestRevokeMirrorsToDurable asserts Revoke writes through to the durable store.
func TestRevokeMirrorsToDurable(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDurable()
	s := New(Options{Durable: fake})

	if err := s.RevokeSession(ctx, "sess-1", "abuse"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if got := fake.upsertCount(); got != 1 {
		t.Fatalf("durable upserts = %d, want 1", got)
	}
	// Re-revoking the same session upserts the same row (bounded growth).
	if err := s.RevokeSession(ctx, "sess-1", "abuse again"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	fake.mu.Lock()
	rows := len(fake.rows)
	fake.mu.Unlock()
	if rows != 1 {
		t.Fatalf("durable rows after re-revoke = %d, want 1", rows)
	}
}

// TestStartSyncWarmsFromDurable simulates a restart: a fresh Store must
// repopulate its hot-path map from the durable mirror, and skip expired rows.
func TestStartSyncWarmsFromDurable(t *testing.T) {
	// Cancel the poll goroutine StartSync spawns so it does not leak past the test.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	fake := newFakeDurable()
	// Seed the durable store as if a previous process had written these.
	active := Revocation{Kind: KindSession, ID: "sess-live", Reason: "x", RevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	expired := Revocation{Kind: KindUser, ID: "9", Reason: "x", RevokedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour)}
	_ = fake.Upsert(ctx, active)
	_ = fake.Upsert(ctx, expired)

	s := New(Options{Durable: fake})
	s.StartSync(ctx)

	if !s.IsRevoked("sess-live", 0, time.Time{}) {
		t.Fatalf("expected sess-live to be revoked after warm from durable")
	}
	// startedAt predates the (expired) user revocation, so only expiry decides.
	if s.IsRevoked("whatever", 9, time.Now().Add(-3*time.Hour)) {
		t.Fatalf("expected expired user 9 not to be warmed into the cache")
	}
}

// TestWarmFromDurableRetries proves the bounded boot-warm retry (Fix 2): a
// transient ListActive failure at startup is retried rather than leaving the
// kill list empty until the first poll tick.
func TestWarmFromDurableRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	fake := newFakeDurable()
	live := Revocation{Kind: KindSession, ID: "sess-retry", Reason: "x", RevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	_ = fake.Upsert(ctx, live)
	// Fail the first two ListActive calls; the third (within the 3-attempt
	// bound) succeeds.
	fake.mu.Lock()
	fake.failNext = 2
	fake.lists = 0
	fake.mu.Unlock()

	s := New(Options{Durable: fake})
	s.StartSync(ctx)

	if !s.IsRevoked("sess-retry", 0, time.Time{}) {
		t.Fatalf("expected sess-retry warmed into cache after retried ListActive")
	}
	if got := fake.listCount(); got != 3 {
		t.Fatalf("ListActive calls = %d, want 3 (two failures + one success)", got)
	}
}

// TestWarmFromDurableGivesUpBounded proves the retry is bounded: when every
// attempt fails, warmFromDurable stops after the fixed attempt budget instead
// of blocking startup.
func TestWarmFromDurableGivesUpBounded(t *testing.T) {
	fake := newFakeDurable()
	fake.mu.Lock()
	fake.failNext = 100 // always fail
	fake.mu.Unlock()

	s := New(Options{Durable: fake})
	if _, err := s.warmFromDurable(context.Background()); err == nil {
		t.Fatalf("expected warmFromDurable to return an error when all attempts fail")
	}
	if got := fake.listCount(); got != 3 {
		t.Fatalf("ListActive attempts = %d, want 3 (bounded)", got)
	}
}

// TestMaintainPrunesAndReWarmsFromDurable asserts the poll-tick body calls the
// durable Prune and re-warms live rows into the hot-path map (self-healing a
// Redis flush that cleared the local cache).
func TestMaintainPrunesAndReWarmsFromDurable(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDurable()
	live := Revocation{Kind: KindSession, ID: "sess-heal", Reason: "x", RevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	dead := Revocation{Kind: KindSession, ID: "sess-dead", Reason: "x", RevokedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour)}
	_ = fake.Upsert(ctx, live)
	_ = fake.Upsert(ctx, dead)

	s := New(Options{Durable: fake})
	// Local cache starts empty (as after a Redis flush + local reconcile miss).
	if s.IsRevoked("sess-heal", 0, time.Time{}) {
		t.Fatalf("did not expect sess-heal in a fresh empty cache")
	}

	s.maintain(ctx)

	if got := fake.pruneCount(); got != 1 {
		t.Fatalf("durable prune calls = %d, want 1", got)
	}
	if !s.IsRevoked("sess-heal", 0, time.Time{}) {
		t.Fatalf("expected sess-heal re-warmed into cache by maintain")
	}
	fake.mu.Lock()
	_, deadStillThere := fake.rows[dead.key()]
	fake.mu.Unlock()
	if deadStillThere {
		t.Fatalf("expected expired durable row to be pruned")
	}
}

func TestMaintainDurableSelfHealDoesNotRaceUnrevoke(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDurable()
	fake.upsertStarted = make(chan struct{})
	fake.unblockUpsert = make(chan struct{})
	s := New(Options{Durable: fake})
	key := Key{Kind: KindSession, ID: "sess-maintain-unrevoke"}
	s.applyLocal(Revocation{
		Kind:      key.Kind,
		ID:        key.ID,
		RevokedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	})

	maintainDone := make(chan struct{})
	go func() {
		s.maintain(ctx)
		close(maintainDone)
	}()
	<-fake.upsertStarted

	unrevokeDone := make(chan error, 1)
	go func() {
		unrevokeDone <- s.Unrevoke(ctx, key)
	}()

	select {
	case err := <-unrevokeDone:
		t.Fatalf("Unrevoke completed before maintain released the operation lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(fake.unblockUpsert)
	<-maintainDone
	if err := <-unrevokeDone; err != nil {
		t.Fatalf("Unrevoke: %v", err)
	}

	fake.mu.Lock()
	_, exists := fake.rows[key]
	fake.mu.Unlock()
	if exists {
		t.Fatal("durable self-heal resurrected the unrevoked row")
	}
}

func TestUnrevokeTombstoneBlocksReconcileAndNewerRevokeClearsIt(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	key := Key{Kind: KindSession, ID: "sess-unrevoke"}
	old := Revocation{Kind: key.Kind, ID: key.ID, RevokedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}
	s.applyLocal(old)

	if err := s.Unrevoke(ctx, key); err != nil {
		t.Fatalf("Unrevoke: %v", err)
	}
	s.applyLocal(old)
	if s.IsRevoked(key.ID, 0, time.Time{}) {
		t.Fatal("stale reconcile resurrected an unrevoked entry")
	}

	if err := s.Revoke(ctx, key, "new kill", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("newer Revoke: %v", err)
	}
	if !s.IsRevoked(key.ID, 0, time.Time{}) {
		t.Fatal("newer revoke was suppressed by tombstone")
	}
	s.mu.RLock()
	_, tombstoned := s.tombstones[key]
	s.mu.RUnlock()
	if tombstoned {
		t.Fatal("newer revoke did not clear tombstone")
	}
}

func TestConcurrentRevokeUnrevokeThenReconcileDoesNotResurrect(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	key := Key{Kind: KindSession, ID: "sess-race"}
	var wg sync.WaitGroup
	for range 25 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = s.Revoke(ctx, key, "race", time.Now().Add(time.Hour))
		}()
		go func() {
			defer wg.Done()
			_ = s.Unrevoke(ctx, key)
		}()
	}
	wg.Wait()

	stale := Revocation{Kind: key.Kind, ID: key.ID, RevokedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.Unrevoke(ctx, key); err != nil {
		t.Fatalf("final Unrevoke: %v", err)
	}
	s.applyLocal(stale)
	if s.IsRevoked(key.ID, 0, time.Time{}) {
		t.Fatal("stale reconcile resurrected entry after concurrent operations")
	}
}

func TestDelayedUnrevokeEventDoesNotDeleteNewerRevoke(t *testing.T) {
	s := newMemStore()
	key := Key{Kind: KindSession, ID: "sess-order"}
	issuedAt := time.Now().Add(-time.Minute)
	s.applyLocal(Revocation{Kind: key.Kind, ID: key.ID, RevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
	payload, err := json.Marshal(unrevocation{Kind: key.Kind, ID: key.ID, IssuedAt: issuedAt, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	s.handleEvent(cache.Event{Type: EventStreamUnrevoked, Payload: string(payload)})
	if !s.IsRevoked(key.ID, 0, time.Time{}) {
		t.Fatal("delayed unrevoke event deleted a newer revocation")
	}
}

func TestUnrevokePropagationFailuresLeaveLocalStateUnrevoked(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	s := New(Options{Redis: rdb, Bus: failingBus{}})
	key := Key{Kind: KindSession, ID: "sess-local"}
	s.applyLocal(Revocation{Kind: key.Kind, ID: key.ID, RevokedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)})

	warnings, err := s.UnrevokeWithWarnings(context.Background(), key)
	if err != nil {
		t.Fatalf("UnrevokeWithWarnings: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want Redis and publish failures", warnings)
	}
	if s.IsRevoked(key.ID, 0, time.Time{}) {
		t.Fatal("local revocation remained after propagation failures")
	}
}

func TestMaintainRepairsShorterDurableExpiry(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDurable()
	key := Key{Kind: KindSession, ID: "sess-short-durable"}
	short := Revocation{Kind: key.Kind, ID: key.ID, RevokedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Minute)}
	long := Revocation{Kind: key.Kind, ID: key.ID, RevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	fake.rows[key] = short
	s := New(Options{Durable: fake})
	s.applyLocal(long)

	s.maintain(ctx)

	fake.mu.Lock()
	got := fake.rows[key]
	upserts := fake.upserts
	fake.mu.Unlock()
	if got.ExpiresAt.Before(long.ExpiresAt) {
		t.Fatalf("durable expiry = %v, want at least %v", got.ExpiresAt, long.ExpiresAt)
	}
	if upserts == 0 {
		t.Fatal("shorter durable row was not re-upserted")
	}
}

func TestIsRevoked(t *testing.T) {
	ctx := context.Background()

	// preCutoff stands in for a stream credential issued before any revocation
	// a test writes; user-kind matching requires the credential to predate the
	// revocation (the cutoff), session-kind matching ignores it.
	preCutoff := time.Now().Add(-time.Minute)

	tests := []struct {
		name       string
		setup      func(t *testing.T, s *Store)
		sessionID  string
		userID     int
		startedAt  time.Time
		wantRevoke bool
	}{
		{
			name: "revoked session is revoked",
			setup: func(t *testing.T, s *Store) {
				if err := s.RevokeSession(ctx, "sess-1", "abuse"); err != nil {
					t.Fatalf("RevokeSession: %v", err)
				}
			},
			sessionID:  "sess-1",
			userID:     42,
			startedAt:  preCutoff,
			wantRevoke: true,
		},
		{
			name: "revoked user revokes that user's pre-revocation streams",
			setup: func(t *testing.T, s *Store) {
				if err := s.RevokeUser(ctx, 7, "banned"); err != nil {
					t.Fatalf("RevokeUser: %v", err)
				}
			},
			sessionID:  "any-session-for-user-7",
			userID:     7,
			startedAt:  preCutoff,
			wantRevoke: true,
		},
		{
			name: "user revocation is a cutoff: a post-revocation credential plays",
			setup: func(t *testing.T, s *Store) {
				if err := s.RevokeUser(ctx, 7, "sessions_revoked"); err != nil {
					t.Fatalf("RevokeUser: %v", err)
				}
			},
			sessionID:  "fresh-session-for-user-7",
			userID:     7,
			startedAt:  time.Now().Add(time.Second),
			wantRevoke: false,
		},
		{
			name: "user revocation never matches an unknown credential time (fail open)",
			setup: func(t *testing.T, s *Store) {
				if err := s.RevokeUser(ctx, 7, "banned"); err != nil {
					t.Fatalf("RevokeUser: %v", err)
				}
			},
			sessionID:  "sess-without-start-time",
			userID:     7,
			startedAt:  time.Time{},
			wantRevoke: false,
		},
		{
			name: "session revocation ignores the credential time",
			setup: func(t *testing.T, s *Store) {
				if err := s.RevokeSession(ctx, "sess-exact", "admin_terminate"); err != nil {
					t.Fatalf("RevokeSession: %v", err)
				}
			},
			sessionID:  "sess-exact",
			userID:     42,
			startedAt:  time.Now().Add(time.Hour),
			wantRevoke: true,
		},
		{
			name: "expired revocation is not revoked",
			setup: func(t *testing.T, s *Store) {
				past := time.Now().Add(-time.Minute)
				if err := s.Revoke(ctx, Key{Kind: KindSession, ID: "sess-old"}, "stale", past); err != nil {
					t.Fatalf("Revoke: %v", err)
				}
			},
			sessionID:  "sess-old",
			userID:     1,
			startedAt:  preCutoff,
			wantRevoke: false,
		},
		{
			name:       "unrelated session and user are not revoked",
			setup:      func(t *testing.T, s *Store) {},
			sessionID:  "unknown",
			userID:     999,
			startedAt:  preCutoff,
			wantRevoke: false,
		},
		{
			name: "unrelated session with a different revoked user is not revoked",
			setup: func(t *testing.T, s *Store) {
				if err := s.RevokeUser(ctx, 7, "banned"); err != nil {
					t.Fatalf("RevokeUser: %v", err)
				}
			},
			sessionID:  "sess-other",
			userID:     8,
			startedAt:  preCutoff,
			wantRevoke: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newMemStore()
			tt.setup(t, s)
			if got := s.IsRevoked(tt.sessionID, tt.userID, tt.startedAt); got != tt.wantRevoke {
				t.Fatalf("IsRevoked(%q, %d, %v) = %v, want %v", tt.sessionID, tt.userID, tt.startedAt, got, tt.wantRevoke)
			}
		})
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	if got := s.List(); len(got) != 0 {
		t.Fatalf("List on empty store = %d entries, want 0", len(got))
	}

	if err := s.RevokeSession(ctx, "sess-a", "r1"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if err := s.RevokeUser(ctx, 5, "r2"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	// An expired entry must not appear in List.
	if err := s.Revoke(ctx, Key{Kind: KindSession, ID: "sess-exp"}, "r3", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	got := s.List()
	if len(got) != 2 {
		t.Fatalf("List = %d active entries, want 2: %+v", len(got), got)
	}

	seen := make(map[Key]Revocation, len(got))
	for _, r := range got {
		seen[Key{Kind: r.Kind, ID: r.ID}] = r
	}
	if _, ok := seen[Key{Kind: KindSession, ID: "sess-a"}]; !ok {
		t.Errorf("List missing revoked session sess-a")
	}
	if _, ok := seen[Key{Kind: KindUser, ID: "5"}]; !ok {
		t.Errorf("List missing revoked user 5")
	}
	if _, ok := seen[Key{Kind: KindSession, ID: "sess-exp"}]; ok {
		t.Errorf("List returned expired entry sess-exp")
	}
}

func TestExpiryPrunesFromCache(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	// Revoke with a short future TTL, then confirm it lapses.
	until := time.Now().Add(30 * time.Millisecond)
	if err := s.Revoke(ctx, Key{Kind: KindUser, ID: "3"}, "temp", until); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	preCutoff := time.Now().Add(-time.Minute)
	if !s.IsRevoked("whatever", 3, preCutoff) {
		t.Fatalf("expected user 3 to be revoked before expiry")
	}

	time.Sleep(50 * time.Millisecond)

	if s.IsRevoked("whatever", 3, preCutoff) {
		t.Fatalf("expected user 3 to no longer be revoked after expiry")
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List after expiry = %d entries, want 0", len(got))
	}
}

// TestMonotonicExpiry guards the invariant that a re-revoke with a SHORTER TTL
// can never shorten a longer-lived kill. The async over-cap enforcer re-revokes
// with a short self-healing TTL; without monotonic expiry it would shrink an
// admin's 24h RevokeSession on the same session key and reopen the
// restart-resurrection window.
func TestMonotonicExpiry(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	long := time.Now().Add(24 * time.Hour)
	if err := s.Revoke(ctx, Key{Kind: KindSession, ID: "sess-1"}, "admin_terminate", long); err != nil {
		t.Fatalf("Revoke long: %v", err)
	}
	// A shorter re-revoke (the enforcer's 5m self-heal TTL) must not win.
	short := time.Now().Add(5 * time.Minute)
	if err := s.Revoke(ctx, Key{Kind: KindSession, ID: "sess-1"}, "over_cap", short); err != nil {
		t.Fatalf("Revoke short: %v", err)
	}
	got := s.List()
	if len(got) != 1 {
		t.Fatalf("List = %d entries, want 1", len(got))
	}
	if !got[0].ExpiresAt.Equal(long) {
		t.Fatalf("ExpiresAt = %v, want the longer %v (shorter re-revoke must not shorten the kill)", got[0].ExpiresAt, long)
	}

	// A LONGER re-revoke does extend the kill.
	longer := time.Now().Add(48 * time.Hour)
	if err := s.Revoke(ctx, Key{Kind: KindSession, ID: "sess-1"}, "extended", longer); err != nil {
		t.Fatalf("Revoke longer: %v", err)
	}
	got = s.List()
	if len(got) != 1 || !got[0].ExpiresAt.Equal(longer) {
		t.Fatalf("ExpiresAt after longer re-revoke = %v, want %v", got[0].ExpiresAt, longer)
	}
}

// TestUserZeroNeverMatches guards the "no resolved owner" sentinel: a session-
// only check (userID 0, e.g. from the transcode node) must never be caught by a
// stray user:"0" revocation, which would read as "every ownerless request is
// revoked".
func TestUserZeroNeverMatches(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	// RevokeUser(0) is rejected outright: IsRevoked never matches a user:0 entry,
	// so creating one would be a silently-ineffective kill. Guarding here means no
	// stray user:0 revocation can exist to be misread as "every ownerless request
	// is revoked".
	if err := s.RevokeUser(ctx, 0, "should-not-nuke-everything"); err == nil {
		t.Fatalf("RevokeUser(0) must be rejected, got nil error")
	}
	if s.IsRevoked("some-unrelated-session", 0, time.Now().Add(-time.Minute)) {
		t.Fatalf("userID 0 sentinel must not match a user:0 revocation")
	}
	// A real session revocation still works with a 0 owner id.
	if err := s.RevokeSession(ctx, "sess-x", "abuse"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if !s.IsRevoked("sess-x", 0, time.Time{}) {
		t.Fatalf("expected sess-x to be revoked even with owner id 0")
	}
}

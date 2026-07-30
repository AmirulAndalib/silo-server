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

// errFakeDurable is the error fakeDurable.List returns while failNext > 0.
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
// List/Prune concurrently with the test's own reads, so every field is
// mutex-guarded. failNext, when > 0, makes List return an error that many
// times before succeeding, to exercise the bounded boot-warm retry.
type fakeDurable struct {
	mu              sync.Mutex
	rows            map[Key]Revocation
	tombstones      map[Key]Tombstone
	upserts         int
	prunes          int
	failNext        int
	lists           int
	upsertStarted   chan struct{}
	unblockUpsert   chan struct{}
	upsertStartOnce sync.Once
	listStarted     chan struct{}
	unblockList     chan struct{}
	listStartOnce   sync.Once
}

type failingBus struct{}

func (failingBus) Publish(context.Context, string, cache.Event) error {
	return errors.New("publish failed")
}
func (failingBus) Subscribe(context.Context, string, cache.EventHandler) error { return nil }
func (failingBus) Close() error                                                { return nil }

type recordingBus struct {
	mu     sync.Mutex
	events []cache.Event
}

func (b *recordingBus) Publish(_ context.Context, _ string, event cache.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}
func (*recordingBus) Subscribe(context.Context, string, cache.EventHandler) error { return nil }
func (*recordingBus) Close() error                                                { return nil }

type recordingRedisHook struct {
	set chan struct{}
}

func (h *recordingRedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *recordingRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "set" {
			select {
			case h.set <- struct{}{}:
			default:
			}
			return nil
		}
		return next(ctx, cmd)
	}
}

func (h *recordingRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (f *fakeDurable) UpsertTombstone(_ context.Context, tombstone Tombstone) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := tombstone.key()
	if r, ok := f.rows[key]; ok && r.RevokedAt.After(tombstone.UnrevokedAt) {
		return nil
	}
	if previous, ok := f.tombstones[key]; ok && previous.UnrevokedAt.After(tombstone.UnrevokedAt) {
		return nil
	}
	delete(f.rows, key)
	f.tombstones[key] = tombstone
	return nil
}

func newFakeDurable() *fakeDurable {
	return &fakeDurable{
		rows:       make(map[Key]Revocation),
		tombstones: make(map[Key]Tombstone),
	}
}

func (f *fakeDurable) Upsert(ctx context.Context, r Revocation) error {
	if f.upsertStarted != nil {
		f.upsertStartOnce.Do(func() { close(f.upsertStarted) })
		select {
		case <-f.unblockUpsert:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.key()
	if tombstone, ok := f.tombstones[key]; ok {
		if !time.Now().Before(tombstone.ExpiresAt) {
			delete(f.tombstones, key)
		} else if !r.RevokedAt.After(tombstone.UnrevokedAt) {
			f.upserts++
			return nil
		} else {
			delete(f.tombstones, key)
		}
	}
	if existing, ok := f.rows[key]; ok {
		if r.effectiveExpiry().After(existing.effectiveExpiry()) {
			existing.ExpiresAt = r.ExpiresAt
		}
		if r.RevokedAt.After(existing.RevokedAt) {
			existing.RevokedAt = r.RevokedAt
			existing.Reason = r.Reason
		}
		r = existing
	}
	f.rows[key] = r
	f.upserts++
	return nil
}

func (f *fakeDurable) List(ctx context.Context) (DurableState, error) {
	if f.listStarted != nil {
		f.listStartOnce.Do(func() { close(f.listStarted) })
		select {
		case <-f.unblockList:
		case <-ctx.Done():
			return DurableState{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	if f.failNext > 0 {
		f.failNext--
		return DurableState{}, errFakeDurable
	}
	now := time.Now()
	state := DurableState{Revocations: make([]Revocation, 0, len(f.rows))}
	for _, r := range f.rows {
		if !r.expired(now) {
			state.Revocations = append(state.Revocations, r)
		}
	}
	for _, tombstone := range f.tombstones {
		if now.Before(tombstone.ExpiresAt) {
			state.Tombstones = append(state.Tombstones, tombstone)
		}
	}
	return state, nil
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
	for k, tombstone := range f.tombstones {
		if !now.Before(tombstone.ExpiresAt) {
			delete(f.tombstones, k)
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

func TestApplyLocalMergesCutoffAndExpiryIndependently(t *testing.T) {
	const secondReason = "second"
	s := newMemStore()
	key := Key{Kind: KindUser, ID: "7"}
	firstCutoff := time.Now().Add(-2 * time.Minute)
	secondCutoff := firstCutoff.Add(time.Minute)
	longExpiry := time.Now().Add(12 * time.Hour)
	s.applyLocal(Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "first",
		RevokedAt: firstCutoff, ExpiresAt: longExpiry,
	})
	s.applyLocal(Revocation{
		Kind: key.Kind, ID: key.ID, Reason: secondReason,
		RevokedAt: secondCutoff, ExpiresAt: time.Now().Add(time.Hour),
	})

	got := s.effective(key)
	if !got.ExpiresAt.Equal(longExpiry) {
		t.Fatalf("expiry = %v, want longer %v", got.ExpiresAt, longExpiry)
	}
	if !got.RevokedAt.Equal(secondCutoff) || got.Reason != secondReason {
		t.Fatalf("cutoff/reason = %v/%q, want %v/%s", got.RevokedAt, got.Reason, secondCutoff, secondReason)
	}
	between := firstCutoff.Add(30 * time.Second)
	if !s.IsRevoked("session", 7, between) {
		t.Fatal("credential between the two cutoffs was not refused")
	}
}

func TestDurableMergeMatchesLocalMerge(t *testing.T) {
	const secondReason = "second"
	fake := newFakeDurable()
	key := Key{Kind: KindUser, ID: "9"}
	firstCutoff := time.Now().Add(-2 * time.Minute)
	secondCutoff := firstCutoff.Add(time.Minute)
	longExpiry := time.Now().Add(12 * time.Hour)
	if err := fake.Upsert(context.Background(), Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "first",
		RevokedAt: firstCutoff, ExpiresAt: longExpiry,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fake.Upsert(context.Background(), Revocation{
		Kind: key.Kind, ID: key.ID, Reason: secondReason,
		RevokedAt: secondCutoff, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	got := fake.rows[key]
	fake.mu.Unlock()
	if !got.ExpiresAt.Equal(longExpiry) || !got.RevokedAt.Equal(secondCutoff) || got.Reason != secondReason {
		t.Fatalf("durable merge = %+v", got)
	}
}

func TestRevokePublishesMergedRecord(t *testing.T) {
	bus := &recordingBus{}
	s := New(Options{Bus: bus})
	key := Key{Kind: KindUser, ID: "11"}
	longExpiry := time.Now().Add(12 * time.Hour)
	s.applyLocal(Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "old-long",
		RevokedAt: time.Now().Add(-time.Hour), ExpiresAt: longExpiry,
	})

	if err := s.Revoke(context.Background(), key, "new-cutoff", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	bus.mu.Lock()
	events := append([]cache.Event(nil), bus.events...)
	bus.mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want 1", len(events))
	}
	var published Revocation
	if err := json.Unmarshal([]byte(events[0].Payload), &published); err != nil {
		t.Fatal(err)
	}
	if !published.ExpiresAt.Equal(longExpiry) || published.Reason != "new-cutoff" {
		t.Fatalf("published record = %+v, want merged expiry and new reason", published)
	}
}

func TestBlockedDurableDoesNotDelayRedisAndIsBounded(t *testing.T) {
	fake := newFakeDurable()
	fake.upsertStarted = make(chan struct{})
	fake.unblockUpsert = make(chan struct{})
	rdb := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	t.Cleanup(func() { _ = rdb.Close() })
	redisSet := make(chan struct{}, 2)
	rdb.AddHook(&recordingRedisHook{set: redisSet})
	s := New(Options{
		Redis: rdb, Durable: fake, PropagationTimeout: 25 * time.Millisecond,
	})

	start := time.Now()
	warnings, err := s.RevokeWithWarnings(
		context.Background(), Key{Kind: KindSession, ID: "bounded-1"},
		"test", time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-redisSet:
	default:
		t.Fatal("Redis mirror was not attempted before the durable store blocked")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded revoke took %v", elapsed)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "durable") {
		t.Fatalf("warnings = %v, want durable timeout warning", warnings)
	}

	secondDone := make(chan struct{})
	go func() {
		_, _ = s.RevokeWithWarnings(
			context.Background(), Key{Kind: KindSession, ID: "bounded-2"},
			"test", time.Now().Add(time.Hour),
		)
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second revoke remained blocked behind the timed-out durable store")
	}
}

func TestAutomatedRevokeIfAbsentDoesNotWeakenAdminMerge(t *testing.T) {
	s := newMemStore()
	ctx := context.Background()
	created, err := s.RevokeSessionForIfAbsent(ctx, "shared-key", "over-cap", time.Hour)
	if err != nil || !created {
		t.Fatalf("initial automated revoke = %v, %v", created, err)
	}
	first := s.effective(Key{Kind: KindSession, ID: "shared-key"})
	created, err = s.RevokeSessionForIfAbsent(ctx, "shared-key", "over-cap", 12*time.Hour)
	if err != nil || created {
		t.Fatalf("second automated revoke = %v, %v; want skipped", created, err)
	}
	if got := s.effective(first.key()); !got.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("automated re-evaluation extended expiry from %v to %v", first.ExpiresAt, got.ExpiresAt)
	}

	adminUntil := time.Now().Add(12 * time.Hour)
	if err := s.Revoke(ctx, first.key(), "admin", adminUntil); err != nil {
		t.Fatal(err)
	}
	got := s.effective(first.key())
	if got.ExpiresAt.Before(adminUntil) || got.Reason != "admin" {
		t.Fatalf("admin merge = %+v, want longer admin kill", got)
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
// transient List failure at startup is retried rather than leaving the
// kill list empty until the first poll tick.
func TestWarmFromDurableRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	fake := newFakeDurable()
	live := Revocation{Kind: KindSession, ID: "sess-retry", Reason: "x", RevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	_ = fake.Upsert(ctx, live)
	// Fail the first two List calls; the third (within the 3-attempt
	// bound) succeeds.
	fake.mu.Lock()
	fake.failNext = 2
	fake.lists = 0
	fake.mu.Unlock()

	s := New(Options{Durable: fake})
	s.StartSync(ctx)

	if !s.IsRevoked("sess-retry", 0, time.Time{}) {
		t.Fatalf("expected sess-retry warmed into cache after retried List")
	}
	if got := fake.listCount(); got != 3 {
		t.Fatalf("List calls = %d, want 3 (two failures + one success)", got)
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
		t.Fatalf("List attempts = %d, want 3 (bounded)", got)
	}
}

func TestStartSyncDurableWarmHasDeadline(t *testing.T) {
	fake := newFakeDurable()
	fake.listStarted = make(chan struct{})
	fake.unblockList = make(chan struct{})
	s := New(Options{Durable: fake, PropagationTimeout: 25 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	s.StartSync(ctx)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("StartSync durable warm took %v", elapsed)
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

func TestMaintainDurableTimeoutReleasesOperationLock(t *testing.T) {
	fake := newFakeDurable()
	fake.listStarted = make(chan struct{})
	fake.unblockList = make(chan struct{})
	s := New(Options{Durable: fake, PropagationTimeout: 25 * time.Millisecond})

	maintainDone := make(chan struct{})
	go func() {
		s.maintain(context.Background())
		close(maintainDone)
	}()
	<-fake.listStarted

	revokeDone := make(chan error, 1)
	go func() {
		revokeDone <- s.RevokeSession(context.Background(), "after-maintain-timeout", "test")
	}()
	select {
	case err := <-revokeDone:
		if err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("revoke remained blocked behind timed-out durable maintenance")
	}
	select {
	case <-maintainDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("durable maintenance did not return after its bounded context expired")
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

func TestDurableTombstoneSurvivesRestartAndRejectsStaleReplica(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDurable()
	key := Key{Kind: KindSession, ID: "sess-durable-unrevoke"}
	old := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "old",
		RevokedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour),
	}
	first := New(Options{Durable: fake})
	first.applyLocal(old)
	if err := fake.Upsert(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := first.Unrevoke(ctx, key); err != nil {
		t.Fatalf("Unrevoke: %v", err)
	}

	// A stale replica that missed the unrevocation cannot overwrite its durable
	// tombstone during self-heal.
	if err := fake.Upsert(ctx, old); err != nil {
		t.Fatal(err)
	}

	syncCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fresh := New(Options{Durable: fake})
	fresh.StartSync(syncCtx)
	if fresh.IsRevoked(key.ID, 0, time.Time{}) {
		t.Fatal("fresh store resurrected a durably tombstoned revocation")
	}
	fake.mu.Lock()
	_, rowExists := fake.rows[key]
	_, tombstoneExists := fake.tombstones[key]
	fake.mu.Unlock()
	if rowExists || !tombstoneExists {
		t.Fatalf("durable state row=%v tombstone=%v, want tombstone only", rowExists, tombstoneExists)
	}
}

func TestDurableTombstoneAllowsGenuinelyNewerRevocation(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDurable()
	key := Key{Kind: KindSession, ID: "sess-new-after-unrevoke"}
	unrevokedAt := time.Now().Add(-time.Minute)
	tombstone := Tombstone{
		Kind: key.Kind, ID: key.ID, UnrevokedAt: unrevokedAt, ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := fake.UpsertTombstone(ctx, tombstone); err != nil {
		t.Fatal(err)
	}
	newer := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "new",
		RevokedAt: unrevokedAt.Add(time.Second), ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	if err := fake.Upsert(ctx, newer); err != nil {
		t.Fatal(err)
	}
	state, err := fake.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Revocations) != 1 || len(state.Tombstones) != 0 {
		t.Fatalf("durable state = %+v, want newer revocation only", state)
	}
}

func TestDurableListDoesNotSurfaceTombstonedRow(t *testing.T) {
	fake := newFakeDurable()
	tombstone := Tombstone{
		Kind: KindUser, ID: "13", UnrevokedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := fake.UpsertTombstone(context.Background(), tombstone); err != nil {
		t.Fatal(err)
	}
	state, err := fake.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Revocations) != 0 || len(state.Tombstones) != 1 {
		t.Fatalf("durable state = %+v", state)
	}
}

func TestDurablePruneRemovesExpiredTombstone(t *testing.T) {
	fake := newFakeDurable()
	key := Key{Kind: KindSession, ID: "expired-tombstone"}
	fake.tombstones[key] = Tombstone{
		Kind: key.Kind, ID: key.ID,
		UnrevokedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour),
	}
	if err := fake.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	_, exists := fake.tombstones[key]
	fake.mu.Unlock()
	if exists {
		t.Fatal("expired durable tombstone was not pruned")
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

func TestMaintainRepairsOlderDurableCutoffWithLongExpiry(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDurable()
	key := Key{Kind: KindUser, ID: "17"}
	longExpiry := time.Now().Add(12 * time.Hour)
	oldCutoff := time.Now().Add(-2 * time.Hour)
	newCutoff := oldCutoff.Add(time.Hour)
	fake.rows[key] = Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "old",
		RevokedAt: oldCutoff, ExpiresAt: longExpiry,
	}
	s := New(Options{Durable: fake})
	s.applyLocal(Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "new",
		RevokedAt: newCutoff, ExpiresAt: time.Now().Add(time.Hour),
	})

	s.maintain(ctx)

	fake.mu.Lock()
	got := fake.rows[key]
	fake.mu.Unlock()
	if !got.RevokedAt.Equal(newCutoff) || got.Reason != "new" || !got.ExpiresAt.Equal(longExpiry) {
		t.Fatalf("durable merge after maintain = %+v", got)
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
	// A shorter re-revoke must not win.
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

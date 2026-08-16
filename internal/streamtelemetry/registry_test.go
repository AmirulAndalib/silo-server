package streamtelemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/httpstream"
)

func testConfig() Config {
	cfg := DefaultConfig("test-node")
	cfg.Enabled = true
	cfg.PublisherID = "test-publisher"
	cfg.Retention = time.Millisecond
	return cfg
}

func testRoute(class Class) MediaRoute {
	return MediaRoute{Family: FamilyNative, Method: http.MethodGet, Pattern: "/media/{id}",
		Class: class, Role: RoleViewerEgress, CapRelevant: class != ClassTransfer, Enrolled: true}
}

func testAttachment(id string) Attachment {
	return Attachment{Subject: UserSubject(7), ProfileID: "profile", SessionID: id, MediaFileID: 42,
		PlayMethod: "direct", StartedAt: time.Unix(100, 0), StartedAtSource: StartedAtSourceSession,
		TokenIssuedAtSource: TokenIssuedAtSourceNone}
}

func TestProvisionalObservationDoesNotCreateLogicalActivity(t *testing.T) {
	registry := NewRegistry(testConfig(), NewLocalStore(), slog.New(slog.DiscardHandler))
	handler := registry.Observe(testRoute(ClassPlayback))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("denied"))
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/x", nil))
	snapshot := registry.Sweep()
	if len(snapshot.Sessions) != 0 || len(snapshot.Transfers) != 0 {
		t.Fatalf("provisional request created logical activity: %+v", snapshot)
	}
	if snapshot.UnattributedObservations != 1 || snapshot.UnattributedBytes != 6 {
		t.Fatalf("unattributed counters = %d/%d", snapshot.UnattributedObservations, snapshot.UnattributedBytes)
	}
}

func TestReleaseFoldsShortObservationAndCollectorAdvancesByteClock(t *testing.T) {
	registry := NewRegistry(testConfig(), NewLocalStore(), nil)
	handler := registry.Observe(testRoute(ClassPlayback))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Attach(r.Context(), testAttachment("session-1"))
		_, _ = w.Write([]byte("payload"))
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media/x", nil))
	before := registry.Snapshot()
	if len(before.Sessions) != 1 || before.Sessions[0].OpenObservations != 0 {
		t.Fatalf("released session = %+v", before.Sessions)
	}
	swept := registry.Sweep()
	if swept.Sessions[0].BytesAccepted != 7 || swept.Sessions[0].LastByteAccepted.IsZero() {
		t.Fatalf("swept session = %+v", swept.Sessions[0])
	}
	if got := swept.Sessions[0].Routes[0].BytesAccepted; got != 7 {
		t.Fatalf("route bytes = %d", got)
	}
}

func TestReleaseConcurrentWithSweepDoesNotLoseOrDoubleCount(t *testing.T) {
	registry := NewRegistry(testConfig(), NewLocalStore(), nil)
	obs := registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, testAttachment("session-race"))
	obs.AddBytes(4096)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); registry.release(obs, httpstreamOutcomeCompleted) }()
	go func() { defer wg.Done(); _ = registry.Sweep() }()
	wg.Wait()
	snapshot := registry.Sweep()
	if got := snapshot.Sessions[0].BytesAccepted; got != 4096 {
		t.Fatalf("bytes after concurrent release/sweep = %d", got)
	}
}

const httpstreamOutcomeCompleted = "completed"

func TestExactObservationBoundServesThroughAndCountsDrops(t *testing.T) {
	cfg := testConfig()
	cfg.MaxObservations = 2
	registry := NewRegistry(cfg, NewLocalStore(), nil)
	one := registry.begin(testRoute(ClassPlayback), CaptureSet{})
	two := registry.begin(testRoute(ClassPlayback), CaptureSet{})
	three := registry.begin(testRoute(ClassPlayback), CaptureSet{})
	three.AddBytes(9)
	registry.release(three, OutcomeUnknown)
	if !three.countingOnly || registry.observationReservations.Load() != 2 {
		t.Fatalf("bound was not exact: counting=%t reservations=%d", three.countingOnly, registry.observationReservations.Load())
	}
	registry.release(one, OutcomeUnknown)
	registry.release(two, OutcomeUnknown)
	snapshot := registry.Snapshot()
	if !snapshot.Truncated || snapshot.DroppedObservations != 1 || snapshot.DroppedBytes != 9 {
		t.Fatalf("drop counters = %+v", snapshot)
	}
}

func TestStartedAtImprovementAndIdentityConflict(t *testing.T) {
	registry := NewRegistry(testConfig(), NewLocalStore(), nil)
	obs := registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Unix(200, 0)})
	first := testAttachment("session-conflict")
	first.StartedAt = time.Time{}
	first.StartedAtSource = ""
	registry.attach(obs, first)
	offered := first
	offered.Subject = UserSubject(8)
	offered.StartedAt = time.Unix(50, 0)
	offered.StartedAtSource = StartedAtSourceClaim
	registry.attach(obs, offered)
	registry.release(obs, httpstreamOutcomeCompleted)
	snapshot := registry.Sweep()
	session := snapshot.Sessions[0]
	if !session.HasIdentityConflict || session.Subject != UserSubject(7) {
		t.Fatalf("conflict did not preserve identity: %+v", session)
	}
	if session.StartedAtSource != StartedAtSourceClaim || !session.StartedAt.Equal(offered.StartedAt) || session.StartedAtDegraded {
		t.Fatalf("started-at authority was not improved: %+v", session)
	}
}

func TestMidPlaybackReplanUpdatesStateWithoutIdentityConflict(t *testing.T) {
	registry := NewRegistry(testConfig(), NewLocalStore(), nil)
	first := testAttachment("session-replan")
	first.MediaFileID = 100
	first.PlayMethod = "direct"
	obs := registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, first)
	registry.release(obs, httpstreamOutcomeCompleted)

	replanned := first
	replanned.MediaFileID = 200
	replanned.PlayMethod = "transcode"
	obs = registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, replanned)
	registry.release(obs, httpstreamOutcomeCompleted)

	session := registry.Sweep().Sessions[0]
	if session.HasIdentityConflict || len(session.IdentityConflicts) != 0 {
		t.Fatalf("replan recorded identity conflict: %+v", session.IdentityConflicts)
	}
	if session.MediaFileID != 200 || session.PlayMethod != "transcode" {
		t.Fatalf("current replan state = media %d, method %q", session.MediaFileID, session.PlayMethod)
	}
	if len(session.MediaFileIDs) != 2 || session.MediaFileIDs[0] != 100 || session.MediaFileIDs[1] != 200 {
		t.Fatalf("observed media files = %v", session.MediaFileIDs)
	}
	if len(session.PlayMethods) != 2 || session.PlayMethods[0] != "direct" || session.PlayMethods[1] != "transcode" {
		t.Fatalf("observed play methods = %v", session.PlayMethods)
	}

	changedOwner := replanned
	changedOwner.Subject = UserSubject(8)
	obs = registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, changedOwner)
	registry.release(obs, httpstreamOutcomeCompleted)
	if session = registry.Sweep().Sessions[0]; !session.HasIdentityConflict {
		t.Fatal("changed subject did not record an identity conflict")
	}
}

func TestUnknownAttachmentFieldsDoNotDisagreeWithSession(t *testing.T) {
	registry := NewRegistry(testConfig(), NewLocalStore(), nil)
	first := testAttachment("session-partial")
	obs := registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, first)
	registry.release(obs, httpstreamOutcomeCompleted)

	partial := Attachment{SessionID: first.SessionID}
	obs = registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, partial)
	registry.release(obs, httpstreamOutcomeCompleted)

	session := registry.Sweep().Sessions[0]
	if session.HasIdentityConflict || len(session.IdentityConflicts) != 0 {
		t.Fatalf("unknown fields recorded disagreement: %+v", session.IdentityConflicts)
	}
	if session.MediaFileID != first.MediaFileID || session.PlayMethod != first.PlayMethod {
		t.Fatalf("unknown fields replaced current state: media %d, method %q", session.MediaFileID, session.PlayMethod)
	}
}

func TestPruneReleasesReservations(t *testing.T) {
	cfg := testConfig()
	registry := NewRegistry(cfg, NewLocalStore(), nil)
	obs := registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, testAttachment("prune"))
	registry.release(obs, httpstreamOutcomeCompleted)
	registry.sweep(time.Now().Add(2 * cfg.Retention))
	if registry.sessionReservations.Load() != 0 || registry.observationReservations.Load() != 0 {
		t.Fatalf("reservations leaked: sessions=%d observations=%d", registry.sessionReservations.Load(), registry.observationReservations.Load())
	}
}

func TestRouteBoundDropsNewestRouteWithoutDroppingObservation(t *testing.T) {
	cfg := testConfig()
	cfg.MaxRoutesPerSession = 1
	registry := NewRegistry(cfg, NewLocalStore(), nil)
	for index, pattern := range []string{"/one", "/two"} {
		obs := registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: pattern, ReceivedAt: time.Now()})
		registry.attach(obs, testAttachment("route-bound"))
		obs.AddBytes(int64(index + 1))
		registry.release(obs, httpstreamOutcomeCompleted)
	}
	snapshot := registry.Sweep()
	session := snapshot.Sessions[0]
	if !session.RoutesOverflowed || len(session.Routes) != 1 || session.BytesAccepted != 3 || snapshot.DroppedObservations != 0 {
		t.Fatalf("route overflow = %+v", snapshot)
	}
}

func TestSetRealtimeConnectionIgnoresUnknownAndIsIdempotent(t *testing.T) {
	registry := NewRegistry(testConfig(), NewLocalStore(), nil)
	registry.SetRealtimeConnection("missing", true)
	if len(registry.Snapshot().Sessions) != 0 {
		t.Fatal("realtime update created a session")
	}
	obs := registry.begin(testRoute(ClassPlayback), CaptureSet{Method: http.MethodGet, Pattern: "/media/{id}", ReceivedAt: time.Now()})
	registry.attach(obs, testAttachment("known"))
	registry.SetRealtimeConnection("known", true)
	registry.SetRealtimeConnection("known", true)
	if !registry.Snapshot().Sessions[0].RealtimeConnectionAlive {
		t.Fatal("realtime connection not recorded")
	}
	registry.release(obs, httpstreamOutcomeCompleted)
}

type failingStore struct{ published atomic.Int64 }

func (s *failingStore) Publish(context.Context, Snapshot) error {
	s.published.Add(1)
	return errors.New("publish failed")
}
func (s *failingStore) Load(context.Context) (Snapshot, error) { return Snapshot{}, nil }

func TestStartContinuesAfterPublishError(t *testing.T) {
	cfg := testConfig()
	cfg.SweepInterval = time.Millisecond
	store := &failingStore{}
	registry := NewRegistry(cfg, store, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	registry.Start(ctx)
	time.Sleep(8 * time.Millisecond)
	cancel()
	if store.published.Load() < 2 {
		t.Fatalf("collector stopped after publish error: %d publishes", store.published.Load())
	}
}

func TestLocalStoreDeepCopies(t *testing.T) {
	store := NewLocalStore()
	source := Snapshot{Sessions: []SessionView{{ViewerIPs: []string{"one"}, Routes: []RouteActivityView{{Pattern: "/one"}}, Outcomes: map[httpstream.StreamOutcome]int64{"completed": 1}}}}
	if err := store.Publish(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	source.Sessions[0].ViewerIPs[0] = "mutated-source"
	loaded, _ := store.Load(context.Background())
	loaded.Sessions[0].ViewerIPs[0] = "mutated-load"
	loaded.Sessions[0].Routes[0].Pattern = "/mutated"
	loadedAgain, _ := store.Load(context.Background())
	if loadedAgain.Sessions[0].ViewerIPs[0] != "one" || loadedAgain.Sessions[0].Routes[0].Pattern != "/one" {
		t.Fatalf("store returned aliased snapshot: %+v", loadedAgain)
	}
}

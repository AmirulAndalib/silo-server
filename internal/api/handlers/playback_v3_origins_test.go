package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// recordingProxyGrantStoreV3 stands in for the shared Redis grant store: it
// records what a proxy would be told to serve, so a test can assert on the
// authority the URL depends on rather than only on the URL's shape.
type recordingProxyGrantStoreV3 struct {
	disabled bool
	putErr   error
	cards    map[string]playback.RecipeCard
	deleted  []string
}

func (s *recordingProxyGrantStoreV3) Enabled() bool { return !s.disabled }

func (s *recordingProxyGrantStoreV3) Put(_ context.Context, sessionID string, card playback.RecipeCard) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.cards == nil {
		s.cards = map[string]playback.RecipeCard{}
	}
	s.cards[sessionID] = card
	return nil
}

func (s *recordingProxyGrantStoreV3) Delete(_ context.Context, sessionID string) error {
	s.deleted = append(s.deleted, sessionID)
	return nil
}

func authorizedOriginsModeV3() mediaAuthModeV3 {
	return headerAuthenticatedMediaV3([]string{playback.FeatureHeaderAuthenticatedMediaV3, playback.FeatureAuthorizedMediaOriginsV3})
}

// The point of the mode: a header-authenticated attempt egresses from the pool
// again. The URL must name the proxy and carry no credential of any kind, and
// the proxy must have been handed the recipe it will serve from.
func TestPrepareTransportV3AuthorizedOriginsRestoreDirectPlayProxyEgress(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	planner := &recordingNodePlannerV3{plan: nodepool.Plan{ProxyNode: &nodepool.Node{URL: "http://proxy-1"}}}
	handler.NodePlanner = planner
	grants := &recordingProxyGrantStoreV3{}
	handler.ProxyGrantStore = grants
	file := v3HandlerFixtureFile(t)

	transport, transportErr := handler.prepareTransportV3(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&playback.Session{ID: "session-origin-direct", UserID: 7, ProfileID: "profile-1"},
		file,
		playback.PlannerResultV3{Plan: identityProxyPlanV3(playback.DeliveryOriginalHTTPV3), PlayMethod: playback.PlayDirect},
		authorizedOriginsModeV3())
	if transportErr != nil {
		t.Fatalf("prepare identity transport: %v", transportErr)
	}

	if transport.url != "http://proxy-1/stream/v3/session-origin-direct" {
		t.Fatalf("stream url = %q, want the credential-free proxy route", transport.url)
	}
	assertNoPlaybackCredentialV3(t, transport.url)

	card, ok := grants.cards["session-origin-direct"]
	if !ok {
		t.Fatal("no grant was written; the proxy has nothing to serve this session from")
	}
	if card.InputPath != file.FilePath {
		t.Fatalf("grant media path = %q, want %q", card.InputPath, file.FilePath)
	}
	if card.UserID != 7 || card.PlayMethod != playback.PlayDirect {
		t.Fatalf("grant identity = %#v, want the session's owner and play method", card)
	}

	// A transport that never reaches the client must not leave a live grant
	// behind, or the proxy keeps serving playback the server considers over.
	transport.rollback()
	if len(grants.deleted) != 1 || grants.deleted[0] != "session-origin-direct" {
		t.Fatalf("grants deleted on rollback = %v, want the session's grant revoked", grants.deleted)
	}
	if len(planner.released) != 1 {
		t.Fatalf("planner releases = %v, want the proxy reservation released", planner.released)
	}
}

// A remux egresses from the proxy too, and the grant has to carry the source
// facts the proxy cannot look up: without them it would serve a subtly
// different stream than the plan promised.
func TestPrepareTransportV3AuthorizedOriginsCarryRemuxSourceFacts(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	stubCopySeekAnchorV3(handler)
	proxy := capableProxyStubV3(t)
	handler.NodePlanner = &recordingNodePlannerV3{plan: nodepool.Plan{ProxyNode: &nodepool.Node{URL: proxy.URL + "/"}}}
	grants := &recordingProxyGrantStoreV3{}
	handler.ProxyGrantStore = grants

	file := v3HandlerFixtureFile(t)
	file.VideoTracks[0].DVProfile = 7
	plan := identityProxyPlanV3(playback.DeliveryRemuxProgressiveV3, playback.TransformationV3{Name: playback.TransformationAudioToAACV3, Executor: playback.ExecutorServerV3, RecipeVersion: "1"})
	plan.Timeline = playback.TimelineV3{SourceStartSeconds: 39.5}

	transport, transportErr := handler.prepareTransportV3(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&playback.Session{ID: "session-origin-remux", UserID: 7, ProfileID: "profile-1"},
		file,
		playback.PlannerResultV3{Plan: plan, PlayMethod: playback.PlayRemux, TranscodeAudio: true, TargetAudioCodec: "aac"},
		authorizedOriginsModeV3())
	if transportErr != nil {
		t.Fatalf("prepare identity transport: %v", transportErr)
	}
	defer transport.rollback()

	want := proxy.URL + "/stream/v3/session-origin-remux?seek=39.5"
	if transport.url != want {
		t.Fatalf("stream url = %q, want %q", transport.url, want)
	}
	assertNoPlaybackCredentialV3(t, transport.url)

	card := grants.cards["session-origin-remux"]
	if card.DVProfile != 7 {
		t.Fatalf("grant DV profile = %d, want 7 so the proxy strips the dangling RPU", card.DVProfile)
	}
	if !card.TranscodeAudio {
		t.Fatal("grant must tell the proxy to convert audio")
	}
}

// A grant that cannot be stored is not fatal: the attempt degrades to exactly
// what a header-authenticated attempt without origins would have gotten.
func TestPrepareTransportV3AuthorizedOriginsFallBackToTheAPIWhenTheGrantFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		store *recordingProxyGrantStoreV3
	}{
		{name: "write error", store: &recordingProxyGrantStoreV3{putErr: errors.New("redis is down")}},
		{name: "store disabled", store: &recordingProxyGrantStoreV3{disabled: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
			handler.JWTSecret = "test-secret"
			planner := &recordingNodePlannerV3{plan: nodepool.Plan{ProxyNode: &nodepool.Node{URL: "http://proxy-1"}}}
			handler.NodePlanner = planner
			handler.ProxyGrantStore = test.store

			transport, transportErr := handler.prepareTransportV3(
				httptest.NewRequest(http.MethodPost, "/", nil),
				&playback.Session{ID: "session-origin-fallback", UserID: 7, ProfileID: "profile-1"},
				v3HandlerFixtureFile(t),
				playback.PlannerResultV3{Plan: identityProxyPlanV3(playback.DeliveryOriginalHTTPV3), PlayMethod: playback.PlayDirect},
				authorizedOriginsModeV3())
			if transportErr != nil {
				t.Fatalf("prepare identity transport: %v", transportErr)
			}
			defer transport.rollback()

			if transport.url != "/stream/session-origin-fallback" {
				t.Fatalf("stream url = %q, want the API-local route", transport.url)
			}
			if len(planner.released) != 1 {
				t.Fatalf("planner releases = %v, want the unusable proxy reservation released", planner.released)
			}
		})
	}
}

// The grant-failure fallback lands on the API server, which is ffmpeg work for
// a remux carrying a server transformation. An operator who disabled
// playback.local_transcode_fallback disabled exactly that, so the fallback has
// to honor the same gate the no-origins mode enforces rather than quietly
// spawning an encoder. Escalation cannot cover this: it was legitimately
// skipped at plan time because the pool does offer a proxy.
func TestPrepareTransportV3AuthorizedOriginsRefuseLocalRemuxWhenTheGrantFails(t *testing.T) {
	handler, _, result := escalationFixtureV3(t, true)
	proxy := capableProxyStubV3(t)
	planner := &recordingNodePlannerV3{plan: nodepool.Plan{ProxyNode: &nodepool.Node{URL: proxy.URL}}}
	handler.NodePlanner = planner
	handler.ProxyGrantStore = &recordingProxyGrantStoreV3{putErr: errors.New("redis is down")}

	transport, transportErr := handler.prepareTransportV3(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&playback.Session{ID: "session-origin-refused", UserID: 7, ProfileID: "profile-1"},
		v3HandlerFixtureFile(t),
		result,
		authorizedOriginsModeV3())
	if transportErr == nil {
		transport.rollback()
		t.Fatalf("grant failure produced an API-local remux at %q; local fallback is disabled", transport.url)
	}
	if transportErr.reason != "capacity_unavailable" || !transportErr.retryable {
		t.Fatalf("transport error = %#v, want a retryable capacity_unavailable", transportErr)
	}
	if len(planner.released) != 1 {
		t.Fatalf("planner releases = %v, want the unusable proxy reservation released", planner.released)
	}
}

// Without the origins opt-in the mode is unchanged from what PR #723 shipped:
// everything stays on the API, and no grant is written at all.
func TestPrepareTransportV3HeaderAuthOnlyStaysOnTheAPIOrigin(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	handler.NodePlanner = &recordingNodePlannerV3{plan: nodepool.Plan{ProxyNode: &nodepool.Node{URL: "http://proxy-1"}}}
	grants := &recordingProxyGrantStoreV3{}
	handler.ProxyGrantStore = grants

	transport, transportErr := handler.prepareTransportV3(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&playback.Session{ID: "session-header-only", UserID: 7, ProfileID: "profile-1"},
		v3HandlerFixtureFile(t),
		playback.PlannerResultV3{Plan: identityProxyPlanV3(playback.DeliveryOriginalHTTPV3), PlayMethod: playback.PlayDirect},
		headerAuthenticatedMediaV3([]string{playback.FeatureHeaderAuthenticatedMediaV3}))
	if transportErr != nil {
		t.Fatalf("prepare identity transport: %v", transportErr)
	}
	defer transport.rollback()

	if transport.url != "/stream/session-header-only" {
		t.Fatalf("stream url = %q, want the API-local route", transport.url)
	}
	if len(grants.cards) != 0 {
		t.Fatalf("grants written = %v, want none for an attempt that negotiated no origins", grants.cards)
	}
}

// HLS keeps its pooled transcode node and gets its proxy back: the manifest is
// fetched from the proxy, whose relative segment URIs stay inside the same
// credential-free /stream/v3 family.
func TestPrepareTransportV3AuthorizedOriginsPublishGrantBackedHLSManifest(t *testing.T) {
	var startRequest transcodenode.TranscodeStartRequest
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{Transformations: []playback.TransformationV3{
				{Name: playback.TransformationVideoToH264V3, Executor: playback.ExecutorServerV3, RecipeVersion: playback.TransformationVideoToH264RecipeVersionV3},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			if err := json.NewDecoder(r.Body).Decode(&startRequest); err != nil {
				t.Errorf("decode remote start: %v", err)
			}
			writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: startRequest.SessionID, Status: "started"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer node.Close()

	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	planner := &recordingNodePlannerV3{plan: nodepool.Plan{TranscodeNode: &nodepool.Node{URL: node.URL}, ProxyNode: &nodepool.Node{URL: "http://proxy-1"}}}
	handler.NodePlanner = planner
	grants := &recordingProxyGrantStoreV3{}
	handler.ProxyGrantStore = grants

	plan := &playback.PlanV3{
		PlanID:          "plan:origin-hls",
		Delivery:        playback.DeliveryTranscodeHLSV3,
		Transformations: []playback.TransformationV3{{Name: playback.TransformationVideoToH264V3, Executor: playback.ExecutorServerV3, RecipeVersion: playback.TransformationVideoToH264RecipeVersionV3}},
	}
	transport, transportErr := handler.prepareTransportV3(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&playback.Session{ID: "session-origin-hls", UserID: 7, ProfileID: "profile-1"},
		v3HandlerFixtureFile(t),
		playback.PlannerResultV3{Plan: plan, PlayMethod: playback.PlayTranscode, TargetVideoCodec: "h264", TargetAudioCodec: "aac"},
		authorizedOriginsModeV3())
	if transportErr != nil {
		t.Fatalf("prepare remote transport: %v", transportErr)
	}
	defer transport.rollback()

	if transport.url != "http://proxy-1/stream/v3/session-origin-hls/master.m3u8" {
		t.Fatalf("manifest url = %q, want the credential-free proxy manifest", transport.url)
	}
	assertNoPlaybackCredentialV3(t, transport.url)

	card, ok := grants.cards["session-origin-hls"]
	if !ok {
		t.Fatal("no grant was written; the proxy cannot relay this transcode")
	}
	if card.TranscodeNodeURL != node.URL {
		t.Fatalf("grant transcode node = %q, want %q", card.TranscodeNodeURL, node.URL)
	}
	if card.TranscodeTransportID != transport.transportID {
		t.Fatalf("grant transport id = %q, want the plan-scoped transport %q", card.TranscodeTransportID, transport.transportID)
	}
}

// The escalation exists because header-authenticated identity work had no
// executor. Authorized origins give it one, so an attempt with a proxy
// available must keep the route the planner chose.
func TestEscalateRefusedProgressiveRemuxV3SkipsEscalationWhenOriginsHaveAProxy(t *testing.T) {
	handler, input, result := escalationFixtureV3(t, true)
	handler.NodePlanner = &recordingNodePlannerV3{plan: nodepool.Plan{ProxyNode: &nodepool.Node{URL: "http://proxy-1"}}}

	escalated, transportErr := handler.escalateRefusedProgressiveRemuxV3(context.Background(), authorizedOriginsModeV3(), func() playback.PlannerInputV3 { return input }, result)
	if transportErr != nil {
		t.Fatalf("escalation error = %#v", transportErr)
	}
	if escalated.Plan == nil || escalated.Plan.Delivery != playback.DeliveryRemuxProgressiveV3 {
		t.Fatalf("escalated delivery = %#v, want the planned progressive remux left alone", escalated.Plan)
	}
}

// With origins negotiated but no proxy in the pool the refusal is back, so the
// escalation must be too — otherwise the attempt plans a route nothing can run.
func TestEscalateRefusedProgressiveRemuxV3StillEscalatesWithoutAnyProxyOrigin(t *testing.T) {
	handler, input, result := escalationFixtureV3(t, true)
	handler.NodePlanner = &recordingNodePlannerV3{}

	escalated, transportErr := handler.escalateRefusedProgressiveRemuxV3(context.Background(), authorizedOriginsModeV3(), func() playback.PlannerInputV3 { return input }, result)
	if transportErr != nil {
		t.Fatalf("escalation error = %#v", transportErr)
	}
	if escalated.Plan == nil || escalated.Plan.Delivery != playback.DeliveryRemuxHLSV3 {
		t.Fatalf("escalated delivery = %#v, want %q", escalated.Plan, playback.DeliveryRemuxHLSV3)
	}
}

// assertNoPlaybackCredentialV3 fails when a published URL carries any playback
// credential — the whole promise of the mode, on the proxy origin as much as on
// the API one.
func assertNoPlaybackCredentialV3(t *testing.T, rawURL string) {
	t.Helper()
	if strings.Contains(rawURL, streamTokenParam+"=") || strings.Contains(rawURL, "/stream/direct/") ||
		strings.Contains(rawURL, "/stream/remux/") || strings.Contains(rawURL, "/stream/transcode/") {
		t.Fatalf("URL %q carries a playback credential", rawURL)
	}
}

package jellycompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// stubRecipeNodeStore is an in-memory stand-in for the control-plane recipe
// store (*noderecipe.Store) so the round-trip tests can assert what central
// wrote and let the "node" read it back without Redis.
type stubRecipeNodeStore struct {
	cards map[string]playback.RecipeCard
}

func (s *stubRecipeNodeStore) Put(_ context.Context, sessionID string, card playback.RecipeCard) error {
	if s.cards == nil {
		s.cards = make(map[string]playback.RecipeCard)
	}
	s.cards[sessionID] = card
	return nil
}

func (s *stubRecipeNodeStore) Get(sessionID string) (playback.RecipeCard, bool) {
	card, ok := s.cards[sessionID]
	return card, ok
}

func (s *stubRecipeNodeStore) Delete(_ context.Context, sessionID string) error {
	delete(s.cards, sessionID)
	return nil
}

// localSessionRegistry is a GetSession + RegisterReconstructed double for
// exercising TranscodeManager reconstruction from the jellycompat package
// (the playback package's own fake is not exported across packages).
type localSessionRegistry struct {
	sessions map[string]*playback.Session
}

func (r *localSessionRegistry) GetSession(id string) (*playback.Session, error) {
	if s, ok := r.sessions[id]; ok {
		return s, nil
	}
	return nil, playback.ErrSessionNotFound
}

func (r *localSessionRegistry) RegisterReconstructed(s *playback.Session) *playback.Session {
	if r.sessions == nil {
		r.sessions = map[string]*playback.Session{}
	}
	if existing, ok := r.sessions[s.ID]; ok {
		return existing
	}
	r.sessions[s.ID] = s
	return s
}

func (r *localSessionRegistry) RegisterReconstructedWithLimits(_ context.Context, s *playback.Session) (*playback.Session, error) {
	return r.RegisterReconstructed(s), nil
}

// newRemoteTranscodeHandler builds a handler wired for the remote (offloaded)
// transcode path: a fake node that accepts /transcode/start, an upstream
// session carrying the native identity used to build the recipe, and a stub
// recipe store standing in for the control-plane Redis store.
func newRemoteTranscodeHandler(t *testing.T, nodeURL string, recipeStore *stubRecipeNodeStore) (*PlaybackHandler, *testCompatSessionManager, *PlaybackSessionStore) {
	t.Helper()

	playbackStore := NewPlaybackSessionStore(time.Hour, nil)
	sessionMgr := &testCompatSessionManager{
		sessions: map[string]*playback.Session{
			"upstream-1": {
				ID:               "upstream-1",
				UserID:           7,
				ProfileID:        "profile-1",
				MediaFileID:      42,
				PlayMethod:       playback.PlayTranscode,
				BasePlayMethod:   playback.PlayTranscode,
				TranscodeNodeURL: nodeURL,
			},
		},
	}
	handler := &PlaybackHandler{
		playbackStore:   playbackStore,
		sessionMgr:      sessionMgr,
		fileResolver:    testCompatFileResolver{file: &models.MediaFile{ID: 42, FilePath: "/media/movie.mkv"}},
		tm:              playback.NewTranscodeManager(),
		JWTSecret:       "test-secret",
		RecipeNodeStore: recipeStore,
	}
	return handler, sessionMgr, playbackStore
}

// fakeTranscodeNode returns an httptest server that accepts /transcode/start
// with 202 Accepted (the only response the start path inspects) and records
// the request body it received.
func fakeTranscodeNode(t *testing.T, received *transcodenode.TranscodeStartRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcode/start" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if received != nil {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, received)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testRemoteTranscodeSource() PlaybackMediaSource {
	codec := NewResourceIDCodec()
	version := testCompatVersion()
	source := testCompatSource(codec, version)
	return source
}

// TestStartRemoteTranscodeReportsConfirmedExecutor verifies remote execution facts are recorded.
func TestStartRemoteTranscodeReportsConfirmedExecutor(t *testing.T) {
	recipeStore := &stubRecipeNodeStore{}
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcode/start" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(transcodenode.TranscodeStartResponse{HWAccel: "qsv"})
	}))
	t.Cleanup(node.Close)
	handler, sessionMgr, playbackStore := newRemoteTranscodeHandler(t, node.URL, recipeStore)
	playbackStore.Put(PlaybackSession{ID: "play-1", UpstreamSessionID: "upstream-1"})

	if err := handler.startRemoteTranscode(context.Background(), "play-1", "upstream-1", testRemoteTranscodeSource(), &models.MediaFile{ID: 42, FilePath: "/media/movie.mkv"}, 0, node.URL); err != nil {
		t.Fatalf("startRemoteTranscode: %v", err)
	}

	session := sessionMgr.sessions["upstream-1"]
	if session.TargetVideoCodec != compatTargetVideoCodec || session.TargetAudioCodec != compatTargetAudioCodec || session.TranscodeHWAccel != "qsv" || session.ToneMapMode != "" {
		t.Fatalf("reported remote execution facts are incomplete: %+v", session)
	}
	card, ok := recipeStore.Get("upstream-1")
	if !ok || card.HWAccel != "qsv" {
		t.Fatalf("persisted remote recipe executor = %q, found=%v", card.HWAccel, ok)
	}
}

func TestStartRemoteTranscodePreservesRequestedExecutorForLegacyEmptyResponse(t *testing.T) {
	recipeStore := &stubRecipeNodeStore{}
	node := fakeTranscodeNode(t, nil)
	handler, sessionMgr, playbackStore := newRemoteTranscodeHandler(t, node.URL, recipeStore)
	handler.HWAccel = tonemap.BackendQSV
	playbackStore.Put(PlaybackSession{ID: "play-1", UpstreamSessionID: "upstream-1"})

	if err := handler.startRemoteTranscode(context.Background(), "play-1", "upstream-1", testRemoteTranscodeSource(), &models.MediaFile{ID: 42, FilePath: "/media/movie.mkv"}, 0, node.URL); err != nil {
		t.Fatalf("startRemoteTranscode: %v", err)
	}

	session := sessionMgr.sessions["upstream-1"]
	if session.TranscodeHWAccel != tonemap.BackendQSV {
		t.Fatalf("reported executor = %q, want %q", session.TranscodeHWAccel, tonemap.BackendQSV)
	}
	card, ok := recipeStore.Get("upstream-1")
	if !ok || card.HWAccel != tonemap.BackendQSV {
		t.Fatalf("persisted executor = %q, found=%v", card.HWAccel, ok)
	}
}

// TestStartRemoteToneMapReportsConfirmedExecutorAndFallback verifies remote fallback facts remain accurate.
func TestStartRemoteToneMapReportsConfirmedExecutorAndFallback(t *testing.T) {
	tests := []struct {
		name           string
		rejectHardware bool
		rejectSoftware bool
		wantHWAccel    string
		wantMode       tonemap.Mode
		wantErrParts   []string
	}{
		{name: "hardware", wantHWAccel: tonemap.BackendQSV, wantMode: tonemap.ModeHardware},
		{name: "software fallback", rejectHardware: true, wantHWAccel: playback.HWAccelNone, wantMode: tonemap.ModeSoftware},
		{name: "software fallback rejected", rejectHardware: true, rejectSoftware: true, wantErrParts: []string{"initial status 422", "retry status 503"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := tonemap.Capabilities{
				{Mode: tonemap.ModeHardware, Backend: tonemap.BackendQSV, Filter: tonemap.HardwareFilterVAAPI, SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ}},
				{Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390, SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ}},
			}
			node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hw-capabilities":
					writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: capabilities})
				case "/transcode/start":
					var request transcodenode.TranscodeStartRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode start request: %v", err)
						http.Error(w, "invalid transcode start request", http.StatusBadRequest)
						return
					}
					if test.rejectHardware && request.ToneMapMode == tonemap.ModeHardware {
						w.WriteHeader(http.StatusUnprocessableEntity)
						return
					}
					if test.rejectSoftware && request.ToneMapMode == tonemap.ModeSoftware {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(transcodenode.TranscodeStartResponse{HWAccel: request.HWAccel, ToneMapMode: request.ToneMapMode})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(node.Close)

			recipeStore := &stubRecipeNodeStore{}
			handler, sessionMgr, playbackStore := newRemoteTranscodeHandler(t, node.URL, recipeStore)
			handler.SettingsRepo = stubSettingsReader{values: map[string]string{
				config.PlaybackTranscodeHardwareToneMapSettingKey: "true",
				config.PlaybackTranscodeSoftwareToneMapSettingKey: "true",
			}}
			playbackStore.Put(PlaybackSession{ID: "play-1", UpstreamSessionID: "upstream-1"})
			file := &models.MediaFile{ID: 42, FilePath: "/media/movie.mkv", HDR: true, VideoTracks: []models.VideoTrack{{Codec: "hevc", VideoRangeType: "HDR10", ColorTransfer: "smpte2084", ColorPrimaries: "bt2020", ColorSpace: "bt2020nc", BitDepth: 10}}}

			err := handler.startRemoteTranscode(context.Background(), "play-1", "upstream-1", testRemoteTranscodeSource(), file, 0, node.URL)
			if len(test.wantErrParts) > 0 {
				if err == nil {
					t.Fatal("startRemoteTranscode succeeded after both node dispatches were rejected")
				}
				for _, want := range test.wantErrParts {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("startRemoteTranscode error = %q, want %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("startRemoteTranscode: %v", err)
			}

			session := sessionMgr.sessions["upstream-1"]
			if session.TranscodeHWAccel != test.wantHWAccel || session.ToneMapMode != test.wantMode {
				t.Fatalf("reported execution facts = hw %q tone_map %q, want %q %q", session.TranscodeHWAccel, session.ToneMapMode, test.wantHWAccel, test.wantMode)
			}
			card, ok := recipeStore.Get("upstream-1")
			if !ok || card.HWAccel != test.wantHWAccel || card.ToneMapMode != test.wantMode {
				t.Fatalf("persisted execution facts = hw %q tone_map %q, found=%v", card.HWAccel, card.ToneMapMode, ok)
			}
		})
	}
}

// TestStartRemoteTranscode_NodeRestartReconstruct covers the node-restart leg:
// central persists the recipe to the control-plane store at start, the node
// loses all in-memory state, and a reconstruct fetch via the store's Get
// rebuilds the same recipe the node would re-spawn ffmpeg from (rather than a
// Redis miss → 404).
func TestStartRemoteTranscode_NodeRestartReconstruct(t *testing.T) {
	recipeStore := &stubRecipeNodeStore{}
	node := fakeTranscodeNode(t, nil)
	handler, _, playbackStore := newRemoteTranscodeHandler(t, node.URL, recipeStore)

	playbackStore.Put(PlaybackSession{
		ID:                 "play-1",
		CompatToken:        "token-1",
		UpstreamSessionID:  "upstream-1",
		UpstreamPlayMethod: "transcode",
		MediaSources:       []PlaybackMediaSource{testRemoteTranscodeSource()},
	})

	source := testRemoteTranscodeSource()
	err := handler.startRemoteTranscode(
		context.Background(),
		"play-1",
		"upstream-1",
		source,
		&models.MediaFile{ID: 42, FilePath: "/media/movie.mkv"},
		0,
		node.URL,
	)
	if err != nil {
		t.Fatalf("startRemoteTranscode: %v", err)
	}

	// Node side: it lost its session map; reconstruct reads the recipe central
	// wrote keyed by the upstream session id.
	card, ok := recipeStore.Get("upstream-1")
	if !ok {
		t.Fatal("expected recipe in control-plane store after remote start")
	}
	if card.SessionID != "upstream-1" {
		t.Fatalf("recipe SessionID = %q, want upstream-1", card.SessionID)
	}
	if card.TranscodeNodeURL != node.URL {
		t.Fatalf("recipe TranscodeNodeURL = %q, want %q", card.TranscodeNodeURL, node.URL)
	}
	// The recipe must be reconstruct-complete: the node refuses to rebuild from a
	// recipe lacking encode parameters (server.go reconstructFromToken).
	if card.SegmentDuration <= 0 || card.TargetCodecVideo == "" {
		t.Fatalf("recipe not reconstruct-complete: seg=%d video=%q", card.SegmentDuration, card.TargetCodecVideo)
	}
	if card.UserID != 7 || card.ProfileID != "profile-1" || card.MediaFileID != 42 {
		t.Fatalf("recipe identity wrong: user=%d profile=%q file=%d", card.UserID, card.ProfileID, card.MediaFileID)
	}
	if card.InputPath != "/media/movie.mkv" {
		t.Fatalf("recipe InputPath = %q, want /media/movie.mkv", card.InputPath)
	}
}

// TestStartRemoteTranscode_CentralRestartReconstruct covers the central-restart
// leg: the recipe lives in the compat store (PlaybackSession.Recipe), the
// in-memory native session is gone, and LoadOrReconstructSession rebuilds the
// session from the stored recipe rather than returning SessionMissing (which a
// remote segment serve renders as a 404).
func TestStartRemoteTranscode_CentralRestartReconstruct(t *testing.T) {
	recipeStore := &stubRecipeNodeStore{}
	node := fakeTranscodeNode(t, nil)
	handler, _, playbackStore := newRemoteTranscodeHandler(t, node.URL, recipeStore)

	playbackStore.Put(PlaybackSession{
		ID:                 "play-1",
		CompatToken:        "token-1",
		UpstreamSessionID:  "upstream-1",
		UpstreamPlayMethod: "transcode",
		MediaSources:       []PlaybackMediaSource{testRemoteTranscodeSource()},
	})

	source := testRemoteTranscodeSource()
	if err := handler.startRemoteTranscode(context.Background(), "play-1", "upstream-1", source, &models.MediaFile{ID: 42, FilePath: "/media/movie.mkv"}, 0, node.URL); err != nil {
		t.Fatalf("startRemoteTranscode: %v", err)
	}

	// The compat store must now carry the recipe so central can reconstruct.
	stored, ok := playbackStore.Get("play-1")
	if !ok {
		t.Fatal("expected compat session")
	}
	if !stored.TranscodeStarted {
		t.Fatal("expected TranscodeStarted=true after remote start")
	}
	if stored.Recipe == nil {
		t.Fatal("expected PlaybackSession.Recipe persisted after remote start")
	}

	// Simulate a central restart: the in-memory native session map is empty, so
	// GetSession misses. The recipe from the compat store must let
	// LoadOrReconstructSession rebuild the session rather than 404.
	reg := &localSessionRegistry{}
	tm := playback.NewTranscodeManager()
	tm.Sessions = reg
	tm.JWTSecretFn = func() string { return "test-secret" }

	session, status := tm.LoadOrReconstructSession(
		context.Background(),
		reg.GetSession,
		"upstream-1",
		stored.Recipe.UserID,
		stored.Recipe,
	)
	if status != playback.SessionLoaded || session == nil {
		t.Fatalf("LoadOrReconstructSession status=%v session=%v, want reconstructed", status, session)
	}
	if session.ID != "upstream-1" || session.UserID != 7 || session.MediaFileID != 42 {
		t.Fatalf("reconstructed session wrong: %+v", session)
	}
	if session.TranscodeNodeURL != node.URL {
		t.Fatalf("reconstructed TranscodeNodeURL = %q, want %q", session.TranscodeNodeURL, node.URL)
	}
}

// TestStartRemoteTranscode_UpdateFailureRollsBackNode asserts the local-path
// rollback is mirrored: a compat-store Update failure closes the already-started
// node ffmpeg (DELETE /transcode/{id}) so it isn't leaked.
func TestStartRemoteTranscode_UpdateFailureRollsBackNode(t *testing.T) {
	recipeStore := &stubRecipeNodeStore{}
	deleted := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodDelete:
			deleted <- r.URL.Path
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	handler, _, _ := newRemoteTranscodeHandler(t, srv.URL, recipeStore)
	// No play session put → Update("missing") fails, triggering rollback.
	source := testRemoteTranscodeSource()
	err := handler.startRemoteTranscode(context.Background(), "missing", "upstream-1", source, &models.MediaFile{ID: 42, FilePath: "/media/movie.mkv"}, 0, srv.URL)
	if err == nil {
		t.Fatal("expected error when compat Update fails")
	}

	select {
	case path := <-deleted:
		if path != "/transcode/upstream-1" {
			t.Fatalf("rollback DELETE path = %q, want /transcode/upstream-1", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected rollback DELETE to the node after Update failure")
	}
}

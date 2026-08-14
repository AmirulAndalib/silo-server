package jellycompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// withCompatSession attaches a compat session carrying tok to req, so the
// ActiveEncodings ownership guard (CompatToken == session.Token) is satisfied.
func withCompatSession(req *http.Request, tok string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), compatSessionKey, &Session{Token: tok}))
}

func TestAudioSelectionChanged(t *testing.T) {
	selected := 2
	session := &PlaybackSession{
		MediaSources: []PlaybackMediaSource{
			{ID: "src-a", SelectedAudioStreamIndex: &selected},
			{ID: "src-b", SelectedAudioStreamIndex: nil},
		},
	}

	tests := []struct {
		name          string
		session       *PlaybackSession
		mediaSourceID string
		incoming      int
		want          bool
	}{
		{"same index on known source", session, "src-a", 2, false},
		{"different index on known source", session, "src-a", 3, true},
		{"nil current on known source", session, "src-b", 2, true},
		{"unknown media source id", session, "src-missing", 2, true},
		{"empty media source id uses first match", session, "", 2, false},
		{"nil session", nil, "src-a", 2, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := audioSelectionChanged(tc.session, tc.mediaSourceID, tc.incoming)
			if got != tc.want {
				t.Errorf("audioSelectionChanged(%q, %d) = %v, want %v", tc.mediaSourceID, tc.incoming, got, tc.want)
			}
		})
	}
}

func TestEnsureTranscodeSessionDoesNotHoldLifecycleLockWhileWaitingForManifest(t *testing.T) {
	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(mediaPath, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	startedMarker := filepath.Join(dir, "ffmpeg-started")
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	ffmpegScript := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    -filters) printf ' .S. zscale V->V\\n .S. tonemapx V->V\\n .S. sidedata V->V\\n'; exit 0;;\n" +
		"    -encoders) printf 'libx264\\n'; exit 0;;\n" +
		"  esac\n" +
		"done\n" +
		"eval \"last=\\\"\\${$#}\\\"\"\n" +
		"if [ \"$last\" = '-' ]; then exit 0; fi\n" +
		"touch " + startedMarker + "\n" +
		"sleep 30\n"
	if err := os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatal(err)
	}

	modifiedAt := info.ModTime()
	probeUpdatedAt := time.Now().UTC()
	file := &models.MediaFile{
		ID: 42, FilePath: mediaPath, FileSize: info.Size(), FileModifiedAt: &modifiedAt, FileHash: "hash", ProbeUpdatedAt: &probeUpdatedAt, HDR: true,
		VideoTracks: []models.VideoTrack{{
			Codec: "hevc", Profile: "Main 10", Width: 1920, Height: 1080, BitDepth: 10, PixelFormat: "yuv420p10le",
			VideoRange: "HDR10", ColorRange: "tv", ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
		}},
	}
	version := testCompatVersion()
	source := testCompatSource(NewResourceIDCodec(), version)
	playbackStore := NewPlaybackSessionStore(time.Hour, nil)
	playbackStore.Put(PlaybackSession{ID: "play-1", UpstreamSessionID: "upstream-1", UpstreamPlayMethod: "transcode", MediaSources: []PlaybackMediaSource{source}})
	handler := &PlaybackHandler{
		playbackStore: playbackStore,
		sessionMgr: &testCompatSessionManager{sessions: map[string]*playback.Session{
			"upstream-1": {ID: "upstream-1", UserID: 7, ProfileID: "profile-1", MediaFileID: file.ID, PlayMethod: playback.PlayTranscode},
		}},
		fileResolver: testCompatFileResolver{file: file},
		SettingsRepo: stubSettingsReader{values: map[string]string{config.PlaybackTranscodeSoftwareToneMapSettingKey: "true"}},
		TranscodeDir: dir,
		FFmpegPath:   ffmpegPath,
		HWAccel:      playback.HWAccelNone,
		tm:           playback.NewTranscodeManager(),
	}

	type ensureResult struct {
		session *playback.TranscodeSession
		err     error
	}
	firstResult := make(chan ensureResult, 1)
	go func() {
		session, ensureErr := handler.ensureTranscodeSession(context.Background(), "play-1", "upstream-1", source)
		firstResult <- ensureResult{session: session, err: ensureErr}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, statErr := os.Stat(startedMarker); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake FFmpeg did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	secondResult := make(chan ensureResult, 1)
	go func() {
		session, ensureErr := handler.ensureTranscodeSession(context.Background(), "play-1", "upstream-1", source)
		secondResult <- ensureResult{session: session, err: ensureErr}
	}()
	var second ensureResult
	select {
	case second = <-secondResult:
	case <-time.After(time.Second):
		writeCompatStartupManifest(t, filepath.Join(dir, "upstream-1"))
		first := <-firstResult
		<-secondResult
		if first.session != nil {
			handler.tm.CloseTranscodeSession("upstream-1", "")
		}
		t.Fatal("concurrent manifest request blocked behind the lifecycle lock")
	}
	if second.err != nil || second.session == nil {
		t.Fatalf("concurrent ensure result = session %p, error %v", second.session, second.err)
	}

	writeCompatStartupManifest(t, filepath.Join(dir, "upstream-1"))
	first := <-firstResult
	if first.err != nil || first.session == nil {
		t.Fatalf("initial ensure result = session %p, error %v", first.session, first.err)
	}
	if first.session != second.session {
		t.Fatalf("concurrent ensure returned a different transcode session: first=%p second=%p", first.session, second.session)
	}
	handler.tm.CloseTranscodeSession("upstream-1", "")
}

func writeCompatStartupManifest(t *testing.T, outputDir string) {
	t.Helper()
	for _, name := range []string{"seg_00000.m4s", "seg_00001.m4s", "seg_00002.m4s"} {
		if err := os.WriteFile(filepath.Join(outputDir, name), []byte("segment"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXTINF:2,\nseg_00000.m4s\n#EXTINF:2,\nseg_00001.m4s\n#EXTINF:2,\nseg_00002.m4s\n"
	if err := os.WriteFile(filepath.Join(outputDir, "stream.m3u8"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureTranscodeSessionGivesSoftwareFallbackFreshManifestBudget(t *testing.T) {
	previousTimeout := compatManifestStartupTimeout
	compatManifestStartupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { compatManifestStartupTimeout = previousTimeout })

	ffmpegPath := filepath.Join(t.TempDir(), "fallback-ffmpeg.sh")
	script := "#!/bin/sh\n" +
		"case \"$*\" in *tonemap_vaapi*) sleep 30; exit 0;; esac\n" +
		"out=\"\"\n" +
		"for arg in \"$@\"; do case \"$arg\" in *.m3u8) out=\"$(dirname \"$arg\")\";; esac; done\n" +
		"mkdir -p \"$out\"\n" +
		"for name in seg_00000.m4s seg_00001.m4s seg_00002.m4s; do printf segment > \"$out/$name\"; done\n" +
		"printf '#EXTM3U\\n#EXT-X-TARGETDURATION:2\\n#EXT-X-MEDIA-SEQUENCE:0\\n#EXTINF:2,\\nseg_00000.m4s\\n#EXTINF:2,\\nseg_00001.m4s\\n#EXTINF:2,\\nseg_00002.m4s\\n' > \"$out/stream.m3u8\"\n" +
		"sleep 30\n"
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	file := &models.MediaFile{ID: 42, FilePath: filepath.Join(t.TempDir(), "movie.mkv"), HDR: true, VideoTracks: []models.VideoTrack{{
		Codec: "hevc", Profile: "Main 10", BitDepth: 10, VideoRange: "HDR10",
		ColorRange: "tv", ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
	}}}
	if err := os.WriteFile(file.FilePath, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	modifiedAt := info.ModTime()
	file.FileSize = info.Size()
	file.FileModifiedAt = &modifiedAt
	version := testCompatVersion()
	version.FileID = file.ID
	version.VideoTracks = file.VideoTracks
	source := testCompatSource(NewResourceIDCodec(), version)
	store := NewPlaybackSessionStore(time.Hour, nil)
	store.Put(PlaybackSession{ID: "play-1", UpstreamSessionID: "upstream-1", MediaSources: []PlaybackMediaSource{source}})
	handler := &PlaybackHandler{
		playbackStore: store,
		sessionMgr: &testCompatSessionManager{sessions: map[string]*playback.Session{
			"upstream-1": {ID: "upstream-1", UserID: 7, ProfileID: "profile-1", MediaFileID: file.ID, PlayMethod: playback.PlayTranscode},
		}},
		fileResolver: testCompatFileResolver{file: file},
		SettingsRepo: stubSettingsReader{values: map[string]string{
			config.PlaybackTranscodeHardwareToneMapSettingKey: "true",
			config.PlaybackTranscodeSoftwareToneMapSettingKey: "true",
		}},
		TranscodeDir: t.TempDir(), FFmpegPath: ffmpegPath, HWAccel: tonemap.BackendQSV,
		tm: playback.NewTranscodeManager(),
		compatToneMapProbe: func(context.Context, string, string, string) (tonemap.Capabilities, error) {
			return tonemap.Capabilities{
				{Mode: tonemap.ModeHardware, Backend: tonemap.BackendQSV, Filter: tonemap.HardwareFilterVAAPI, SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ}},
				{Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390, SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ}},
			}, nil
		},
	}

	session, err := handler.ensureTranscodeSession(context.Background(), "play-1", "upstream-1", source)
	if err != nil {
		t.Fatalf("ensureTranscodeSession() error = %v, want software fallback ready", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if opts := session.Opts(); opts.ToneMapMode != tonemap.ModeSoftware || opts.HWAccel != playback.HWAccelNone {
		t.Fatalf("fallback opts = mode %q hw %q, want software/none", opts.ToneMapMode, opts.HWAccel)
	}
}

func TestGenerateFullManifest_HLSVersionForResumeStartTag(t *testing.T) {
	cases := []struct {
		name        string
		fmp4        bool
		startOffset float64
		wantVersion string
		wantStart   bool
	}{
		{"ts no resume", false, 0, "#EXT-X-VERSION:3", false},
		{"ts with resume", false, 5.5, "#EXT-X-VERSION:6", true},
		{"fmp4 no resume", true, 0, "#EXT-X-VERSION:7", false},
		{"fmp4 with resume", true, 5.5, "#EXT-X-VERSION:7", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(generateFullManifest(60, 2, tc.fmp4, tc.startOffset))
			if !strings.Contains(got, tc.wantVersion+"\n") {
				t.Fatalf("missing %s; manifest:\n%s", tc.wantVersion, got)
			}
			hasStart := strings.Contains(got, "#EXT-X-START:")
			if hasStart != tc.wantStart {
				t.Fatalf("EXT-X-START presence = %v, want %v; manifest:\n%s", hasStart, tc.wantStart, got)
			}
		})
	}
}

func TestShouldGenerateCompatFullManifestBoundsSegmentCount(t *testing.T) {
	short := PlaybackMediaSource{Version: catalog.FileVersion{Duration: 100_000}}
	if !shouldGenerateCompatFullManifest(short, 2) {
		t.Fatal("historical 50,000-segment compatibility manifest should remain supported")
	}

	long := PlaybackMediaSource{Version: catalog.FileVersion{Duration: 1_000_000}}
	if shouldGenerateCompatFullManifest(long, 2) {
		t.Fatal("long compatibility playback should use FFmpeg's bounded real manifest")
	}
}

func TestCompatInitialTranscodePositionKeepsResumeNearRequestedSegment(t *testing.T) {
	short := PlaybackMediaSource{Version: catalog.FileVersion{Duration: 100_000}}
	seek, segment := compatInitialTranscodePosition(short, 2, 17.3)
	if seek != 17.3 || segment != 8 {
		t.Fatalf("bounded manifest position = (%v, %d), want (17.3, 8)", seek, segment)
	}

	long := PlaybackMediaSource{Version: catalog.FileVersion{Duration: 1_000_000}}
	seek, segment = compatInitialTranscodePosition(long, 2, 17.3)
	if seek != 17.3 || segment != 8 {
		t.Fatalf("real manifest position = (%v, %d), want (17.3, 8)", seek, segment)
	}
}

func TestBuildProxyRedirectURLRequestsSourceAlignedCompatManifest(t *testing.T) {
	h := &PlaybackHandler{JWTSecret: "test-secret"}
	redirectURL, err := h.buildProxyRedirectURL(
		"play-1",
		"upstream-1",
		string(playback.PlayTranscode),
		&models.MediaFile{FilePath: "/media/movie.mkv"},
		PlaybackMediaSource{},
		"http://transcode-1",
		0,
		&nodepool.Node{URL: "http://proxy-1"},
	)
	if err != nil {
		t.Fatalf("buildProxyRedirectURL: %v", err)
	}
	if !strings.HasSuffix(redirectURL, "/master.m3u8?"+playback.SourceTimelineQueryParam+"=1") {
		t.Fatalf("redirect URL = %q, want source-timeline opt-in", redirectURL)
	}
}

func TestBuildProxyRedirectURLCarriesAudioOnlyRemuxClaim(t *testing.T) {
	h := &PlaybackHandler{JWTSecret: "test-secret"}
	redirectURL, err := h.buildProxyRedirectURL(
		"play-1",
		"upstream-1",
		string(playback.PlayRemux),
		&models.MediaFile{FilePath: "/media/book.m4b", BaseType: "audiobook", CodecAudio: "aac"},
		PlaybackMediaSource{},
		"",
		0,
		&nodepool.Node{URL: "http://proxy-1"},
	)
	if err != nil {
		t.Fatalf("buildProxyRedirectURL: %v", err)
	}
	token := strings.TrimPrefix(redirectURL, "http://proxy-1/stream/remux/")
	claims, err := streamtoken.Verify(token, h.JWTSecret)
	if err != nil {
		t.Fatalf("verify redirect token: %v", err)
	}
	if !claims.AudioOnly {
		t.Fatalf("audio-only remux claim = false: %#v", claims)
	}
}

func TestRewriteManifest_PreservesPlaybackAndMediaSourceIDs(t *testing.T) {
	manifest := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:7",
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXTINF:2.000000,",
		"seg_00000.m4s",
		"#EXTINF:2.000000,",
		"stream.m3u8",
		"",
	}, "\n")

	got := string(rewriteManifest([]byte(manifest), "item-1", "play-1", "source-1"))

	if !strings.Contains(got, "#EXT-X-MAP:URI=\"/Videos/item-1/hls/play-1/init.mp4?MediaSourceId=source-1&PlaySessionId=play-1\"") {
		t.Fatalf("expected init segment to include media and playback session ids, got:\n%s", got)
	}
	if !strings.Contains(got, "/Videos/item-1/hls/play-1/seg_00000.m4s?MediaSourceId=source-1&PlaySessionId=play-1") {
		t.Fatalf("expected media segment to include media and playback session ids, got:\n%s", got)
	}
	if !strings.Contains(got, "/Videos/item-1/hls/play-1/stream.m3u8?MediaSourceId=source-1&PlaySessionId=play-1") {
		t.Fatalf("expected nested manifest to include media and playback session ids, got:\n%s", got)
	}
}

func TestEnsureUpstreamPlayback_ReplacesStaleUpstreamWhenRecipeMissing(t *testing.T) {
	store := NewPlaybackSessionStore(time.Hour, nil)
	store.Put(PlaybackSession{
		ID:                 "ps-1",
		CompatToken:        "tok",
		UpstreamSessionID:  "stale-upstream",
		UpstreamPlayMethod: "direct",
	})
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{}}
	h := &PlaybackHandler{
		playbackStore: store,
		sessionMgr:    mgr,
		tm:            playback.NewTranscodeManager(),
	}

	got, err := h.ensureUpstreamPlayback(
		context.Background(),
		&Session{Token: "tok", StreamAppUserID: 7, ProfileID: "profile-1"},
		"ps-1",
		PlaybackMediaSource{FileID: 42},
		"direct",
	)
	if err != nil {
		t.Fatalf("ensureUpstreamPlayback returned error: %v", err)
	}
	if got.UpstreamSessionID != "upstream-started" {
		t.Fatalf("UpstreamSessionID = %q, want fresh upstream session", got.UpstreamSessionID)
	}
	if mgr.startCalls != 1 {
		t.Fatalf("StartSession calls = %d, want 1", mgr.startCalls)
	}
	reloaded, ok := store.Get("ps-1")
	if !ok {
		t.Fatal("play session missing after upstream replacement")
	}
	if reloaded.UpstreamSessionID != "upstream-started" || reloaded.UpstreamPlayMethod != "direct" {
		t.Fatalf("store not updated with fresh upstream session: %+v", reloaded)
	}
}

// newActiveEncodingsHandler builds a PlaybackHandler literal directly (not
// NewPlaybackHandler, which touches the filesystem) with a transcode manager
// wired — teardown calls tm.CloseTranscodeSession and would nil-panic otherwise.
func newActiveEncodingsHandler(mgr *testCompatSessionManager) (*PlaybackHandler, *PlaybackSessionStore) {
	store := NewPlaybackSessionStore(time.Hour, nil)
	h := &PlaybackHandler{
		playbackStore: store,
		sessionMgr:    mgr,
		tm:            playback.NewTranscodeManager(),
	}
	return h, store
}

// TestHandleDeleteActiveEncodings_StopsTranscodeAndDeletesSession verifies the
// happy path: the upstream session is stopped and the compat play session is
// removed from the store, returning 204.
func TestHandleDeleteActiveEncodings_StopsTranscodeAndDeletesSession(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "tok"})

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings?PlaySessionId=ps-1", nil), "tok")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("ps-1"); ok {
		t.Fatal("play session should be deleted")
	}
	if len(mgr.stopCalls) != 1 || mgr.stopCalls[0] != "upstream-1" {
		t.Fatalf("expected StopSession(upstream-1); got %v", mgr.stopCalls)
	}
}

// TestTeardownPlaySession_DeletesNodeRecipe verifies the deliberate stop path
// drops the node recipe keyed by the upstream session id, so a buffered/retrying
// request after a node restart cannot resurrect ffmpeg for the stopped session.
func TestTeardownPlaySession_DeletesNodeRecipe(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	recipeStore := &stubRecipeNodeStore{cards: map[string]playback.RecipeCard{
		"upstream-1": {SessionID: "upstream-1"},
	}}
	h.RecipeNodeStore = recipeStore
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "tok"})

	playSession, ok := store.Get("ps-1")
	if !ok {
		t.Fatal("expected play session")
	}
	h.teardownPlaySession(context.Background(), playSession, nil, nil)

	if _, ok := recipeStore.Get("upstream-1"); ok {
		t.Fatal("node recipe should be deleted on deliberate teardown")
	}
	if len(mgr.stopCalls) != 1 || mgr.stopCalls[0] != "upstream-1" {
		t.Fatalf("expected StopSession(upstream-1); got %v", mgr.stopCalls)
	}
}

// TestHandleDeleteActiveEncodings_MissingPlaySessionIdReturns204 verifies a
// request with no PlaySessionId is a 204 no-op (no teardown).
func TestHandleDeleteActiveEncodings_MissingPlaySessionIdReturns204(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "tok"})

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings", nil), "tok")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("ps-1"); !ok {
		t.Fatal("unrelated play session must not be torn down")
	}
	if len(mgr.stopCalls) != 0 {
		t.Fatalf("expected no StopSession calls; got %v", mgr.stopCalls)
	}
}

// TestHandleDeleteActiveEncodings_UnknownPlaySessionReturns204 verifies an
// unknown PlaySessionId is a 204 no-op (idempotent "already gone" semantics).
func TestHandleDeleteActiveEncodings_UnknownPlaySessionReturns204(t *testing.T) {
	mgr := &testCompatSessionManager{}
	h, _ := newActiveEncodingsHandler(mgr)

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings?PlaySessionId=does-not-exist", nil), "tok")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if len(mgr.stopCalls) != 0 {
		t.Fatalf("expected no StopSession calls; got %v", mgr.stopCalls)
	}
}

// TestHandleDeleteActiveEncodings_CaseInsensitivePlaySessionId verifies a
// lowercase playSessionId key (as Wholphin sends) still resolves and tears down
// the session — the reason newCaseInsensitiveQuery is used.
func TestHandleDeleteActiveEncodings_CaseInsensitivePlaySessionId(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "tok"})

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings?playSessionId=ps-1", nil), "tok")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("ps-1"); ok {
		t.Fatal("lowercase playSessionId should still resolve and delete the session")
	}
}

// TestHandleDeleteActiveEncodings_ForeignPlaySessionNotTornDown proves the
// ownership guard: a caller whose token differs from the play session's
// CompatToken gets a uniform 204 no-op and does NOT tear down the foreign
// session (no cross-session IDOR teardown).
func TestHandleDeleteActiveEncodings_ForeignPlaySessionNotTornDown(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "owner"})

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings?PlaySessionId=ps-1", nil), "attacker")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("ps-1"); !ok {
		t.Fatal("foreign play session must not be torn down")
	}
	if len(mgr.stopCalls) != 0 {
		t.Fatalf("expected no StopSession calls; got %v", mgr.stopCalls)
	}
}

// TestHandleDeleteActiveEncodings_RealClientShape exercises the dominant real
// JellyCon call shape (DeviceId present alongside PlaySessionId): with a
// matching-token session the session is still torn down (DeviceId ignored).
func TestHandleDeleteActiveEncodings_RealClientShape(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "tok"})

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings?DeviceId=dev1&PlaySessionId=ps-1", nil), "tok")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("ps-1"); ok {
		t.Fatal("play session should be torn down when DeviceId accompanies a matching PlaySessionId")
	}
	if len(mgr.stopCalls) != 1 || mgr.stopCalls[0] != "upstream-1" {
		t.Fatalf("expected StopSession(upstream-1); got %v", mgr.stopCalls)
	}
}

// TestHandleDeleteActiveEncodings_NotYetStartedNotTornDown guards the early
// window between PlaybackInfo and the first manifest request, when the play
// session exists but UpstreamSessionID is still empty. A DELETE that lands then
// must be a 204 no-op that leaves the session in the store, so the pending
// manifest request still resolves (mirrors the Stopped report path). Removing
// the UpstreamSessionID == "" guard makes this test fail.
func TestHandleDeleteActiveEncodings_NotYetStartedNotTornDown(t *testing.T) {
	mgr := &testCompatSessionManager{}
	h, store := newActiveEncodingsHandler(mgr)
	store.Put(PlaybackSession{ID: "ps-1", CompatToken: "tok"})

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings?PlaySessionId=ps-1", nil), "tok")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("ps-1"); !ok {
		t.Fatal("not-yet-started play session must survive teardown so the pending manifest still resolves")
	}
	if len(mgr.stopCalls) != 0 {
		t.Fatalf("expected no StopSession calls; got %v", mgr.stopCalls)
	}
}

// TestRestartCompatTranscodeForAudioSelection_LocalRePersistsRecipe covers the
// integrated/single-box leg of an audio switch: the live ffmpeg is restarted on
// the new track, and the durable PlaybackSession.Recipe must be re-persisted so
// that a reconstruct after a central restart rebuilds ffmpeg from the NEWLY
// selected audio track rather than the stale original. Without the re-persist,
// Recipe.AudioTrackIndex keeps the original value and the stream silently
// resumes on the wrong language after a restart.
func TestRestartCompatTranscodeForAudioSelection_LocalRePersistsRecipe(t *testing.T) {
	codec := NewResourceIDCodec()
	version := testCompatVersion() // 1 video track, 2 audio tracks.

	// Initial source selects the first (main) audio track -> AudioTrackIndex 0.
	mainSource := testCompatSource(codec, version)
	mainSource.SelectedAudioStreamIndex = intPtr(len(version.VideoTracks)) // stream index 1 -> track 0.

	// Switch target selects the second (commentary) audio track -> AudioTrackIndex 1.
	commentarySource := testCompatSource(codec, version)
	commentarySource.SelectedAudioStreamIndex = intPtr(len(version.VideoTracks) + 1) // stream index 2 -> track 1.

	filePath := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(filePath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	playbackStore := NewPlaybackSessionStore(time.Hour, nil)
	playbackStore.Put(PlaybackSession{
		ID:                 "play-1",
		UpstreamSessionID:  "upstream-1",
		UpstreamPlayMethod: "transcode",
		MediaSources:       []PlaybackMediaSource{commentarySource},
	})

	sessionMgr := &testCompatSessionManager{
		sessions: map[string]*playback.Session{
			"upstream-1": {
				ID:             "upstream-1",
				UserID:         7,
				ProfileID:      "profile-1",
				MediaFileID:    version.FileID,
				PlayMethod:     playback.PlayTranscode,
				BasePlayMethod: playback.PlayTranscode,
			},
		},
	}

	handler := &PlaybackHandler{
		playbackStore: playbackStore,
		sessionMgr:    sessionMgr,
		fileResolver:  testCompatFileResolver{file: &models.MediaFile{ID: version.FileID, FilePath: filePath}},
		TranscodeDir:  t.TempDir(),
		FFmpegPath:    writeCompatTestFFmpeg(t),
		tm:            playback.NewTranscodeManager(),
	}

	// Start the live transcode on the main track and persist its initial recipe
	// (AudioTrackIndex 0), mirroring a normal play start.
	transcodeSession, err := handler.ensureTranscodeSession(context.Background(), "play-1", "upstream-1", mainSource)
	if err != nil {
		t.Fatalf("ensureTranscodeSession: %v", err)
	}
	t.Cleanup(func() { _ = transcodeSession.Close() })

	if got := transcodeSession.Opts().AudioTrackIndex; got != 0 {
		t.Fatalf("initial AudioTrackIndex = %d, want 0", got)
	}
	if initial, ok := playbackStore.Get("play-1"); !ok || initial.Recipe == nil {
		t.Fatal("expected initial recipe persisted after ensureTranscodeSession")
	} else if initial.Recipe.AudioTrackIndex != 0 {
		t.Fatalf("initial Recipe.AudioTrackIndex = %d, want 0", initial.Recipe.AudioTrackIndex)
	}

	playSession, ok := playbackStore.Get("play-1")
	if !ok {
		t.Fatal("expected play session")
	}

	// Switch audio to the commentary track via the LOCAL branch.
	restarted, err := handler.restartCompatTranscodeForAudioSelection(
		context.Background(),
		playSession,
		commentarySource,
		0,
	)
	if err != nil {
		t.Fatalf("restartCompatTranscodeForAudioSelection: %v", err)
	}
	if !restarted {
		t.Fatal("expected local transcode restart to report restarted=true")
	}

	// The live ffmpeg opts must reflect the new track...
	if got := transcodeSession.Opts().AudioTrackIndex; got != 1 {
		t.Fatalf("live AudioTrackIndex after switch = %d, want 1", got)
	}

	// ...and, crucially, the durable recipe must track it so a reconstruct after
	// a central restart rebuilds ffmpeg on the commentary track.
	updated, ok := playbackStore.Get("play-1")
	if !ok {
		t.Fatal("expected play session after audio switch")
	}
	if updated.Recipe == nil {
		t.Fatal("expected Recipe to remain persisted after local audio switch")
	}
	if updated.Recipe.AudioTrackIndex != 1 {
		t.Fatalf("Recipe.AudioTrackIndex = %d, want 1 (re-persisted to newly selected track)", updated.Recipe.AudioTrackIndex)
	}
}

// recordingSessionSyncer counts SyncNow calls and records the context state at
// call time, standing in for the reconciler's immediate-sync trigger.
type recordingSessionSyncer struct {
	calls           int
	lastCtxErr      error
	lastHadDeadline bool
}

func (s *recordingSessionSyncer) SyncNow(ctx context.Context) error {
	s.calls++
	s.lastCtxErr = ctx.Err()
	_, s.lastHadDeadline = ctx.Deadline()
	return nil
}

// TestHandleSessionPlayingStopped_TearsDownAndSyncsImmediately verifies the
// Stopped report path removes the compat session AND flushes the live-session
// snapshot right away, so the activity dashboard doesn't show a ghost stream
// until the next reconciler tick (issue #205).
func TestHandleSessionPlayingStopped_TearsDownAndSyncsImmediately(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	syncer := &recordingSessionSyncer{}
	h.SessionSyncer = syncer
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "tok"})

	body := strings.NewReader(`{"PlaySessionId":"ps-1"}`)
	// Cancel the request context up front to simulate the client dropping the
	// connection right after firing the stop report — the sync must still run.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := withCompatSession(httptest.NewRequest("POST", "/Sessions/Playing/Stopped", body).WithContext(ctx), "tok")
	rec := httptest.NewRecorder()
	h.HandleSessionPlayingStopped(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("ps-1"); ok {
		t.Fatal("play session should be deleted")
	}
	if len(mgr.stopCalls) != 1 || mgr.stopCalls[0] != "upstream-1" {
		t.Fatalf("expected StopSession(upstream-1); got %v", mgr.stopCalls)
	}
	if syncer.calls != 1 {
		t.Fatalf("SyncNow calls = %d; want 1", syncer.calls)
	}
	if syncer.lastCtxErr != nil {
		t.Fatalf("sync context canceled with request: %v", syncer.lastCtxErr)
	}
	if !syncer.lastHadDeadline {
		t.Fatal("sync context must carry a deadline so a stalled DB cannot pin the request goroutine")
	}
}

// TestHandleSessionPlayingStopped_UnknownSessionDoesNotSync verifies a stop
// report that tears nothing down doesn't trigger a sync round trip.
func TestHandleSessionPlayingStopped_UnknownSessionDoesNotSync(t *testing.T) {
	mgr := &testCompatSessionManager{}
	h, _ := newActiveEncodingsHandler(mgr)
	syncer := &recordingSessionSyncer{}
	h.SessionSyncer = syncer

	body := strings.NewReader(`{"PlaySessionId":"ps-missing"}`)
	req := withCompatSession(httptest.NewRequest("POST", "/Sessions/Playing/Stopped", body), "tok")
	rec := httptest.NewRecorder()
	h.HandleSessionPlayingStopped(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if syncer.calls != 0 {
		t.Fatalf("SyncNow calls = %d; want 0", syncer.calls)
	}
}

// TestHandleDeleteActiveEncodings_SyncsSessionsImmediately verifies the
// explicit encoder-teardown path also flushes the live-session snapshot.
func TestHandleDeleteActiveEncodings_SyncsSessionsImmediately(t *testing.T) {
	mgr := &testCompatSessionManager{sessions: map[string]*playback.Session{"upstream-1": {ID: "upstream-1"}}}
	h, store := newActiveEncodingsHandler(mgr)
	syncer := &recordingSessionSyncer{}
	h.SessionSyncer = syncer
	store.Put(PlaybackSession{ID: "ps-1", UpstreamSessionID: "upstream-1", CompatToken: "tok"})

	req := withCompatSession(httptest.NewRequest("DELETE", "/Videos/ActiveEncodings?PlaySessionId=ps-1", nil), "tok")
	rec := httptest.NewRecorder()
	h.HandleDeleteActiveEncodings(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, body = %s; want 204", rec.Code, rec.Body.String())
	}
	if syncer.calls != 1 {
		t.Fatalf("SyncNow calls = %d; want 1", syncer.calls)
	}
}

// TestEnsureUpstreamPlayback_SyncsOnNewSession verifies a fresh upstream
// session start flushes the live-session snapshot so the new stream appears in
// the activity dashboard immediately.
func TestEnsureUpstreamPlayback_SyncsOnNewSession(t *testing.T) {
	mgr := &testCompatSessionManager{}
	h, store := newActiveEncodingsHandler(mgr)
	syncer := &recordingSessionSyncer{}
	h.SessionSyncer = syncer
	store.Put(PlaybackSession{ID: "ps-1", CompatToken: "tok"})

	compatSession := &Session{Token: "tok", StreamAppUserID: 7, ProfileID: "prof-1"}
	source := PlaybackMediaSource{ID: "src-1", FileID: 42}
	playSession, err := h.ensureUpstreamPlayback(context.Background(), compatSession, "ps-1", source, "direct")
	if err != nil {
		t.Fatalf("ensureUpstreamPlayback: %v", err)
	}
	if playSession.UpstreamSessionID == "" {
		t.Fatal("expected upstream session to be started")
	}
	if syncer.calls != 1 {
		t.Fatalf("SyncNow calls = %d; want 1", syncer.calls)
	}

	// Re-entering with the same method reuses the session and must not sync again.
	if _, err := h.ensureUpstreamPlayback(context.Background(), compatSession, "ps-1", source, "direct"); err != nil {
		t.Fatalf("ensureUpstreamPlayback reuse: %v", err)
	}
	if syncer.calls != 1 {
		t.Fatalf("SyncNow calls after reuse = %d; want 1", syncer.calls)
	}
}

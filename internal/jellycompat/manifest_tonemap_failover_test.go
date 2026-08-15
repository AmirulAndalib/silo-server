package jellycompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

func TestHandleMasterManifestReplansAfterRemoteSoftwareToneMapFailure(t *testing.T) {
	hardware := tonemap.Capability{
		Mode: tonemap.ModeHardware, Backend: tonemap.BackendQSV, Filter: tonemap.HardwareFilterVAAPI,
		SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
	}
	software := tonemap.Capability{
		Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
		SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
	}

	var failedModes []tonemap.Mode
	var failedNodeCleaned atomic.Bool
	failedNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{hardware, software}})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			var request transcodenode.TranscodeStartRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode failed-node request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			failedModes = append(failedModes, request.ToneMapMode)
			if request.ToneMapMode == tonemap.ModeHardware {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			// The node accepted the software job but did not confirm its promised
			// output. The manifest path must stop that job before trying a sibling.
			writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{})
		case r.Method == http.MethodDelete && r.URL.Path == "/transcode/upstream-1":
			failedNodeCleaned.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(failedNode.Close)

	var fallbackModes []tonemap.Mode
	fallbackNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{hardware, software}})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			if !failedNodeCleaned.Load() {
				t.Error("fallback node started before failed node cleanup completed")
				w.WriteHeader(http.StatusConflict)
				return
			}
			var request transcodenode.TranscodeStartRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode fallback-node request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fallbackModes = append(fallbackModes, request.ToneMapMode)
			writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{HWAccel: request.HWAccel, ToneMapMode: request.ToneMapMode})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fallbackNode.Close)

	var staleCapabilityFetches atomic.Int32
	var staleStartRequests atomic.Int32
	staleNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			if staleCapabilityFetches.Add(1) <= 2 {
				// Capability inventory admits this sibling, but its fresh start-time
				// validation observes that software support has disappeared.
				writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{hardware, software}})
				return
			}
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{hardware}})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			staleStartRequests.Add(1)
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(staleNode.Close)

	handler, planner, sessionMgr, store, source := newManifestToneMapFailoverHandler(t, failedNode.URL, false, staleNode.URL, fallbackNode.URL)
	request := httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8?PlaySessionId=play-1&MediaSourceId="+source.ID, nil)
	request = request.WithContext(context.WithValue(request.Context(), compatSessionKey, &Session{Token: "compat-token", StreamAppUserID: 7, ProfileID: "profile-1"}))
	recorder := httptest.NewRecorder()

	handler.HandleMasterManifest(recorder, request)

	if recorder.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307; body = %s", recorder.Code, recorder.Body.String())
	}
	if got := failedModes; len(got) != 2 || got[0] != tonemap.ModeHardware || got[1] != tonemap.ModeSoftware {
		t.Fatalf("failed-node modes = %v, want [hardware software]", got)
	}
	if got := fallbackModes; len(got) != 1 || got[0] != tonemap.ModeSoftware {
		t.Fatalf("fallback-node modes = %v, want [software]", got)
	}
	if got := staleStartRequests.Load(); got != 0 {
		t.Fatalf("stale-capability node received %d start requests, want 0", got)
	}
	if location := recorder.Header().Get("Location"); !strings.HasPrefix(location, "https://proxy.example/stream/transcode/") {
		t.Fatalf("redirect location = %q, want proxy transcode URL", location)
	}
	if got := sessionMgr.sessions["upstream-1"].TranscodeNodeURL; got != fallbackNode.URL {
		t.Fatalf("session transcode node = %q, want %q", got, fallbackNode.URL)
	}
	stored, ok := store.Get("play-1")
	if !ok || stored.Recipe == nil || stored.Recipe.TranscodeNodeURL != fallbackNode.URL || stored.Recipe.ToneMapMode != tonemap.ModeSoftware {
		t.Fatalf("stored fallback recipe = %+v, found=%v", stored.Recipe, ok)
	}

	// Replanning the same session must have replaced its provisional reservation:
	// with a one-job cap, the failed node is available to another session while
	// the successful fallback node remains reserved by upstream-1.
	probe := planner.PlanSessionWith("reservation-probe", "", true, source.Version.Bitrate, func(node *nodepool.Node) bool {
		return node != nil && node.URL == failedNode.URL
	})
	if probe.TranscodeNode == nil || probe.TranscodeNode.URL != failedNode.URL {
		t.Fatalf("failed-node reservation was not released: %+v", probe)
	}
}

func TestHandleMasterManifestFallsBackToValidatedLocalSoftwareAfterRemoteFailures(t *testing.T) {
	previousTimeout := compatManifestStartupTimeout
	compatManifestStartupTimeout = time.Second
	t.Cleanup(func() { compatManifestStartupTimeout = previousTimeout })

	hardware := tonemap.Capability{
		Mode: tonemap.ModeHardware, Backend: tonemap.BackendQSV, Filter: tonemap.HardwareFilterVAAPI,
		SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
	}
	software := tonemap.Capability{
		Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
		SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
	}

	var firstModes []tonemap.Mode
	firstNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{hardware, software}})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			var request transcodenode.TranscodeStartRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode first-node request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			firstModes = append(firstModes, request.ToneMapMode)
			if request.ToneMapMode == tonemap.ModeHardware {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(firstNode.Close)

	var secondModes []tonemap.Mode
	secondNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{software}})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			var request transcodenode.TranscodeStartRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode second-node request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			secondModes = append(secondModes, request.ToneMapMode)
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(secondNode.Close)

	disabledHandler, disabledPlanner, _, _, disabledSource := newManifestToneMapFailoverHandler(t, firstNode.URL, false, secondNode.URL)
	disabledRequest := httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8?PlaySessionId=play-1&MediaSourceId="+disabledSource.ID, nil)
	disabledRequest = disabledRequest.WithContext(context.WithValue(disabledRequest.Context(), compatSessionKey, &Session{Token: "compat-token", StreamAppUserID: 7, ProfileID: "profile-1"}))
	disabledRecorder := httptest.NewRecorder()
	disabledHandler.HandleMasterManifest(disabledRecorder, disabledRequest)
	if disabledRecorder.Code != http.StatusBadGateway || !strings.Contains(disabledRecorder.Body.String(), `"Error":"TranscodeStartFailed"`) {
		t.Fatalf("disabled local fallback response = %d %s, want preserved 502 TranscodeStartFailed", disabledRecorder.Code, disabledRecorder.Body.String())
	}
	if probe := disabledPlanner.PlanSession("disabled-reservation-probe", "", true, disabledSource.Version.Bitrate); probe.TranscodeNode == nil {
		t.Fatal("remote reservations were not released after disabled local fallback")
	}
	firstModes = nil
	secondModes = nil

	handler, planner, sessionMgr, store, source := newManifestToneMapFailoverHandler(t, firstNode.URL, true, secondNode.URL)
	handler.compatToneMapProbe = func(context.Context, string, string, string) (tonemap.Capabilities, error) {
		return tonemap.Capabilities{hardware, software}, nil
	}
	handler.HWAccel = playback.HWAccelNone
	handler.TranscodeDir = t.TempDir()
	localHardwareMarker := filepath.Join(t.TempDir(), "local-hardware-attempted")
	handler.FFmpegPath = filepath.Join(t.TempDir(), "ffmpeg")
	ffmpegScript := "#!/bin/sh\n" +
		"case \"$*\" in *tonemap_vaapi*) touch " + localHardwareMarker + "; exit 41;; esac\n" +
		"out=\"\"\n" +
		"for arg in \"$@\"; do case \"$arg\" in *.m3u8) out=\"$(dirname \"$arg\")\";; esac; done\n" +
		"mkdir -p \"$out\"\n" +
		"for name in seg_00000.m4s seg_00001.m4s seg_00002.m4s; do printf segment > \"$out/$name\"; done\n" +
		"printf '#EXTM3U\\n#EXT-X-TARGETDURATION:2\\n#EXT-X-MEDIA-SEQUENCE:0\\n#EXTINF:2,\\nseg_00000.m4s\\n#EXTINF:2,\\nseg_00001.m4s\\n#EXTINF:2,\\nseg_00002.m4s\\n' > \"$out/stream.m3u8\"\n" +
		"sleep 30\n"
	if err := os.WriteFile(handler.FFmpegPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/Videos/item/master.m3u8?PlaySessionId=play-1&MediaSourceId="+source.ID, nil)
	request = request.WithContext(context.WithValue(request.Context(), compatSessionKey, &Session{Token: "compat-token", StreamAppUserID: 7, ProfileID: "profile-1"}))
	recorder := httptest.NewRecorder()

	handler.HandleMasterManifest(recorder, request)
	t.Cleanup(func() { handler.tm.CloseTranscodeSession("upstream-1", "") })

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if got := firstModes; len(got) != 2 || got[0] != tonemap.ModeHardware || got[1] != tonemap.ModeSoftware {
		t.Fatalf("first-node modes = %v, want [hardware software]", got)
	}
	if got := secondModes; len(got) != 1 || got[0] != tonemap.ModeSoftware {
		t.Fatalf("second-node modes = %v, want [software]", got)
	}
	if _, err := os.Stat(localHardwareMarker); !os.IsNotExist(err) {
		t.Fatalf("local hardware was attempted before software fallback: %v", err)
	}
	session := sessionMgr.sessions["upstream-1"]
	if session.TranscodeNodeURL != "" || session.TranscodeHWAccel != playback.HWAccelNone || session.ToneMapMode != tonemap.ModeSoftware {
		t.Fatalf("local fallback execution facts = %+v, want integrated software", session)
	}
	stored, ok := store.Get("play-1")
	if !ok || stored.Recipe == nil || stored.Recipe.TranscodeNodeURL != "" || stored.Recipe.ToneMapMode != tonemap.ModeSoftware {
		t.Fatalf("stored local fallback recipe = %+v, found=%v", stored.Recipe, ok)
	}

	probe := planner.PlanSession("reservation-probe", "", true, source.Version.Bitrate)
	if probe.TranscodeNode == nil {
		t.Fatal("remote reservations were not released before local fallback")
	}
}

func TestEnsureTranscodeSessionRequiredSoftwareReplacesExistingHardware(t *testing.T) {
	previousTimeout := compatManifestStartupTimeout
	compatManifestStartupTimeout = time.Second
	t.Cleanup(func() { compatManifestStartupTimeout = previousTimeout })

	hardware := tonemap.Capability{
		Mode: tonemap.ModeHardware, Backend: tonemap.BackendQSV, Filter: tonemap.HardwareFilterVAAPI,
		SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
	}
	software := tonemap.Capability{
		Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
		SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
	}
	handler, _, _, _, source := newManifestToneMapFailoverHandler(t, "http://unused.invalid", true)
	handler.compatToneMapProbe = func(context.Context, string, string, string) (tonemap.Capabilities, error) {
		return tonemap.Capabilities{hardware, software}, nil
	}
	handler.HWAccel = tonemap.BackendQSV
	handler.TranscodeDir = t.TempDir()
	hardwareMarker := filepath.Join(t.TempDir(), "hardware")
	softwareMarker := filepath.Join(t.TempDir(), "software")
	handler.FFmpegPath = filepath.Join(t.TempDir(), "ffmpeg")
	ffmpegScript := "#!/bin/sh\n" +
		"case \"$*\" in *tonemap_vaapi*) touch " + hardwareMarker + ";; *tonemapx*) touch " + softwareMarker + ";; esac\n" +
		"out=\"\"\n" +
		"for arg in \"$@\"; do case \"$arg\" in *.m3u8) out=\"$(dirname \"$arg\")\";; esac; done\n" +
		"mkdir -p \"$out\"\n" +
		"for name in seg_00000.m4s seg_00001.m4s seg_00002.m4s; do printf segment > \"$out/$name\"; done\n" +
		"printf '#EXTM3U\\n#EXT-X-TARGETDURATION:2\\n#EXT-X-MEDIA-SEQUENCE:0\\n#EXTINF:2,\\nseg_00000.m4s\\n#EXTINF:2,\\nseg_00001.m4s\\n#EXTINF:2,\\nseg_00002.m4s\\n' > \"$out/stream.m3u8\"\n" +
		"sleep 30\n"
	if err := os.WriteFile(handler.FFmpegPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := handler.ensureTranscodeSession(context.Background(), "play-1", "upstream-1", source)
	if err != nil {
		t.Fatalf("start hardware transcode: %v", err)
	}
	t.Cleanup(func() { handler.tm.CloseTranscodeSession("upstream-1", "") })
	if got := first.Opts().ToneMapMode; got != tonemap.ModeHardware {
		t.Fatalf("initial tone-map mode = %q, want hardware", got)
	}

	second, err := handler.ensureTranscodeSessionWithToneMapMode(
		context.Background(), "play-1", "upstream-1", source, tonemap.ModeSoftware,
	)
	if err != nil {
		t.Fatalf("force software transcode: %v", err)
	}
	if second == first {
		t.Fatal("required software fallback reused the existing hardware runtime")
	}
	if got := second.Opts().ToneMapMode; got != tonemap.ModeSoftware {
		t.Fatalf("replacement tone-map mode = %q, want software", got)
	}
	if first.IsRunning() {
		t.Fatal("replaced hardware runtime is still running")
	}
	if _, err := os.Stat(hardwareMarker); err != nil {
		t.Fatalf("hardware marker: %v", err)
	}
	if _, err := os.Stat(softwareMarker); err != nil {
		t.Fatalf("software marker: %v", err)
	}
}

func newManifestToneMapFailoverHandler(
	t *testing.T,
	failedNodeURL string,
	localFallback bool,
	fallbackNodeURLs ...string,
) (*PlaybackHandler, *nodepool.Planner, *testCompatSessionManager, *PlaybackSessionStore, PlaybackMediaSource) {
	t.Helper()

	mediaPath := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(mediaPath, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	modifiedAt := info.ModTime()
	probeUpdatedAt := time.Now().UTC()
	file := &models.MediaFile{
		ID: 42, FilePath: mediaPath, FileSize: info.Size(), FileModifiedAt: &modifiedAt, FileHash: "hash", ProbeUpdatedAt: &probeUpdatedAt,
		Bitrate: 8_000, HDR: true,
		VideoTracks: []models.VideoTrack{{
			Codec: "hevc", Profile: "Main 10", Width: 1920, Height: 1080, BitDepth: 10,
			VideoRange: "HDR10", VideoRangeType: "HDR10", ColorRange: "tv", PixelFormat: "yuv420p10le",
			ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
		}},
	}
	version := testCompatVersion()
	version.FileID = file.ID
	version.Resolution = "1080p"
	version.Bitrate = file.Bitrate
	version.HDR = true
	version.VideoTracks = file.VideoTracks
	source := testCompatSource(NewResourceIDCodec(), version)
	source.SupportsDirectPlay = false
	source.SupportsDirectStream = false

	store := NewPlaybackSessionStore(time.Hour, nil)
	store.Put(PlaybackSession{
		ID: "play-1", CompatToken: "compat-token", RouteItemID: "item", MediaSources: []PlaybackMediaSource{source},
		UpstreamSessionID: "upstream-1", UpstreamPlayMethod: "transcode",
	})
	sessionMgr := &testCompatSessionManager{sessions: map[string]*playback.Session{
		"upstream-1": {
			ID: "upstream-1", UserID: 7, ProfileID: "profile-1", MediaFileID: file.ID,
			PlayMethod: playback.PlayTranscode, BasePlayMethod: playback.PlayTranscode,
		},
	}}

	maxJobs := 1
	transcodes := nodepool.NewTranscodePool()
	nodes := []*nodepool.Node{{ID: 1, URL: failedNodeURL, Enabled: true, Healthy: true, MaxJobs: &maxJobs}}
	for index, nodeURL := range fallbackNodeURLs {
		nodes = append(nodes, &nodepool.Node{ID: index + 2, URL: nodeURL, Enabled: true, Healthy: true, MaxJobs: &maxJobs})
	}
	transcodes.SetNodes(nodes)
	proxies := nodepool.NewProxyPool()
	proxies.SetNodes([]*nodepool.Node{{ID: 3, URL: "https://proxy.example", Enabled: true, Healthy: true}})
	planner := nodepool.NewPlanner(proxies, transcodes)

	handler := &PlaybackHandler{
		playbackStore: store,
		sessionMgr:    sessionMgr,
		fileResolver:  testCompatFileResolver{file: file},
		NodePlanner:   planner,
		SettingsRepo: stubSettingsReader{values: map[string]string{
			config.PlaybackTranscodeHardwareToneMapSettingKey: "true",
			config.PlaybackTranscodeSoftwareToneMapSettingKey: "true",
			config.PlaybackLocalTranscodeFallbackSettingKey:   strconv.FormatBool(localFallback),
		}},
		JWTSecret: "test-secret",
		tm:        playback.NewTranscodeManager(),
	}
	return handler, planner, sessionMgr, store, source
}

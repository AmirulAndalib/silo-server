package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// enumeratingNodePlannerV3 is a SessionPlanner stub that also exposes pooled
// transcode node URLs, matching *nodepool.Planner's production shape.
type enumeratingNodePlannerV3 struct {
	staticNodePlannerV3
	urls []string
}

func (p enumeratingNodePlannerV3) TranscodeNodeURLs() []string { return p.urls }

// presetLocalRegistryV3 pins the handler's local transformation registry so
// tests never probe the machine's real ffmpeg.
func presetLocalRegistryV3(h *PlaybackHandler, registry *playback.TransformationRegistryV3) {
	h.v3RegistryOnce.Do(func() {})
	h.v3Registry = registry
}

// stableToneMapTransportFileV3 returns an HDR source whose filesystem and
// catalog revision facts agree, allowing transport tests to exercise the
// executor gate without weakening the production source-revision check.
func stableToneMapTransportFileV3(t *testing.T) *models.MediaFile {
	t.Helper()
	file := v3HandlerFixtureFile(t)
	info, err := os.Stat(file.FilePath)
	if err != nil {
		t.Fatalf("stat tone-map fixture: %v", err)
	}
	modifiedAt := info.ModTime()
	probeUpdatedAt := time.Now().UTC()
	file.FileSize = info.Size()
	file.FileModifiedAt = &modifiedAt
	file.FileHash = "tone-map-fixture"
	file.ProbeUpdatedAt = &probeUpdatedAt
	file.CodecVideo = "hevc"
	file.Resolution = "2160p"
	file.Bitrate = 32_000
	file.HDR = true
	file.VideoTracks[0] = models.VideoTrack{
		Codec: "hevc", Profile: "main 10", Level: 51, Width: 3840, Height: 2160,
		FrameRate: "24000/1001", Bitrate: 32_000, BitDepth: 10, PixelFormat: "yuv420p10le",
		VideoRange: "DolbyVision", VideoRangeType: "DOVIWithHDR10", DVProfile: 7, DVBLCompatID: 6,
		DVConfigPresent: true, DVBLCompatIDPresent: true, DVBLPresent: true, DVRPUPresent: true,
		ColorRange: "tv", ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
	}
	return file
}

func TestHLSPlanningRegistryV3UnionsPooledNodeCapabilities(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hw-capabilities" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, playback.HWAccelInfo{Transformations: []playback.TransformationV3{
			{Name: "video_to_h264", Executor: "server", RecipeVersion: "2"},
			{Name: "audio_to_aac", Executor: "server", RecipeVersion: "1"},
		}})
	}))
	defer remote.Close()

	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	presetLocalRegistryV3(handler, playback.NewTransformationRegistryV3([]playback.TransformationSpecV3{
		{Name: "video_to_h264", RecipeVersion: "2"},
		{Name: "audio_to_aac", RecipeVersion: "1"},
		{Name: "server_dv7_to_hdr10", RecipeVersion: "1"},
	}))
	handler.NodePlanner = enumeratingNodePlannerV3{urls: []string{remote.URL}}

	registry := handler.hlsPlanningRegistryV3(context.Background())
	if !registry.Available("video_to_h264") || !registry.Available("audio_to_aac") {
		t.Fatal("pooled node capabilities must widen the HLS planning registry")
	}
	if registry.Available("server_dv7_to_hdr10") {
		t.Fatal("transformations no node advertises must stay unavailable")
	}
	if handler.transformationRegistryV3(context.Background()).Available("video_to_h264") {
		t.Fatal("the local registry must not be widened by node capabilities")
	}
}

func TestHLSPlanningRegistryV3WithoutEnumeratorIsLocal(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	local := playback.NewTransformationRegistryV3([]playback.TransformationSpecV3{{Name: "audio_to_aac", RecipeVersion: "1", Available: true}})
	presetLocalRegistryV3(handler, local)
	handler.NodePlanner = staticNodePlannerV3{plan: nodepool.Plan{}}

	if registry := handler.hlsPlanningRegistryV3(context.Background()); registry != local {
		t.Fatal("a planner without node enumeration must plan from the local registry")
	}
}

func TestHLSPlanningRegistryV3EnablesValidatedLocalToneMapWithoutRestart(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	local := playback.NewTransformationRegistryV3([]playback.TransformationSpecV3{{
		Name: playback.TransformationHDRToSDRToneMapV3, RecipeVersion: playback.TransformationHDRToSDRToneMapRecipeVersionV3,
	}})
	presetLocalRegistryV3(handler, local)
	capabilities := tonemap.Capabilities{{
		Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
		SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
	}}
	handler.v3ToneMapProbe = func(context.Context, string, string, string) tonemap.Capabilities {
		return capabilities
	}
	settings := &mutablePlaybackSettingsV3{values: map[string]string{}}
	handler.SettingsRepo = settings

	if handler.hlsPlanningRegistryV3(context.Background()).Available(playback.TransformationHDRToSDRToneMapV3) {
		t.Fatal("disabled tone-map policy widened the local transformation registry")
	}
	settings.values["playback.transcode_software_tone_map_enabled"] = "true"
	if !handler.hlsPlanningRegistryV3(context.Background()).Available(playback.TransformationHDRToSDRToneMapV3) {
		t.Fatal("enabled validated tone-map executor was not available without restart")
	}
}

func TestLocalToneMapCapabilitiesV3RefreshesWhenPlaybackHardwareChanges(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	cfg := config.PlaybackConfig{FFmpegPath: "/opt/ffmpeg-a", HWAccel: "qsv", HWDevice: "/dev/dri/renderD128"}
	handler.PlaybackConfig = func() config.PlaybackConfig { return cfg }
	var calls []string
	handler.v3ToneMapProbe = func(_ context.Context, ffmpegPath, backend, device string) tonemap.Capabilities {
		calls = append(calls, ffmpegPath+"|"+backend+"|"+device)
		return tonemap.Capabilities{{Mode: tonemap.ModeHardware, Backend: backend, Filter: tonemap.HardwareFilterVAAPI, SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ}}}
	}

	for i := 0; i < 2; i++ {
		if got := handler.localToneMapCapabilitiesV3(context.Background()); len(got) != 1 || got[0].Backend != "qsv" {
			t.Fatalf("initial capabilities = %#v", got)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("unchanged fingerprint probed %d times, want 1", len(calls))
	}

	cfg.FFmpegPath = "/opt/ffmpeg-b"
	cfg.HWAccel = "vaapi"
	cfg.HWDevice = "/dev/dri/renderD129"
	if got := handler.localToneMapCapabilitiesV3(context.Background()); len(got) != 1 || got[0].Backend != "vaapi" {
		t.Fatalf("updated capabilities = %#v", got)
	}
	if len(calls) != 2 || calls[0] == calls[1] {
		t.Fatalf("probe fingerprints = %v, want one refresh for the live config change", calls)
	}
}

func TestHLSToneMapCapabilitiesV3FetchesNodesConcurrently(t *testing.T) {
	var active atomic.Int32
	var startedOnce sync.Once
	bothStarted := make(chan struct{})
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if active.Add(1) == 2 {
			startedOnce.Do(func() { close(bothStarted) })
		}
		<-release
		writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{{
			Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
			SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
		}}})
	}))
	defer remote.Close()

	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	handler.NodePlanner = enumeratingNodePlannerV3{urls: []string{remote.URL, remote.URL}}
	handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{config.PlaybackLocalTranscodeFallbackSettingKey: "false"}}
	result := make(chan tonemap.Capabilities, 1)
	go func() { result <- handler.hlsToneMapCapabilitiesV3(context.Background()) }()
	select {
	case <-bothStarted:
		close(release)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("node capability probes did not overlap")
	}
	if got := <-result; len(got) != 2 {
		t.Fatalf("aggregated capabilities = %#v, want both nodes", got)
	}
}

func TestHLSToneMapCapabilitiesV3HonorsSharedDeadline(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{{
			Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
			SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
		}}})
	}))
	defer fast.Close()

	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	handler.NodePlanner = enumeratingNodePlannerV3{urls: []string{slow.URL, slow.URL, fast.URL}}
	handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{config.PlaybackLocalTranscodeFallbackSettingKey: "false"}}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	got := handler.hlsToneMapCapabilitiesV3(ctx)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("capability aggregation took %s, want shared caller deadline", elapsed)
	}
	if len(got) != 1 || !got.Supports(tonemap.ModeSoftware, tonemap.SourcePQ) {
		t.Fatalf("aggregated capabilities = %#v, want the successful node retained", got)
	}
}

func TestRemoteTransformationsV3FailureCacheSplit(t *testing.T) {
	hits := 0
	fail := true
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, playback.HWAccelInfo{Transformations: []playback.TransformationV3{{Name: "audio_to_aac", Executor: "server", RecipeVersion: "1"}}})
	}))
	defer remote.Close()

	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	if _, err := handler.remoteTransformationsPlanningV3(context.Background(), remote.URL); err == nil {
		t.Fatal("fetch against a failing node must error")
	}
	if _, err := handler.remoteTransformationsPlanningV3(context.Background(), remote.URL); err == nil {
		t.Fatal("planning lookups must surface the memoized failure")
	}
	if hits != 1 {
		t.Fatalf("failing node was fetched %d times; planning must memoize the failure", hits)
	}

	// The transport path must fetch through the memoized failure: it may
	// have been produced by a planning deadline far shorter than this
	// path's budget, and rejecting the already-selected node on it would
	// fail a start a fresh fetch could still validate.
	fail = false
	transformations, err := handler.remoteTransformationsV3(context.Background(), remote.URL)
	if err != nil || len(transformations) != 1 {
		t.Fatalf("transport lookup must refetch through a memoized failure: %v %#v", err, transformations)
	}
	if hits != 2 {
		t.Fatalf("transport lookup fetched %d times, want 2", hits)
	}
	// The refetched success replaces the failure for planning too.
	if _, err := handler.remoteTransformationsPlanningV3(context.Background(), remote.URL); err != nil {
		t.Fatalf("planning lookup after transport success: %v", err)
	}
	if hits != 2 {
		t.Fatalf("cached success was refetched (%d hits)", hits)
	}
}

// In a heterogeneous pool, a plan that needs server transformations must be
// placed on a node advertising them even when load balancing prefers an
// incapable node, while transformation-free plans keep load-based selection.
func TestPlanNodeSessionV3PrefersCapabilityMatchingNode(t *testing.T) {
	capable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, playback.HWAccelInfo{Transformations: []playback.TransformationV3{
			{Name: "video_to_h264", Executor: "server", RecipeVersion: "2"},
			{Name: "audio_to_aac", Executor: "server", RecipeVersion: "1"},
		}})
	}))
	defer capable.Close()
	incapable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, playback.HWAccelInfo{})
	}))
	defer incapable.Close()

	transcodes := nodepool.NewTranscodePool()
	transcodes.SetNodes([]*nodepool.Node{
		{ID: 1, Name: "incapable", Type: nodepool.NodeTypeTranscode, URL: incapable.URL, Enabled: true, Healthy: true, ActiveJobs: 0},
		{ID: 2, Name: "capable", Type: nodepool.NodeTypeTranscode, URL: capable.URL, Enabled: true, Healthy: true, ActiveJobs: 5},
	})
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.JWTSecret = "test-secret"
	handler.NodePlanner = nodepool.NewPlanner(nodepool.NewProxyPool(), transcodes)

	plan := &playback.PlanV3{
		PlanID:   "plan:heterogeneous",
		Delivery: playback.DeliveryTranscodeHLSV3,
		Transformations: []playback.TransformationV3{
			{Name: "video_to_h264", Executor: "server", RecipeVersion: "2"},
			{Name: "audio_to_aac", Executor: "server", RecipeVersion: "1"},
		},
	}
	selected := handler.planNodeSessionV3(context.Background(), &playback.Session{ID: "session-hetero"}, playback.PlannerResultV3{Plan: plan, PlayMethod: playback.PlayTranscode})
	if selected.TranscodeNode == nil || selected.TranscodeNode.URL != capable.URL {
		t.Fatalf("capability-requiring plan selected %+v, want the capable node", selected.TranscodeNode)
	}

	free := &playback.PlanV3{PlanID: "plan:copy", Delivery: playback.DeliveryRemuxHLSV3, Transformations: []playback.TransformationV3{}}
	loadBased := handler.planNodeSessionV3(context.Background(), &playback.Session{ID: "session-copy"}, playback.PlannerResultV3{Plan: free, PlayMethod: playback.PlayRemux})
	if loadBased.TranscodeNode == nil || loadBased.TranscodeNode.URL != incapable.URL {
		t.Fatalf("transformation-free plan selected %+v, want load-based selection", loadBased.TranscodeNode)
	}
}

func TestPrepareTransportV3LocalFallbackRejectsUnavailableTransformations(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	presetLocalRegistryV3(handler, playback.NewTransformationRegistryV3([]playback.TransformationSpecV3{
		{Name: "video_to_h264", RecipeVersion: "2"},
		{Name: "audio_to_aac", RecipeVersion: "1"},
	}))
	plan := &playback.PlanV3{
		PlanID:   "plan:local-capability",
		Delivery: playback.DeliveryTranscodeHLSV3,
		Transformations: []playback.TransformationV3{
			{Name: "video_to_h264", Executor: "server", RecipeVersion: "2"},
			{Name: "audio_to_aac", Executor: "server", RecipeVersion: "1"},
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	_, transportErr := handler.prepareTransportV3(request, &playback.Session{ID: "session-local-capability"}, v3HandlerFixtureFile(t), playback.PlannerResultV3{Plan: plan, PlayMethod: playback.PlayTranscode, TargetVideoCodec: "h264", TargetAudioCodec: "aac"})
	if transportErr == nil || transportErr.reason != "transcode_node_capability_unavailable" || !transportErr.retryable {
		t.Fatalf("transport error = %#v", transportErr)
	}
}

func TestPrepareTransportV3AcceptsEveryValidatedLocalToneMapExecutor(t *testing.T) {
	tests := []struct {
		name           string
		mode           tonemap.Mode
		backend        string
		filter         string
		policy         tonemap.Policy
		settingKey     string
		configuredHW   string
		hardwareDevice string
	}{
		{name: "QSV", mode: tonemap.ModeHardware, backend: tonemap.BackendQSV, filter: tonemap.HardwareFilterVAAPI, policy: tonemap.PolicyHardwareOnly, settingKey: config.PlaybackTranscodeHardwareToneMapSettingKey, configuredHW: tonemap.BackendQSV, hardwareDevice: "/dev/dri/renderD128"},
		{name: "VAAPI", mode: tonemap.ModeHardware, backend: tonemap.BackendVAAPI, filter: tonemap.HardwareFilterVAAPI, policy: tonemap.PolicyHardwareOnly, settingKey: config.PlaybackTranscodeHardwareToneMapSettingKey, configuredHW: tonemap.BackendVAAPI, hardwareDevice: "/dev/dri/renderD128"},
		{name: "NVENC", mode: tonemap.ModeHardware, backend: tonemap.BackendNVENC, filter: tonemap.HardwareFilterCUDA, policy: tonemap.PolicyHardwareOnly, settingKey: config.PlaybackTranscodeHardwareToneMapSettingKey, configuredHW: tonemap.BackendNVENC, hardwareDevice: "0"},
		{name: "software", mode: tonemap.ModeSoftware, backend: tonemap.BackendSoftware, filter: tonemap.SoftwareFilterBT2390, policy: tonemap.PolicySoftwareOnly, settingKey: config.PlaybackTranscodeSoftwareToneMapSettingKey, configuredHW: playback.HWAccelNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := stableToneMapTransportFileV3(t)
			manager := playback.NewSessionManager(0, 0)
			handler := NewPlaybackHandler(manager)
			ffmpegPath := writePlaybackTestFFmpeg(t)
			transcodeDir := t.TempDir()
			handler.PlaybackConfig = func() config.PlaybackConfig {
				return config.PlaybackConfig{
					FFmpegPath: ffmpegPath, TranscodeDir: transcodeDir, TranscodeEnabled: true,
					HWAccel: test.configuredHW, HWDevice: test.hardwareDevice,
				}
			}
			handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{test.settingKey: "true"}}
			handler.v3ToneMapProbe = func(context.Context, string, string, string) tonemap.Capabilities {
				return tonemap.Capabilities{{
					Mode: test.mode, Backend: test.backend, Filter: test.filter,
					SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
				}}
			}
			presetLocalRegistryV3(handler, playback.NewTransformationRegistryV3([]playback.TransformationSpecV3{
				{Name: playback.TransformationVideoToH264V3, RecipeVersion: playback.TransformationVideoToH264RecipeVersionV3, Available: true},
				{Name: playback.TransformationAudioToAACV3, RecipeVersion: "1", Available: true},
				{Name: playback.TransformationHDRToSDRToneMapV3, RecipeVersion: playback.TransformationHDRToSDRToneMapRecipeVersionV3},
			}))
			plan := &playback.PlanV3{
				PlanID: "plan:local-tone-map:" + test.backend, Delivery: playback.DeliveryTranscodeHLSV3,
				Transformations: []playback.TransformationV3{
					{Name: playback.TransformationVideoToH264V3, Executor: playback.ExecutorServerV3, RecipeVersion: playback.TransformationVideoToH264RecipeVersionV3},
					{Name: playback.TransformationAudioToAACV3, Executor: playback.ExecutorServerV3, RecipeVersion: "1"},
					{Name: playback.TransformationHDRToSDRToneMapV3, Executor: playback.ExecutorServerV3, RecipeVersion: playback.TransformationHDRToSDRToneMapRecipeVersionV3},
				},
			}
			result := playback.PlannerResultV3{
				Plan: plan, PlayMethod: playback.PlayTranscode,
				TargetVideoCodec: "h264", TargetAudioCodec: "aac", TargetResolution: "2160p", TargetBitrateKbps: 32_000,
				SubtitleTrackIndex: -1, SubtitleTransportTrackIndex: -1,
				ToneMapPolicy: test.policy, ToneMapMode: test.mode, ToneMapSourceKind: tonemap.SourcePQ,
				ToneMapRecipeVersion:  playback.TransformationHDRToSDRToneMapRecipeVersionV3,
				ToneMapSourceRevision: tonemap.RevisionForFile(file),
			}
			session, err := manager.StartSession(7, "profile-1", file.ID, playback.PlayTranscode, true)
			if err != nil {
				t.Fatalf("start playback session: %v", err)
			}
			transport, transportErr := handler.prepareTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result)
			if transportErr != nil {
				t.Fatalf("prepare %s tone-map transport: %v", test.name, transportErr)
			}
			transport.commit()
			t.Cleanup(func() { handler.tm.CloseTranscodeSession(session.ID, "") })
			live := handler.tm.GetTranscodeSession(session.ID)
			if live == nil {
				t.Fatal("validated local tone-map transport was not registered")
			}
			opts := live.Opts()
			if opts.ToneMapMode != test.mode || opts.ToneMapFilter != test.filter || opts.HWAccel != test.configuredHW {
				t.Fatalf("executor opts = mode %q filter %q hw %q, want %q %q %q", opts.ToneMapMode, opts.ToneMapFilter, opts.HWAccel, test.mode, test.filter, test.configuredHW)
			}
		})
	}
}

func TestPlanRequiresServerTransformationsV3(t *testing.T) {
	if planRequiresServerTransformationsV3(nil) {
		t.Fatal("nil plan must not require server transformations")
	}
	clientOnly := &playback.PlanV3{Transformations: []playback.TransformationV3{{Name: playback.ClientDV7ToDV81V3, Executor: "client", RecipeVersion: "1"}}}
	if planRequiresServerTransformationsV3(clientOnly) {
		t.Fatal("client-executed transformations must not require a server executor")
	}
	server := &playback.PlanV3{Transformations: []playback.TransformationV3{{Name: "audio_to_aac", Executor: "server", RecipeVersion: "1"}}}
	if !planRequiresServerTransformationsV3(server) {
		t.Fatal("server-executed transformations must require executor validation")
	}
}

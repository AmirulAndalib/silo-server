package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
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

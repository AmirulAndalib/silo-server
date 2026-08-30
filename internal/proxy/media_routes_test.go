package proxy

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

var updateRouteManifest = flag.Bool("update-route-manifest", false, "update checked-in route manifest")

func TestMediaRouteManifest(t *testing.T) {
	declareProxyMediaRoutes()
	makeRouter := func() chi.Routes {
		return NewServer(nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{}), nodesessions.NewTracker(nil, "", "", "")).Handler().(chi.Routes)
	}
	assertMediaManifest(t, []chi.Routes{makeRouter(), makeRouter()}, proxyMediaRoutes, "testdata/media_routes.txt")
}

// A pre-v2 proxy only has /stream/remux/{token}. The extra literal segment is
// intentional: chi must return 404 instead of treating "audio-v2" as the token
// and ever reaching the legacy FFmpeg path.
func TestAudioV2RemuxPathIsNotCapturedByLegacyRoute(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/stream/remux/{token}", func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("versioned request reached the legacy remux handler")
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stream/remux/audio-v2/signed-token", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("legacy router status = %d, want 404", recorder.Code)
	}
}

func TestAudioV2RemuxClaimsRequireExactStereoEncodeShape(t *testing.T) {
	valid := streamtoken.Claims{
		PlayMethod:          streamtoken.PlayMethodAudioDownmixRemux,
		TranscodeAudio:      true,
		TargetCodecAudio:    "aac",
		SourceAudioChannels: 6,
		TargetAudioChannels: 2,
	}
	tests := []struct {
		name   string
		mutate func(*streamtoken.Claims)
		want   bool
	}{
		{name: "complete recipe", want: true},
		{name: "ordinary method", mutate: func(c *streamtoken.Claims) { c.PlayMethod = "remux" }},
		{name: "audio copy", mutate: func(c *streamtoken.Claims) { c.TranscodeAudio = false }},
		{name: "default AAC codec", mutate: func(c *streamtoken.Claims) { c.TargetCodecAudio = "" }, want: true},
		{name: "non AAC codec", mutate: func(c *streamtoken.Claims) { c.TargetCodecAudio = "eac3" }},
		{name: "stereo source", mutate: func(c *streamtoken.Claims) { c.SourceAudioChannels = 2 }},
		{name: "missing target", mutate: func(c *streamtoken.Claims) { c.TargetAudioChannels = 0 }},
		{name: "surround target", mutate: func(c *streamtoken.Claims) { c.TargetAudioChannels = 6 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := valid
			if test.mutate != nil {
				test.mutate(&claims)
			}
			if got := validAudioV2RemuxClaims(&claims); got != test.want {
				t.Fatalf("validAudioV2RemuxClaims() = %t, want %t for %#v", got, test.want, claims)
			}
		})
	}
}

func TestProxyTokenRoutesEnforceCommittedProxyEgress(t *testing.T) {
	const secret = "proxy-route-secret"
	mediaPath := writeSocketProxyMedia(t)
	srv := newSocketProxyServer(t, secret, nil)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	sign := func(claims streamtoken.Claims) string {
		t.Helper()
		token, err := streamtoken.Sign(claims, secret, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	base := streamtoken.Claims{
		SessionID: "route-bound", MediaPath: mediaPath, PlayMethod: string(playback.PlayDirect),
		UserID: 7, ProfileID: "profile-1", MediaFileID: 42,
		RoutingWorkload:  string(noderouting.WorkloadDirectPlay),
		RoutingExecution: string(noderouting.ExecutionNone),
		RoutingEgress:    string(noderouting.EgressAPI),
	}
	apiToken := sign(base)
	for _, route := range []string{
		"/stream/direct/" + apiToken,
		"/stream/remux/" + apiToken,
		"/stream/remux/audio-v2/" + apiToken,
		"/stream/transcode/" + apiToken + "/master.m3u8",
		"/stream/transcode/" + apiToken + "/segment/000.ts",
		"/stream/subtitles/" + apiToken + "/0",
		"/stream/subtitles/" + apiToken + "/0/fonts",
	} {
		t.Run(route, func(t *testing.T) {
			response := socketProxyRequest(t, server.Client(), http.MethodGet, server.URL+route, nil)
			if response.status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body = %q", response.status, response.body)
			}
		})
	}

	partial := base
	partial.RoutingWorkload = ""
	partialToken := sign(partial)
	partialResponse := socketProxyRequest(t, server.Client(), http.MethodGet, server.URL+"/stream/direct/"+partialToken, nil)
	if partialResponse.status != http.StatusConflict {
		t.Fatalf("partial route status = %d, want 409; body = %q", partialResponse.status, partialResponse.body)
	}

	proxy := base
	proxy.RoutingEgress = string(noderouting.EgressProxy)
	proxyToken := sign(proxy)
	proxyResponse := socketProxyRequest(t, server.Client(), http.MethodGet, server.URL+"/stream/direct/"+proxyToken, nil)
	if proxyResponse.status != http.StatusOK || proxyResponse.body != socketProxyMedia {
		t.Fatalf("proxy route response = %d %q, want 200 %q", proxyResponse.status, proxyResponse.body, socketProxyMedia)
	}
}

func assertMediaManifest(t *testing.T, fixtures []chi.Routes, declared []streamtelemetry.MediaRoute, path string) {
	t.Helper()
	actual, err := streamtelemetry.BuildRouteManifest(fixtures, declared)
	if err != nil {
		t.Fatal(err)
	}
	if *updateRouteManifest {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != actual {
		t.Fatalf("route manifest changed; inspect it and run go test . -update-route-manifest")
	}
	for _, route := range declared {
		if !route.Enrolled || route.Capture == nil {
			t.Fatalf("proxy route not fully enrolled: %s %s", route.Method, route.Pattern)
		}
	}
}

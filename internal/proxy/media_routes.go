package proxy

import (
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
)

var proxyMediaRoutes = []streamtelemetry.MediaRoute{
	proxyRoute(http.MethodGet, "/stream/direct/{token}", streamtelemetry.ClassPlayback, true),
	proxyRoute(http.MethodHead, "/stream/direct/{token}", streamtelemetry.ClassPlayback, true),
	proxyRoute(http.MethodGet, "/stream/remux/{token}", streamtelemetry.ClassPlayback, true),
	proxyRoute(http.MethodHead, "/stream/remux/{token}", streamtelemetry.ClassPlayback, true),
	proxyRoute(http.MethodGet, "/stream/transcode/{token}/master.m3u8", streamtelemetry.ClassManifest, true),
	proxyRoute(http.MethodHead, "/stream/transcode/{token}/master.m3u8", streamtelemetry.ClassManifest, true),
	proxyRoute(http.MethodGet, "/stream/transcode/{token}/segment/{name}", streamtelemetry.ClassPlayback, true),
	proxyRoute(http.MethodGet, "/stream/subtitles/{token}/{track}", streamtelemetry.ClassPlayback, true),
	proxyRoute(http.MethodGet, "/stream/subtitles/{token}/{track}/fonts", streamtelemetry.ClassPlayback, true),
	proxyRoute(http.MethodGet, "/downloads/file/{token}", streamtelemetry.ClassTransfer, false),
	proxyRoute(http.MethodHead, "/downloads/file/{token}", streamtelemetry.ClassTransfer, false),
}

func proxyRoute(method, pattern string, class streamtelemetry.Class, capRelevant bool) streamtelemetry.MediaRoute {
	return streamtelemetry.MediaRoute{Family: streamtelemetry.FamilyProxy, Method: method, Pattern: pattern,
		Class: class, Role: streamtelemetry.RoleViewerEgress, CanonicalSessionKey: "verified_stream_token",
		CapRelevant: capRelevant, Enrolled: true, Capture: proxyCapture(pattern)}
}

func declareProxyMediaRoutes() { streamtelemetry.DeclareRoutes(proxyMediaRoutes...) }

func proxyCapture(pattern string) func(*http.Request) streamtelemetry.CaptureSet {
	return func(r *http.Request) streamtelemetry.CaptureSet {
		client := playback.ClientInfoFromRequest(r)
		viewerIP := streamtelemetry.ViewerIP(r)
		return streamtelemetry.CaptureSet{
			Method: r.Method, Pattern: pattern, ViewerIP: viewerIP,
			DeviceID:  r.Header.Get("X-Silo-Device-ID"),
			Client:    streamtelemetry.ClientVariant{Name: client.Name, Version: client.Version, Build: client.Build, Channel: client.Channel},
			UserAgent: client.UserAgent, ReceivedAt: time.Now(),
		}
	}
}

func proxyMediaRoute(method, pattern string) streamtelemetry.MediaRoute {
	for _, route := range proxyMediaRoutes {
		if route.Method == method && route.Pattern == pattern {
			return route
		}
	}
	panic("undeclared proxy media route: " + method + " " + pattern)
}

func observeProxy(registry *streamtelemetry.Registry, method, pattern string, handler http.HandlerFunc) http.HandlerFunc {
	if registry == nil {
		return handler
	}
	return registry.Observe(proxyMediaRoute(method, pattern))(handler).ServeHTTP
}

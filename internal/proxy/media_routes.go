package proxy

import (
	"net/http"

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
		CapRelevant: capRelevant, Enrolled: false}
}

func declareProxyMediaRoutes() { streamtelemetry.DeclareRoutes(proxyMediaRoutes...) }

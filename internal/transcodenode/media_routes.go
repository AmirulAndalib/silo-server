package transcodenode

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
)

var transcodeNodeMediaRoutes = []streamtelemetry.MediaRoute{
	nodeRoute(http.MethodGet, "/downloads/artifacts/{artifact_id}", streamtelemetry.ClassTransfer),
	nodeRoute(http.MethodHead, "/downloads/artifacts/{artifact_id}", streamtelemetry.ClassTransfer),
	nodeRoute(http.MethodGet, "/transcode/{session_id}/master.m3u8", streamtelemetry.ClassManifest),
	nodeRoute(http.MethodGet, "/transcode/{session_id}/segment/{name}", streamtelemetry.ClassPlayback),
}

func nodeRoute(method, pattern string, class streamtelemetry.Class) streamtelemetry.MediaRoute {
	return streamtelemetry.MediaRoute{Family: streamtelemetry.FamilyTranscodeNode, Method: method, Pattern: pattern,
		Class: class, Role: streamtelemetry.RoleInternalRelay, CanonicalSessionKey: "transport_session_id",
		CapRelevant: false, Enrolled: false}
}

func declareTranscodeNodeMediaRoutes() { streamtelemetry.DeclareRoutes(transcodeNodeMediaRoutes...) }

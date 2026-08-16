package jellycompat

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
)

var jellycompatMediaRoutes = []streamtelemetry.MediaRoute{
	compatRoute(http.MethodGet, "/Playback/BitrateTest", streamtelemetry.ClassTransfer, false),
	compatRoute(http.MethodGet, "/Items/{id}/Download", streamtelemetry.ClassTransfer, false),
	compatRoute(http.MethodHead, "/Items/{id}/Download", streamtelemetry.ClassTransfer, false),
	compatRoute(http.MethodGet, "/Videos/{id}/stream", streamtelemetry.ClassPlayback, true),
	compatRoute(http.MethodHead, "/Videos/{id}/stream", streamtelemetry.ClassPlayback, true),
	compatRoute(http.MethodGet, "/Videos/{id}/stream.{container}", streamtelemetry.ClassPlayback, true),
	compatRoute(http.MethodHead, "/Videos/{id}/stream.{container}", streamtelemetry.ClassPlayback, true),
	compatRoute(http.MethodGet, "/Videos/{id}/master.m3u8", streamtelemetry.ClassManifest, true),
	compatRoute(http.MethodGet, "/Videos/{id}/hls/{playlistId}/stream.m3u8", streamtelemetry.ClassManifest, true),
	compatRoute(http.MethodGet, "/Videos/{id}/hls/{playlistId}/{segmentId}.{segmentContainer}", streamtelemetry.ClassPlayback, true),
	compatRoute(http.MethodGet, "/Videos/{routeItemId}/{routeMediaSourceId}/Subtitles/{routeIndex}/stream.{routeFormat}", streamtelemetry.ClassPlayback, true),
	compatRoute(http.MethodGet, "/Videos/{routeItemId}/{routeMediaSourceId}/Subtitles/{routeIndex}/{routeDeliveryIndex}/stream.{routeFormat}", streamtelemetry.ClassPlayback, true),
}

func compatRoute(method, pattern string, class streamtelemetry.Class, capRelevant bool) streamtelemetry.MediaRoute {
	return streamtelemetry.MediaRoute{Family: streamtelemetry.FamilyJellycompat, Method: method, Pattern: pattern,
		Class: class, Role: streamtelemetry.RoleViewerEgress, CanonicalSessionKey: "compat_play_session",
		CapRelevant: capRelevant, Enrolled: false}
}

func declareJellycompatMediaRoutes() { streamtelemetry.DeclareRoutes(jellycompatMediaRoutes...) }

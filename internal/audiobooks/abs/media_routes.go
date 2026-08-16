package abs

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
)

var absMediaRoutes = func() []streamtelemetry.MediaRoute {
	const absAPIPrefix = "/abs/api"
	const apiPrefix = "/api"
	routes := []streamtelemetry.MediaRoute{
		absRoute(http.MethodGet, "/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodHead, "/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodGet, "/abs/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodHead, "/abs/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodGet, "/feed/{slug}/file/{ino}", streamtelemetry.ClassTransfer, false, "feed_owner"),
	}
	for _, prefix := range []string{apiPrefix, absAPIPrefix} {
		routes = append(routes,
			absRoute(http.MethodGet, prefix+"/items/{libraryItemId}/file/{ino}", streamtelemetry.ClassTransfer, false, "abs_user"),
			absRoute(http.MethodGet, prefix+"/items/{libraryItemId}/file/{ino}/download", streamtelemetry.ClassTransfer, false, "abs_user"),
			absRoute(http.MethodGet, prefix+"/items/{id}/ebook/{fileid}", streamtelemetry.ClassTransfer, false, "abs_user"),
		)
	}
	return routes
}()

func absRoute(method, pattern string, class streamtelemetry.Class, capRelevant bool, key string) streamtelemetry.MediaRoute {
	return streamtelemetry.MediaRoute{Family: streamtelemetry.FamilyABS, Method: method, Pattern: pattern,
		Class: class, Role: streamtelemetry.RoleViewerEgress, CanonicalSessionKey: key,
		CapRelevant: capRelevant, Enrolled: false}
}

func declareABSMediaRoutes() { streamtelemetry.DeclareRoutes(absMediaRoutes...) }

package dashmetrics

import "github.com/Silo-Server/silo-server/internal/streamtelemetry"

// computeEgressDelta returns the viewer bytes this process served between two
// telemetry snapshots, together with the cumulative counters the next call must
// compare against.
//
// Only RoleViewerEgress routes and transfers count. A proxy node's viewer
// traffic also traverses the API node as RoleInternalRelay, and counting both
// would report every relayed byte twice.
//
// Counters only ever grow, but a session can be pruned and re-created under the
// same id, and a restarted registry starts from zero. A shrinking counter is
// therefore read as a fresh start and contributes nothing rather than a
// negative rate. Entries that vanished from the snapshot are dropped, which
// keeps the map bounded by the live session count.
func computeEgressDelta(prev map[string]int64, snapshot streamtelemetry.Snapshot) (int64, map[string]int64) {
	next := make(map[string]int64, len(snapshot.Sessions)+len(snapshot.Transfers))
	var delta int64

	record := func(key string, cumulative int64) {
		next[key] = cumulative
		if grown := cumulative - prev[key]; grown > 0 {
			delta += grown
		}
	}

	for _, session := range snapshot.Sessions {
		var bytes int64
		for _, route := range session.Routes {
			if route.Role == streamtelemetry.RoleViewerEgress {
				bytes += route.BytesAccepted
			}
		}
		record("session:"+session.SessionID, bytes)
	}

	for _, transfer := range snapshot.Transfers {
		if transfer.Role != streamtelemetry.RoleViewerEgress {
			continue
		}
		record("transfer:"+transfer.ID, transfer.BytesAccepted)
	}

	return delta, next
}

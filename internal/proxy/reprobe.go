package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// reprobeCapabilitiesResponse mirrors the transcode node's re-probe answer, so
// an operator action does not have to know which node type it is talking to.
type reprobeCapabilitiesResponse struct {
	// Resolved is the backend this proxy would now use.
	Resolved string `json:"resolved"`
	// CapabilityHash identifies the snapshot this re-probe published.
	CapabilityHash string `json:"capability_hash"`
}

// handleReprobeCapabilities discards this proxy's cached probe verdicts and
// rebuilds the capability snapshot against live hardware.
//
// A proxy runs ffmpeg too (remux, Dolby Vision RPU strip), and the probe caches
// behind its snapshot keep a successful verdict for the process lifetime. That
// is correct for playback and blind to hardware that has since stopped working
// underneath it, which no cache key can observe — see the transcode node's copy
// of this handler for the full reasoning. A rebuild that does not finish keeps
// the previously published hash.
//
// Unlike the transcode node this does not refuse while busy. That guard is about
// encoder sessions: a proxy's jobs are remuxes and RPU strips, which hold no
// encoder slot for a probe's smoke encode to lose a race against.
func (s *Server) handleReprobeCapabilities(w http.ResponseWriter, r *http.Request) {
	playback.InvalidateHWProbeCache()
	tonemap.InvalidateProbeCache()

	// buildCapabilitySnapshot owns the probe deadline, so a re-probe can never
	// cost more than a cold capability fetch already may.
	info, err := s.buildCapabilitySnapshot(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "proxy capability re-probe incomplete", "component", "proxy", "error", err)
		http.Error(w, "capability probe unavailable", http.StatusServiceUnavailable)
		return
	}
	previous := s.storedCapabilityHash()
	s.storeCapabilityHash(info.CapabilityHash)
	slog.InfoContext(r.Context(), "proxy capabilities re-probed", "component", "proxy",
		"previous_hash", previous, "hash", info.CapabilityHash, "resolved", info.Resolved)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(reprobeCapabilitiesResponse{
		Resolved:       info.Resolved,
		CapabilityHash: info.CapabilityHash,
	}); err != nil {
		slog.WarnContext(r.Context(), "encode proxy re-probe result", "component", "proxy", "error", err)
	}
}

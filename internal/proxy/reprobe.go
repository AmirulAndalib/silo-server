package proxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

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

	ctx, cancel := context.WithTimeout(r.Context(), s.capabilityProbeBudget())
	defer cancel()
	info, err := s.buildCapabilitySnapshot(ctx)
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

// capabilityProbeBudget is the deadline one snapshot rebuild gets.
//
// A proxy's snapshot is the bounded hardware walk plus the transformation
// registry's own bounded commands — not the tone-map matrix — so this is an
// over-allowance rather than a measurement. It is deliberately the same number
// the transcode node uses: both node types advertise one probe budget to
// callers, and a second constant here would be a second thing to keep in step
// with the walk and registry timeouts.
func (s *Server) capabilityProbeBudget() time.Duration {
	hwAccel := playback.HWAccelNone
	hwDevice := ""
	if cfg := s.watcher.Config(); cfg != nil {
		hwAccel = cfg.Playback.HWAccel
		hwDevice = cfg.Playback.HWDevice
	}
	return tonemap.ProbeEndpointTimeout(hwAccel, hwDevice)
}

package proxy

import (
	"encoding/json"
	"fmt"
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
// Unlike the transcode node this does not refuse while *jobs* are running. That
// guard is about encoder sessions: a proxy's jobs are remuxes and RPU strips,
// which hold no encoder slot for a probe's smoke encode to lose a race against.
// It does refuse while another probe is running, which is a different thing —
// see below.
func (s *Server) handleReprobeCapabilities(w http.ResponseWriter, r *http.Request) {
	// Held across the invalidation and the rebuild together: discarding the
	// verdicts and recomputing them has to be one step, or the scheduled
	// snapshot could start its own cold matrix in between and run ffmpeg on the
	// same GPU at the same time.
	s.capabilityBuildMu.Lock()
	defer s.capabilityBuildMu.Unlock()

	// The mutex is not enough on its own. A probe outlives its caller by
	// design — both singleflights run on background contexts so a canceled
	// request cannot kill work another request is waiting on — so a capability
	// request abandoned mid-probe releases this mutex while ffmpeg is still
	// encoding. Invalidating then starts a second matrix beside the first, and
	// two smoke encodes contending for one card publish a hardware failure for
	// hardware that is fine. That is the same false verdict the transcode node's
	// gate exists to prevent, and it does not care which kind of node it is on.
	if busy := s.probesInFlight(); busy > 0 {
		slog.InfoContext(r.Context(), "proxy capability re-probe refused while probes are still running",
			"component", "proxy", "probes_in_flight", busy)
		http.Error(w, fmt.Sprintf(
			"node is running %d hardware probe(s); a re-probe smoke-encodes on the GPU and two at once would report working hardware as failed. Retry shortly.",
			busy), http.StatusConflict)
		return
	}

	playback.InvalidateHWProbeCache()
	tonemap.InvalidateProbeCache()
	// The resource sampler retires nvidia-smi after repeated failure, and a
	// driver that was broken at start is exactly what a re-probe is called for.
	// A proxy samples the same GPU a transcode node does, so it needs the same
	// nudge — without it the node re-verifies its encoders here and still
	// reports no GPU utilization until the breaker's own retry interval.
	s.metrics.RetrySources()

	// buildCapabilitySnapshotLocked owns the probe deadline, so a re-probe can
	// never cost more than a cold capability fetch already may.
	info, err := s.buildCapabilitySnapshotLocked(r.Context())
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

// probesInFlight counts the hardware and tone-map probes this process has
// claimed the encoder for. It is a method so a test can drive the refusal
// without reaching into either package's unexported singleflight.
func (s *Server) probesInFlight() int {
	if s.countProbesInFlight != nil {
		return s.countProbesInFlight()
	}
	return playback.HWProbesInFlight() + tonemap.ProbesInFlight()
}

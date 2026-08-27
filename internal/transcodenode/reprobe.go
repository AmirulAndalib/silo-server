package transcodenode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// reprobeCapabilitiesResponse is what an operator-triggered re-probe reports
// back. It is deliberately tiny: the caller that wants the whole inventory
// fetches /hw-capabilities, and after this call that endpoint answers from the
// freshly warmed caches.
type reprobeCapabilitiesResponse struct {
	// Resolved is the backend the node would now use.
	Resolved string `json:"resolved"`
	// CapabilityHash identifies the snapshot this re-probe published, so the
	// caller can tell a re-probe that changed something from one that confirmed
	// the previous answer.
	CapabilityHash string `json:"capability_hash"`
}

// handleReprobeCapabilities discards this node's cached probe verdicts and
// rebuilds the capability snapshot against live hardware.
//
// Both probe caches keep a *successful* verdict for the process lifetime, which
// is the right default — a GPU that encoded a frame does not stop being able to
// between two playback requests, and re-verifying per request would put ffmpeg
// execs on the playback path. The blind spot that leaves is hardware that has
// stopped working underneath a running node: a driver replaced, a device taken
// out of the container, an ffmpeg swapped in place. No cache key sees any of
// that, so a verdict that was true keeps being served until the node restarts.
// The opposite direction needs no help — a failed GPU probe carries a 15-second
// negative TTL and is retried on its own, so a repaired driver is picked up by
// the next capability snapshot. What this route adds there is the tone-map
// matrix, which caches any non-empty inventory permanently and so can stay
// software-only on a host whose GPU was broken at start.
//
// The rebuild reuses the ordinary snapshot assembly and budget, so a re-probe
// can never cost more than a cold capability fetch already may, and a rebuild
// that does not finish keeps the previously published hash: an incomplete probe
// is not evidence the hardware changed, and republishing a degraded snapshot
// would announce a hardware change that did not happen.
//
// It is refused on a node that is transcoding, for the same reason: every
// hardware probe ends in a real smoke encode that opens an encoder session, and
// a card at its concurrent session cap fails that encode with an error nothing
// can tell apart from a missing device or a broken driver. The verdict would
// then be published as verified:false, and the server would persist a hardware
// regression for a GPU that is fine and is at that moment encoding. Waiting for
// the node to drain costs an operator a retry; a false regression costs them a
// hardware investigation and, through the tone-map inventory in the same
// snapshot, degraded routing until the next clean probe.
func (s *Server) handleReprobeCapabilities(w http.ResponseWriter, r *http.Request) {
	if active := s.activeJobs.Load(); active > 0 {
		slog.InfoContext(r.Context(), "transcode node capability re-probe refused while busy",
			"component", "transcodenode", "active_jobs", active)
		http.Error(w, fmt.Sprintf(
			"node is running %d transcode job(s); a re-probe smoke-encodes on the GPU and a busy encoder would report working hardware as failed. Retry when the node is idle.",
			active), http.StatusConflict)
		return
	}

	playback.InvalidateHWProbeCache()
	tonemap.InvalidateProbeCache()

	ctx, cancel := context.WithTimeout(r.Context(), s.capabilityProbeBudget())
	defer cancel()
	info, err := s.buildCapabilitySnapshot(ctx)
	if err != nil {
		slog.WarnContext(r.Context(), "transcode node capability re-probe incomplete",
			"component", "transcodenode", "error", err)
		http.Error(w, "capability probe unavailable", http.StatusServiceUnavailable)
		return
	}
	previous := s.storedCapabilityHash()
	// A re-probed report is as authoritative as a scheduled snapshot, so health
	// starts advertising this hash immediately rather than at the next tick.
	s.storeCapabilityHash(info.CapabilityHash)
	slog.InfoContext(r.Context(), "transcode node capabilities re-probed", "component", "transcodenode",
		"previous_hash", previous, "hash", info.CapabilityHash, "resolved", info.Resolved)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(reprobeCapabilitiesResponse{
		Resolved:       info.Resolved,
		CapabilityHash: info.CapabilityHash,
	}); err != nil {
		slog.WarnContext(r.Context(), "encode transcode node re-probe result", "component", "transcodenode", "error", err)
	}
}

// capabilityProbeBudget is the deadline one snapshot rebuild gets. It is the
// same budget buildCapabilitySnapshot applies internally, named here so the
// re-probe route is bounded whether or not its caller sent one.
func (s *Server) capabilityProbeBudget() time.Duration {
	hwAccel := playback.HWAccelNone
	hwDevice := ""
	if cfg := s.watcher.Config(); cfg != nil {
		hwAccel = cfg.Playback.HWAccel
		hwDevice = cfg.Playback.HWDevice
	}
	return toneMapCapabilityResolveTimeout(hwAccel, hwDevice)
}

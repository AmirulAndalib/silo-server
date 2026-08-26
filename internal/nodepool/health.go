package nodepool

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"
)

// healthResponse is the JSON response from a node's /health endpoint.
type healthResponse struct {
	Status     string `json:"status"`
	ActiveJobs int    `json:"active_jobs"`
	EgressKbps int    `json:"egress_kbps"`
	// CapabilitiesHash identifies the node's current hardware capability
	// snapshot. A node that predates capability snapshots reports none, which
	// is how the sweep tells "nothing changed" from "cannot say".
	CapabilitiesHash string `json:"capabilities_hash"`
}

// CheckNode pings a node's /health endpoint and returns its health status,
// active job count, reported egress bandwidth, and capability hash.
func CheckNode(ctx context.Context, n *Node) (healthy bool, activeJobs, egressKbps int, capabilitiesHash string) {
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.URL+"/api/v1/health", nil)
	if err != nil {
		return false, 0, 0, ""
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, 0, ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, 0, 0, ""
	}

	var hr healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return false, 0, 0, ""
	}

	return true, hr.ActiveJobs, hr.EgressKbps, hr.CapabilitiesHash
}

// CapabilityFetcher retrieves one node's full capability report and the hash
// the payload identifies itself by. The payload is stored opaquely, so this
// package never has to understand — or import — the playback capability model.
// An empty hash means the payload cannot be tracked for change and must not be
// persisted.
type CapabilityFetcher func(ctx context.Context, nodeURL string) (payload []byte, hash string, err error)

// capabilityFetchTimeout bounds one capability fetch. Node-side capability
// answers can involve ffmpeg probes on a cold cache — the node's own advertised
// probe budget reaches ~2 minutes — and the fetch runs detached from the
// health sweep, so the bound covers a genuinely cold node rather than
// abandoning it every sweep.
const capabilityFetchTimeout = 2 * time.Minute

// HealthChecker runs periodic health checks on all nodes in both pools,
// updating in-memory state and optionally persisting to the database.
type HealthChecker struct {
	proxyPool     *ProxyPool
	transcodePool *TranscodePool
	repo          *Repository // may be nil (proxy/transcode modes have no DB)
	interval      time.Duration

	// mu guards the two injected hooks. Both are wired after construction —
	// the capability-change callback because the playback handler that consumes
	// it is built later, during router assembly — while the sweep may already
	// be running.
	mu                    sync.RWMutex
	capFetch              CapabilityFetcher
	onCapabilitiesChanged func(nodeURL string)

	// capabilityRefreshes tracks the detached capability fetches so shutdown —
	// and tests — can wait for them. The sweep itself must never wait on one.
	capabilityRefreshes sync.WaitGroup
	// capabilityRefreshInFlight holds the node ids currently being fetched, so
	// a fetch that outlives the sweep that started it is not started again by
	// the next sweep. Node ids are unique across both pools (one table).
	capabilityRefreshInFlight sync.Map
}

// NewHealthChecker creates a health checker for the given pools.
func NewHealthChecker(proxyPool *ProxyPool, transcodePool *TranscodePool, repo *Repository) *HealthChecker {
	return &HealthChecker{
		proxyPool:     proxyPool,
		transcodePool: transcodePool,
		repo:          repo,
		interval:      30 * time.Second,
	}
}

// SetCapabilityFetcher wires how the sweep retrieves a node's capability report
// once the node reports a hash it has not stored. Without one the sweep behaves
// exactly as it did before capability tracking.
func (hc *HealthChecker) SetCapabilityFetcher(fetch CapabilityFetcher) {
	if hc == nil {
		return
	}
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.capFetch = fetch
}

// SetCapabilitiesChangedCallback wires the notification fired after a node's
// capabilities were refetched and stored, so caches keyed on node capability
// can be invalidated without waiting for their own TTL.
func (hc *HealthChecker) SetCapabilitiesChangedCallback(fn func(nodeURL string)) {
	if hc == nil {
		return
	}
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.onCapabilitiesChanged = fn
}

func (hc *HealthChecker) hooks() (CapabilityFetcher, func(nodeURL string)) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.capFetch, hc.onCapabilitiesChanged
}

// Start runs health checks in a background goroutine. Stops when ctx is cancelled.
func (hc *HealthChecker) Start(ctx context.Context) {
	go func() {
		hc.checkAll(ctx)
		ticker := time.NewTicker(hc.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hc.checkAll(ctx)
			}
		}
	}()
}

// applyCapabilitiesFunc is a pool's copy-on-write capability writer.
type applyCapabilitiesFunc func(id int, capabilities []byte, hash string, refreshedAt time.Time)

func (hc *HealthChecker) checkAll(ctx context.Context) {
	var wg sync.WaitGroup
	check := func(n *Node, applyHealth func(int, bool, int, int, time.Time), applyCapabilities applyCapabilitiesFunc) {
		wg.Go(func() {
			healthy, activeJobs, egressKbps, capabilitiesHash := CheckNode(ctx, n)

			// Publish the result through the pool lock so readers never see
			// a Node struct mutated in place (the pool swaps in a copy).
			applyHealth(n.ID, healthy, activeJobs, egressKbps, time.Now())

			if n.Healthy && !healthy {
				slog.WarnContext(ctx, "stream node unhealthy", "component", "nodepool", "id", n.ID, "name", n.Name, "url", n.URL)
			} else if !n.Healthy && healthy {
				slog.InfoContext(ctx, "stream node recovered", "component", "nodepool", "id", n.ID, "name", n.Name, "url", n.URL)
			}

			if hc.repo != nil {
				if err := hc.repo.UpdateHealth(ctx, n.ID, healthy, activeJobs, egressKbps); err != nil {
					slog.ErrorContext(ctx, "failed to persist node health", "component", "nodepool", "id", n.ID, "error", err)
				}
			}

			if healthy && capabilitiesHash != "" && capabilitiesHash != storedCapabilitiesHash(n) {
				hc.startCapabilityRefresh(ctx, n, applyCapabilities)
			}
		})
	}
	for _, n := range hc.proxyPool.Nodes() {
		check(n, hc.proxyPool.ApplyHealth, hc.proxyPool.ApplyCapabilities)
	}
	for _, n := range hc.transcodePool.Nodes() {
		check(n, hc.transcodePool.ApplyHealth, hc.transcodePool.ApplyCapabilities)
	}
	wg.Wait()
}

// startCapabilityRefresh runs one node's capability fetch off the sweep's
// WaitGroup, deduplicated per node id.
//
// The fetch budget is larger than the sweep interval by design (a cold node
// runs ffmpeg probes to answer), so waiting for it inside the sweep would let
// one slow node stretch every other node's health cadence past that interval —
// and pool health is what routes streams away from a node that died. The
// in-flight guard is what keeps the detached fetches from stacking up: without
// it, a node that answers slower than the interval would collect one goroutine
// per sweep, since a fetch that has not completed cannot have moved the stored
// hash that triggered it.
func (hc *HealthChecker) startCapabilityRefresh(ctx context.Context, n *Node, applyCapabilities applyCapabilitiesFunc) {
	if fetch, _ := hc.hooks(); fetch == nil {
		return
	}
	if _, loaded := hc.capabilityRefreshInFlight.LoadOrStore(n.ID, struct{}{}); loaded {
		return
	}
	hc.capabilityRefreshes.Add(1)
	go func() {
		defer hc.capabilityRefreshes.Done()
		defer hc.capabilityRefreshInFlight.Delete(n.ID)
		hc.refreshCapabilities(ctx, n, applyCapabilities)
	}()
}

// waitForCapabilityRefreshes blocks until every detached capability fetch
// started so far has finished. Callers must not hold the sweep open on it.
func (hc *HealthChecker) waitForCapabilityRefreshes() {
	hc.capabilityRefreshes.Wait()
}

// refreshCapabilities fetches and stores one node's capability report. A
// failure leaves the stored row alone and is retried on the next sweep, because
// a fetch that failed is no evidence about what the node has.
func (hc *HealthChecker) refreshCapabilities(ctx context.Context, n *Node, applyCapabilities applyCapabilitiesFunc) {
	fetch, onChanged := hc.hooks()
	if fetch == nil {
		return
	}
	fetchCtx, cancel := context.WithTimeout(ctx, capabilityFetchTimeout)
	defer cancel()
	payload, hash, err := fetch(fetchCtx, n.URL)
	if err != nil {
		slog.WarnContext(ctx, "node capability fetch failed", "component", "nodepool",
			"id", n.ID, "name", n.Name, "url", n.URL, "error", err)
		return
	}
	if hash == "" || len(payload) == 0 {
		// A hash is what makes the payload trackable; storing one without it
		// would refetch every sweep forever. Never synthesize one here — the
		// node is the only thing that knows what it hashed.
		slog.WarnContext(ctx, "node capability report carries no hash", "component", "nodepool",
			"id", n.ID, "name", n.Name, "url", n.URL)
		return
	}

	refreshedAt := time.Now()
	if hc.repo != nil {
		if err := hc.repo.UpdateCapabilities(ctx, n.ID, payload, hash, refreshedAt); err != nil {
			slog.WarnContext(ctx, "failed to persist node capabilities", "component", "nodepool",
				"id", n.ID, "name", n.Name, "error", err)
			return
		}
	}
	logCapabilityChange(ctx, n, payload)
	if applyCapabilities != nil {
		applyCapabilities(n.ID, payload, hash, refreshedAt)
	}
	if onChanged != nil {
		onChanged(n.URL)
	}
}

func storedCapabilitiesHash(n *Node) string {
	if n == nil || n.CapabilitiesHash == nil {
		return ""
	}
	return *n.CapabilitiesHash
}

// capabilityDriftView is the minimal projection this package parses out of an
// otherwise opaque capability payload. It is deliberately local and partial:
// nodepool must not depend on playback, and drift only needs to know which
// backends were verified and which render devices existed.
type capabilityDriftView struct {
	Resolved         string `json:"resolved"`
	DetectedBackends []struct {
		Backend  string `json:"backend"`
		Verified bool   `json:"verified"`
	} `json:"detected_backends"`
	RenderDevices []string `json:"render_devices"`
}

// logCapabilityChange records what changed between the stored report and the
// new one. Losing a verified backend or a render device is the case worth
// waking an operator for: it means a node that was picked for hardware work
// silently became less capable, which otherwise only shows up as slow or
// failing transcodes.
func logCapabilityChange(ctx context.Context, n *Node, payload []byte) {
	if len(n.Capabilities) == 0 {
		slog.InfoContext(ctx, "node capabilities stored", "component", "nodepool",
			"id", n.ID, "name", n.Name, "url", n.URL)
		return
	}
	var previous, current capabilityDriftView
	if json.Unmarshal(n.Capabilities, &previous) != nil || json.Unmarshal(payload, &current) != nil {
		return
	}
	verifiedNow := make(map[string]bool, len(current.DetectedBackends))
	for _, backend := range current.DetectedBackends {
		verifiedNow[backend.Backend] = backend.Verified
	}
	var lostBackends []string
	for _, backend := range previous.DetectedBackends {
		if backend.Verified && !verifiedNow[backend.Backend] {
			lostBackends = append(lostBackends, backend.Backend)
		}
	}
	var lostDevices []string
	for _, device := range previous.RenderDevices {
		if !slices.Contains(current.RenderDevices, device) {
			lostDevices = append(lostDevices, device)
		}
	}
	if len(lostBackends) == 0 && len(lostDevices) == 0 {
		return
	}
	slog.WarnContext(ctx, "node capability drift", "component", "nodepool",
		"id", n.ID, "name", n.Name, "url", n.URL,
		"lost_verified_backends", lostBackends, "lost_render_devices", lostDevices,
		"previous_resolved", previous.Resolved, "resolved", current.Resolved)
}

// Package nodemetrics samples the host resources a media node actually runs
// out of — CPU, memory, disk, network, and GPU engine busyness — into a small
// in-memory snapshot that health responses, the admin API, and Prometheus all
// read without doing any work of their own.
//
// The contract every reader depends on is that sampling is fully decoupled from
// reading. A sample runs on the sampler's own goroutine on a fixed interval and
// publishes an immutable snapshot with one atomic store; a reader loads the
// latest pointer and never waits, never touches /proc, and never runs a
// subprocess. That is what makes it safe to put these numbers in a node's
// /health response, which is the signal the cluster uses to route streams away
// from a dying node: a wedged NFS mount or a hung nvidia-smi degrades one field
// of one snapshot instead of stalling health for every node in the pool.
//
// All sampling is Linux-only. Everywhere else the snapshot reports
// Available=false and the loop idles, so the package is safe to construct
// unconditionally on any platform.
package nodemetrics

import "time"

// Snapshot is one complete sampling pass. Snapshots are immutable once
// published: a sampler builds a new one and swaps it in rather than mutating
// the one readers hold.
type Snapshot struct {
	// Available reports whether this host can be sampled at all. It is false on
	// non-Linux hosts and before the first sample completes, which is why System
	// and GPU are pointers/slices rather than values — "no numbers yet" must be
	// distinguishable from "everything is zero".
	Available bool `json:"available"`
	// SampledAt is when this pass ran. Readers use it to tell a live sample from
	// a sampler that stopped ticking.
	SampledAt time.Time `json:"sampled_at,omitzero"`
	// System is the host's CPU/memory/disk/network sample, omitted when the host
	// could not be sampled.
	System *SystemStats `json:"system,omitempty"`
	// GPU is one entry per GPU this process can say anything about, omitted when
	// there are none.
	GPU []GPUStats `json:"gpu,omitempty"`
}

// SystemStats is the host resource sample.
type SystemStats struct {
	// CPUPct is aggregate busy percentage across all cores between the previous
	// sample and this one, 0-100. Under a cgroup it is this container's own
	// usage against its own quota, not the host's.
	CPUPct int `json:"cpu_pct"`
	// Load1 is the 1-minute load average, which unlike CPUPct also counts
	// uninterruptible-sleep tasks — a node blocked on storage looks idle in
	// CPUPct and busy here.
	Load1 float64 `json:"load1"`
	// Cores is how many CPUs this process may run on — the cgroup's quota
	// rounded up where one is set, otherwise every CPU the kernel reports. It is
	// what CPUPct is already normalized against, and what Load1 must be read
	// relative to.
	Cores      int   `json:"cores"`
	MemUsedMB  int64 `json:"mem_used_mb"`
	MemTotalMB int64 `json:"mem_total_mb"`
	// Disks holds the sampled mounts: the transcode scratch dir first, then any
	// media roots the process was told about. Deduplicated by filesystem, so two
	// paths on one volume are reported once.
	Disks []DiskStats `json:"disks"`
	// NetRxBps and NetTxBps are aggregate throughput in *bits* per second,
	// matching the node egress_kbps unit used elsewhere in the cluster, with
	// loopback excluded.
	NetRxBps int64 `json:"net_rx_bps"`
	NetTxBps int64 `json:"net_tx_bps"`
}

// DiskStats is one sampled mount.
type DiskStats struct {
	Path    string  `json:"path"`
	UsedGB  float64 `json:"used_gb"`
	TotalGB float64 `json:"total_gb"`
	// Stale marks numbers carried over from an earlier pass because the current
	// probe has not returned — the normal reading for a network mount whose
	// server went away. The values are real, just old.
	Stale bool `json:"stale,omitempty"`
	// Unavailable marks a path that has never been measured successfully (it
	// does not exist on this node, or the very first probe is still hanging).
	// UsedGB/TotalGB are meaningless when it is set.
	Unavailable bool `json:"unavailable,omitempty"`
}

// vendorNVIDIA is the GPUStats.Vendor value nvidia-smi enrichment reports.
const vendorNVIDIA = "nvidia"

// GPU measurement sources, in order of how much they can see.
const (
	// SourceUnavailable means nothing could measure this device: no owned
	// process held it and no whole-GPU query answered.
	SourceUnavailable = "unavailable"
	// SourceFdinfo is the unprivileged DRM baseline. It sees only engine time
	// spent by this process's own ffmpeg children, never another tenant's.
	SourceFdinfo = "fdinfo"
	// SourceNVIDIASMI is whole-GPU utilization from nvidia-smi, which is the
	// only signal for NVIDIA (the proprietary driver implements no fdinfo).
	SourceNVIDIASMI = "nvidia-smi"
	// SourceFdinfoNVIDIASMI is both, for a device that has DRM counters and an
	// nvidia-smi row.
	SourceFdinfoNVIDIASMI = "fdinfo+nvidia-smi"
)

// GPUStats is one GPU's sample.
type GPUStats struct {
	// Device is the render-node path (/dev/dri/renderD128) where one is known,
	// otherwise the PCI address or a "cuda:N" index for an NVIDIA GPU with no
	// DRM node this process can see.
	Device string `json:"device"`
	// Vendor is "intel", "nvidia", "amd" or empty when unknown.
	Vendor string `json:"vendor,omitempty"`
	// Sessions is how many of this process's GPU workloads are currently pinned
	// to this device. It comes from the playback device balancer, not from the
	// driver, so it is exact for our own work and blind to everyone else's.
	Sessions int `json:"sessions"`
	// VideoBusyPct and RenderBusyPct are engine busy percentages over the last
	// sample interval. From fdinfo they cover only our own processes.
	VideoBusyPct  int `json:"video_busy_pct"`
	RenderBusyPct int `json:"render_busy_pct"`
	// TotalBusyPct is whole-GPU utilization including other tenants. It is a
	// pointer because "no enrichment source" and "idle" are different facts and
	// an operator must not read the first as the second.
	TotalBusyPct *int   `json:"total_busy_pct,omitempty"`
	VRAMUsedMB   *int64 `json:"vram_used_mb,omitempty"`
	VRAMTotalMB  *int64 `json:"vram_total_mb,omitempty"`
	// Source names what produced the numbers above; see the Source* constants.
	Source string `json:"source"`
}

// DeviceIdentity is one render device as the host's hardware detection sees it.
// The sampler needs it only to translate the PCI addresses DRM fdinfo reports
// into the /dev/dri paths every other surface (settings, session accounting,
// the admin UI) speaks, which is why this package takes it as a provider
// instead of doing its own hardware walk.
type DeviceIdentity struct {
	Path       string
	PCIAddress string
	Vendor     string
}

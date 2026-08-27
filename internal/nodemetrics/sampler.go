package nodemetrics

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultInterval is how often the sampler takes a reading. Rate-based fields
// (CPU, network, GPU engine busy) are averages over exactly this window, so it
// doubles as the resolution of every derived percentage. Five seconds is short
// enough that an operator watching a transcode start sees it, and long enough
// that the sampling itself is not the load.
const DefaultInterval = 5 * time.Second

// Options configures a Sampler. Every provider is optional: a nil provider
// means that dimension simply is not reported, which is the correct behavior
// for a proxy node with no scratch dir or a host with no GPU.
type Options struct {
	// ScratchDir is the transcode working directory. It is sampled first
	// because it is the volume whose filling up silently kills transcodes.
	ScratchDir string
	// MediaRoots returns library folder paths to sample alongside the scratch
	// dir. It is called on the sampling goroutine each pass, so it must be cheap
	// and must respect ctx; a nil provider samples the scratch dir only.
	MediaRoots func(ctx context.Context) []string
	// FFmpegChildren returns the pids whose DRM fdinfo counts as this node's GPU
	// work. The default is this process's direct ffmpeg children.
	FFmpegChildren func() []int
	// DeviceSessions returns active GPU workloads per device, keyed the way the
	// playback device balancer keys them (a render node path, or "cuda:N").
	DeviceSessions func() map[string]int
	// DeviceIdentities returns this host's render devices, used to translate
	// the PCI addresses DRM reports into the /dev/dri paths every other surface
	// speaks.
	DeviceIdentities func() []DeviceIdentity
	// Interval overrides DefaultInterval.
	Interval time.Duration
	// Now overrides the clock. Tests inject one instead of sleeping.
	Now func() time.Time
}

// Sampler periodically reads host and GPU resource usage into a snapshot.
//
// Exactly one goroutine performs sampling, which is why the per-sample delta
// state below needs no locking; everything a caller can reach concurrently is
// either atomic (the published snapshot) or explicitly guarded (the disk
// entries, which detached probe goroutines also write).
type Sampler struct {
	interval   time.Duration
	now        func() time.Time
	goos       string
	scratchDir string
	mediaRoots func(ctx context.Context) []string
	sessions   func() map[string]int
	identities func() []DeviceIdentity
	ffmpegPIDs func() []int

	// Path seams. Production values point at the real filesystem; tests point
	// them at a fake /proc tree.
	procDir string
	// hostProcDir is where an LXC's lxcfs-virtualized /proc files can be
	// bind-mounted when this sampler runs in Docker nested inside an LXC
	// container. See procDirFor for why it takes priority when present.
	hostProcDir      string
	cgroupLimitPaths []string
	cgroupUsagePaths []cgroupUsagePath
	cgroupCPUPaths   []cgroupCPUPath

	snapshot atomic.Pointer[Snapshot]

	// Delta state, owned by the sampling goroutine.
	prevCPU cpuTimes
	prevNet netCounters
	// prevGPU is keyed by DRM client, not by device: only a client's own engine
	// counter is monotone, so a per-device baseline would read every client exit
	// as negative work for the whole card.
	prevGPU       map[fdinfoClient]engineCounters
	prevGPUAt     time.Time
	prevCgroupCPU cgroupCPUSample
	// droppedRoots is how many configured media roots the last pass left
	// unsampled because of the maxSampledDisks cap; see noteDroppedRoots.
	droppedRoots int

	// Disk probe state, shared with detached probe goroutines.
	diskMu    sync.Mutex
	disks     map[string]*diskEntry
	diskOrder []string
	statfs    func(string) (fsStats, error)
	// probesInFlight is how many statfs goroutines are outstanding right now,
	// bounded by maxOutstandingDiskProbes. Guarded by diskMu.
	probesInFlight int
	// probeBudgetExhausted latches the ceiling warning to one line per episode.
	probeBudgetExhausted bool
	// diskProbeDone, when non-nil, receives each completed probe's path. Tests
	// wait on it instead of sleeping; production leaves it nil.
	diskProbeDone chan string

	runNVIDIASMI  func(ctx context.Context) ([]byte, error)
	nvidiaBreaker *sourceBreaker
}

// NewSampler creates a sampler. It performs no I/O; nothing is read until
// Start.
func NewSampler(opts Options) *Sampler {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	procDir := "/proc"
	hostProcDir := "/host/proc"
	ffmpegPIDs := opts.FFmpegChildren
	if ffmpegPIDs == nil {
		pid := os.Getpid()
		ffmpegPIDs = func() []int { return defaultFFmpegChildren(procDir, pid) }
	}
	s := &Sampler{
		interval:         interval,
		now:              now,
		goos:             runtime.GOOS,
		scratchDir:       opts.ScratchDir,
		mediaRoots:       opts.MediaRoots,
		sessions:         opts.DeviceSessions,
		identities:       opts.DeviceIdentities,
		ffmpegPIDs:       ffmpegPIDs,
		procDir:          procDir,
		hostProcDir:      hostProcDir,
		cgroupLimitPaths: CgroupMemoryLimitPaths(),
		cgroupUsagePaths: slices.Clone(cgroupMemoryUsagePaths),
		cgroupCPUPaths:   slices.Clone(cgroupCPUPaths),
		prevGPU:          map[fdinfoClient]engineCounters{},
		disks:            map[string]*diskEntry{},
		statfs:           osStatfs,
		runNVIDIASMI:     runNVIDIASMI,
		nvidiaBreaker:    &sourceBreaker{name: "nvidia-smi"},
	}
	s.snapshot.Store(&Snapshot{})
	return s
}

// NewFixedSamplerForTest returns a sampler that always answers with the given
// snapshot and never samples anything.
//
// It exists for tests in other packages — the node health and status handlers,
// the admin resources endpoint — that need a known reading without a Linux host
// under them. Nothing in production calls it.
func NewFixedSamplerForTest(snapshot Snapshot) *Sampler {
	s := NewSampler(Options{})
	s.snapshot.Store(&snapshot)
	return s
}

// Snapshot returns the most recent sample. It never blocks, never fails, and
// never does I/O — it is safe to call from an HTTP handler on the request path,
// which is the whole reason the sampling loop exists.
func (s *Sampler) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	if snapshot := s.snapshot.Load(); snapshot != nil {
		return *snapshot
	}
	return Snapshot{}
}

// Start samples in the background until ctx is canceled. It returns
// immediately.
//
// On a non-Linux host the loop still runs but does nothing, so callers do not
// need their own platform checks and the published snapshot stays a truthful
// Available=false rather than an absent one.
func (s *Sampler) Start(ctx context.Context) {
	if s == nil || ctx == nil {
		return
	}
	registerCollector(s)
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		s.sample(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sample(ctx)
			}
		}
	}()
}

// sample takes one reading and publishes it.
func (s *Sampler) sample(ctx context.Context) {
	if s.goos != "linux" {
		s.snapshot.Store(&Snapshot{})
		return
	}
	now := s.now()
	snapshot := Snapshot{
		Available: true,
		SampledAt: now,
		System:    s.sampleSystem(ctx, now),
		GPU:       s.sampleGPU(ctx, now),
	}
	s.snapshot.Store(&snapshot)
}

func (s *Sampler) sampleSystem(ctx context.Context, now time.Time) *SystemStats {
	cpuPct, cores := s.cpuStats(now)

	net := readNetCounters(s.procDir, now)
	rxBps, txBps, _ := netThroughputBps(s.prevNet, net)
	if net.valid {
		s.prevNet = net
	}

	usedBytes, totalBytes := s.memoryStats()

	paths := s.diskPaths(ctx)
	s.refreshDisks(paths, now)

	return &SystemStats{
		CPUPct:     cpuPct,
		Load1:      readLoad1(s.procDirFor("loadavg")),
		Cores:      cores,
		MemUsedMB:  bytesToMB(usedBytes),
		MemTotalMB: bytesToMB(totalBytes),
		Disks:      s.diskStats(paths, now),
		NetRxBps:   rxBps,
		NetTxBps:   txBps,
	}
}

// cpuStats reports busy percentage and the core count that percentage is
// normalized against.
//
// Under a cgroup both come from the cgroup, not the host: /proc/stat is
// host-wide even in a container, so a node capped at two cores of a busy
// 64-core machine would otherwise report the machine's load instead of its own
// — and a node pinned at its quota, which is the state worth alerting on, would
// look nearly idle.
func (s *Sampler) cpuStats(now time.Time) (busyPct, cores int) {
	host, hostCores := readCPUTimes(s.procDirFor("stat"))
	busyPct, _ = cpuBusyPercent(s.prevCPU, host)
	if host.valid {
		s.prevCPU = host
	}
	cores = hostCores
	if cores == 0 {
		cores = runtime.NumCPU()
	}

	sample, quota := s.cgroupCPU(now)
	if quota > 0 {
		cores = cgroupQuotaCores(quota, hostCores)
	}
	if !sample.valid {
		return busyPct, cores
	}
	budget := quota
	if budget <= 0 {
		budget = float64(cores)
	}
	// Once the cgroup can be read it is the only honest source, so its answer
	// stands even when this pass cannot derive one (the first sample, or a
	// counter reset). Falling back to the host figure would silently mix two
	// different machines' busyness across intervals.
	cgroupPct, _ := cgroupCPUPercent(s.prevCgroupCPU, sample, budget)
	s.prevCgroupCPU = sample
	return cgroupPct, cores
}

// diskPaths lists the mounts to sample, scratch dir first, bounded at
// maxSampledDisks.
//
// The bound is on what is *probed*, not only on what is published, because a
// probe is not free and cannot be taken back. Each path gets its own statfs
// goroutine every interval, and statfs on a dead network mount is
// uninterruptible — the goroutine parks until the mount recovers or the process
// exits. A deployment with forty library roots would otherwise start forty
// probes every five seconds to fill eight slots, and every unreachable root
// would leave a goroutine parked forever. Capping the input makes the worst
// case a fixed eight parked goroutines regardless of library count.
//
// The scratch dir is always first and so is never the entry dropped: it is the
// one mount admission control reads.
func (s *Sampler) diskPaths(ctx context.Context) []string {
	var paths []string
	if s.scratchDir != "" {
		paths = append(paths, s.scratchDir)
	}
	if s.mediaRoots != nil {
		for _, root := range s.mediaRoots(ctx) {
			if root != "" && !slices.Contains(paths, root) {
				paths = append(paths, root)
			}
		}
	}
	if len(paths) <= maxSampledDisks {
		s.noteDroppedRoots(0)
		return paths
	}
	s.noteDroppedRoots(len(paths) - maxSampledDisks)
	return paths[:maxSampledDisks]
}

// noteDroppedRoots reports that the disk sample does not cover every configured
// media root, so the omission is a log line rather than a silent truncation an
// operator would read as "every mount is fine".
//
// Logged on transitions only: the sampling loop runs every few seconds, and a
// library count is a standing property, not an event. Called from the sampling
// goroutine, which owns this field.
func (s *Sampler) noteDroppedRoots(dropped int) {
	if dropped == s.droppedRoots {
		return
	}
	s.droppedRoots = dropped
	if dropped == 0 {
		slog.Info("node metrics disk sampling now covers every configured root", "component", "nodemetrics")
		return
	}
	slog.Info("node metrics disk sampling is capped; the roots past the cap are not reported",
		"component", "nodemetrics", "sampled", maxSampledDisks, "not_sampled", dropped)
}

// sampleGPU merges three independent views of the host's GPUs: the hardware
// inventory (which devices exist), DRM fdinfo (what our own transcodes are
// doing on them), and nvidia-smi (what everyone is doing on an NVIDIA GPU).
//
// Devices are keyed by normalized PCI address wherever one is known, because
// that is the only identifier all three views share — /dev/dri paths are an
// enumeration order and CUDA indices are an nvidia-smi ordering.
func (s *Sampler) sampleGPU(ctx context.Context, now time.Time) []GPUStats {
	sessions := map[string]int{}
	if s.sessions != nil {
		sessions = s.sessions()
	}

	byKey := map[string]*GPUStats{}
	order := []string{}
	// aliases holds every name a device answers to, because the workload counter
	// is keyed by whatever playback was configured with (a render path, a CUDA
	// index, a GPU UUID) while entries here are keyed by PCI address.
	aliases := map[string][]string{}
	upsert := func(key string) *GPUStats {
		if existing, ok := byKey[key]; ok {
			return existing
		}
		entry := &GPUStats{Device: key, Source: SourceUnavailable}
		byKey[key] = entry
		order = append(order, key)
		aliases[key] = append(aliases[key], key)
		return entry
	}
	alias := func(key string, names ...string) {
		for _, name := range names {
			if name != "" && !slices.Contains(aliases[key], name) {
				aliases[key] = append(aliases[key], name)
			}
		}
	}

	// 1. Known hardware, so a device with no activity still appears.
	pathByPCI := map[string]string{}
	for _, identity := range s.identityList() {
		key := NormalizePCIAddress(identity.PCIAddress)
		if key == "" {
			key = identity.Path
		}
		if key == "" {
			continue
		}
		if identity.PCIAddress != "" && identity.Path != "" {
			pathByPCI[NormalizePCIAddress(identity.PCIAddress)] = identity.Path
		}
		entry := upsert(key)
		if identity.Path != "" {
			entry.Device = identity.Path
		}
		alias(key, identity.Path)
		entry.Vendor = identity.Vendor
	}

	// 2. DRM fdinfo deltas for our own ffmpeg children.
	clients := readFdinfoCounters(s.procDir, s.ffmpegPIDs())
	elapsedNS := int64(0)
	if !s.prevGPUAt.IsZero() {
		elapsedNS = now.Sub(s.prevGPUAt).Nanoseconds()
	}
	for pdev, delta := range deviceEngineDeltas(s.prevGPU, clients) {
		entry := upsert(pdev)
		if path, ok := pathByPCI[pdev]; ok {
			entry.Device = path
			alias(pdev, path)
		}
		if elapsedNS > 0 {
			entry.VideoBusyPct = engineBusyPercent(delta.videoNS, elapsedNS)
			entry.RenderBusyPct = engineBusyPercent(delta.renderNS, elapsedNS)
		}
		entry.Source = SourceFdinfo
	}
	// Clients that vanished (their transcode exited) drop out of the baseline
	// with their counters, so the next client on that device is not measured
	// against a stale origin.
	s.prevGPU = clients
	s.prevGPUAt = now

	// 3. nvidia-smi enrichment: the only signal for NVIDIA, and whole-GPU
	// (other tenants included) where it applies.
	for _, gpu := range s.queryNVIDIA(ctx) {
		cudaName := "cuda:" + strconv.Itoa(gpu.Index)
		key := gpu.PCIAddress
		if key == "" {
			key = cudaName
		}
		entry := upsert(key)
		if entry.Device == key && entry.Vendor == "" {
			// No DRM node for it (the proprietary driver exposes none this
			// process can read), so name it the way playback addresses it.
			entry.Device = cudaName
		}
		// NVENC workloads are counted under a CUDA name or a GPU UUID even when
		// the device is displayed by its render path, so both have to resolve to
		// this entry.
		alias(key, cudaName, gpu.UUID)
		entry.Vendor = vendorNVIDIA
		total := gpu.GPUUtil
		entry.TotalBusyPct = &total
		used, capacity := gpu.MemUsedMB, gpu.MemTotalMB
		entry.VRAMUsedMB = &used
		entry.VRAMTotalMB = &capacity
		if entry.Source == SourceFdinfo {
			entry.Source = SourceFdinfoNVIDIASMI
		} else {
			entry.Source = SourceNVIDIASMI
			// fdinfo is unimplemented by the proprietary driver, so the video
			// engines only have an nvidia-smi reading.
			entry.VideoBusyPct = max(gpu.EncoderUtil, gpu.DecoderUtil)
		}
	}

	if len(order) == 0 {
		return nil
	}
	claimed := map[string]bool{}
	out := make([]GPUStats, 0, len(order))
	for _, key := range order {
		entry := byKey[key]
		entry.Sessions = deviceSessions(sessions, aliases[key], claimed)
		out = append(out, *entry)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

// deviceSessions totals the balancer's workload counts for one device.
//
// The balancer counts a workload under whatever playback was configured with,
// which is not always the name this package displays: an NVENC job on a card
// that also has a readable render node is counted as "cuda:0" while the entry
// is shown as /dev/dri/renderD128. Looking the count up by display name alone
// reports 0 sessions on a node that is transcoding. Every alias is summed once
// — claimed keeps a name shared by two entries from being counted twice.
// The display name is always among the aliases: it is only ever set to one.
func deviceSessions(sessions map[string]int, aliases []string, claimed map[string]bool) int {
	total := 0
	for _, name := range aliases {
		if name == "" || claimed[name] {
			continue
		}
		claimed[name] = true
		total += sessions[name]
	}
	return total
}

func (s *Sampler) identityList() []DeviceIdentity {
	if s.identities == nil {
		return nil
	}
	return s.identities()
}

package nodemetrics

import (
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus exposure.
//
// The collector is built over the published snapshot rather than over
// promauto gauges written during sampling. A scrape therefore reports exactly
// the numbers the health response and the admin API report for the same
// instant, and — more importantly — a scrape can never wait on sampling, so a
// wedged mount or a hung nvidia-smi cannot turn into a Prometheus scrape
// timeout that looks like the node being down.
//
// Names carry the streamapp_ prefix the rest of the server already uses, and
// the node_ infix so a scrape of an integrated deployment separates host
// resources from request and domain metrics.
var (
	descCPUPercent = prometheus.NewDesc(
		"streamapp_node_cpu_percent",
		"Aggregate CPU busy percentage across all cores over the last sampling interval.",
		nil, nil)
	descLoad1 = prometheus.NewDesc(
		"streamapp_node_load1",
		"1-minute load average.",
		nil, nil)
	descMemoryUsed = prometheus.NewDesc(
		"streamapp_node_memory_used_bytes",
		"Memory in use, corrected by the cgroup limit and usage when running under one.",
		nil, nil)
	descMemoryTotal = prometheus.NewDesc(
		"streamapp_node_memory_total_bytes",
		"Memory available to this process's cgroup, or the host's when unconstrained.",
		nil, nil)
	descDiskUsed = prometheus.NewDesc(
		"streamapp_node_disk_used_bytes",
		"Used bytes on a sampled mount, labeled by role rather than by path.",
		[]string{"mount"}, nil)
	descDiskTotal = prometheus.NewDesc(
		"streamapp_node_disk_total_bytes",
		"Total bytes on a sampled mount, labeled by role rather than by path.",
		[]string{"mount"}, nil)
	descNetworkRx = prometheus.NewDesc(
		"streamapp_node_network_rx_bps",
		"Aggregate received bits per second, loopback excluded.",
		nil, nil)
	descNetworkTx = prometheus.NewDesc(
		"streamapp_node_network_tx_bps",
		"Aggregate transmitted bits per second, loopback excluded.",
		nil, nil)
	descGPUVideoBusy = prometheus.NewDesc(
		"streamapp_node_gpu_video_busy_percent",
		"GPU video engine busy percentage. From DRM fdinfo this covers only this node's own transcodes.",
		[]string{gpuDeviceLabel}, nil)
	descGPURenderBusy = prometheus.NewDesc(
		"streamapp_node_gpu_render_busy_percent",
		"GPU render engine busy percentage. From DRM fdinfo this covers only this node's own transcodes.",
		[]string{gpuDeviceLabel}, nil)
	descGPUSessions = prometheus.NewDesc(
		"streamapp_node_gpu_sessions",
		"Active GPU workloads this node has pinned to a device.",
		[]string{gpuDeviceLabel}, nil)
	descGPUVRAMUsed = prometheus.NewDesc(
		"streamapp_node_gpu_vram_used_bytes",
		"GPU memory in use, where an enrichment source reports it.",
		[]string{gpuDeviceLabel}, nil)
	descGPUVRAMTotal = prometheus.NewDesc(
		"streamapp_node_gpu_vram_total_bytes",
		"Total GPU memory, where an enrichment source reports it.",
		[]string{gpuDeviceLabel}, nil)
)

// gpuDeviceLabel is the Prometheus label name every per-GPU series is keyed
// by.
const gpuDeviceLabel = "device"

// isMeasuredGPUSource reports whether a source actually produced engine
// readings. SourceUnavailable means nothing could measure the device, so its
// zeros carry no measurement.
func isMeasuredGPUSource(source string) bool {
	return source != "" && source != SourceUnavailable
}

// collector adapts a Sampler to prometheus.Collector.
type collector struct{ sampler *Sampler }

// Describe is deliberately unimplemented (an unchecked collector): the disk and
// GPU label sets are discovered at sample time and legitimately change when a
// mount or a card appears, which a checked collector would reject.
func (collector) Describe(chan<- *prometheus.Desc) {}

// Collect reads the latest snapshot. It performs no I/O and takes no lock the
// sampler holds while doing I/O, so it cannot block a scrape.
func (c collector) Collect(ch chan<- prometheus.Metric) {
	snapshot := c.sampler.Snapshot()
	if !snapshot.Available {
		return
	}
	gauge := func(desc *prometheus.Desc, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
	if system := snapshot.System; system != nil {
		gauge(descCPUPercent, float64(system.CPUPct))
		gauge(descLoad1, system.Load1)
		gauge(descMemoryUsed, float64(system.MemUsedMB)*float64(bytesPerMB))
		gauge(descMemoryTotal, float64(system.MemTotalMB)*float64(bytesPerMB))
		gauge(descNetworkRx, float64(system.NetRxBps))
		gauge(descNetworkTx, float64(system.NetTxBps))
		const bytesPerGB = float64(1024 * 1024 * 1024)
		for _, disk := range system.Disks {
			if disk.Unavailable || disk.Role == "" {
				// A path this node cannot measure has no value to report, and a
				// zero would read as an empty disk in every alert rule.
				continue
			}
			gauge(descDiskUsed, disk.UsedGB*bytesPerGB, disk.Role)
			gauge(descDiskTotal, disk.TotalGB*bytesPerGB, disk.Role)
		}
	}
	const bytesPerMBFloat = float64(1024 * 1024)
	for _, gpu := range snapshot.GPU {
		// Engine percentages are omitted rather than reported as zero when
		// nothing measured them. The JSON surfaces carry `source` alongside and
		// can render the difference; a Prometheus sample cannot, so an exported
		// 0 would read as an idle GPU on every dashboard and alert — including
		// for a card that is busy and merely unobservable, which is exactly the
		// state `unavailable` names. An absent series is the honest shape for a
		// number that was not taken.
		if isMeasuredGPUSource(gpu.Source) {
			gauge(descGPUVideoBusy, float64(gpu.VideoBusyPct), gpu.Device)
			gauge(descGPURenderBusy, float64(gpu.RenderBusyPct), gpu.Device)
		}
		// Sessions always ships: it comes from this process's own workload
		// accounting, not from a driver, so it is exact whatever the driver can
		// or cannot tell us — and a busy GPU with no engine reading is precisely
		// when an operator needs it.
		gauge(descGPUSessions, float64(gpu.Sessions), gpu.Device)
		if gpu.VRAMUsedMB != nil {
			gauge(descGPUVRAMUsed, float64(*gpu.VRAMUsedMB)*bytesPerMBFloat, gpu.Device)
		}
		if gpu.VRAMTotalMB != nil {
			gauge(descGPUVRAMTotal, float64(*gpu.VRAMTotalMB)*bytesPerMBFloat, gpu.Device)
		}
	}
}

// Disk series are labeled by DiskStats.Role rather than by path.
//
// /metrics is deliberately unauthenticated on the same listener that serves the
// API and the SPA, so anything labeled here is public. A library root's path is
// deployment layout, not a host resource counter: publishing it would let any
// anonymous client enumerate a deployment's media mounts, which is precisely
// what the admin-authenticated /admin/system/resources exists to gate. Roles
// keep the series useful — scratch is the volume that kills transcodes when it
// fills, and library ordering is stable for a given configuration — while the
// paths themselves stay behind auth. The role is assigned once when the sample
// is built, so this scrape and the node's /health name a mount identically.

// collectorRegistration keeps the default registry to one node collector.
// Integrated mode constructs one sampler, but a test — or a future deployment
// that ran two — must not panic the process on a duplicate registration.
var collectorRegistration sync.Once

// registerCollector publishes a sampler's readings on the default registry.
func registerCollector(sampler *Sampler) {
	collectorRegistration.Do(func() {
		if err := prometheus.Register(collector{sampler: sampler}); err != nil {
			slog.Warn("node metrics collector not registered", "component", "nodemetrics", "error", err)
		}
	})
}

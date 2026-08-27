import type {
  HostDiskStats,
  HostGPUStats,
  HostSystemStats,
  NodeCapabilities,
  NodeRenderDevice,
  StreamNode,
  SystemResources,
} from "@/api/types";
import { formatFileSize } from "@/lib/mediaFormat";

/**
 * How long a stored capability report may go unconfirmed before it is called
 * out. The health sweep only refetches when a node advertises a changed hash,
 * so an unchanged node legitimately keeps a report from hours ago — its age
 * says nothing. What does tick is the sweep itself: every check that finds the
 * advertised hash unchanged re-confirms the stored report. So the report is
 * only doubtful once the checks stop, which at a 30s cadence this allows twenty
 * of before saying so.
 */
export const CAPABILITY_STALE_AFTER_MS = 10 * 60 * 1000;

/** Verification state of the backend a node resolved to. */
export type NodeGPUBackendState = "verified" | "failed" | "unverified" | "none";

export interface NodeGPUBackendBadge {
  /** Uppercase backend name, or "SW" when no hardware backend is in use. */
  label: string;
  state: NodeGPUBackendState;
  /** Tinted badge classes, shared with the activity-method tags. */
  badgeClass: string;
  /** Hover text: the probe outcome, including a failure reason. */
  title: string;
}

/** A backend that had candidate hardware but failed its FFmpeg probe. */
export interface NodeGPUFailure {
  label: string;
  reason: string;
}

export type NodeGPUPresentation =
  | {
      kind: "awaiting";
      label: string;
      title: string;
    }
  | {
      kind: "reported";
      backend: NodeGPUBackendBadge;
      /** Failed backends other than the resolved one, whose reason is in its title. */
      failures: NodeGPUFailure[];
      /** Compact device list, e.g. "2× Intel GPU"; null when none were reported. */
      deviceSummary: string | null;
      /** Full device paths, one per line, for the summary's tooltip. */
      deviceTitle: string | null;
      /** No health check has re-confirmed this report recently. */
      stale: boolean;
      /**
       * Live per-device readings from the node's last health check, matched
       * against the capability inventory. Empty when the node reports no
       * sample — a server or node predating resource sampling renders exactly
       * as it did before, rather than showing zeros it never measured.
       */
      live: NodeGPULiveDevice[];
    };

/** One GPU's live reading, as rendered next to the capability inventory. */
export interface NodeGPULiveDevice {
  /** Stable key for list rendering; the device id as the node reported it. */
  key: string;
  /** Short device name: "renderD128", or the raw id like "cuda:0". */
  label: string;
  /** Video-engine busy percentage, or a dash when nothing measured it. */
  busy: string;
  /** No measurement behind `busy`: render it muted, since it is not a zero. */
  busyMuted: boolean;
  /** "2 sessions", or "idle" for none. */
  sessions: string;
  /** Hover text: device identity, both engines, whole-GPU/VRAM, and source. */
  title: string;
}

const BACKEND_BADGE_CLASS: Record<NodeGPUBackendState, string> = {
  verified: "bg-success/10 text-success border-success/15",
  failed: "bg-warning/10 text-warning border-warning/15",
  unverified: "bg-surface text-muted-foreground border-border",
  none: "bg-surface text-muted-foreground border-border",
};

/**
 * Describe a node's GPU column. `now` is injected so staleness is testable and
 * so a rendered table can be pinned to one clock reading.
 */
export function describeNodeGPU(node: StreamNode, now: number = Date.now()): NodeGPUPresentation {
  const capabilities = node.capabilities;
  if (!capabilities) {
    return {
      kind: "awaiting",
      label: "Awaiting first report",
      title: "No hardware capability report has been stored for this node yet.",
    };
  }

  const devices = summarizeRenderDevices(capabilities);
  const resolved = capabilities.resolved?.trim().toLowerCase() ?? "";
  return {
    kind: "reported",
    backend: describeBackend(resolved, capabilities.detected_backends ?? []),
    failures: otherFailures(resolved, capabilities.detected_backends ?? []),
    deviceSummary: devices.summary,
    deviceTitle: devices.title,
    stale: isCapabilityReportStale(node, now),
    live: describeLiveGPUs(node),
  };
}

/**
 * Live GPU readings for a node, matched to its capability inventory.
 *
 * An unhealthy node contributes none: its sample is from before the check that
 * failed, and a busy percentage that stopped moving reads as a live one.
 */
function describeLiveGPUs(node: StreamNode): NodeGPULiveDevice[] {
  if (!node.healthy) {
    return [];
  }
  const details = node.capabilities?.render_device_details ?? [];
  return (node.last_stats?.gpu ?? []).map((stats) => describeLiveGPU(stats, details));
}

function describeLiveGPU(
  stats: HostGPUStats,
  details: readonly NodeRenderDevice[],
): NodeGPULiveDevice {
  const device = stats.device?.trim() ?? "";
  // The inventory speaks /dev/dri paths; a sample for a GPU with no readable
  // DRM node names a PCI address or a cuda index instead, which is why the
  // fallback match exists rather than the path lookup alone.
  const detail =
    details.find((candidate) => candidate.path?.trim() === device && device !== "") ??
    details.find((candidate) => candidate.pci_address?.trim() === device && device !== "");
  const measured = isMeasuredGPUSource(stats.source);
  const video = finiteNumber(stats.video_busy_pct);
  const sessions = Math.max(0, Math.trunc(finiteNumber(stats.sessions) ?? 0));

  return {
    key: device || (detail?.path ?? "gpu"),
    label: shortDeviceLabel(device, detail),
    busy: measured && video != null ? `${clampPercent(video)}%` : DASH,
    busyMuted: !measured || video == null,
    sessions: sessions === 1 ? "1 session" : sessions === 0 ? "idle" : `${sessions} sessions`,
    title: liveGPUTitle(stats, detail, device, measured),
  };
}

function liveGPUTitle(
  stats: HostGPUStats,
  detail: NodeRenderDevice | undefined,
  device: string,
  measured: boolean,
): string {
  const identity = [
    device || detail?.path?.trim() || "(unknown device)",
    detail?.description?.trim(),
  ]
    .filter((part): part is string => !!part)
    .join(" — ");
  const lines = [identity];

  if (measured) {
    const engines = [
      formatEngine("video", stats.video_busy_pct),
      formatEngine("render", stats.render_busy_pct),
    ].filter((part): part is string => part !== null);
    if (engines.length > 0) {
      lines.push(engines.join(" · "));
    }
  } else {
    // Zeros with no measurement behind them must not read as an idle GPU.
    lines.push("No source could measure this device on the last sample.");
  }

  const whole = finiteNumber(stats.total_busy_pct);
  if (whole != null) {
    lines.push(`whole GPU ${clampPercent(whole)}% (all tenants)`);
  }
  const vram = formatUsedOfTotal(stats.vram_used_mb, stats.vram_total_mb, mebibytesToBytes);
  if (vram) {
    lines.push(`VRAM ${vram}`);
  }
  const source = stats.source?.trim();
  if (source) {
    lines.push(`source: ${source}`);
  }
  return lines.join("\n");
}

function formatEngine(name: string, value: number | null | undefined): string | null {
  const percent = finiteNumber(value);
  return percent == null ? null : `${name} ${clampPercent(percent)}%`;
}

function isMeasuredGPUSource(source: string | undefined): boolean {
  const normalized = source?.trim().toLowerCase() ?? "";
  return normalized !== "" && normalized !== "unavailable";
}

function shortDeviceLabel(device: string, detail: NodeRenderDevice | undefined): string {
  const path = device || detail?.path?.trim() || "";
  if (path === "") {
    return "GPU";
  }
  const tail = path.split("/").filter((part) => part !== "");
  return tail[tail.length - 1] ?? path;
}

function describeBackend(resolved: string, detected: readonly NodeDetected[]): NodeGPUBackendBadge {
  if (resolved === "" || resolved === "none") {
    const skipped = detected.filter((entry) => entry.skipped);
    if (skipped.length > 0 && skipped.length === detected.length) {
      return badge(
        "SW",
        "none",
        "Encoding in software — the configured GPU devices are not accessible on this node.",
      );
    }
    return badge("SW", "none", "No hardware backend verified — encoding in software.");
  }

  const label = resolved.toUpperCase();
  const entry = detected.find((candidate) => candidate.backend?.trim().toLowerCase() === resolved);
  if (!entry) {
    // A configured backend wins resolution even with no candidate hardware to
    // probe, so absence of an entry is unknown, not failure.
    return badge(
      label,
      "unverified",
      `${label} is in use but this node reported no verification probe for it.`,
    );
  }
  if (!entry.verified) {
    return badge(label, "failed", `${label} probe failed: ${failureReason(entry)}`);
  }
  const device = entry.device?.trim();
  return badge(
    label,
    "verified",
    device
      ? `${label} verified by FFmpeg probe on ${device}.`
      : `${label} verified by FFmpeg probe.`,
  );
}

function badge(label: string, state: NodeGPUBackendState, title: string): NodeGPUBackendBadge {
  return { label, state, badgeClass: BACKEND_BADGE_CLASS[state], title };
}

type NodeDetected = NonNullable<NodeCapabilities["detected_backends"]>[number];

function otherFailures(resolved: string, detected: readonly NodeDetected[]): NodeGPUFailure[] {
  return detected
    .filter((entry) => {
      const backend = entry.backend?.trim().toLowerCase() ?? "";
      // Skipped entries were never probed (their devices are not accessible
      // on this node) — expected on proxies, so they do not warrant a warning.
      return !entry.verified && !entry.skipped && backend !== "" && backend !== resolved;
    })
    .map((entry) => ({
      label: (entry.backend?.trim() ?? "").toUpperCase(),
      reason: failureReason(entry),
    }));
}

function failureReason(entry: NodeDetected): string {
  return entry.reason?.trim() || "no reason reported";
}

function isCapabilityReportStale(node: StreamNode, now: number): boolean {
  // An unhealthy node cannot refresh its report; calling that stale would blame
  // the inventory for the outage the Health column already shows.
  if (!node.healthy) {
    return false;
  }
  if (Number.isNaN(Date.parse(node.capabilities_refreshed_at ?? ""))) {
    return false;
  }
  // Measured against the health check, not against the report's own age: the
  // check is what re-confirms the report, and it is the only one of the two
  // that moves on a node whose hardware never changes.
  const lastCheck = Date.parse(node.last_health_check ?? "");
  if (Number.isNaN(lastCheck)) {
    return false;
  }
  return now - lastCheck > CAPABILITY_STALE_AFTER_MS;
}

function summarizeRenderDevices(capabilities: NodeCapabilities): {
  summary: string | null;
  title: string | null;
} {
  const details = capabilities.render_device_details ?? [];
  if (details.length > 0) {
    return {
      summary: countedDescriptions(details),
      title: details.map(describeDeviceLine).join("\n"),
    };
  }

  // A report with paths but no details is still worth a count.
  const paths = (capabilities.render_devices ?? []).filter((path) => path.trim() !== "");
  if (paths.length === 0) {
    return { summary: null, title: null };
  }
  return {
    summary: paths.length === 1 ? "1 render device" : `${paths.length} render devices`,
    title: paths.join("\n"),
  };
}

/** "2× Intel GPU, NVIDIA GPU (0x2204)" — identical descriptions collapse. */
function countedDescriptions(details: readonly NodeRenderDevice[]): string {
  const counted: { label: string; count: number }[] = [];
  for (const device of details) {
    const label = deviceLabel(device);
    const existing = counted.find((entry) => entry.label === label);
    if (existing) {
      existing.count += 1;
    } else {
      counted.push({ label, count: 1 });
    }
  }
  return counted
    .map((entry) => (entry.count > 1 ? `${entry.count}× ${entry.label}` : entry.label))
    .join(", ");
}

function deviceLabel(device: NodeRenderDevice): string {
  return device.description?.trim() || device.path?.trim() || "GPU";
}

function describeDeviceLine(device: NodeRenderDevice): string {
  const path = device.path?.trim() || "(unknown path)";
  const parts = [path, device.description?.trim()].filter((part): part is string => !!part);
  const line = parts.join(" — ");
  const address = device.pci_address?.trim();
  return address ? `${line} (${address})` : line;
}

// --- Host resource samples -------------------------------------------------
//
// A node's last_stats and the API host's /admin/system/resources carry the same
// shapes, so both surfaces derive their numbers here. Nothing below invents a
// value: a field the sampler could not measure renders as a dash, never as a
// zero, because a zero on a dashboard is read as "measured and idle".

/** Placeholder for a reading the sample does not carry. */
const DASH = "—";

/**
 * Fill percentage at which a mount is called out. A transcode scratch volume
 * that fills stops transcodes with no other warning, and the last few percent
 * of a volume disappear fast under a segment writer, so the threshold sits far
 * enough below full to leave an operator time to act.
 */
export const DISK_FILL_WARNING_PCT = 85;

/** One derived reading, ready to render in a table cell or a stat tile. */
export interface ResourceMetric {
  label: string;
  /** Rendered value, or a dash when `muted`. */
  value: string;
  /** Short secondary line for the dashboard tiles; empty when there is none. */
  detail: string;
  /** Hover text explaining the value, or why there is none. */
  title: string;
  /** Nothing measured this reading: render it in the muted color. */
  muted: boolean;
  /** Past an attention threshold; render with the warning tint. */
  warning: boolean;
}

export type NodeSystemPresentation =
  | {
      kind: "unreported";
      /** Dash, so the column keeps its shape. */
      label: string;
      title: string;
    }
  | {
      kind: "reported";
      cpu: ResourceMetric;
      memory: ResourceMetric;
      disk: ResourceMetric;
      network: ResourceMetric;
    };

/**
 * Describe a node's System column from the sample its last health check
 * carried.
 *
 * An unhealthy node reports nothing rather than its last numbers: the sample
 * predates the check that failed, and a frozen CPU percentage is indis-
 * tinguishable from a live one on screen.
 */
export function describeNodeSystem(node: StreamNode): NodeSystemPresentation {
  const system = node.last_stats?.system;
  if (!system) {
    return {
      kind: "unreported",
      label: DASH,
      title: node.healthy
        ? "This node reported no resource sample. Sampling is Linux-only, and a node running a build from before resource sampling reports none."
        : "This node is not answering health checks, so it has no current resource sample.",
    };
  }
  if (!node.healthy) {
    return {
      kind: "unreported",
      label: DASH,
      title:
        "The last health check did not reach this node, so its most recent resource sample is no longer current.",
    };
  }
  return describeSystemStats(system);
}

/** Derive the four host readings from one system sample. */
export function describeSystemStats(system: HostSystemStats): {
  kind: "reported";
  cpu: ResourceMetric;
  memory: ResourceMetric;
  disk: ResourceMetric;
  network: ResourceMetric;
} {
  return {
    kind: "reported",
    cpu: describeCPU(system),
    memory: describeMemory(system),
    disk: describeWorstDisk(system.disks ?? []),
    network: describeNetwork(system),
  };
}

function describeCPU(system: HostSystemStats): ResourceMetric {
  const cpu = finiteNumber(system.cpu_pct);
  const cores = finiteNumber(system.cores);
  const load1 = finiteNumber(system.load1);
  if (cpu == null) {
    return mutedMetric("CPU", "This sample carries no CPU reading.");
  }

  const percent = clampPercent(cpu);
  const detail = [
    cores != null && cores > 0 ? (cores === 1 ? "1 core" : `${cores} cores`) : null,
    load1 != null ? `load ${load1.toFixed(2)}` : null,
  ]
    .filter((part): part is string => part !== null)
    .join(" · ");

  const title = [
    `${percent}% busy across all cores over the last sampling interval.`,
    cores != null && cores > 0
      ? `${cores} CPU(s) available to this host — its cgroup quota where one is set.`
      : null,
    load1 != null
      ? `1-minute load ${load1.toFixed(2)}, which also counts tasks blocked on storage.`
      : null,
  ]
    .filter((part): part is string => part !== null)
    .join(" ");

  return { label: "CPU", value: `${percent}%`, detail, title, muted: false, warning: false };
}

function describeMemory(system: HostSystemStats): ResourceMetric {
  const used = finiteNumber(system.mem_used_mb);
  const total = finiteNumber(system.mem_total_mb);
  if (used == null || total == null || total <= 0) {
    return mutedMetric("RAM", "This sample carries no memory reading.");
  }

  const value = formatUsedOfTotal(used, total, mebibytesToBytes) ?? DASH;
  const percent = clampPercent((used / total) * 100);
  return {
    label: "RAM",
    value,
    detail: `${percent}% used`,
    title: `${value} used (${percent}%). Under a cgroup this is the container's limit and working set, not the host's.`,
    muted: false,
    warning: false,
  };
}

/**
 * The fullest sampled mount, which for a transcode node is its scratch dir —
 * the only mount it samples. Reporting the worst rather than the first keeps
 * the same rule working on the API host, which also samples the media roots.
 */
export function describeWorstDisk(disks: readonly HostDiskStats[]): ResourceMetric {
  const measured = disks
    .map((disk) => ({ disk, fill: diskFillPercent(disk) }))
    .filter((entry): entry is { disk: HostDiskStats; fill: number } => entry.fill !== null);

  const title = disks.length > 0 ? disks.map(describeDiskLine).join("\n") : "";
  if (measured.length === 0) {
    // Paths that exist but could not be measured are still worth naming in the
    // tooltip, so an operator sees which mount went away rather than a dash
    // with no explanation.
    return mutedMetric("Disk", title === "" ? "This sample carries no disk reading." : title);
  }

  const worst = measured.reduce((a, b) => (b.fill > a.fill ? b : a));
  const path = worst.disk.path?.trim() ?? "";
  return {
    label: "Disk",
    value: `${worst.fill}%`,
    detail: path === "" ? "full" : path,
    title,
    muted: false,
    warning: worst.fill >= DISK_FILL_WARNING_PCT,
  };
}

function describeNetwork(system: HostSystemStats): ResourceMetric {
  const rx = formatBitsPerSecond(system.net_rx_bps);
  const tx = formatBitsPerSecond(system.net_tx_bps);
  if (rx === null && tx === null) {
    return mutedMetric("Net", "This sample carries no network reading.");
  }

  return {
    label: "Net",
    value: `↓ ${rx ?? DASH} · ↑ ${tx ?? DASH}`,
    detail: "rx · tx",
    title: `Aggregate throughput with loopback excluded: ${rx ?? DASH} in, ${tx ?? DASH} out. In a container this is the container's own network namespace.`,
    muted: false,
    warning: false,
  };
}

export type ResourceSamplePresentation =
  | {
      kind: "unavailable";
      title: string;
    }
  | {
      kind: "sampled";
      cpu: ResourceMetric;
      memory: ResourceMetric;
      disk: ResourceMetric;
      network: ResourceMetric;
      /** Busiest GPU's video engine; null when this host reports no GPU. */
      gpu: ResourceMetric | null;
      /** When the sample was taken, for a freshness label; null when unstamped. */
      sampledAt: string | null;
    };

/**
 * Describe the API host's own sample. A server predating the endpoint answers
 * 404 and leaves `resources` undefined, which is the same story as a host that
 * cannot be sampled: no numbers, said plainly, rather than an error.
 */
export function describeResourceSample(
  resources: SystemResources | undefined | null,
): ResourceSamplePresentation {
  const system = resources?.system;
  if (!resources || resources.available !== true || !system) {
    return {
      kind: "unavailable",
      title:
        "This host is not being sampled. Resource sampling is Linux-only, and the first sample lands a few seconds after startup.",
    };
  }

  return {
    ...describeSystemStats(system),
    kind: "sampled",
    gpu: describeGPUBusy(resources.gpu ?? []),
    sampledAt: resources.sampled_at?.trim() || null,
  };
}

/**
 * The busiest GPU's video engine, which is the one an operator asks about when
 * transcodes queue. Averaging would hide a saturated card behind an idle one.
 * Null when the host reports no GPU at all, so the tile is omitted rather than
 * showing a zero.
 */
export function describeGPUBusy(gpu: readonly HostGPUStats[]): ResourceMetric | null {
  if (gpu.length === 0) {
    return null;
  }

  const sessions = gpu.reduce((total, stats) => total + (finiteNumber(stats.sessions) ?? 0), 0);
  const sessionLabel = sessions === 1 ? "1 session" : `${sessions} sessions`;
  const title = gpu
    .map((stats) => {
      const device = stats.device?.trim() || "(unknown device)";
      const measured = isMeasuredGPUSource(stats.source);
      const video = finiteNumber(stats.video_busy_pct);
      const reading = measured && video != null ? `video ${clampPercent(video)}%` : "not measured";
      const count = Math.max(0, Math.trunc(finiteNumber(stats.sessions) ?? 0));
      return `${device} — ${reading} · ${count === 1 ? "1 session" : `${count} sessions`}`;
    })
    .join("\n");

  const busiest = gpu
    .filter((stats) => isMeasuredGPUSource(stats.source))
    .map((stats) => finiteNumber(stats.video_busy_pct))
    .filter((value): value is number => value !== null)
    .reduce<number | null>((best, value) => (best === null || value > best ? value : best), null);

  if (busiest === null) {
    return mutedMetric("GPU", title === "" ? "No GPU reading in this sample." : title);
  }

  const label = gpu.length === 1 ? "video engine" : `busiest of ${gpu.length} GPUs`;
  return {
    label: "GPU",
    value: `${clampPercent(busiest)}%`,
    detail: `${label} · ${sessionLabel}`,
    title,
    muted: false,
    warning: false,
  };
}

function describeDiskLine(disk: HostDiskStats): string {
  const path = disk.path?.trim() || "(unknown path)";
  if (disk.unavailable) {
    return `${path} — unavailable on this host`;
  }
  const fill = diskFillPercent(disk);
  if (fill === null) {
    return `${path} — no capacity reported`;
  }
  const capacity = formatUsedOfTotal(disk.used_gb, disk.total_gb, gibibytesToBytes);
  const line = `${path} — ${fill}% full${capacity ? ` (${capacity})` : ""}`;
  // "Real but old" is the normal reading for a network mount whose server went
  // away, and it is a different fact from "never measured".
  return disk.stale ? `${line}, carried over from an earlier pass` : line;
}

function diskFillPercent(disk: HostDiskStats): number | null {
  if (disk.unavailable) {
    return null;
  }
  const used = finiteNumber(disk.used_gb);
  const total = finiteNumber(disk.total_gb);
  if (used == null || total == null || total <= 0) {
    return null;
  }
  return clampPercent((used / total) * 100);
}

/** "12.4 GiB of 31.3 GiB", or null when either side is missing. */
function formatUsedOfTotal(
  used: number | null | undefined,
  total: number | null | undefined,
  toBytes: (value: number) => number,
): string | null {
  const usedValue = finiteNumber(used);
  const totalValue = finiteNumber(total);
  if (usedValue == null || totalValue == null || totalValue <= 0) {
    return null;
  }
  const usedLabel = formatFileSize(toBytes(usedValue), { iecUnits: true, fallback: "0 B" });
  const totalLabel = formatFileSize(toBytes(totalValue), { iecUnits: true });
  return `${usedLabel} of ${totalLabel}`;
}

/**
 * Humanize a *bits*-per-second rate, which is the unit the sampler reports and
 * the unit egress is already quoted in elsewhere in the cluster. Decimal scale,
 * because network rates are decimal everywhere they are quoted.
 */
export function formatBitsPerSecond(bps: number | null | undefined): string | null {
  const value = finiteNumber(bps);
  if (value == null || value < 0) {
    return null;
  }
  if (value >= 1e9) {
    return `${(value / 1e9).toFixed(1)} Gbps`;
  }
  if (value >= 1e6) {
    return `${(value / 1e6).toFixed(1)} Mbps`;
  }
  if (value >= 1e3) {
    return `${Math.round(value / 1e3)} kbps`;
  }
  return `${Math.round(value)} bps`;
}

function mebibytesToBytes(value: number): number {
  return value * 1024 ** 2;
}

function gibibytesToBytes(value: number): number {
  return value * 1024 ** 3;
}

function mutedMetric(label: string, title: string): ResourceMetric {
  return { label, value: DASH, detail: "", title, muted: true, warning: false };
}

function finiteNumber(value: number | null | undefined): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function clampPercent(value: number): number {
  return Math.min(100, Math.max(0, Math.round(value)));
}

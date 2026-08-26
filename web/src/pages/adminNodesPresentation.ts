import type { NodeCapabilities, NodeRenderDevice, StreamNode } from "@/api/types";

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
    };

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
  };
}

function describeBackend(resolved: string, detected: readonly NodeDetected[]): NodeGPUBackendBadge {
  if (resolved === "" || resolved === "none") {
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
      return !entry.verified && backend !== "" && backend !== resolved;
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

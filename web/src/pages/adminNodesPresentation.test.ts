import { describe, expect, it } from "vitest";
import type { HostSystemStats, StreamNode } from "@/api/types";
import {
  CAPABILITY_STALE_AFTER_MS,
  DISK_FILL_WARNING_PCT,
  describeGPUBusy,
  describeNodeAccelerationOverride,
  describeNodeGPU,
  describeNodeSystem,
  describeResourceSample,
  describeSharedGPU,
  formatBitsPerSecond,
  parseHWDeviceOverride,
} from "./adminNodesPresentation";

const NOW = Date.parse("2026-08-26T12:00:00Z");

function makeNode(overrides: Partial<StreamNode> = {}): StreamNode {
  return {
    id: 1,
    name: "transcode-1",
    type: "transcode",
    url: "http://10.0.0.5:8082",
    enabled: true,
    healthy: true,
    active_jobs: 0,
    group: null,
    max_jobs: null,
    max_bandwidth_kbps: null,
    egress_kbps: 0,
    last_health_check: "2026-08-26T11:59:50Z",
    created_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

describe("describeNodeGPU", () => {
  it("reports a node with no stored capabilities as awaiting its first report", () => {
    expect(describeNodeGPU(makeNode(), NOW)).toEqual({
      kind: "awaiting",
      label: "Awaiting first report",
      title: "No hardware capability report has been stored for this node yet.",
    });
  });

  it("treats an explicit null payload the same as an absent one", () => {
    expect(describeNodeGPU(makeNode({ capabilities: null }), NOW).kind).toBe("awaiting");
  });

  it("marks the resolved backend verified when its probe passed", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "qsv",
          detected_backends: [
            {
              backend: "qsv",
              verified: true,
              devices: ["/dev/dri/renderD128"],
              device: "/dev/dri/renderD128",
            },
          ],
        },
        capabilities_refreshed_at: "2026-08-26T11:59:00Z",
      }),
      NOW,
    );

    expect(presentation).toMatchObject({
      kind: "reported",
      backend: {
        label: "QSV",
        state: "verified",
        badgeClass: "bg-success/10 text-success border-success/15",
        title: "QSV verified by FFmpeg probe on /dev/dri/renderD128.",
      },
      failures: [],
      stale: false,
    });
  });

  it("omits the device from an NVENC title, which has no render node", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "nvenc",
          detected_backends: [{ backend: "nvenc", verified: true }],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.backend.title).toBe(
      "NVENC verified by FFmpeg probe.",
    );
  });

  it("warns with the failure reason when the resolved backend failed its probe", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "qsv",
          detected_backends: [
            {
              backend: "qsv",
              verified: false,
              reason: "h264_qsv smoke encode failed: device busy",
            },
          ],
        },
      }),
      NOW,
    );

    expect(presentation).toMatchObject({
      kind: "reported",
      backend: {
        label: "QSV",
        state: "failed",
        badgeClass: "bg-warning/10 text-warning border-warning/15",
        title: "QSV probe failed: h264_qsv smoke encode failed: device busy",
      },
      failures: [],
    });
  });

  it("names a failed backend with no reason rather than showing an empty title", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "vaapi",
          detected_backends: [{ backend: "vaapi", verified: false }],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.backend.title).toBe(
      "VAAPI probe failed: no reason reported",
    );
  });

  it("lists failed backends other than the resolved one", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "vaapi",
          detected_backends: [
            { backend: "qsv", verified: false, reason: "h264_qsv encoder unavailable" },
            { backend: "vaapi", verified: true, device: "/dev/dri/renderD128" },
          ],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.failures).toEqual([
      { label: "QSV", reason: "h264_qsv encoder unavailable" },
    ]);
  });

  it("does not warn about skipped backends whose devices are inaccessible", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "none",
          detected_backends: [
            {
              backend: "qsv",
              verified: false,
              skipped: true,
              reason: "/dev/dri/renderD128: device not accessible on this node",
            },
            {
              backend: "vaapi",
              verified: false,
              skipped: true,
              reason: "/dev/dri/renderD128: device not accessible on this node",
            },
          ],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.failures).toEqual([]);
    expect(presentation.kind === "reported" && presentation.backend.label).toBe("SW");
    expect(presentation.kind === "reported" && presentation.backend.title).toContain(
      "not accessible on this node",
    );
  });

  it("falls back to software with no hardware backend resolved", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: { resolved: "none", render_devices: [], render_device_details: [] },
      }),
      NOW,
    );

    expect(presentation).toMatchObject({
      kind: "reported",
      backend: { label: "SW", state: "none" },
      deviceSummary: null,
      deviceTitle: null,
    });
  });

  it("treats a configured backend with no probe entry as unverified, not failed", () => {
    const presentation = describeNodeGPU(makeNode({ capabilities: { resolved: "qsv" } }), NOW);

    expect(presentation).toMatchObject({
      kind: "reported",
      backend: {
        label: "QSV",
        state: "unverified",
        title: "QSV is in use but this node reported no verification probe for it.",
      },
    });
  });

  it("collapses identical device descriptions and keeps full paths in the title", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "qsv",
          render_device_details: [
            { path: "/dev/dri/renderD128", description: "Intel GPU", pci_address: "0000:00:02.0" },
            { path: "/dev/dri/renderD129", description: "Intel GPU" },
            { path: "/dev/dri/renderD130", description: "NVIDIA GPU (0x2204)" },
          ],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.deviceSummary).toBe(
      "2× Intel GPU, NVIDIA GPU (0x2204)",
    );
    expect(presentation.kind === "reported" && presentation.deviceTitle).toBe(
      [
        "/dev/dri/renderD128 — Intel GPU (0000:00:02.0)",
        "/dev/dri/renderD129 — Intel GPU",
        "/dev/dri/renderD130 — NVIDIA GPU (0x2204)",
      ].join("\n"),
    );
  });

  it("counts render device paths when a report carries no details", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "vaapi",
          render_devices: ["/dev/dri/renderD128", "/dev/dri/renderD129"],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.deviceSummary).toBe("2 render devices");
    expect(presentation.kind === "reported" && presentation.deviceTitle).toBe(
      "/dev/dri/renderD128\n/dev/dri/renderD129",
    );
  });

  it("marks a node stale once the health checks that confirm its report stop", () => {
    const node = makeNode({
      capabilities: { resolved: "qsv" },
      capabilities_refreshed_at: new Date(NOW - 6 * 60 * 60 * 1000).toISOString(),
      last_health_check: new Date(NOW - CAPABILITY_STALE_AFTER_MS - 1000).toISOString(),
    });

    expect(describeNodeGPU(node, NOW)).toMatchObject({ stale: true });
    // The same node read earlier was still being checked: the clock decides.
    expect(describeNodeGPU(node, NOW - CAPABILITY_STALE_AFTER_MS)).toMatchObject({ stale: false });
  });

  // The sweep refetches only when a node advertises a changed hash, so an
  // untouched GPU keeps its original report forever by design. Calling that
  // stale would light the warning on every steady-state node.
  it("does not call an old report stale while health checks keep confirming it", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: { resolved: "qsv" },
        capabilities_refreshed_at: new Date(NOW - 6 * 60 * 60 * 1000).toISOString(),
        last_health_check: new Date(NOW - 20 * 1000).toISOString(),
      }),
      NOW,
    );

    expect(presentation).toMatchObject({ stale: false });
  });

  it("does not call an unhealthy node's report stale", () => {
    const presentation = describeNodeGPU(
      makeNode({
        healthy: false,
        capabilities: { resolved: "qsv" },
        capabilities_refreshed_at: new Date(NOW - 24 * 60 * 60 * 1000).toISOString(),
        last_health_check: new Date(NOW - 24 * 60 * 60 * 1000).toISOString(),
      }),
      NOW,
    );

    expect(presentation).toMatchObject({ stale: false });
  });

  it("is not stale when the server sent no refresh timestamp", () => {
    expect(describeNodeGPU(makeNode({ capabilities: { resolved: "qsv" } }), NOW)).toMatchObject({
      stale: false,
    });
  });

  it("is not stale when the server sent no health check timestamp", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: { resolved: "qsv" },
        capabilities_refreshed_at: new Date(NOW - 6 * 60 * 60 * 1000).toISOString(),
        last_health_check: null,
      }),
      NOW,
    );

    expect(presentation).toMatchObject({ stale: false });
  });

  it("reports no live devices for a node whose server sends no last_stats", () => {
    const presentation = describeNodeGPU(makeNode({ capabilities: { resolved: "qsv" } }), NOW);

    expect(presentation.kind === "reported" && presentation.live).toEqual([]);
  });

  it("matches a live reading to the inventory device it names", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "qsv",
          render_device_details: [{ path: "/dev/dri/renderD128", description: "Intel GPU" }],
        },
        last_stats: {
          gpu: [
            {
              device: "/dev/dri/renderD128",
              vendor: "intel",
              sessions: 2,
              video_busy_pct: 42,
              render_busy_pct: 12,
              source: "fdinfo",
            },
          ],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.live).toEqual([
      {
        key: "/dev/dri/renderD128",
        label: "renderD128",
        busy: "42%",
        busyMuted: false,
        sessions: "2 sessions",
        title: ["/dev/dri/renderD128 — Intel GPU", "video 42% · render 12%", "source: fdinfo"].join(
          "\n",
        ),
      },
    ]);
  });

  it("matches a live reading by PCI address when the inventory has no matching path", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: {
          resolved: "vaapi",
          render_device_details: [
            {
              path: "/dev/dri/renderD129",
              pci_address: "0000:03:00.0",
              description: "AMD GPU",
            },
          ],
        },
        last_stats: {
          gpu: [{ device: "0000:03:00.0", sessions: 1, video_busy_pct: 7, source: "fdinfo" }],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.live[0]).toMatchObject({
      label: "0000:03:00.0",
      sessions: "1 session",
      title: expect.stringContaining("0000:03:00.0 — AMD GPU"),
    });
  });

  it("keeps an unmatched device rather than dropping the reading", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: { resolved: "nvenc", render_device_details: [] },
        last_stats: {
          gpu: [
            {
              device: "cuda:0",
              vendor: "nvidia",
              sessions: 0,
              video_busy_pct: 61,
              total_busy_pct: 74,
              vram_used_mb: 1024,
              vram_total_mb: 8192,
              source: "nvidia-smi",
            },
          ],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.live[0]).toEqual({
      key: "cuda:0",
      label: "cuda:0",
      busy: "61%",
      busyMuted: false,
      sessions: "idle",
      title: [
        "cuda:0",
        "video 61%",
        "whole GPU 74% (all tenants)",
        "VRAM 1.0 GiB of 8.0 GiB",
        "source: nvidia-smi",
      ].join("\n"),
    });
  });

  // The zeros an unavailable source reports are placeholders, and an operator
  // who reads them as an idle GPU draws the wrong conclusion.
  it("mutes the busy percentage when nothing measured the device", () => {
    const presentation = describeNodeGPU(
      makeNode({
        capabilities: { resolved: "qsv" },
        last_stats: {
          gpu: [{ device: "/dev/dri/renderD128", sessions: 1, source: "unavailable" }],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.live[0]).toMatchObject({
      busy: "—",
      busyMuted: true,
      sessions: "1 session",
      title: expect.stringContaining("No source could measure this device"),
    });
  });

  it("drops live readings for an unhealthy node whose sample stopped moving", () => {
    const presentation = describeNodeGPU(
      makeNode({
        healthy: false,
        capabilities: { resolved: "qsv" },
        last_stats: {
          gpu: [
            { device: "/dev/dri/renderD128", sessions: 3, video_busy_pct: 90, source: "fdinfo" },
          ],
        },
      }),
      NOW,
    );

    expect(presentation.kind === "reported" && presentation.live).toEqual([]);
  });

  it("tolerates physical_gpu_keys without letting it change the presentation", () => {
    const capabilities = {
      resolved: "nvenc",
      detected_backends: [{ backend: "nvenc", verified: true }],
    };

    expect(
      describeNodeGPU(makeNode({ capabilities, physical_gpu_keys: ["GPU-abc"] }), NOW),
    ).toEqual(describeNodeGPU(makeNode({ capabilities }), NOW));
  });
});

describe("describeSharedGPU", () => {
  const alone = makeNode({ id: 1, name: "transcode-1" });
  const nvidiaA = makeNode({ id: 2, name: "transcode-a", physical_gpu_keys: ["GPU-aaa"] });
  const nvidiaB = makeNode({ id: 3, name: "transcode-b", physical_gpu_keys: ["GPU-aaa"] });
  const unique = makeNode({ id: 4, name: "transcode-c", physical_gpu_keys: ["GPU-ccc"] });

  it("says nothing about a node that reports no identifiable GPU", () => {
    expect(describeSharedGPU(alone, [alone, nvidiaA, nvidiaB])).toBeNull();
  });

  it("says nothing when a node's GPUs are its own", () => {
    expect(describeSharedGPU(unique, [unique, nvidiaA, nvidiaB])).toBeNull();
  });

  it("names the other node on the same card, from either side", () => {
    const nodes = [nvidiaA, nvidiaB, unique];
    expect(describeSharedGPU(nvidiaA, nodes)).toEqual({
      label: "Shared GPU",
      title: "Shares a physical GPU with: transcode-b",
    });
    expect(describeSharedGPU(nvidiaB, nodes)).toEqual({
      label: "Shared GPU",
      title: "Shares a physical GPU with: transcode-a",
    });
  });

  it("matches on one key of several, across node types", () => {
    const dualGPU = makeNode({
      id: 5,
      name: "transcode-dual",
      physical_gpu_keys: ["GPU-aaa", "boot-1|0000:04:00.0"],
    });
    const proxy = makeNode({
      id: 6,
      name: "proxy-same-host",
      type: "proxy",
      physical_gpu_keys: ["boot-1|0000:04:00.0"],
    });

    expect(describeSharedGPU(dualGPU, [dualGPU, nvidiaA, proxy])).toEqual({
      label: "Shared GPU",
      title: "Shares a physical GPU with: transcode-a, proxy-same-host",
    });
  });

  it("reports nothing for a server that predates the field", () => {
    const olderA = makeNode({ id: 7, name: "old-a" });
    const olderB = makeNode({ id: 8, name: "old-b" });
    expect(describeSharedGPU(olderA, [olderA, olderB])).toBeNull();
  });

  it("does not match a node against itself when the list repeats its id", () => {
    expect(describeSharedGPU(nvidiaA, [nvidiaA, nvidiaA])).toBeNull();
  });
});

const FULL_SAMPLE: HostSystemStats = {
  cpu_pct: 42,
  load1: 1.35,
  cores: 8,
  mem_used_mb: 12800,
  mem_total_mb: 32000,
  disks: [{ path: "/tmp/silo-transcode", used_gb: 435, total_gb: 500 }],
  net_rx_bps: 12_400_000,
  net_tx_bps: 3_100_000,
};

describe("describeNodeSystem", () => {
  it("explains a healthy node that reports no sample at all", () => {
    expect(describeNodeSystem(makeNode())).toEqual({
      kind: "unreported",
      label: "—",
      title:
        "This node reported no resource sample. Sampling is Linux-only, and a node running a build from before resource sampling reports none.",
    });
  });

  it("blames the outage, not the sampler, when an unreachable node has no sample", () => {
    expect(describeNodeSystem(makeNode({ healthy: false }))).toMatchObject({
      kind: "unreported",
      title: "This node is not answering health checks, so it has no current resource sample.",
    });
  });

  // A frozen CPU percentage is indistinguishable from a live one on screen.
  it("shows dashes for an unhealthy node still carrying an older sample", () => {
    expect(
      describeNodeSystem(makeNode({ healthy: false, last_stats: { system: FULL_SAMPLE } })),
    ).toMatchObject({
      kind: "unreported",
      label: "—",
      title: expect.stringContaining("no longer current"),
    });
  });

  it("derives every reading from a complete sample", () => {
    const system = describeNodeSystem(makeNode({ last_stats: { system: FULL_SAMPLE } }));

    expect(system).toMatchObject({
      kind: "reported",
      cpu: { label: "CPU", value: "42%", detail: "8 cores · load 1.35", muted: false },
      memory: {
        label: "RAM",
        value: "12.5 GiB of 31.3 GiB",
        detail: "40% used",
        muted: false,
      },
      disk: {
        label: "Disk",
        value: "87%",
        detail: "/tmp/silo-transcode",
        title: "/tmp/silo-transcode — 87% full (435.0 GiB of 500.0 GiB)",
        muted: false,
        warning: true,
      },
      network: { label: "Net", value: "↓ 12.4 Mbps · ↑ 3.1 Mbps", muted: false },
    });
  });

  it("mutes only the readings a partial sample is missing", () => {
    const system = describeNodeSystem(
      makeNode({ last_stats: { system: { cpu_pct: 12, mem_total_mb: 0, disks: [] } } }),
    );

    expect(system).toMatchObject({
      kind: "reported",
      cpu: { value: "12%", detail: "", muted: false },
      memory: { value: "—", muted: true, title: "This sample carries no memory reading." },
      disk: { value: "—", muted: true, title: "This sample carries no disk reading." },
      network: { value: "—", muted: true, title: "This sample carries no network reading." },
    });
  });

  it("warns exactly at the disk fill threshold and not one point below", () => {
    const atThreshold = describeNodeSystem(
      makeNode({
        last_stats: {
          system: { disks: [{ path: "/scratch", used_gb: DISK_FILL_WARNING_PCT, total_gb: 100 }] },
        },
      }),
    );
    const below = describeNodeSystem(
      makeNode({
        last_stats: {
          system: {
            disks: [{ path: "/scratch", used_gb: DISK_FILL_WARNING_PCT - 1, total_gb: 100 }],
          },
        },
      }),
    );

    expect(atThreshold).toMatchObject({ disk: { value: "85%", warning: true } });
    expect(below).toMatchObject({ disk: { value: "84%", warning: false } });
  });

  it("reports the fullest mount and keeps every mount in the tooltip", () => {
    const system = describeNodeSystem(
      makeNode({
        last_stats: {
          system: {
            disks: [
              { path: "/tmp/silo-transcode", used_gb: 10, total_gb: 100 },
              { path: "/media/movies", used_gb: 95, total_gb: 100, stale: true },
              { path: "/media/gone", unavailable: true },
            ],
          },
        },
      }),
    );

    expect(system).toMatchObject({
      disk: {
        value: "95%",
        detail: "/media/movies",
        warning: true,
        title: [
          "/tmp/silo-transcode — 10% full (10.0 GiB of 100.0 GiB)",
          "/media/movies — 95% full (95.0 GiB of 100.0 GiB), carried over from an earlier pass",
          "/media/gone — unavailable on this host",
        ].join("\n"),
      },
    });
  });

  it("names the mount that went away instead of showing a bare dash", () => {
    const system = describeNodeSystem(
      makeNode({ last_stats: { system: { disks: [{ path: "/media", unavailable: true }] } } }),
    );

    expect(system).toMatchObject({
      disk: { value: "—", muted: true, title: "/media — unavailable on this host" },
    });
  });
});

describe("formatBitsPerSecond", () => {
  it("scales a bits-per-second rate to the unit an operator reads", () => {
    expect(formatBitsPerSecond(0)).toBe("0 bps");
    expect(formatBitsPerSecond(940)).toBe("940 bps");
    expect(formatBitsPerSecond(12_500)).toBe("13 kbps");
    expect(formatBitsPerSecond(12_400_000)).toBe("12.4 Mbps");
    expect(formatBitsPerSecond(2_500_000_000)).toBe("2.5 Gbps");
  });

  it("has nothing to say about an absent or impossible rate", () => {
    expect(formatBitsPerSecond(undefined)).toBeNull();
    expect(formatBitsPerSecond(null)).toBeNull();
    expect(formatBitsPerSecond(-1)).toBeNull();
    expect(formatBitsPerSecond(Number.NaN)).toBeNull();
  });
});

describe("describeGPUBusy", () => {
  it("reports nothing for a host with no GPU rather than an idle one", () => {
    expect(describeGPUBusy([])).toBeNull();
  });

  it("reports the busiest video engine and the total pinned sessions", () => {
    expect(
      describeGPUBusy([
        { device: "/dev/dri/renderD128", video_busy_pct: 42, sessions: 2, source: "fdinfo" },
        { device: "cuda:0", video_busy_pct: 71, sessions: 1, source: "nvidia-smi" },
      ]),
    ).toMatchObject({
      label: "GPU",
      value: "71%",
      detail: "busiest of 2 GPUs · 3 sessions",
      muted: false,
      title: [
        "/dev/dri/renderD128 — video 42% · 2 sessions",
        "cuda:0 — video 71% · 1 session",
      ].join("\n"),
    });
  });

  it("mutes the tile when no device could be measured", () => {
    expect(
      describeGPUBusy([{ device: "/dev/dri/renderD128", sessions: 0, source: "unavailable" }]),
    ).toMatchObject({
      value: "—",
      muted: true,
      title: "/dev/dri/renderD128 — not measured · 0 sessions",
    });
  });
});

describe("describeResourceSample", () => {
  it("treats a server with no such endpoint as an unsampled host", () => {
    expect(describeResourceSample(undefined)).toMatchObject({ kind: "unavailable" });
  });

  it("treats an explicit available:false the same way", () => {
    expect(describeResourceSample({ available: false })).toMatchObject({ kind: "unavailable" });
  });

  it("does not claim a sample when available is true but the body carries none", () => {
    expect(describeResourceSample({ available: true })).toMatchObject({ kind: "unavailable" });
  });

  it("derives the host readings and omits the GPU tile when there is no GPU", () => {
    const sample = describeResourceSample({
      available: true,
      sampled_at: "2026-08-26T12:00:00Z",
      system: FULL_SAMPLE,
    });

    expect(sample).toMatchObject({
      kind: "sampled",
      cpu: { value: "42%" },
      memory: { value: "12.5 GiB of 31.3 GiB" },
      disk: { value: "87%", warning: true },
      gpu: null,
      sampledAt: "2026-08-26T12:00:00Z",
    });
  });

  it("carries the GPU reading through when the host reports one", () => {
    const sample = describeResourceSample({
      available: true,
      system: FULL_SAMPLE,
      gpu: [{ device: "/dev/dri/renderD128", video_busy_pct: 30, sessions: 1, source: "fdinfo" }],
    });

    expect(sample).toMatchObject({
      kind: "sampled",
      gpu: { value: "30%", detail: "video engine · 1 session" },
      sampledAt: null,
    });
  });
});

describe("describeNodeAccelerationOverride", () => {
  it("renders nothing for a node that inherits the cluster-wide settings", () => {
    expect(describeNodeAccelerationOverride(makeNode())).toBeNull();
    expect(
      describeNodeAccelerationOverride(
        makeNode({ hw_accel_override: null, hw_device_override: null }),
      ),
    ).toBeNull();
    // Whitespace is not an override.
    expect(describeNodeAccelerationOverride(makeNode({ hw_device_override: " , " }))).toBeNull();
  });

  it("names the backend a node is pinned to", () => {
    const override = describeNodeAccelerationOverride(makeNode({ hw_accel_override: "qsv" }));

    expect(override?.label).toBe("override: qsv");
    expect(override?.title).toContain("Acceleration: qsv");
    expect(override?.title).toContain("GPU devices: inherited");
  });

  it("calls a software override software rather than none", () => {
    expect(describeNodeAccelerationOverride(makeNode({ hw_accel_override: "none" }))?.label).toBe(
      "override: software",
    );
  });

  it("shows a single pinned device inline and counts several", () => {
    expect(
      describeNodeAccelerationOverride(
        makeNode({ hw_accel_override: "vaapi", hw_device_override: "/dev/dri/renderD129" }),
      )?.label,
    ).toBe("override: vaapi · /dev/dri/renderD129");

    const many = describeNodeAccelerationOverride(
      makeNode({ hw_device_override: "/dev/dri/renderD128, /dev/dri/renderD129" }),
    );
    expect(many?.label).toBe("override: 2 devices");
    expect(many?.title).toContain("GPU devices: /dev/dri/renderD128, /dev/dri/renderD129.");
    expect(many?.title).toContain("Acceleration: inherited");
  });

  it("says when the override takes effect", () => {
    const title = describeNodeAccelerationOverride(makeNode({ hw_accel_override: "nvenc" }))?.title;
    expect(title).toContain("applies to new transcodes within a minute");
    expect(title).toContain("sessions already running keep the backend they started with");
  });
});

describe("parseHWDeviceOverride", () => {
  it("splits, trims, and drops empty entries", () => {
    expect(parseHWDeviceOverride(" /dev/dri/renderD128 ,, /dev/dri/renderD129,")).toEqual([
      "/dev/dri/renderD128",
      "/dev/dri/renderD129",
    ]);
  });

  it("treats absent and empty values as no devices", () => {
    expect(parseHWDeviceOverride(null)).toEqual([]);
    expect(parseHWDeviceOverride(undefined)).toEqual([]);
    expect(parseHWDeviceOverride("  ")).toEqual([]);
  });
});

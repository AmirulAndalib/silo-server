import { describe, expect, it } from "vitest";
import type { StreamNode } from "@/api/types";
import { CAPABILITY_STALE_AFTER_MS, describeNodeGPU } from "./adminNodesPresentation";

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

import type { HWAccelInfo } from "@/hooks/queries/admin/system";

// Helpers for the playback.hw_device GPU picker. The setting stores a
// comma-separated render-device list; the UI presents it as per-device
// toggles, with no selection meaning "auto" (server picks the first
// available device). The setting is cluster-wide, so rows carry per-node
// presence info when transcode nodes report their inventories.
//
// Parsing and toggling that list is the same problem as editing one node's
// hw_device_override, so both live in @/lib/hwDevices and are re-exported here
// for the callers (and tests) that already know them by these names.

import { parseHWDeviceList, toggleHWDevice } from "@/lib/hwDevices";

export { parseHWDeviceList, toggleHWDevice };

export interface HWDeviceRow {
  path: string;
  description: string;
  /** Present in the primary detection result. */
  detected: boolean;
  /** Names/URLs of responding nodes whose inventory lacks this device. */
  missingOnNodes: string[];
}

/**
 * Builds the picker rows: the union of detected devices and configured
 * entries, so configured-but-missing devices stay visible (and deselectable)
 * even when detection returns nothing or an older node omits
 * render_device_details.
 */
export function buildHWDeviceRows(
  detection: HWAccelInfo | undefined,
  configured: string | undefined,
): HWDeviceRow[] {
  const detected = detectedDevices(detection);
  const respondingNodes = (detection?.nodes ?? []).filter((node) => !node.error);
  const missingOn = (path: string) =>
    respondingNodes
      .filter((node) => !(node.render_devices ?? []).includes(path))
      .map((node) => node.node_name || node.node_url);

  const rows: HWDeviceRow[] = detected.map((device) => ({
    path: device.path,
    description: device.description,
    detected: true,
    missingOnNodes: missingOn(device.path),
  }));
  for (const path of parseHWDeviceList(configured)) {
    if (rows.some((row) => row.path === path)) continue;
    rows.push({
      path,
      description: "Configured device not detected",
      detected: false,
      missingOnNodes: missingOn(path),
    });
  }
  return rows;
}

/**
 * True when more than one transcode node responded and their render-device
 * inventories differ — the cluster-wide hw_device value is only safe for
 * paths present on every node, so the UI shows a warning.
 */
export function nodeInventoriesDiverge(detection: HWAccelInfo | undefined): boolean {
  const inventories = (detection?.nodes ?? [])
    .filter((node) => !node.error)
    .map((node) => [...(node.render_devices ?? [])].sort().join(","));
  return inventories.length > 1 && new Set(inventories).size > 1;
}

function detectedDevices(
  detection: HWAccelInfo | undefined,
): { path: string; description: string }[] {
  if (!detection) return [];
  if (detection.render_device_details && detection.render_device_details.length > 0) {
    return detection.render_device_details;
  }
  // Older nodes report only render_devices paths.
  return (detection.render_devices ?? []).map((path) => ({ path, description: "GPU" }));
}

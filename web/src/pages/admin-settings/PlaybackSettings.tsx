import { useMemo } from "react";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useAdminServerStatus } from "@/hooks/queries/admin/settings";
import { useHWAccelDetection, type HWAccelInfo } from "@/hooks/queries/admin/system";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { LimitField } from "@/components/settings/LimitField";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { StatusStrip, type StatusStripItem } from "@/components/settings/StatusStrip";
import { SettingField, SettingFieldStatus } from "./SettingField";
import { SaveBar } from "./SaveBar";
import { FieldGroup } from "./FieldGroup";
import {
  buildHWDeviceRows,
  nodeInventoriesDiverge,
  parseHWDeviceList,
  toggleHWDevice,
} from "./playbackSettings.utils";

// Shown without a disclosure: the handful of controls a household admin
// actually touches.
const TRANSCODING_ESSENTIAL_KEYS = [
  "playback.transcode_enabled",
  "playback.hw_accel",
  "allow_4k_transcode",
];

const TRANSCODING_ADVANCED_KEYS = [
  "playback.ffmpeg_path",
  "playback.transcode_dir",
  "playback.hw_device",
  "playback.local_transcode_fallback",
  "playback.transcode_hardware_tone_map_enabled",
  "playback.transcode_software_tone_map_enabled",
  "enable_transcode_throttle",
  "transcode_throttle_seconds",
  "playback.chapter_thumbnail_workers",
  "playback.chapter_thumbnail_execution",
  "playback.chapter_thumbnail_hdr_policy",
  "playback.chapter_thumbnail_software_tone_map_enabled",
];

const WATCH_KEYS = ["playback.watched_threshold", "playback.min_resume_threshold"];

const DOWNLOAD_ESSENTIAL_KEYS = ["download.enabled", "download.user_bandwidth_mbps"];

const DOWNLOAD_ADVANCED_KEYS = [
  "download.server_bandwidth_mbps",
  "download.max_concurrent_per_user",
  "download.max_per_period",
  "download.period_duration",
  "download.transcode_enabled",
  "download.artifact_dir",
  "download.max_concurrent_prepares",
  "download.artifact_max_bytes",
];

// `playback.chapter_thumbnail_node_capacity` is deliberately absent from the
// UI (hidden tier): it is still saved and read through the settings API, but
// the per-node budget is derived from the node pool rather than typed in.
const KEYS = [
  ...TRANSCODING_ESSENTIAL_KEYS,
  ...TRANSCODING_ADVANCED_KEYS,
  ...WATCH_KEYS,
  ...DOWNLOAD_ESSENTIAL_KEYS,
  ...DOWNLOAD_ADVANCED_KEYS,
];

export default function PlaybackSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const restartKeys = useRestartKeys();
  // Already fetched (and cached) by the settings shell for its own banner —
  // a save this session isn't the only way the running server can be behind
  // the saved config, e.g. another admin (or another tab) changed a
  // restart-gated setting since this process booted.
  const { data: serverStatus } = useAdminServerStatus();
  const hwAccel = form.getValue("playback.hw_accel");
  const hwDetection = useHWAccelDetection(hwAccel !== "none");
  const hwDevice = form.getValue("playback.hw_device");
  const selectedDevices = parseHWDeviceList(hwDevice);
  const deviceRows = buildHWDeviceRows(hwDetection.data, hwDevice);
  const detectedPaths = deviceRows.filter((row) => row.detected).map((row) => row.path);
  // Balancing is QSV/VAAPI-only: NVENC addresses GPUs by CUDA index/UUID, so
  // the multi-select picker is hidden for it (the server uses the first
  // configured entry).
  const isNvenc =
    hwAccel === "nvenc" || (hwAccel === "auto" && hwDetection.data?.resolved === "nvenc");
  const inventoriesDiverge = nodeInventoriesDiverge(hwDetection.data);
  const showDevicePicker = hwAccel !== "none" && !isNvenc && deviceRows.length > 0;

  const isDirty = form.isDirty;
  const anyDirty = (keys: string[]) => keys.some((key) => isDirty(key));
  const countDirty = (keys: string[]) => keys.filter((key) => isDirty(key)).length;
  const restartCount = KEYS.filter((key) => isDirty(key) && restartKeys.has(key)).length;

  const transcodingOn = form.getValue("playback.transcode_enabled") !== "false";
  const detection = hwAccel === "none" ? undefined : hwDetection.data;
  const detectedLabel = describeDetection(detection);

  const stripItems: StatusStripItem[] = [
    transcodingOn
      ? { tone: "ok", label: "Transcoding on" }
      : { tone: "warn", label: "Transcoding off" },
  ];
  if (hwAccel === "none") {
    stripItems.push({ tone: "muted", label: "Software encoding" });
  } else if (detectedLabel) {
    stripItems.push({
      tone: detection?.resolved && detection.resolved !== "none" ? "ok" : "warn",
      label: detectedLabel,
    });
  } else if (hwDetection.isLoading) {
    stripItems.push({ tone: "muted", label: "Detecting graphics hardware" });
  }
  if (form.restartRequired || serverStatus?.restart_required) {
    stripItems.push({ tone: "warn", label: "Restart pending" });
  }

  if (form.isLoading) return <div>Loading...</div>;

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader
        title="Playback"
        description="How Silo decides to direct play, stream, or transcode — and what happens on the way to the client."
        strip={<StatusStrip items={stripItems} />}
        className="mb-8"
      />

      <div className="flex-1 space-y-9">
        <FieldGroup
          label="Transcoding"
          clarifier="How Silo converts media the client cannot play directly"
        >
          <SettingField
            label="Transcoding"
            type="toggle"
            description="Turn off to only serve files clients can play as-is. Incompatible titles then fail instead of converting."
            value={form.getValue("playback.transcode_enabled")}
            onChange={(v) => form.setValue("playback.transcode_enabled", v)}
            restartRequired={restartKeys.has("playback.transcode_enabled")}
            dirty={isDirty("playback.transcode_enabled")}
          />
          <SettingField
            label="Hardware acceleration"
            type="select"
            options={[
              { value: "auto", label: "Auto" },
              { value: "qsv", label: "Intel Quick Sync (QSV)" },
              { value: "vaapi", label: "VA-API" },
              { value: "nvenc", label: "NVIDIA NVENC" },
              { value: "videotoolbox", label: "VideoToolbox (macOS)" },
              { value: "none", label: "Software" },
            ]}
            description="Offload video encoding to the GPU. Auto picks the best device this server can see, and falls back to the CPU when there is none."
            status={
              hwAccel === "none" ? undefined : detectedLabel ? (
                <SettingFieldStatus
                  tone={detection?.resolved && detection.resolved !== "none" ? "ok" : "warn"}
                >
                  {detectedLabel}
                </SettingFieldStatus>
              ) : hwDetection.isLoading ? (
                <SettingFieldStatus tone="muted">Detecting hardware…</SettingFieldStatus>
              ) : undefined
            }
            value={hwAccel}
            onChange={(v) => form.setValue("playback.hw_accel", v)}
            restartRequired={restartKeys.has("playback.hw_accel")}
            dirty={isDirty("playback.hw_accel")}
          />
          {hwAccel !== "none" && isNvenc && selectedDevices.length > 1 && (
            <p className="py-2 text-xs text-amber-500">
              Multi-GPU balancing supports QSV/VA-API only; with NVENC the server uses the first
              configured device ({selectedDevices[0]}).
            </p>
          )}
          <SettingField
            label="Allow 4K transcoding"
            type="toggle"
            description="Heavy load on most hardware. Leave off to send 4K only to clients that can play it directly."
            value={form.getValue("allow_4k_transcode")}
            onChange={(v) => form.setValue("allow_4k_transcode", v)}
            restartRequired={restartKeys.has("allow_4k_transcode")}
            dirty={isDirty("allow_4k_transcode")}
          />

          <AdvancedSection
            id="playback.transcoding"
            count={TRANSCODING_ADVANCED_KEYS.length - (showDevicePicker ? 0 : 1)}
            changedCount={countDirty(TRANSCODING_ADVANCED_KEYS)}
            forceOpen={anyDirty(TRANSCODING_ADVANCED_KEYS)}
          >
            <SettingField
              label="FFmpeg path"
              hint="/usr/lib/silo-ffmpeg/ffmpeg"
              description="Leave empty to use the FFmpeg build that ships with the server."
              value={form.getValue("playback.ffmpeg_path")}
              onChange={(v) => form.setValue("playback.ffmpeg_path", v)}
              restartRequired={restartKeys.has("playback.ffmpeg_path")}
              dirty={isDirty("playback.ffmpeg_path")}
            />
            <SettingField
              label="Transcode directory"
              hint="/var/lib/silo/transcode"
              description="Where in-progress transcodes are written. Use fast local storage with room to spare."
              value={form.getValue("playback.transcode_dir")}
              onChange={(v) => form.setValue("playback.transcode_dir", v)}
              restartRequired={restartKeys.has("playback.transcode_dir")}
              dirty={isDirty("playback.transcode_dir")}
            />
            {showDevicePicker && (
              <div className="flex flex-col gap-2 py-3.5">
                <div className="space-y-1">
                  <Label className="text-sm font-medium">GPU devices</Label>
                  <p className="text-muted-foreground text-xs leading-relaxed">
                    {selectedDevices.length === 0
                      ? "Auto — the first available device handles every transcode. Select devices to pin or balance."
                      : selectedDevices.length === 1
                        ? "All transcodes run on the selected device."
                        : "Transcode sessions balance across the selected devices (least loaded first)."}
                    {hwDetection.data?.source === "transcode_node" &&
                      " Devices reported by a transcode node."}
                  </p>
                  {inventoriesDiverge && (
                    <p className="text-xs text-amber-500">
                      This setting applies to every transcode node, but the nodes report different
                      devices. Only paths present on all nodes are safe to select.
                    </p>
                  )}
                </div>
                <div className="space-y-2">
                  {deviceRows.map((row) => (
                    <div key={row.path} className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <p
                          className={`truncate text-sm ${row.detected ? "" : "text-muted-foreground"}`}
                        >
                          {row.description}
                        </p>
                        <p className="text-muted-foreground truncate font-mono text-xs">
                          {row.path}
                        </p>
                        {row.missingOnNodes.length > 0 && (
                          <p className="truncate text-xs text-amber-500">
                            Not present on: {row.missingOnNodes.join(", ")}
                          </p>
                        )}
                      </div>
                      <Switch
                        checked={selectedDevices.includes(row.path)}
                        onCheckedChange={() =>
                          form.setValue(
                            "playback.hw_device",
                            toggleHWDevice(
                              form.getValue("playback.hw_device"),
                              row.path,
                              detectedPaths,
                            ),
                          )
                        }
                      />
                    </div>
                  ))}
                </div>
              </div>
            )}
            <SettingField
              label="Local transcode fallback"
              type="toggle"
              description="Encode on this server when no node in the pool is free. Turn it off to keep every transcode on dedicated nodes; playback that needs one then fails while no node is available."
              value={form.getValue("playback.local_transcode_fallback") || "true"}
              onChange={(v) => form.setValue("playback.local_transcode_fallback", v)}
              restartRequired={restartKeys.has("playback.local_transcode_fallback")}
              dirty={isDirty("playback.local_transcode_fallback")}
            />
            <SettingField
              label="Enable Hardware HDR Tone Mapping"
              type="toggle"
              hint="Allows validated local or remote GPU executors to convert HDR video to SDR when transcoding."
              value={form.getValue("playback.transcode_hardware_tone_map_enabled") || "false"}
              onChange={(v) => form.setValue("playback.transcode_hardware_tone_map_enabled", v)}
              restartRequired={restartKeys.has("playback.transcode_hardware_tone_map_enabled")}
              dirty={isDirty("playback.transcode_hardware_tone_map_enabled")}
            />
            <SettingField
              label="Enable Software HDR Tone Mapping"
              type="toggle"
              hint="Allows the CPU to convert HDR video to SDR when transcoding. This can be very CPU-intensive."
              value={form.getValue("playback.transcode_software_tone_map_enabled") || "false"}
              onChange={(v) => form.setValue("playback.transcode_software_tone_map_enabled", v)}
              restartRequired={restartKeys.has("playback.transcode_software_tone_map_enabled")}
              dirty={isDirty("playback.transcode_software_tone_map_enabled")}
            />
            <SettingField
              label="Throttle transcoding"
              type="toggle"
              description="Pause encoding once the client is far enough ahead. Saves CPU when a viewer is unlikely to watch the whole file, at the cost of a slower seek."
              value={form.getValue("enable_transcode_throttle")}
              onChange={(v) => form.setValue("enable_transcode_throttle", v)}
              restartRequired={restartKeys.has("enable_transcode_throttle")}
              dirty={isDirty("enable_transcode_throttle")}
            />
            {form.getValue("enable_transcode_throttle") === "true" && (
              <SettingField
                label="Buffer ahead (seconds)"
                type="number"
                description="How far ahead of the viewer the server transcodes before pausing. Minimum 60."
                value={form.getValue("transcode_throttle_seconds")}
                onChange={(v) => form.setValue("transcode_throttle_seconds", v)}
                restartRequired={restartKeys.has("transcode_throttle_seconds")}
                dirty={isDirty("transcode_throttle_seconds")}
              />
            )}
            <SettingField
              label="Chapter thumbnail workers"
              type="number"
              description="Parallel extraction jobs per library scan. Higher finishes sooner but takes more of the server's capacity."
              value={form.getValue("playback.chapter_thumbnail_workers")}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_workers", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_workers")}
              dirty={isDirty("playback.chapter_thumbnail_workers")}
            />
            <SettingField
              label="Generate chapter thumbnails on"
              type="select"
              options={[
                { value: "local", label: "This server" },
                { value: "prefer_transcode_nodes", label: "Transcode nodes when available" },
                { value: "transcode_nodes_only", label: "Transcode nodes only" },
              ]}
              description="Whether this server does the work itself or hands it to transcode nodes."
              value={form.getValue("playback.chapter_thumbnail_execution") || "local"}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_execution", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_execution")}
              dirty={isDirty("playback.chapter_thumbnail_execution")}
            />
            <SettingField
              label="HDR handling"
              type="select"
              options={[
                { value: "best_effort", label: "Generate when possible" },
                { value: "disabled", label: "Skip HDR and Dolby Vision" },
              ]}
              description="HDR frames need extra color conversion to look right as thumbnails. Standard video is unaffected."
              value={form.getValue("playback.chapter_thumbnail_hdr_policy") || "best_effort"}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_hdr_policy", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_hdr_policy")}
              dirty={isDirty("playback.chapter_thumbnail_hdr_policy")}
            />
            <SettingField
              label="Convert HDR colors on the CPU when the GPU cannot"
              type="toggle"
              description="Lets HDR chapter thumbnails still be generated without graphics hardware. Off by default because it is slow."
              value={
                form.getValue("playback.chapter_thumbnail_software_tone_map_enabled") || "false"
              }
              onChange={(v) =>
                form.setValue("playback.chapter_thumbnail_software_tone_map_enabled", v)
              }
              disabled={form.getValue("playback.chapter_thumbnail_hdr_policy") === "disabled"}
              restartRequired={restartKeys.has(
                "playback.chapter_thumbnail_software_tone_map_enabled",
              )}
              dirty={isDirty("playback.chapter_thumbnail_software_tone_map_enabled")}
            />
          </AdvancedSection>
        </FieldGroup>

        <FieldGroup label="Watch behavior" clarifier="When a title counts as watched or resumable">
          <SettingField
            label="Mark watched at (%)"
            type="number"
            description="Percent of runtime before Silo marks an item finished. Default 90."
            value={form.getValue("playback.watched_threshold")}
            onChange={(v) => form.setValue("playback.watched_threshold", v)}
            restartRequired={restartKeys.has("playback.watched_threshold")}
            dirty={isDirty("playback.watched_threshold")}
          />
          <SettingField
            label="Show in Continue Watching after (%)"
            type="number"
            description="Progress below this much of an item is ignored, so a brief preview does not land in Continue Watching. Default 5."
            value={form.getValue("playback.min_resume_threshold")}
            onChange={(v) => form.setValue("playback.min_resume_threshold", v)}
            restartRequired={restartKeys.has("playback.min_resume_threshold")}
            dirty={isDirty("playback.min_resume_threshold")}
          />
        </FieldGroup>

        <FieldGroup label="Downloads" clarifier="Offline copies people keep on their devices">
          <SettingField
            label="Allow downloads"
            type="toggle"
            description="Let profiles keep titles offline on mobile clients."
            value={form.getValue("download.enabled")}
            onChange={(v) => form.setValue("download.enabled", v)}
            restartRequired={restartKeys.has("download.enabled")}
            dirty={isDirty("download.enabled")}
          />
          <LimitField
            label="Per-user bandwidth"
            unit="Mbps"
            hint="Cap on concurrent download throughput per account."
            value={form.getValue("download.user_bandwidth_mbps")}
            onChange={(v) => form.setValue("download.user_bandwidth_mbps", v)}
            restartRequired={restartKeys.has("download.user_bandwidth_mbps")}
            dirty={isDirty("download.user_bandwidth_mbps")}
          />

          <AdvancedSection
            id="playback.downloads"
            count={DOWNLOAD_ADVANCED_KEYS.length}
            changedCount={countDirty(DOWNLOAD_ADVANCED_KEYS)}
            forceOpen={anyDirty(DOWNLOAD_ADVANCED_KEYS)}
          >
            <LimitField
              label="Server bandwidth"
              unit="Mbps"
              hint="Speed cap for all downloads on this server combined."
              value={form.getValue("download.server_bandwidth_mbps")}
              onChange={(v) => form.setValue("download.server_bandwidth_mbps", v)}
              restartRequired={restartKeys.has("download.server_bandwidth_mbps")}
              dirty={isDirty("download.server_bandwidth_mbps")}
            />
            <LimitField
              label="Downloads at once per user"
              hint="How many downloads one user can have running at the same time."
              value={form.getValue("download.max_concurrent_per_user")}
              onChange={(v) => form.setValue("download.max_concurrent_per_user", v)}
              restartRequired={restartKeys.has("download.max_concurrent_per_user")}
              dirty={isDirty("download.max_concurrent_per_user")}
            />
            <LimitField
              label="Downloads per period"
              hint="How many downloads one user can start within the period below."
              value={form.getValue("download.max_per_period")}
              onChange={(v) => form.setValue("download.max_per_period", v)}
              restartRequired={restartKeys.has("download.max_per_period")}
              dirty={isDirty("download.max_per_period")}
            />
            <SettingField
              label="Period length"
              type="duration"
              description="Rolling window for the per-period limit (for example 24h, 168h, 720h)."
              value={form.getValue("download.period_duration")}
              onChange={(v) => form.setValue("download.period_duration", v)}
              restartRequired={restartKeys.has("download.period_duration")}
              dirty={isDirty("download.period_duration")}
            />
            <SettingField
              label="Prepare offline copies in a device-friendly format"
              type="toggle"
              description="Converts a file before download for devices that cannot play the original. Needs the per-user download permission. Files already on a device stay there until the user deletes them."
              value={form.getValue("download.transcode_enabled")}
              onChange={(v) => form.setValue("download.transcode_enabled", v)}
              restartRequired={restartKeys.has("download.transcode_enabled")}
              dirty={isDirty("download.transcode_enabled")}
            />
            <SettingField
              label="Prepared file directory"
              description="Where prepared download files are written. Empty puts them next to the transcode directory."
              value={form.getValue("download.artifact_dir")}
              onChange={(v) => form.setValue("download.artifact_dir", v)}
              restartRequired={restartKeys.has("download.artifact_dir")}
              dirty={isDirty("download.artifact_dir")}
            />
            {/* Not a LimitField: the server reads 0 as "use the built-in
                worker count" (2), not as unlimited. */}
            <SettingField
              label="Files prepared at once"
              type="number"
              description="How many downloads the server prepares at the same time. Leave at 0 to use the built-in default of 2."
              value={form.getValue("download.max_concurrent_prepares")}
              onChange={(v) => form.setValue("download.max_concurrent_prepares", v)}
              restartRequired={restartKeys.has("download.max_concurrent_prepares")}
              dirty={isDirty("download.max_concurrent_prepares")}
            />
            <LimitField
              label="Prepared file storage budget"
              unit="bytes"
              hint="Once prepared files pass this size, the least recently used ones are deleted."
              value={form.getValue("download.artifact_max_bytes")}
              onChange={(v) => form.setValue("download.artifact_max_bytes", v)}
              restartRequired={restartKeys.has("download.artifact_max_bytes")}
              dirty={isDirty("download.artifact_max_bytes")}
            />
          </AdvancedSection>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
        restartRequired={form.restartRequired}
        restartCount={restartCount}
      />
    </div>
  );
}

function formatResolved(resolved: string): string {
  switch (resolved) {
    case "qsv":
      return "Intel Quick Sync (QSV)";
    case "vaapi":
      return "VA-API";
    case "nvenc":
      return "NVIDIA NVENC";
    case "videotoolbox":
      return "VideoToolbox (macOS)";
    case "none":
      return "Software";
    default:
      return resolved;
  }
}

/**
 * One-line detection result, e.g. "Detected VA-API on renderD128". Returns
 * undefined while nothing has been probed yet so the caller can show its own
 * "detecting" state instead of an empty phrase.
 */
function describeDetection(detection: HWAccelInfo | undefined): string | undefined {
  if (!detection) return undefined;
  if (detection.resolved === "none") return "No supported graphics hardware found";
  const device = detection.render_devices?.[0];
  const onNode = detection.source === "transcode_node" ? " (transcode node)" : "";
  return `Detected ${formatResolved(detection.resolved)}${device ? ` on ${device}` : ""}${onNode}`;
}

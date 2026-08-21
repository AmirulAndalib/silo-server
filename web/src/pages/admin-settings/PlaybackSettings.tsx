import { useMemo } from "react";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useHWAccelDetection, type HWAccelInfo } from "@/hooks/queries/admin/system";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { LimitField } from "@/components/settings/LimitField";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
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
  const allRestart = (keys: string[]) => keys.every((key) => restartKeys.has(key));

  const detection = hwAccel === "none" ? undefined : hwDetection.data;
  const detectedLabel = describeDetection(detection);

  if (form.isLoading) return <div>Loading...</div>;

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader title="Playback" className="mb-8" />

      <div className="flex-1 space-y-9">
        <FieldGroup
          label="Transcoding"
          restartAll={allRestart([...TRANSCODING_ESSENTIAL_KEYS, ...TRANSCODING_ADVANCED_KEYS])}
        >
          <SettingField
            label="Transcoding"
            type="toggle"
            description="Off serves only files clients can already play."
            value={form.getValue("playback.transcode_enabled")}
            onChange={(v) => form.setValue("playback.transcode_enabled", v)}
            restartRequired={restartKeys.has("playback.transcode_enabled")}
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
            description="Auto picks the best device this server can see."
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
          />
          {hwAccel !== "none" && isNvenc && selectedDevices.length > 1 && (
            <p className="py-2 text-xs text-amber-500">
              NVENC uses the first configured device ({selectedDevices[0]}).
            </p>
          )}
          <SettingField
            label="Allow 4K transcoding"
            type="toggle"
            description="Heavy load on most hardware."
            value={form.getValue("allow_4k_transcode")}
            onChange={(v) => form.setValue("allow_4k_transcode", v)}
            restartRequired={restartKeys.has("allow_4k_transcode")}
          />

          <AdvancedSection
            id="playback.transcoding"
            count={TRANSCODING_ADVANCED_KEYS.length - (showDevicePicker ? 0 : 1)}
            forceOpen={anyDirty(TRANSCODING_ADVANCED_KEYS)}
          >
            <SettingField
              label="FFmpeg path"
              hint="/usr/lib/silo-ffmpeg/ffmpeg"
              description="Empty uses the FFmpeg that ships with the server."
              value={form.getValue("playback.ffmpeg_path")}
              onChange={(v) => form.setValue("playback.ffmpeg_path", v)}
              restartRequired={restartKeys.has("playback.ffmpeg_path")}
            />
            <SettingField
              label="Transcode directory"
              hint="/var/lib/silo/transcode"
              description="Use fast local storage with room to spare."
              value={form.getValue("playback.transcode_dir")}
              onChange={(v) => form.setValue("playback.transcode_dir", v)}
              restartRequired={restartKeys.has("playback.transcode_dir")}
            />
            {showDevicePicker && (
              <div className="flex flex-col gap-2 py-3.5">
                <div className="space-y-1">
                  <Label className="text-sm font-medium">GPU devices</Label>
                  <p className="text-muted-foreground text-xs leading-relaxed">
                    {selectedDevices.length === 0
                      ? "Auto: the first available device takes every transcode."
                      : selectedDevices.length === 1
                        ? "All transcodes run on the selected device."
                        : "Transcodes balance across the selected devices."}
                  </p>
                  {inventoriesDiverge && (
                    <p className="text-xs text-amber-500">
                      Nodes report different devices. Only paths on every node are safe to select.
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
              description="Encode here when no transcode node is free."
              value={form.getValue("playback.local_transcode_fallback") || "true"}
              onChange={(v) => form.setValue("playback.local_transcode_fallback", v)}
              restartRequired={restartKeys.has("playback.local_transcode_fallback")}
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
              description="Pause encoding once the client is far enough ahead."
              value={form.getValue("enable_transcode_throttle")}
              onChange={(v) => form.setValue("enable_transcode_throttle", v)}
              restartRequired={restartKeys.has("enable_transcode_throttle")}
            />
            {form.getValue("enable_transcode_throttle") === "true" && (
              <SettingField
                label="Buffer ahead"
                type="number"
                unit="seconds"
                value={form.getValue("transcode_throttle_seconds")}
                onChange={(v) => form.setValue("transcode_throttle_seconds", v)}
                restartRequired={restartKeys.has("transcode_throttle_seconds")}
              />
            )}
            <SettingField
              label="Chapter thumbnail workers"
              type="number"
              description="Parallel extraction jobs per library scan."
              value={form.getValue("playback.chapter_thumbnail_workers")}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_workers", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_workers")}
            />
            <SettingField
              label="Generate chapter thumbnails on"
              type="select"
              options={[
                { value: "local", label: "This server" },
                { value: "prefer_transcode_nodes", label: "Transcode nodes when available" },
                { value: "transcode_nodes_only", label: "Transcode nodes only" },
              ]}
              value={form.getValue("playback.chapter_thumbnail_execution") || "local"}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_execution", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_execution")}
            />
            <SettingField
              label="HDR handling"
              type="select"
              options={[
                { value: "best_effort", label: "Generate when possible" },
                { value: "disabled", label: "Skip HDR and Dolby Vision" },
              ]}
              description="HDR frames need extra color conversion."
              value={form.getValue("playback.chapter_thumbnail_hdr_policy") || "best_effort"}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_hdr_policy", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_hdr_policy")}
            />
            <SettingField
              label="Software HDR tone mapping"
              type="toggle"
              description="Slow, but works without graphics hardware."
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
            />
          </AdvancedSection>
        </FieldGroup>

        <FieldGroup label="Watch behavior" restartAll={allRestart(WATCH_KEYS)}>
          <SettingField
            label="Mark watched at"
            type="number"
            unit="%"
            value={form.getValue("playback.watched_threshold")}
            onChange={(v) => form.setValue("playback.watched_threshold", v)}
            restartRequired={restartKeys.has("playback.watched_threshold")}
          />
          <SettingField
            label="Show in Continue Watching after"
            type="number"
            unit="%"
            description="Progress below this is ignored."
            value={form.getValue("playback.min_resume_threshold")}
            onChange={(v) => form.setValue("playback.min_resume_threshold", v)}
            restartRequired={restartKeys.has("playback.min_resume_threshold")}
          />
        </FieldGroup>

        <FieldGroup
          label="Downloads"
          restartAll={allRestart([...DOWNLOAD_ESSENTIAL_KEYS, ...DOWNLOAD_ADVANCED_KEYS])}
        >
          <SettingField
            label="Allow downloads"
            type="toggle"
            value={form.getValue("download.enabled")}
            onChange={(v) => form.setValue("download.enabled", v)}
            restartRequired={restartKeys.has("download.enabled")}
          />
          <LimitField
            label="Per-user bandwidth"
            unit="Mbps"
            value={form.getValue("download.user_bandwidth_mbps")}
            onChange={(v) => form.setValue("download.user_bandwidth_mbps", v)}
            restartRequired={restartKeys.has("download.user_bandwidth_mbps")}
          />

          <AdvancedSection
            id="playback.downloads"
            count={DOWNLOAD_ADVANCED_KEYS.length}
            forceOpen={anyDirty(DOWNLOAD_ADVANCED_KEYS)}
          >
            <LimitField
              label="Server bandwidth"
              unit="Mbps"
              hint="All downloads on this server combined."
              value={form.getValue("download.server_bandwidth_mbps")}
              onChange={(v) => form.setValue("download.server_bandwidth_mbps", v)}
              restartRequired={restartKeys.has("download.server_bandwidth_mbps")}
            />
            <LimitField
              label="Downloads at once per user"
              value={form.getValue("download.max_concurrent_per_user")}
              onChange={(v) => form.setValue("download.max_concurrent_per_user", v)}
              restartRequired={restartKeys.has("download.max_concurrent_per_user")}
            />
            <LimitField
              label="Downloads per period"
              hint="Counted against the period below."
              value={form.getValue("download.max_per_period")}
              onChange={(v) => form.setValue("download.max_per_period", v)}
              restartRequired={restartKeys.has("download.max_per_period")}
            />
            <SettingField
              label="Period length"
              type="duration"
              description="Rolling window, e.g. 24h or 168h."
              value={form.getValue("download.period_duration")}
              onChange={(v) => form.setValue("download.period_duration", v)}
              restartRequired={restartKeys.has("download.period_duration")}
            />
            <SettingField
              label="Prepare device-friendly copies"
              type="toggle"
              description="Converts a file the device cannot play before download."
              value={form.getValue("download.transcode_enabled")}
              onChange={(v) => form.setValue("download.transcode_enabled", v)}
              restartRequired={restartKeys.has("download.transcode_enabled")}
            />
            <SettingField
              label="Prepared file directory"
              description="Empty puts them next to the transcode directory."
              value={form.getValue("download.artifact_dir")}
              onChange={(v) => form.setValue("download.artifact_dir", v)}
              restartRequired={restartKeys.has("download.artifact_dir")}
            />
            {/* Not a LimitField: the server reads 0 as "use the built-in
                worker count" (2), not as unlimited. */}
            <SettingField
              label="Files prepared at once"
              type="number"
              description="0 uses the built-in default of 2."
              value={form.getValue("download.max_concurrent_prepares")}
              onChange={(v) => form.setValue("download.max_concurrent_prepares", v)}
              restartRequired={restartKeys.has("download.max_concurrent_prepares")}
            />
            <LimitField
              label="Prepared file storage budget"
              unit="bytes"
              hint="Least recently used files are deleted first."
              value={form.getValue("download.artifact_max_bytes")}
              onChange={(v) => form.setValue("download.artifact_max_bytes", v)}
              restartRequired={restartKeys.has("download.artifact_max_bytes")}
            />
          </AdvancedSection>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
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

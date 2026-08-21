import { useMemo } from "react";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useHWAccelDetection } from "@/hooks/queries/admin/system";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { LimitField } from "@/components/settings/LimitField";
import { SettingField } from "./SettingField";
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

  if (form.isLoading) return <div>Loading...</div>;

  return (
    <div className="flex h-full flex-col">
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-semibold tracking-tight">Playback</h2>
        <p className="text-muted-foreground text-sm leading-relaxed">
          Transcoding, watch behavior, and downloads.
        </p>
      </div>

      <div className="flex-1 space-y-6">
        <FieldGroup label="Transcoding">
          <SettingField
            label="Transcoding"
            type="toggle"
            hint="Let the server convert media that a client cannot play as-is. Turning this off limits playback to files the client already supports."
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
            hint="Auto uses the graphics hardware this server finds, and falls back to the CPU when there is none."
            value={hwAccel}
            onChange={(v) => form.setValue("playback.hw_accel", v)}
            restartRequired={restartKeys.has("playback.hw_accel")}
          />
          {hwAccel === "auto" && hwDetection.data && (
            <div className="-mt-1 flex items-center gap-2 py-2 text-xs">
              <span
                className={`inline-block h-1.5 w-1.5 rounded-full ${
                  hwDetection.data.resolved !== "none" ? "bg-emerald-500" : "bg-amber-500"
                }`}
              />
              <span className="text-muted-foreground">
                Detected: {formatResolved(hwDetection.data.resolved)}
                {hwDetection.data.render_devices?.[0] && ` — ${hwDetection.data.render_devices[0]}`}
                {hwDetection.data.source === "transcode_node" && " (transcode node)"}
              </span>
            </div>
          )}
          {hwAccel === "auto" && hwDetection.isLoading && (
            <p className="text-muted-foreground -mt-1 py-2 text-xs">Detecting hardware...</p>
          )}
          {hwAccel !== "none" && isNvenc && selectedDevices.length > 1 && (
            <p className="-mt-1 py-2 text-xs text-amber-500">
              Multi-GPU balancing supports QSV/VA-API only; with NVENC the server uses the first
              configured device ({selectedDevices[0]}).
            </p>
          )}
          <SettingField
            label="Allow 4K transcoding"
            type="toggle"
            hint="Converting 4K video is heavy work for most hardware. Leave off to send 4K only to clients that can play it directly."
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
              hint="Leave empty to use the FFmpeg build that ships with the server."
              value={form.getValue("playback.ffmpeg_path")}
              onChange={(v) => form.setValue("playback.ffmpeg_path", v)}
              restartRequired={restartKeys.has("playback.ffmpeg_path")}
            />
            <SettingField
              label="Transcode directory"
              hint="Where in-progress transcodes are written. Use fast local storage with room to spare."
              value={form.getValue("playback.transcode_dir")}
              onChange={(v) => form.setValue("playback.transcode_dir", v)}
              restartRequired={restartKeys.has("playback.transcode_dir")}
            />
            {showDevicePicker && (
              <div className="flex flex-col gap-2 py-3">
                <div className="space-y-0.5">
                  <Label className="text-sm font-medium">GPU devices</Label>
                  <p className="text-muted-foreground text-xs">
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
              label="Transcode on this server when no node is free"
              type="toggle"
              hint="Turn off to keep every transcode on dedicated nodes. Playback that needs a transcode then fails while no node is available."
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
            />
            <SettingField
              label="Enable Software HDR Tone Mapping"
              type="toggle"
              hint="Allows the CPU to convert HDR video to SDR when transcoding. This can be very CPU-intensive."
              value={form.getValue("playback.transcode_software_tone_map_enabled") || "false"}
              onChange={(v) => form.setValue("playback.transcode_software_tone_map_enabled", v)}
              restartRequired={restartKeys.has("playback.transcode_software_tone_map_enabled")}
            />
            <SettingField
              label="Pause transcoding once it runs far ahead"
              type="toggle"
              hint="Saves CPU when a viewer is unlikely to watch the whole file, at the cost of a slower seek."
              value={form.getValue("enable_transcode_throttle")}
              onChange={(v) => form.setValue("enable_transcode_throttle", v)}
              restartRequired={restartKeys.has("enable_transcode_throttle")}
            />
            {form.getValue("enable_transcode_throttle") === "true" && (
              <SettingField
                label="Buffer ahead (seconds)"
                type="number"
                hint="How far ahead of the viewer the server transcodes before pausing. Minimum 60."
                value={form.getValue("transcode_throttle_seconds")}
                onChange={(v) => form.setValue("transcode_throttle_seconds", v)}
                restartRequired={restartKeys.has("transcode_throttle_seconds")}
              />
            )}
            <SettingField
              label="Chapter thumbnail workers"
              type="number"
              hint="How many chapter thumbnails are extracted at the same time. Higher finishes sooner but takes more of the server's capacity."
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
              hint="Choose whether this server does the work itself or hands it to transcode nodes."
              value={form.getValue("playback.chapter_thumbnail_execution") || "local"}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_execution", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_execution")}
            />
            <SettingField
              label="Chapter thumbnails for HDR video"
              type="select"
              options={[
                { value: "best_effort", label: "Generate when possible" },
                { value: "disabled", label: "Skip HDR and Dolby Vision" },
              ]}
              hint="HDR frames need extra color conversion to look right as thumbnails. Standard video is unaffected."
              value={form.getValue("playback.chapter_thumbnail_hdr_policy") || "best_effort"}
              onChange={(v) => form.setValue("playback.chapter_thumbnail_hdr_policy", v)}
              restartRequired={restartKeys.has("playback.chapter_thumbnail_hdr_policy")}
            />
            <SettingField
              label="Convert HDR colors on the CPU when the GPU cannot"
              type="toggle"
              hint="Lets HDR chapter thumbnails still be generated without graphics hardware. Off by default because it is slow."
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

        <FieldGroup label="Watch behavior">
          <SettingField
            label="Mark watched at (%)"
            type="number"
            hint="How much of an item has to be played before it counts as watched. Default 90."
            value={form.getValue("playback.watched_threshold")}
            onChange={(v) => form.setValue("playback.watched_threshold", v)}
            restartRequired={restartKeys.has("playback.watched_threshold")}
          />
          <SettingField
            label="Show resume after (%)"
            type="number"
            hint="Progress below this much of an item is ignored, so a brief preview does not land in Continue Watching. Default 5."
            value={form.getValue("playback.min_resume_threshold")}
            onChange={(v) => form.setValue("playback.min_resume_threshold", v)}
            restartRequired={restartKeys.has("playback.min_resume_threshold")}
          />
        </FieldGroup>

        <FieldGroup label="Downloads">
          <SettingField
            label="Allow downloads"
            type="toggle"
            hint="Let users save media files to their device."
            value={form.getValue("download.enabled")}
            onChange={(v) => form.setValue("download.enabled", v)}
            restartRequired={restartKeys.has("download.enabled")}
          />
          <LimitField
            label="Per-user bandwidth"
            unit="Mbps"
            hint="Speed cap for one user, shared across everything they are downloading."
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
              hint="Speed cap for all downloads on this server combined."
              value={form.getValue("download.server_bandwidth_mbps")}
              onChange={(v) => form.setValue("download.server_bandwidth_mbps", v)}
              restartRequired={restartKeys.has("download.server_bandwidth_mbps")}
            />
            <LimitField
              label="Downloads at once per user"
              hint="How many downloads one user can have running at the same time."
              value={form.getValue("download.max_concurrent_per_user")}
              onChange={(v) => form.setValue("download.max_concurrent_per_user", v)}
              restartRequired={restartKeys.has("download.max_concurrent_per_user")}
            />
            <LimitField
              label="Downloads per period"
              hint="How many downloads one user can start within the period below."
              value={form.getValue("download.max_per_period")}
              onChange={(v) => form.setValue("download.max_per_period", v)}
              restartRequired={restartKeys.has("download.max_per_period")}
            />
            <SettingField
              label="Period length"
              type="duration"
              hint="Rolling window for the per-period limit (for example 24h, 168h, 720h)."
              value={form.getValue("download.period_duration")}
              onChange={(v) => form.setValue("download.period_duration", v)}
              restartRequired={restartKeys.has("download.period_duration")}
            />
            <SettingField
              label="Prepare downloads in a device-friendly format"
              type="toggle"
              hint="Converts a file before download for devices that cannot play the original. Needs the per-user download permission. Files already on a device stay there until the user deletes them."
              value={form.getValue("download.transcode_enabled")}
              onChange={(v) => form.setValue("download.transcode_enabled", v)}
              restartRequired={restartKeys.has("download.transcode_enabled")}
            />
            <SettingField
              label="Prepared file directory"
              hint="Where prepared download files are written. Empty puts them next to the transcode directory."
              value={form.getValue("download.artifact_dir")}
              onChange={(v) => form.setValue("download.artifact_dir", v)}
              restartRequired={restartKeys.has("download.artifact_dir")}
            />
            {/* Not a LimitField: the server reads 0 as "use the built-in
                worker count" (2), not as unlimited. */}
            <SettingField
              label="Files prepared at once"
              type="number"
              hint="How many downloads the server prepares at the same time. Leave at 0 to use the built-in default of 2."
              value={form.getValue("download.max_concurrent_prepares")}
              onChange={(v) => form.setValue("download.max_concurrent_prepares", v)}
              restartRequired={restartKeys.has("download.max_concurrent_prepares")}
            />
            <LimitField
              label="Prepared file storage budget"
              unit="bytes"
              hint="Once prepared files pass this size, the least recently used ones are deleted."
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
        restartRequired={form.restartRequired}
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

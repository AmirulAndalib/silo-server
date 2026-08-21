import { useMemo, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { Link } from "react-router";

import type { ConnectionCheckResponse } from "@/api/types";
import { ConnectionCheckAction } from "@/components/admin/ConnectionCheckAction";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { SecretField } from "@/components/settings/SecretField";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { StatusStrip, type StatusStripItem } from "@/components/settings/StatusStrip";
import { Skeleton } from "@/components/ui/skeleton";
import { useBranding } from "@/hooks/useBranding";
import {
  useCatalogSearchStatus,
  useCheckAdminSettingsConnection,
} from "@/hooks/queries/admin/settings";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { FieldGroup } from "./FieldGroup";
import { MarkerProviderCards, MarkerTasksCard } from "./MarkerProviderSettings";
import { SaveBar } from "./SaveBar";
import { SearchStatusPanel } from "./SearchStatusPanel";
import { SettingField } from "./SettingField";

const CACHE_IMAGES_KEY = "metadata.cache_images";

const METADATA_KEYS = [CACHE_IMAGES_KEY];

const SCANNER_KEYS = ["scanner.workers", "matcher.workers", "matcher.batch_size"];

const MARKER_KEYS = ["markers.mode", "markers.lazy_playback"];

const MARKER_MODE_LABELS: Record<string, string> = {
  off: "Markers off",
  local: "Markers detected here",
  both: "Markers detected here, then online",
  online: "Markers looked up online",
};

const MEILI_URL_KEY = "catalog.search.meilisearch.url";
const MEILI_API_KEY = "catalog.search.meilisearch.api_key";

const MEILI_ADVANCED_KEYS = [
  "catalog.search.meilisearch.index",
  "catalog.search.meilisearch.timeout_ms",
  "catalog.search.meilisearch.matching_strategy",
  "catalog.search.meilisearch.sync_batch_size",
  "catalog.search.meilisearch.semantic_enabled",
  "catalog.search.meilisearch.semantic_ratio",
];

const MEILI_KEYS = [MEILI_URL_KEY, MEILI_API_KEY, ...MEILI_ADVANCED_KEYS];

const SEARCH_KEYS = ["catalog.search.provider", ...MEILI_KEYS];

// Hidden tier: still saved and readable through the settings API, deliberately
// without a control here because the defaults are right for every deployment we
// support — catalog.search.meilisearch.{rebuild_batch_size,
// rebuild_task_queue_depth,index_types,embedder,binary_quantized}.
const KEYS = [...METADATA_KEYS, ...SCANNER_KEYS, ...MARKER_KEYS, ...SEARCH_KEYS];

export default function LibraryMetadataSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const branding = useBranding();
  const restartKeys = useRestartKeys();
  const checkConnection = useCheckAdminSettingsConnection();
  const searchStatus = useCatalogSearchStatus();
  const [connectionResult, setConnectionResult] = useState<ConnectionCheckResponse | null>(null);

  // Image caching writes provider artwork into the public bucket, so the server
  // rejects enabling it when no bucket is configured at all. `storage_available`
  // is the process-level truth (branding uses the same flag for asset uploads);
  // s3.public_bucket only says the setting was saved, which is enough for the
  // server to accept the save and separates "restart pending" from "never
  // configured". s3.public_bucket is not staged here, but getValue falls back
  // to the full settings response.
  const publicBucketSaved = Boolean(form.getValue("s3.public_bucket"));
  const imageStorageAvailable = branding.storageAvailable;
  const cacheImagesOn = form.getValue(CACHE_IMAGES_KEY) === "true";
  // Never trap an admin with it on: turning it off stays available even when
  // the bucket went away.
  const cacheImagesLocked = !imageStorageAvailable && !publicBucketSaved && !cacheImagesOn;

  const provider = form.getValue("catalog.search.provider") || "postgres";
  const meiliEnabled = provider === "meilisearch";
  const anyDirty = (keys: string[]) => keys.some((key) => form.isDirty(key));
  const countDirty = (keys: string[]) => keys.filter((key) => form.isDirty(key)).length;
  const restartCount = KEYS.filter((key) => form.isDirty(key) && restartKeys.has(key)).length;
  // Staged Meilisearch edits stay reachable after switching the provider back,
  // so the save bar can never count a change the admin cannot see.
  const showMeili = meiliEnabled || anyDirty(MEILI_KEYS);

  async function handleCheckConnection() {
    try {
      setConnectionResult(
        await checkConnection.mutateAsync({
          kind: "meilisearch",
          body: form.buildConnectionCheckRequest(MEILI_KEYS),
        }),
      );
    } catch (error) {
      setConnectionResult({
        success: false,
        message: error instanceof Error ? error.message : "Connection check failed.",
      });
    }
  }

  const markerMode = form.getValue("markers.mode") || "local";
  const stripItems: StatusStripItem[] = [
    cacheImagesOn
      ? imageStorageAvailable
        ? { tone: "ok", label: "Artwork cached in S3" }
        : { tone: "warn", label: "Artwork caching waiting on storage" }
      : imageStorageAvailable || publicBucketSaved
        ? { tone: "muted", label: "Artwork caching off" }
        : { tone: "warn", label: "Artwork caching off — no public bucket" },
    {
      tone: markerMode === "off" ? "muted" : "ok",
      label: MARKER_MODE_LABELS[markerMode] ?? markerMode,
    },
    {
      tone: meiliEnabled && searchStatus.data?.meilisearch.healthy === false ? "warn" : "info",
      label: meiliEnabled
        ? `Search: Meilisearch${
            searchStatus.data
              ? searchStatus.data.meilisearch.healthy
                ? " · connected"
                : " · unreachable"
              : ""
          }`
        : "Search: built-in (Postgres)",
    },
  ];

  if (form.isLoading) {
    return (
      <div className="space-y-6" role="status" aria-label="Loading settings">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-32 w-full" />
        <Skeleton className="h-32 w-full" />
        <Skeleton className="h-40 w-full" />
        <span className="sr-only">Loading settings</span>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader
        title="Library & Metadata"
        description="Scanning, artwork caching, markers, and search."
        strip={<StatusStrip items={stripItems} />}
        className="mb-8"
      />

      <div className="flex-1 space-y-9">
        <FieldGroup
          label="Metadata"
          clarifier="Artwork and details fetched from providers. Scan schedules live in Scheduled Tasks."
        >
          {!imageStorageAvailable && (
            <div className="mt-3 flex items-start gap-3 rounded-xl border border-amber-500/20 bg-amber-500/5 p-3">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
              <p className="text-muted-foreground text-[13px] leading-relaxed">
                {publicBucketSaved ? (
                  <>
                    The public bucket is saved, but object storage is not active in this process
                    yet. Restart the server for image caching to start.
                  </>
                ) : (
                  <>
                    S3 image caching needs S3 object storage. Configure a public bucket in{" "}
                    <Link
                      to="/admin/settings?tab=infrastructure"
                      className="text-foreground font-medium underline-offset-2 hover:underline"
                    >
                      Infrastructure
                    </Link>{" "}
                    settings, then restart the server.
                  </>
                )}
              </p>
            </div>
          )}
          <SettingField
            label="S3 image caching"
            type="toggle"
            description="Copies posters and backdrops from metadata providers into your public S3 bucket, so clients load artwork from Silo instead of a third party."
            value={form.getValue(CACHE_IMAGES_KEY)}
            onChange={(value) => form.setValue(CACHE_IMAGES_KEY, value)}
            disabled={cacheImagesLocked}
            restartRequired={restartKeys.has(CACHE_IMAGES_KEY)}
            dirty={form.isDirty(CACHE_IMAGES_KEY)}
          />
        </FieldGroup>

        <FieldGroup label="Scanning" clarifier="How quickly Silo reads new files and looks them up">
          <AdvancedSection
            id="library.scanning"
            count={SCANNER_KEYS.length}
            changedCount={countDirty(SCANNER_KEYS)}
            forceOpen={anyDirty(SCANNER_KEYS)}
          >
            <SettingField
              label="Scanner workers"
              type="number"
              description="How many files Silo reads at once. Raise it on fast storage, lower it if scans slow the server down."
              value={form.getValue("scanner.workers")}
              onChange={(value) => form.setValue("scanner.workers", value)}
              restartRequired={restartKeys.has("scanner.workers")}
              dirty={form.isDirty("scanner.workers")}
            />
            <SettingField
              label="Matcher workers"
              type="number"
              description="How many items Silo looks up with metadata providers at once."
              value={form.getValue("matcher.workers")}
              onChange={(value) => form.setValue("matcher.workers", value)}
              restartRequired={restartKeys.has("matcher.workers")}
              dirty={form.isDirty("matcher.workers")}
            />
            <SettingField
              label="Matcher batch size"
              type="number"
              description="How many items each matcher worker claims per round."
              value={form.getValue("matcher.batch_size")}
              onChange={(value) => form.setValue("matcher.batch_size", value)}
              restartRequired={restartKeys.has("matcher.batch_size")}
              dirty={form.isDirty("matcher.batch_size")}
            />
          </AdvancedSection>
        </FieldGroup>

        <FieldGroup
          label="Intro and credits markers"
          clarifier="Where the Skip button in the player gets its timings"
        >
          <SettingField
            label="Find intros and credits"
            type="select"
            description="Markers let clients offer a Skip button. Detecting on this server costs CPU; looking online shares nothing about your library beyond the episode being matched."
            options={[
              { value: "off", label: "Off" },
              { value: "local", label: "Detect on this server" },
              { value: "both", label: "Detect on this server, then look online" },
              { value: "online", label: "Look online only" },
            ]}
            value={markerMode}
            onChange={(value) => form.setValue("markers.mode", value)}
            restartRequired={restartKeys.has("markers.mode")}
            dirty={form.isDirty("markers.mode")}
          />

          <AdvancedSection
            id="library.markers"
            count={2}
            changedCount={countDirty(["markers.lazy_playback"])}
            forceOpen={form.isDirty("markers.lazy_playback")}
          >
            <SettingField
              label="Look up missing markers when playback starts"
              type="toggle"
              description="Fetches markers for an episode that has none yet, which can delay the first few seconds of playback."
              value={form.getValue("markers.lazy_playback") || "false"}
              onChange={(value) => form.setValue("markers.lazy_playback", value)}
              restartRequired={restartKeys.has("markers.lazy_playback")}
              dirty={form.isDirty("markers.lazy_playback")}
            />
            <MarkerProviderCards />
          </AdvancedSection>

          <div className="py-3.5">
            <MarkerTasksCard />
          </div>
        </FieldGroup>

        <FieldGroup label="Search" clarifier="Which engine answers searches from clients">
          <SettingField
            label="Search engine"
            type="select"
            description="The built-in engine needs no extra service. Meilisearch tolerates typos and stays fast on large libraries, but runs as its own server."
            value={provider}
            onChange={(value) => form.setValue("catalog.search.provider", value)}
            options={[
              { value: "postgres", label: "Built-in (Postgres)" },
              { value: "meilisearch", label: "Meilisearch" },
            ]}
            restartRequired={restartKeys.has("catalog.search.provider")}
            dirty={form.isDirty("catalog.search.provider")}
          />

          {showMeili && (
            <>
              <SettingField
                label="Meilisearch URL"
                value={form.getValue(MEILI_URL_KEY)}
                onChange={(value) => form.setValue(MEILI_URL_KEY, value)}
                hint="http://localhost:7700"
                description="Address of the Meilisearch server Silo indexes into."
                disabled={!meiliEnabled}
                restartRequired={restartKeys.has(MEILI_URL_KEY)}
                dirty={form.isDirty(MEILI_URL_KEY)}
              />
              <SecretField
                label="Meilisearch API key"
                value={form.getValue(MEILI_API_KEY)}
                configured={form.sensitiveConfigured.includes(MEILI_API_KEY)}
                onChange={(value) => form.setValue(MEILI_API_KEY, value)}
                onKeep={() => form.resetValue(MEILI_API_KEY)}
                hint="The master key, or a key allowed to read and write Silo's index."
                disabled={!meiliEnabled}
                restartRequired={restartKeys.has(MEILI_API_KEY)}
                dirty={form.isDirty(MEILI_API_KEY)}
              />
              <div className="py-3">
                <ConnectionCheckAction
                  onClick={handleCheckConnection}
                  result={connectionResult}
                  isPending={checkConnection.isPending}
                  disabled={!meiliEnabled}
                />
              </div>

              <AdvancedSection
                id="library.search.meilisearch"
                count={MEILI_ADVANCED_KEYS.length}
                changedCount={countDirty(MEILI_ADVANCED_KEYS)}
                forceOpen={anyDirty(MEILI_ADVANCED_KEYS)}
              >
                <SettingField
                  label="Index name prefix"
                  value={form.getValue("catalog.search.meilisearch.index") || "silo_media_items"}
                  onChange={(value) => form.setValue("catalog.search.meilisearch.index", value)}
                  description="Change it only when several Silo servers share one Meilisearch instance."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.index")}
                  dirty={form.isDirty("catalog.search.meilisearch.index")}
                />
                <SettingField
                  label="Query timeout (ms)"
                  type="number"
                  value={form.getValue("catalog.search.meilisearch.timeout_ms") || "800"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.timeout_ms", value)
                  }
                  description="Searches that take longer fall back to the built-in engine."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.timeout_ms")}
                  dirty={form.isDirty("catalog.search.meilisearch.timeout_ms")}
                />
                <SettingField
                  label="When a search has several words"
                  type="select"
                  value={form.getValue("catalog.search.meilisearch.matching_strategy") || "last"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.matching_strategy", value)
                  }
                  options={[
                    { value: "last", label: "Drop trailing words until something matches" },
                    { value: "all", label: "Require every word" },
                  ]}
                  description="How Silo handles a search that has several words."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.matching_strategy")}
                  dirty={form.isDirty("catalog.search.meilisearch.matching_strategy")}
                />
                <SettingField
                  label="Items sent to the index per batch"
                  type="number"
                  value={form.getValue("catalog.search.meilisearch.sync_batch_size") || "500"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.sync_batch_size", value)
                  }
                  description="Larger batches index faster and use more memory on the Meilisearch host."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.sync_batch_size")}
                  dirty={form.isDirty("catalog.search.meilisearch.sync_batch_size")}
                />
                <SettingField
                  label="Match by meaning as well as words"
                  type="toggle"
                  value={form.getValue("catalog.search.meilisearch.semantic_enabled") || "false"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.semantic_enabled", value)
                  }
                  description="Also returns items whose description means something close to the search, reusing the embeddings the recommendations feature already builds."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.semantic_enabled")}
                  dirty={form.isDirty("catalog.search.meilisearch.semantic_enabled")}
                />
                <SettingField
                  label="Meaning-based share of results"
                  type="number"
                  value={form.getValue("catalog.search.meilisearch.semantic_ratio") || "0.50"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.semantic_ratio", value)
                  }
                  description="0 ranks purely by matching words, 1 purely by meaning, 0.5 blends the two."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.semantic_ratio")}
                  dirty={form.isDirty("catalog.search.meilisearch.semantic_ratio")}
                />
              </AdvancedSection>
            </>
          )}

          <AdvancedSection id="library.search.status" title="Search status">
            <SearchStatusPanel />
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

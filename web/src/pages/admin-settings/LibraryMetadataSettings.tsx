import { useMemo, useState } from "react";

import type { ConnectionCheckResponse } from "@/api/types";
import { ConnectionCheckAction } from "@/components/admin/ConnectionCheckAction";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { SecretField } from "@/components/settings/SecretField";
import { Skeleton } from "@/components/ui/skeleton";
import { useCheckAdminSettingsConnection } from "@/hooks/queries/admin/settings";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { FieldGroup } from "./FieldGroup";
import { MarkerProviderCards, MarkerTasksCard } from "./MarkerProviderSettings";
import { SaveBar } from "./SaveBar";
import { SearchStatusPanel } from "./SearchStatusPanel";
import { SettingField } from "./SettingField";

const METADATA_KEYS = ["metadata.cache_images"];

const SCANNER_KEYS = ["scanner.workers", "matcher.workers", "matcher.batch_size"];

const MARKER_KEYS = ["markers.mode", "markers.lazy_playback"];

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
  const restartKeys = useRestartKeys();
  const checkConnection = useCheckAdminSettingsConnection();
  const [connectionResult, setConnectionResult] = useState<ConnectionCheckResponse | null>(null);

  const provider = form.getValue("catalog.search.provider") || "postgres";
  const meiliEnabled = provider === "meilisearch";
  const anyDirty = (keys: string[]) => keys.some((key) => form.isDirty(key));
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
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-semibold tracking-tight">Library &amp; Metadata</h2>
        <p className="text-muted-foreground text-sm leading-relaxed">
          How Silo scans your files, fills in artwork and details, finds intros and credits, and
          answers searches. Scan schedules live in Scheduled Tasks.
        </p>
      </div>

      <div className="flex-1 space-y-6">
        <FieldGroup label="Metadata">
          <SettingField
            label="Store artwork on this server"
            type="toggle"
            hint="Copies posters and backdrops from metadata providers into your own storage, so clients load artwork from Silo instead of a third party."
            value={form.getValue("metadata.cache_images")}
            onChange={(value) => form.setValue("metadata.cache_images", value)}
            restartRequired={restartKeys.has("metadata.cache_images")}
          />
        </FieldGroup>

        <FieldGroup label="Scanning">
          <AdvancedSection
            id="library.scanning"
            count={SCANNER_KEYS.length}
            forceOpen={anyDirty(SCANNER_KEYS)}
          >
            <SettingField
              label="Scanner workers"
              type="number"
              hint="How many files Silo reads at once. Raise it on fast storage, lower it if scans slow the server down."
              value={form.getValue("scanner.workers")}
              onChange={(value) => form.setValue("scanner.workers", value)}
              restartRequired={restartKeys.has("scanner.workers")}
            />
            <SettingField
              label="Matcher workers"
              type="number"
              hint="How many items Silo looks up with metadata providers at once."
              value={form.getValue("matcher.workers")}
              onChange={(value) => form.setValue("matcher.workers", value)}
              restartRequired={restartKeys.has("matcher.workers")}
            />
            <SettingField
              label="Matcher batch size"
              type="number"
              hint="How many items each matcher worker claims per round."
              value={form.getValue("matcher.batch_size")}
              onChange={(value) => form.setValue("matcher.batch_size", value)}
              restartRequired={restartKeys.has("matcher.batch_size")}
            />
          </AdvancedSection>
        </FieldGroup>

        <FieldGroup label="Intro and credits markers">
          <SettingField
            label="Find intros and credits"
            type="select"
            hint="Markers let clients offer a Skip button. Detecting on this server costs CPU; looking online shares nothing about your library beyond the episode being matched."
            options={[
              { value: "off", label: "Off" },
              { value: "local", label: "Detect on this server" },
              { value: "both", label: "Detect on this server, then look online" },
              { value: "online", label: "Look online only" },
            ]}
            value={form.getValue("markers.mode") || "local"}
            onChange={(value) => form.setValue("markers.mode", value)}
            restartRequired={restartKeys.has("markers.mode")}
          />

          <AdvancedSection
            id="library.markers"
            count={2}
            forceOpen={form.isDirty("markers.lazy_playback")}
          >
            <SettingField
              label="Look up missing markers when playback starts"
              type="toggle"
              hint="Fetches markers for an episode that has none yet, which can delay the first few seconds of playback."
              value={form.getValue("markers.lazy_playback") || "false"}
              onChange={(value) => form.setValue("markers.lazy_playback", value)}
              restartRequired={restartKeys.has("markers.lazy_playback")}
            />
            <MarkerProviderCards />
          </AdvancedSection>

          <div className="py-3">
            <MarkerTasksCard />
          </div>
        </FieldGroup>

        <FieldGroup label="Search">
          <SettingField
            label="Search engine"
            type="select"
            hint="The built-in engine needs no extra service. Meilisearch tolerates typos and stays fast on large libraries, but runs as its own server."
            value={provider}
            onChange={(value) => form.setValue("catalog.search.provider", value)}
            options={[
              { value: "postgres", label: "Built-in (Postgres)" },
              { value: "meilisearch", label: "Meilisearch" },
            ]}
            restartRequired={restartKeys.has("catalog.search.provider")}
          />

          {showMeili && (
            <>
              <SettingField
                label="Meilisearch URL"
                value={form.getValue(MEILI_URL_KEY)}
                onChange={(value) => form.setValue(MEILI_URL_KEY, value)}
                hint="http://localhost:7700"
                disabled={!meiliEnabled}
                restartRequired={restartKeys.has(MEILI_URL_KEY)}
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
                forceOpen={anyDirty(MEILI_ADVANCED_KEYS)}
              >
                <SettingField
                  label="Index name prefix"
                  value={form.getValue("catalog.search.meilisearch.index") || "silo_media_items"}
                  onChange={(value) => form.setValue("catalog.search.meilisearch.index", value)}
                  hint="Change it only when several Silo servers share one Meilisearch instance."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.index")}
                />
                <SettingField
                  label="Query timeout (ms)"
                  type="number"
                  value={form.getValue("catalog.search.meilisearch.timeout_ms") || "800"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.timeout_ms", value)
                  }
                  hint="Searches that take longer fall back to the built-in engine."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.timeout_ms")}
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
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.matching_strategy")}
                />
                <SettingField
                  label="Items sent to the index per batch"
                  type="number"
                  value={form.getValue("catalog.search.meilisearch.sync_batch_size") || "500"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.sync_batch_size", value)
                  }
                  hint="Larger batches index faster and use more memory on the Meilisearch host."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.sync_batch_size")}
                />
                <SettingField
                  label="Match by meaning as well as words"
                  type="toggle"
                  value={form.getValue("catalog.search.meilisearch.semantic_enabled") || "false"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.semantic_enabled", value)
                  }
                  hint="Also returns items whose description means something close to the search, reusing the embeddings the recommendations feature already builds."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.semantic_enabled")}
                />
                <SettingField
                  label="Meaning-based share of results"
                  type="number"
                  value={form.getValue("catalog.search.meilisearch.semantic_ratio") || "0.50"}
                  onChange={(value) =>
                    form.setValue("catalog.search.meilisearch.semantic_ratio", value)
                  }
                  hint="0 ranks purely by matching words, 1 purely by meaning, 0.5 blends the two."
                  disabled={!meiliEnabled}
                  restartRequired={restartKeys.has("catalog.search.meilisearch.semantic_ratio")}
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
      />
    </div>
  );
}

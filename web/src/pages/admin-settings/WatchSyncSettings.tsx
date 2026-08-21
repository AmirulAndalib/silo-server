import { useState } from "react";
import { toast } from "sonner";

import { ProviderTile, ProviderTileGrid } from "@/components/settings/ProviderTile";
import { RestartBadge } from "@/components/settings/RestartBadge";
import { SecretField } from "@/components/settings/SecretField";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { StatusStrip } from "@/components/settings/StatusStrip";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useUpdateServerSettings } from "@/hooks/queries/admin/settings";
import { useRestartKeys, type RestartKeyMatcher } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";

import { FieldGroup } from "./FieldGroup";
import { RestartServerButton } from "./RestartServerButton";

/**
 * App credentials only. A viewer's own Trakt or Simkl account is linked from
 * their profile settings, never here.
 */
const KEYS = [
  "watchsync.trakt.client_id",
  "watchsync.trakt.client_secret",
  "watchsync.simkl.client_id",
  "watchsync.simkl.client_secret",
];

interface WatchProvider {
  key: string;
  title: string;
  monogram: string;
  monogramClass: string;
}

const PROVIDERS: WatchProvider[] = [
  {
    key: "trakt",
    title: "Trakt",
    monogram: "TR",
    monogramClass: "bg-red-500/20 text-red-700 dark:text-red-300",
  },
  {
    key: "simkl",
    title: "Simkl",
    monogram: "SK",
    monogramClass: "bg-amber-500/20 text-amber-700 dark:text-amber-300",
  },
];

function credentialKeys(providerKey: string) {
  return [
    { key: `watchsync.${providerKey}.client_id`, label: "Client ID" },
    { key: `watchsync.${providerKey}.client_secret`, label: "Client secret" },
  ];
}

function isConnected(providerKey: string, sensitiveConfigured: string[]): boolean {
  return credentialKeys(providerKey).every((field) => sensitiveConfigured.includes(field.key));
}

function WatchProviderTile({
  provider,
  sensitiveConfigured,
  restartKeys,
  expanded,
  onExpand,
  onCollapse,
}: {
  provider: WatchProvider;
  sensitiveConfigured: string[];
  restartKeys: RestartKeyMatcher;
  expanded: boolean;
  onExpand: () => void;
  onCollapse: () => void;
}) {
  const updateSettings = useUpdateServerSettings();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [needsRestart, setNeedsRestart] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);

  const { title } = provider;
  const fields = credentialKeys(provider.key);
  const configuredKeys = new Set(sensitiveConfigured);
  const anyConfigured = fields.some((field) => configuredKeys.has(field.key));
  const allConfigured = fields.every((field) => configuredKeys.has(field.key));
  const draftOf = (key: string) => drafts[key] ?? "";
  const restartRequired = fields.some((field) => restartKeys.has(field.key));

  async function save() {
    const updates: Record<string, string> = {};
    for (const field of fields) {
      if (draftOf(field.key).trim() !== "") updates[field.key] = draftOf(field.key);
    }
    if (Object.keys(updates).length === 0) {
      toast.info(`Nothing to save for ${title}.`);
      return;
    }
    try {
      const result = await updateSettings.mutateAsync(updates);
      setDrafts({});
      setNeedsRestart((current) => current || result.restart_required);
      toast.success(`${title} credentials saved`);
    } catch {
      // The mutation surfaces the API error.
    }
  }

  async function clearAll() {
    try {
      const result = await updateSettings.mutateAsync(
        Object.fromEntries(fields.map((field) => [field.key, ""])),
      );
      setDrafts({});
      setNeedsRestart((current) => current || result.restart_required);
      toast.success(`${title} credentials cleared`);
    } catch {
      // The mutation surfaces the API error.
    }
  }

  return (
    <ProviderTile
      name={title}
      tagline="Watch history sync"
      monogram={provider.monogram}
      monogramClass={provider.monogramClass}
      state={expanded ? "editing" : allConfigured ? "connected" : "not_connected"}
      statePill={!expanded && !allConfigured && anyConfigured ? "Partly set up" : undefined}
      badge={restartRequired ? <RestartBadge /> : undefined}
      meta={
        expanded ? undefined : allConfigured ? "App credentials stored" : "No credentials stored"
      }
      busy={updateSettings.isPending}
      expanded={expanded}
      primaryAction={{
        label: anyConfigured ? "Manage" : "Connect",
        onClick: onExpand,
      }}
    >
      <p className="text-muted-foreground mb-1 text-xs leading-relaxed">
        App credentials from {title}. Once they are saved, each viewer connects their own {title}{" "}
        account from their profile settings.
      </p>
      {fields.map((field) => (
        <SecretField
          key={field.key}
          label={field.label}
          value={draftOf(field.key)}
          configured={configuredKeys.has(field.key)}
          onChange={(next) => setDrafts((prev) => ({ ...prev, [field.key]: next }))}
          restartRequired={restartKeys.has(field.key)}
        />
      ))}
      <div className="mt-3.5 flex flex-wrap items-center gap-2">
        <Button
          type="button"
          size="sm"
          onClick={() => void save()}
          disabled={updateSettings.isPending}
        >
          {updateSettings.isPending ? "Saving..." : "Save"}
        </Button>
        {anyConfigured ? (
          <Button type="button" size="sm" variant="ghost" onClick={() => setConfirmClear(true)}>
            Clear credentials
          </Button>
        ) : null}
        <Button type="button" size="sm" variant="ghost" onClick={onCollapse}>
          Close
        </Button>
      </div>
      {needsRestart && (
        <div className="border-warning/30 bg-warning/10 text-warning mt-3 flex flex-wrap items-center justify-between gap-3 rounded-xl border px-3 py-2 text-xs">
          <span>Restart the server so {title} collection browsing picks up this change.</span>
          <RestartServerButton />
        </div>
      )}
      <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear {title} credentials?</AlertDialogTitle>
            <AlertDialogDescription>
              Viewers can no longer connect a {title} account, and existing connections stop
              syncing.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                void clearAll();
                setConfirmClear(false);
              }}
            >
              Clear
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ProviderTile>
  );
}

export default function WatchSyncSettings() {
  const form = useSettingsForm({ keys: KEYS });
  const restartKeys = useRestartKeys();
  const [expandedTile, setExpandedTile] = useState<string | null>(null);

  if (form.isLoading) {
    return (
      <div className="max-w-5xl space-y-6" role="status" aria-label="Loading watch sync">
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-40 w-full" />
        <span className="sr-only">Loading watch sync</span>
      </div>
    );
  }

  const connectedCount = PROVIDERS.filter((provider) =>
    isConnected(provider.key, form.sensitiveConfigured),
  ).length;

  return (
    <div className="flex h-full max-w-5xl flex-col gap-7">
      <SettingsPageHeader
        title="Watch sync"
        description="Keep watch history in sync with Trakt and Simkl."
        strip={
          <StatusStrip
            items={[
              {
                tone: connectedCount > 0 ? "ok" : "muted",
                label: `${connectedCount} of ${PROVIDERS.length} connected`,
              },
            ]}
          />
        }
      />

      <FieldGroup
        label="Watch providers"
        clarifier="Viewers link their own accounts from profile settings"
      >
        <div className="py-3.5">
          <ProviderTileGrid>
            {PROVIDERS.map((provider) => (
              <WatchProviderTile
                key={provider.key}
                provider={provider}
                sensitiveConfigured={form.sensitiveConfigured}
                restartKeys={restartKeys}
                expanded={expandedTile === provider.key}
                onExpand={() => setExpandedTile(provider.key)}
                onCollapse={() => setExpandedTile(null)}
              />
            ))}
          </ProviderTileGrid>
        </div>
      </FieldGroup>
    </div>
  );
}

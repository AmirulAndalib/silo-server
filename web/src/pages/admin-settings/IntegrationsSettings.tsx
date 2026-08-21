import { useId, useState } from "react";
import {
  AudioLines,
  BookOpen,
  Bot,
  Captions,
  Check,
  ChevronDown,
  CircleAlert,
  CircleCheck,
  Copy,
  ExternalLink,
  Languages,
  ListChecks,
  Tv,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { toast } from "sonner";

import { api } from "@/api/client";
import type { ConnectionCheckResponse, SubtitleProviderConfig } from "@/api/types";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { LimitField } from "@/components/settings/LimitField";
import { ProviderCard, type ProviderTestResult } from "@/components/settings/ProviderCard";
import { SecretField } from "@/components/settings/SecretField";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  useCheckAdminSettingsConnection,
  useUpdateServerSettings,
} from "@/hooks/queries/admin/settings";
import {
  useSubtitleProviders,
  useTestSubtitleProvider,
  useUpdateSubtitleProvider,
} from "@/hooks/queries/admin/subtitles";
import { useRestartKeys, type RestartKeyMatcher } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { QUOTA_PERIODS, QUOTA_PERIOD_WINDOW_LABELS } from "@/lib/quotaPeriods";
import { cn } from "@/lib/utils";

import { FieldGroup } from "./FieldGroup";
import { RestartServerButton } from "./RestartServerButton";
import { SaveBar } from "./SaveBar";
import { SettingField } from "./SettingField";

// ---------------------------------------------------------------------------
// Setting keys
// ---------------------------------------------------------------------------

const TEXT_AI_KEYS = ["ai.base_url", "ai.chat_model", "ai.api_key"] as const;
const SPEECH_AI_KEYS = [
  "ai.base_url",
  "ai.api_key",
  "ai.asr_base_url",
  "ai.asr_model",
  "ai.asr_api_key",
] as const;
/**
 * Pre-`ai.*` keys. They are still read as a fallback so a server that was
 * configured before the rename keeps working until the modern key is saved.
 */
const LEGACY_AI_KEYS = [
  "subtitle_ai.base_url",
  "subtitle_ai.api_key",
  "subtitle_ai.chat_model",
  "subtitle_ai.max_concurrent_jobs",
] as const;

const AI_FEATURE_KEYS = [
  "subtitle_ai.enabled",
  "subtitle_ai.transcribe_enabled",
  "metadata_ai.enabled",
  "metadata_ai.on_view",
];

const AI_ADVANCED_KEYS = [
  "ai.max_concurrent_jobs",
  "subtitle_ai.batch_size",
  "subtitle_ai.context_neighbors",
  "subtitle_ai.asr_chunk_seconds",
  "subtitle_ai.transcribe_quota_jobs",
  "subtitle_ai.transcribe_quota_period",
];

/**
 * Provider credentials are saved per card (they need a Test before they are
 * committed) but they are listed here so one form owns every value the tab
 * reads.
 */
const CREDENTIAL_KEYS = [
  "mdblist.api_key",
  "watchsync.trakt.client_id",
  "watchsync.trakt.client_secret",
  "watchsync.simkl.client_id",
  "watchsync.simkl.client_secret",
  "discord.client_id",
  "discord.client_secret",
  "discord.bot_token",
];

const KEYS: string[] = Array.from(
  new Set([
    ...TEXT_AI_KEYS,
    ...SPEECH_AI_KEYS,
    ...LEGACY_AI_KEYS,
    ...AI_FEATURE_KEYS,
    ...AI_ADVANCED_KEYS,
    ...CREDENTIAL_KEYS,
  ]),
);

// ---------------------------------------------------------------------------
// Subtitle providers
// ---------------------------------------------------------------------------

const SUBTITLE_PROVIDER_NAMES: Record<string, string> = {
  opensubtitles: "OpenSubtitles",
  subdl: "SubDL",
  subsource: "SubSource",
};

const SUBTITLE_PROVIDER_ORDER = ["opensubtitles", "subdl", "subsource"];

function SubtitleProviderCard({ config }: { config: SubtitleProviderConfig }) {
  // `null` means "follow the server"; the switch only pins a value while the
  // admin has an unsaved change, so a refetch can't silently flip it back.
  const [enabledDraft, setEnabledDraft] = useState<boolean | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [testResult, setTestResult] = useState<ProviderTestResult | null>(null);

  const updateProvider = useUpdateSubtitleProvider();
  const testProvider = useTestSubtitleProvider();

  const enabled = enabledDraft ?? config.enabled;
  const providerName = config.provider_name;
  const displayName = SUBTITLE_PROVIDER_NAMES[providerName] ?? providerName;
  const usesAccount = providerName === "opensubtitles";
  const configured = usesAccount ? config.has_credentials : config.has_api_key;

  const draft = usesAccount ? { username, password } : { api_key: apiKey };

  function handleSave() {
    updateProvider.mutate(
      { provider: providerName, config: { enabled, ...draft } },
      {
        onSuccess: () => {
          setEnabledDraft(null);
          setUsername("");
          setPassword("");
          setApiKey("");
        },
      },
    );
  }

  function handleTest() {
    setTestResult(null);
    testProvider.mutate(
      { provider: providerName, config: { enabled, ...draft } },
      {
        onSuccess: (result) =>
          setTestResult({
            success: result.success,
            message: result.success
              ? "Connection successful."
              : (result.error ?? "Connection failed."),
          }),
        onError: (err) =>
          setTestResult({
            success: false,
            message: err instanceof Error ? err.message : "Connection failed.",
          }),
      },
    );
  }

  function handleClear() {
    updateProvider.mutate(
      { provider: providerName, config: { enabled: false, clear_credentials: true } },
      {
        onSuccess: () => {
          setEnabledDraft(null);
          setUsername("");
          setPassword("");
          setApiKey("");
          setTestResult(null);
        },
      },
    );
  }

  const status: "connected" | "unconfigured" | "failing" =
    testResult && !testResult.success ? "failing" : configured ? "connected" : "unconfigured";

  return (
    <ProviderCard
      title={displayName}
      icon={Captions}
      description={
        usesAccount ? "Signs in with an account." : "Signs in with an API key from the provider."
      }
      status={status}
      statusLabel={
        status === "connected" && !enabled
          ? "Connected · off"
          : status === "failing"
            ? "Failing"
            : undefined
      }
      enabled={enabled}
      onEnabledChange={setEnabledDraft}
      busy={updateProvider.isPending || testProvider.isPending}
      onSave={handleSave}
      isSaving={updateProvider.isPending}
      onTest={handleTest}
      testLabel="Test"
      isTesting={testProvider.isPending}
      testResult={testResult}
      onClear={configured ? handleClear : undefined}
      clearDescription={`${displayName} is turned off and removed from subtitle searches right away.`}
      clearActionLabel="Clear and turn off"
      footer={
        <p className="text-muted-foreground text-xs">
          Test uses the values typed here. Saving applies to new searches right away.
        </p>
      }
    >
      {usesAccount ? (
        <>
          <SettingField
            label="Username"
            value={username}
            onChange={setUsername}
            hint={config.has_credentials ? "Leave blank to keep the saved username." : undefined}
          />
          <SecretField
            label="Password"
            value={password}
            configured={config.has_credentials}
            onChange={setPassword}
          />
        </>
      ) : (
        <SecretField
          label="API key"
          value={apiKey}
          configured={config.has_api_key}
          onChange={setApiKey}
        />
      )}
    </ProviderCard>
  );
}

function SubtitleProviderGrid() {
  const { data, isLoading } = useSubtitleProviders();

  if (isLoading) {
    return (
      <div className="grid gap-4 xl:grid-cols-2" role="status" aria-label="Loading providers">
        <Skeleton className="h-52 w-full" />
        <Skeleton className="h-52 w-full" />
        <span className="sr-only">Loading providers</span>
      </div>
    );
  }

  const providers = [...(data?.providers ?? [])].sort((a, b) => {
    const ai = SUBTITLE_PROVIDER_ORDER.indexOf(a.provider_name);
    const bi = SUBTITLE_PROVIDER_ORDER.indexOf(b.provider_name);
    if (ai === -1 && bi === -1) return 0;
    if (ai === -1) return 1;
    if (bi === -1) return -1;
    return ai - bi;
  });

  if (providers.length === 0) {
    return <p className="text-muted-foreground text-sm">No subtitle providers are available.</p>;
  }

  return (
    <div className="grid gap-4 xl:grid-cols-2">
      {providers.map((provider) => (
        <SubtitleProviderCard key={provider.provider_name} config={provider} />
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Credential cards backed by server settings keys
// ---------------------------------------------------------------------------

interface CredentialFieldSpec {
  key: string;
  label: string;
  hint?: string;
}

function SecretCredentialCard({
  title,
  description,
  icon,
  fields,
  sensitiveConfigured,
  restartKeys,
  testConnection,
  testLabel,
  clearDescription,
  restartNote,
  footer,
}: {
  title: string;
  description?: React.ReactNode;
  icon?: LucideIcon;
  fields: CredentialFieldSpec[];
  sensitiveConfigured: string[];
  restartKeys: RestartKeyMatcher;
  /** Omit for providers Silo cannot reach without user interaction. */
  testConnection?: (drafts: Record<string, string>) => Promise<ConnectionCheckResponse>;
  testLabel?: string;
  clearDescription?: string;
  restartNote?: string;
  footer?: React.ReactNode;
}) {
  const updateSettings = useUpdateServerSettings();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [testResult, setTestResult] = useState<ProviderTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [needsRestart, setNeedsRestart] = useState(false);

  const configuredKeys = new Set(sensitiveConfigured);
  const anyConfigured = fields.some((field) => configuredKeys.has(field.key));
  const allConfigured = fields.every((field) => configuredKeys.has(field.key));
  const draftOf = (key: string) => drafts[key] ?? "";
  const hasDraft = fields.some((field) => draftOf(field.key).trim() !== "");

  function setDraft(key: string, value: string) {
    setDrafts((prev) => ({ ...prev, [key]: value }));
    setTestResult(null);
  }

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
      setTestResult(null);
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
      setTestResult(null);
      setNeedsRestart((current) => current || result.restart_required);
      toast.success(`${title} credentials cleared`);
    } catch {
      // The mutation surfaces the API error.
    }
  }

  async function runTest() {
    if (!testConnection) return;
    setTesting(true);
    try {
      const result = await testConnection(
        Object.fromEntries(fields.map((field) => [field.key, draftOf(field.key)])),
      );
      setTestResult({ success: result.success, message: result.message });
    } catch (error) {
      setTestResult({
        success: false,
        message: error instanceof Error ? error.message : "Connection check failed.",
      });
    } finally {
      setTesting(false);
    }
  }

  const status: "connected" | "unconfigured" | "failing" =
    testResult && !testResult.success ? "failing" : allConfigured ? "connected" : "unconfigured";

  return (
    <ProviderCard
      title={title}
      description={description}
      icon={icon}
      status={status}
      statusLabel={status === "unconfigured" && anyConfigured ? "Partly set up" : undefined}
      restartRequired={fields.some((field) => restartKeys.has(field.key))}
      busy={updateSettings.isPending || testing}
      onSave={() => void save()}
      isSaving={updateSettings.isPending}
      onTest={testConnection ? () => void runTest() : undefined}
      testLabel={testLabel}
      isTesting={testing}
      testDisabled={!anyConfigured && !hasDraft}
      testResult={testResult}
      onClear={anyConfigured ? () => void clearAll() : undefined}
      clearDescription={clearDescription}
      footer={
        <>
          {footer}
          {needsRestart && (
            <div className="border-warning/30 bg-warning/10 text-warning flex flex-wrap items-center justify-between gap-3 rounded-xl border px-3 py-2 text-xs">
              <span>{restartNote ?? `Restart the server to finish applying ${title}.`}</span>
              <RestartServerButton />
            </div>
          )}
        </>
      }
    >
      {fields.map((field) => (
        <SecretField
          key={field.key}
          label={field.label}
          value={draftOf(field.key)}
          configured={configuredKeys.has(field.key)}
          onChange={(next) => setDraft(field.key, next)}
          hint={field.hint}
          restartRequired={restartKeys.has(field.key)}
        />
      ))}
    </ProviderCard>
  );
}

function WatchProviderCard({
  providerKey,
  title,
  sensitiveConfigured,
  restartKeys,
}: {
  providerKey: string;
  title: string;
  sensitiveConfigured: string[];
  restartKeys: RestartKeyMatcher;
}) {
  return (
    <SecretCredentialCard
      title={title}
      icon={Tv}
      description={`App credentials from ${title}. Once they are saved, each viewer connects their own ${title} account from their profile settings.`}
      fields={[
        { key: `watchsync.${providerKey}.client_id`, label: "Client ID" },
        { key: `watchsync.${providerKey}.client_secret`, label: "Client secret" },
      ]}
      sensitiveConfigured={sensitiveConfigured}
      restartKeys={restartKeys}
      clearDescription={`Viewers can no longer connect a ${title} account, and existing connections stop syncing.`}
      restartNote={`Restart the server so ${title} collection browsing picks up this change.`}
    />
  );
}

function MDBListCard({
  sensitiveConfigured,
  restartKeys,
}: {
  sensitiveConfigured: string[];
  restartKeys: RestartKeyMatcher;
}) {
  const checkConnection = useCheckAdminSettingsConnection();

  return (
    <SecretCredentialCard
      title="MDBList"
      icon={ListChecks}
      description={
        <>
          Lets viewers search and browse MDBList collections. Importing a list by its URL works
          without a key. Get a free key at{" "}
          <a
            href="https://mdblist.com/preferences/#api"
            target="_blank"
            rel="noreferrer"
            className="underline"
          >
            mdblist.com/preferences
          </a>
          .
        </>
      }
      fields={[{ key: "mdblist.api_key", label: "API key" }]}
      sensitiveConfigured={sensitiveConfigured}
      restartKeys={restartKeys}
      clearDescription="MDBList search and browse stop working. Lists already imported by URL are unaffected."
      testConnection={(drafts) =>
        checkConnection.mutateAsync({
          kind: "mdblist",
          body: {
            values: { "mdblist.api_key": drafts["mdblist.api_key"] ?? "" },
            dirty_keys: (drafts["mdblist.api_key"] ?? "").trim() === "" ? [] : ["mdblist.api_key"],
          },
        })
      }
      footer={
        <p className="text-muted-foreground text-xs">
          Test uses the key typed here, or the saved key when the field is empty.
        </p>
      }
    />
  );
}

// ---------------------------------------------------------------------------
// Discord application credentials
// ---------------------------------------------------------------------------

interface DiscordTestResult {
  ok: boolean;
  duration_ms: number;
  message?: string;
}

/**
 * Invite link for adding the bot to a Discord server. Membership alone is
 * enough to DM, so no permissions are requested.
 */
function discordInviteUrl(clientId: string): string {
  return `https://discord.com/oauth2/authorize?client_id=${encodeURIComponent(clientId)}&scope=bot&permissions=0`;
}

function DiscordSetupGuide() {
  const [open, setOpen] = useState(false);
  const guideId = useId();

  return (
    <div className="py-2">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={guideId}
        onClick={() => setOpen((current) => !current)}
        className="text-muted-foreground hover:text-foreground inline-flex cursor-pointer items-center gap-1.5 text-xs font-medium transition-colors"
      >
        <BookOpen className="h-3.5 w-3.5" />
        {open ? "Hide setup guide" : "Show setup guide"}
        <ChevronDown
          className={cn("h-3.5 w-3.5 transition-transform duration-200", open && "rotate-180")}
        />
      </button>
      {open && (
        <div
          id={guideId}
          className="text-muted-foreground animate-in fade-in-0 mt-3 space-y-1.5 text-xs leading-relaxed duration-200"
        >
          <p>Set up at discord.com/developers/applications:</p>
          <ol className="list-decimal space-y-1.5 pl-4">
            <li>Create an application (or open an existing one).</li>
            <li>
              OAuth2 page: copy the <strong>Client ID</strong>, reset and copy the{" "}
              <strong>Client Secret</strong>, and under Redirects add
              <code className="bg-muted mx-1 rounded px-1">
                {"<public URL>"}/api/v1/notifications/discord/link/callback
              </code>
              using this server&apos;s public URL (SILO_PUBLIC_URL) — it must match exactly.
            </li>
            <li>
              Bot page: reset and copy the <strong>Token</strong>. Leave all Privileged Gateway
              Intents (Presence, Server Members, Message Content) <strong>off</strong> — Silo never
              connects to the gateway; it only sends DMs.
            </li>
            <li>
              Keep <strong>Requires OAuth2 Code Grant</strong> off, or the invite link below
              won&apos;t work. Enable <strong>Public Bot</strong> only if someone other than the
              application owner will be inviting it.
            </li>
            <li>
              Paste the three credentials below, save, then use the invite button to add the bot to
              your Discord server. It needs <strong>no role permissions</strong> — membership alone
              lets it DM members, and users must share that server with the bot to receive DMs.
            </li>
          </ol>
        </div>
      )}
    </div>
  );
}

function InviteBotRow({ clientId }: { clientId: string }) {
  const [copied, setCopied] = useState(false);
  const trimmed = clientId.trim();

  return (
    <div className="space-y-2 py-2">
      <div className="flex flex-wrap gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!trimmed}
          onClick={() => window.open(discordInviteUrl(trimmed), "_blank", "noopener,noreferrer")}
        >
          <ExternalLink className="mr-1.5 h-3.5 w-3.5" />
          Invite bot to server
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!trimmed}
          onClick={() => {
            void navigator.clipboard.writeText(discordInviteUrl(trimmed));
            setCopied(true);
            toast.success("Invite link copied");
            window.setTimeout(() => setCopied(false), 2000);
          }}
        >
          {copied ? (
            <Check className="mr-1.5 h-3.5 w-3.5" />
          ) : (
            <Copy className="mr-1.5 h-3.5 w-3.5" />
          )}
          Copy link
        </Button>
      </div>
      <div className="text-muted-foreground text-xs">
        {trimmed
          ? "Anyone with Manage Server permission on your Discord server can open this link to add the bot. Users must be members of that server to receive DMs."
          : "Enter the Client ID above to generate the invite link."}
      </div>
    </div>
  );
}

function DiscordAppCard({
  savedClientId,
  sensitiveConfigured,
  restartKeys,
}: {
  savedClientId: string;
  sensitiveConfigured: string[];
  restartKeys: RestartKeyMatcher;
}) {
  const updateSettings = useUpdateServerSettings();
  // `null` follows the saved value; a draft is only pinned while the admin is
  // editing, so a refetch cannot overwrite typing in progress.
  const [clientIdDraft, setClientIdDraft] = useState<string | null>(null);
  const [clientSecret, setClientSecret] = useState("");
  const [botToken, setBotToken] = useState("");
  const [testResult, setTestResult] = useState<ProviderTestResult | null>(null);
  const [testing, setTesting] = useState(false);

  const clientId = clientIdDraft ?? savedClientId;
  const configuredKeys = new Set(sensitiveConfigured);
  const secretConfigured = configuredKeys.has("discord.client_secret");
  const tokenConfigured = configuredKeys.has("discord.bot_token");
  const ready = savedClientId.trim() !== "" && secretConfigured && tokenConfigured;
  const unsaved =
    clientId !== savedClientId || clientSecret.trim() !== "" || botToken.trim() !== "";

  async function save() {
    const updates: Record<string, string> = { "discord.client_id": clientId };
    if (clientSecret.trim() !== "") updates["discord.client_secret"] = clientSecret;
    if (botToken.trim() !== "") updates["discord.bot_token"] = botToken;
    try {
      await updateSettings.mutateAsync(updates);
      setClientIdDraft(null);
      setClientSecret("");
      setBotToken("");
      setTestResult(null);
      toast.success("Discord credentials saved");
    } catch {
      // The mutation surfaces the API error.
    }
  }

  async function clearAll() {
    try {
      await updateSettings.mutateAsync({
        "discord.client_id": "",
        "discord.client_secret": "",
        "discord.bot_token": "",
      });
      setClientIdDraft(null);
      setClientSecret("");
      setBotToken("");
      setTestResult(null);
      toast.success("Discord credentials cleared");
    } catch {
      // The mutation surfaces the API error.
    }
  }

  async function runTest() {
    setTesting(true);
    setTestResult(null);
    try {
      const response = await api<DiscordTestResult>("/admin/notifications/discord/test", {
        method: "POST",
      });
      setTestResult({
        success: response.ok,
        message: `${response.ok ? "Success" : "Failed"} (${response.duration_ms}ms)${
          response.message ? ` — ${response.message}` : ""
        }`,
      });
    } catch (error) {
      setTestResult({
        success: false,
        message: error instanceof Error ? error.message : "Test request failed.",
      });
    } finally {
      setTesting(false);
    }
  }

  const status: "connected" | "unconfigured" | "failing" =
    testResult && !testResult.success ? "failing" : ready ? "connected" : "unconfigured";

  return (
    <ProviderCard
      title="Discord app"
      icon={Bot}
      description="The Discord application Silo uses to link accounts and send direct messages. Which events get sent is set under Notifications."
      status={status}
      restartRequired={["discord.client_id", "discord.client_secret", "discord.bot_token"].some(
        (key) => restartKeys.has(key),
      )}
      busy={updateSettings.isPending || testing}
      onSave={() => void save()}
      isSaving={updateSettings.isPending}
      onTest={() => void runTest()}
      testLabel="Test bot token"
      isTesting={testing}
      testDisabled={unsaved || !ready}
      testResult={testResult}
      onClear={
        savedClientId.trim() !== "" || secretConfigured || tokenConfigured
          ? () => void clearAll()
          : undefined
      }
      clearDescription="Account linking and Discord direct messages stop working immediately."
      footer={
        unsaved ? (
          <p className="text-muted-foreground text-xs">
            Save first — the test uses the credentials stored on the server.
          </p>
        ) : undefined
      }
    >
      <DiscordSetupGuide />
      <SettingField
        label="Client ID"
        value={clientId}
        onChange={setClientIdDraft}
        hint="From the application's OAuth2 page."
        restartRequired={restartKeys.has("discord.client_id")}
      />
      <InviteBotRow clientId={clientId} />
      <SecretField
        label="Client secret"
        value={clientSecret}
        configured={secretConfigured}
        onChange={setClientSecret}
        hint="From the application's OAuth2 page. Used to link viewer accounts."
        restartRequired={restartKeys.has("discord.client_secret")}
      />
      <SecretField
        label="Bot token"
        value={botToken}
        configured={tokenConfigured}
        onChange={setBotToken}
        hint="From the application's Bot page. Used to send direct messages."
        restartRequired={restartKeys.has("discord.bot_token")}
      />
    </ProviderCard>
  );
}

// ---------------------------------------------------------------------------
// AI services
// ---------------------------------------------------------------------------

const TRANSCRIPTION_PRESETS = [
  {
    id: "self-hosted",
    label: "Self-hosted",
    description:
      "Speaches or faster-whisper on your network. Replace the hostname with one reachable from the Silo container.",
    baseUrl: "http://speaches:8000",
    model: "deepdml/faster-whisper-large-v3-turbo-ct2",
  },
  {
    id: "groq-turbo",
    label: "Groq - fast",
    description: "Hosted whisper-large-v3-turbo. Requires a Groq API key.",
    baseUrl: "https://api.groq.com/openai",
    model: "whisper-large-v3-turbo",
  },
  {
    id: "groq-accurate",
    label: "Groq - accurate",
    description: "Hosted whisper-large-v3. Requires a Groq API key.",
    baseUrl: "https://api.groq.com/openai",
    model: "whisper-large-v3",
  },
  {
    id: "openai",
    label: "OpenAI",
    description: "Hosted whisper-1. The transcription key can inherit the Text AI key.",
    baseUrl: "https://api.openai.com",
    model: "whisper-1",
  },
] as const;

const CHAT_ONLY_GATEWAY_HOSTS = ["openrouter.ai"];

function isChatOnlyGateway(rawURL: string): boolean {
  const trimmed = rawURL.trim();
  if (!trimmed) return false;
  try {
    const host = new URL(
      trimmed.includes("://") ? trimmed : `https://${trimmed}`,
    ).hostname.toLowerCase();
    return CHAT_ONLY_GATEWAY_HOSTS.some(
      (gateway) => host === gateway || host.endsWith(`.${gateway}`),
    );
  } catch {
    return false;
  }
}

function parseStrictInteger(rawValue: string): number | null {
  const trimmed = rawValue.trim();
  if (!/^-?\d+$/.test(trimmed)) return null;
  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) ? parsed : null;
}

function RequirementNote({
  label,
  ready,
  detail,
}: {
  label: string;
  ready: boolean;
  detail: string;
}) {
  return (
    <div className="text-muted-foreground flex items-start gap-2 pb-3 text-xs leading-relaxed">
      {ready ? (
        <CircleCheck className="mt-0.5 size-3.5 shrink-0 text-green-600" />
      ) : (
        <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-amber-600" />
      )}
      <span>
        <span className="text-foreground font-medium">{label}</span> - {detail}
      </span>
    </div>
  );
}

/** Note shown on an AI card whose values are staged in the page's save bar. */
function PendingSaveNote({ dirty }: { dirty: boolean }) {
  if (!dirty) return null;
  return (
    <p className="text-muted-foreground text-xs">
      Unsaved. Test works on what is typed here; use Save Changes below to store it.
    </p>
  );
}

function TextModelCard({
  baseURL,
  chatModel,
  apiKeyValue,
  apiKeyConfigured,
  ready,
  dirty,
  restartKeys,
  onChange,
  onReset,
  onTest,
  isTesting,
  testResult,
}: {
  baseURL: string;
  chatModel: string;
  apiKeyValue: string;
  apiKeyConfigured: boolean;
  ready: boolean;
  dirty: boolean;
  restartKeys: RestartKeyMatcher;
  onChange: (key: string, value: string) => void;
  onReset: (key: string) => void;
  onTest: () => void;
  isTesting: boolean;
  testResult: ProviderTestResult | null;
}) {
  return (
    <ProviderCard
      title="Text model"
      icon={Languages}
      description="A chat endpoint that speaks the OpenAI API. Silo uses it to translate existing subtitle text, descriptions, and taglines."
      status={testResult && !testResult.success ? "failing" : ready ? "connected" : "unconfigured"}
      statusLabel={ready && !testResult?.success ? "Configured" : undefined}
      onTest={onTest}
      testLabel="Test text model"
      testPendingLabel="Testing text model..."
      isTesting={isTesting}
      testDisabled={!ready}
      testResult={testResult}
      footer={<PendingSaveNote dirty={dirty} />}
    >
      <SettingField
        label="Base URL"
        value={baseURL}
        onChange={(next) => onChange("ai.base_url", next)}
        hint="https://api.openai.com"
        restartRequired={restartKeys.has("ai.base_url")}
      />
      <SettingField
        label="Model"
        value={chatModel}
        onChange={(next) => onChange("ai.chat_model", next)}
        hint="gpt-4o-mini, gemini-flash-latest, llama3.1"
        restartRequired={restartKeys.has("ai.chat_model")}
      />
      <SecretField
        label="API key"
        value={apiKeyValue}
        configured={apiKeyConfigured}
        onChange={(next) => onChange("ai.api_key", next)}
        // Without this, "Keep saved value" would stage an empty string and the
        // next save would erase the stored key.
        onKeep={() => onReset("ai.api_key")}
        hint="Leave empty for a local endpoint that does not need one."
        restartRequired={restartKeys.has("ai.api_key")}
      />
    </ProviderCard>
  );
}

function SpeechModelCard({
  asrBaseURL,
  asrModel,
  apiKeyValue,
  apiKeyConfigured,
  usesTextEndpoint,
  compatible,
  ready,
  checkable,
  dirty,
  restartKeys,
  onChange,
  onReset,
  onTest,
  isTesting,
  testResult,
}: {
  asrBaseURL: string;
  asrModel: string;
  apiKeyValue: string;
  apiKeyConfigured: boolean;
  usesTextEndpoint: boolean;
  compatible: boolean;
  ready: boolean;
  checkable: boolean;
  dirty: boolean;
  restartKeys: RestartKeyMatcher;
  onChange: (key: string, value: string) => void;
  onReset: (key: string) => void;
  onTest: () => void;
  isTesting: boolean;
  testResult: ProviderTestResult | null;
}) {
  const statusLabel = !compatible
    ? "Endpoint cannot transcribe"
    : testResult?.success
      ? "Verified"
      : usesTextEndpoint
        ? "Using the text model endpoint"
        : ready
          ? "Configured"
          : undefined;

  return (
    <ProviderCard
      title="Speech-to-text"
      icon={AudioLines}
      description="A Whisper-compatible endpoint that returns timestamps. Only needed when Silo creates subtitles from an audio track."
      status={
        !compatible || (testResult && !testResult.success)
          ? "failing"
          : ready && !usesTextEndpoint
            ? "connected"
            : "unconfigured"
      }
      statusLabel={statusLabel}
      onTest={onTest}
      testLabel="Test speech-to-text"
      testPendingLabel="Testing speech-to-text..."
      isTesting={isTesting}
      testDisabled={!checkable}
      testResult={testResult}
      footer={
        <>
          <p className="text-muted-foreground text-xs leading-relaxed">
            For a service on your own network, use a hostname or IP the Silo container can reach.
            <code className="mx-1">localhost</code>
            points back at Silo itself.
          </p>
          <PendingSaveNote dirty={dirty} />
        </>
      }
    >
      <div className="flex flex-wrap gap-2 py-2">
        {TRANSCRIPTION_PRESETS.map((preset) => {
          const active = asrBaseURL === preset.baseUrl && asrModel === preset.model;
          return (
            <button
              key={preset.id}
              type="button"
              title={preset.description}
              aria-pressed={active}
              onClick={() => {
                onChange("ai.asr_base_url", preset.baseUrl);
                onChange("ai.asr_model", preset.model);
              }}
              className={cn(
                "border-border hover:bg-accent rounded-md border px-3 py-1.5 text-xs transition-colors",
                active && "border-primary bg-primary/5 text-primary",
              )}
            >
              {preset.label}
            </button>
          );
        })}
      </div>
      <SettingField
        label="Base URL"
        value={asrBaseURL}
        onChange={(next) => onChange("ai.asr_base_url", next)}
        hint="http://speaches:8000 or https://api.groq.com/openai"
        restartRequired={restartKeys.has("ai.asr_base_url")}
      />
      {usesTextEndpoint && (
        <div className="my-2 flex gap-2 rounded-md border border-amber-500/25 bg-amber-500/5 px-3 py-2 text-xs leading-relaxed">
          <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-amber-600" />
          <span>
            Left empty, Silo sends audio to the text model endpoint and key. That only works if the
            provider also transcribes audio and returns timestamps. Test it before turning on
            subtitle generation.
          </span>
        </div>
      )}
      <SettingField
        label="Model"
        value={asrModel}
        onChange={(next) => onChange("ai.asr_model", next)}
        hint="whisper-large-v3-turbo or whisper-1"
        restartRequired={restartKeys.has("ai.asr_model")}
      />
      <SecretField
        label="API key"
        value={apiKeyValue}
        configured={apiKeyConfigured}
        onChange={(next) => onChange("ai.asr_api_key", next)}
        onKeep={() => onReset("ai.asr_api_key")}
        hint="Leave empty to reuse the text model key."
        restartRequired={restartKeys.has("ai.asr_api_key")}
      />
    </ProviderCard>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export default function IntegrationsSettings() {
  const form = useSettingsForm({ keys: KEYS });
  const restartKeys = useRestartKeys();
  const textCheck = useCheckAdminSettingsConnection();
  const speechCheck = useCheckAdminSettingsConnection();
  const [textResult, setTextResult] = useState<ProviderTestResult | null>(null);
  const [speechResult, setSpeechResult] = useState<ProviderTestResult | null>(null);

  if (form.isLoading) {
    return (
      <div className="space-y-6" role="status" aria-label="Loading integrations">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-64 w-full" />
        <Skeleton className="h-64 w-full" />
        <Skeleton className="h-48 w-full" />
        <span className="sr-only">Loading integrations</span>
      </div>
    );
  }

  const value = (key: string, fallback = "") => form.getValue(key) || fallback;
  // Legacy `subtitle_ai.*` values stay authoritative until the modern `ai.*`
  // key holds something, exactly as the old AI Services tab read them.
  const effectiveValue = (key: string, legacyKey: string, fallback: string) =>
    value(key, value(legacyKey, fallback));

  const textBaseURL = effectiveValue(
    "ai.base_url",
    "subtitle_ai.base_url",
    "https://api.openai.com",
  );
  const chatModel = effectiveValue("ai.chat_model", "subtitle_ai.chat_model", "gpt-4o-mini");
  const asrBaseURL = value("ai.asr_base_url");
  const asrModel = value("ai.asr_model", "whisper-1");
  const textReady = textBaseURL.trim() !== "" && chatModel.trim() !== "";
  const speechUsesTextEndpoint = asrBaseURL.trim() === "";
  const speechCheckable =
    (asrBaseURL.trim() !== "" || textBaseURL.trim() !== "") && asrModel.trim() !== "";
  const speechCompatible = !isChatOnlyGateway(speechUsesTextEndpoint ? textBaseURL : asrBaseURL);
  const speechReady = speechCheckable && speechCompatible;
  const descriptionEnabled = value("metadata_ai.enabled", "false") === "true";
  const advancedDirty = AI_ADVANCED_KEYS.some((key) => form.isDirty(key));

  function setValue(key: string, nextValue: string) {
    form.setValue(key, nextValue);
    if (TEXT_AI_KEYS.includes(key as (typeof TEXT_AI_KEYS)[number])) {
      setTextResult(null);
    }
    if (SPEECH_AI_KEYS.includes(key as (typeof SPEECH_AI_KEYS)[number])) {
      setSpeechResult(null);
    }
  }

  async function checkTextConnection() {
    try {
      const result = await textCheck.mutateAsync({
        kind: "ai_chat",
        body: form.buildConnectionCheckRequest([...TEXT_AI_KEYS]),
      });
      setTextResult({ success: result.success, message: result.message });
    } catch (error) {
      setTextResult({
        success: false,
        message: error instanceof Error ? error.message : "Text model connection check failed.",
      });
    }
  }

  async function checkSpeechConnection() {
    try {
      const result = await speechCheck.mutateAsync({
        kind: "ai_transcription",
        body: form.buildConnectionCheckRequest([...SPEECH_AI_KEYS]),
      });
      setSpeechResult({ success: result.success, message: result.message });
    } catch (error) {
      setSpeechResult({
        success: false,
        message: error instanceof Error ? error.message : "Speech-to-text connection check failed.",
      });
    }
  }

  async function save() {
    const batchSize = parseStrictInteger(value("subtitle_ai.batch_size", "40"));
    const contextLines = parseStrictInteger(value("subtitle_ai.context_neighbors", "2"));
    const chunkSeconds = parseStrictInteger(value("subtitle_ai.asr_chunk_seconds", "600"));
    const quotaJobs = Number.parseInt(value("subtitle_ai.transcribe_quota_jobs", "0"), 10);
    const maxConcurrent = parseStrictInteger(
      effectiveValue("ai.max_concurrent_jobs", "subtitle_ai.max_concurrent_jobs", "2"),
    );

    if (!textReady) {
      toast.error("Text AI base URL and chat model are required.");
      return;
    }
    if (maxConcurrent === null || maxConcurrent < 1) {
      toast.error("Max concurrent jobs must be a positive whole number.");
      return;
    }
    if (batchSize === null || batchSize < 1) {
      toast.error("Subtitle batch size must be a positive whole number.");
      return;
    }
    if (contextLines === null || contextLines < 0) {
      toast.error("Subtitle context lines must be zero or a positive whole number.");
      return;
    }
    if (chunkSeconds === null || chunkSeconds < 60 || chunkSeconds > 600) {
      toast.error("Transcription chunk length must be between 60 and 600 seconds.");
      return;
    }
    if (!Number.isInteger(quotaJobs) || quotaJobs < 0) {
      toast.error("Transcription limit must be zero or a positive whole number.");
      return;
    }
    await form.save();
  }

  function discard() {
    form.discard();
    setTextResult(null);
    setSpeechResult(null);
  }

  return (
    <div className="flex h-full max-w-5xl flex-col">
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-semibold tracking-tight">Integrations</h2>
        <p className="text-muted-foreground text-sm leading-relaxed">
          Accounts and API keys for the outside services Silo talks to. Each card saves on its own
          so you can test it before it goes live.
        </p>
      </div>

      <div className="space-y-6">
        <FieldGroup label="Subtitle providers">
          <SubtitleProviderGrid />
        </FieldGroup>

        <FieldGroup label="Watch providers">
          <div className="grid gap-4 xl:grid-cols-2">
            <WatchProviderCard
              providerKey="trakt"
              title="Trakt"
              sensitiveConfigured={form.sensitiveConfigured}
              restartKeys={restartKeys}
            />
            <WatchProviderCard
              providerKey="simkl"
              title="Simkl"
              sensitiveConfigured={form.sensitiveConfigured}
              restartKeys={restartKeys}
            />
          </div>
        </FieldGroup>

        <FieldGroup label="Metadata">
          <div className="grid gap-4 xl:grid-cols-2">
            <MDBListCard sensitiveConfigured={form.sensitiveConfigured} restartKeys={restartKeys} />
          </div>
        </FieldGroup>

        <FieldGroup label="Apps">
          <div className="grid gap-4 xl:grid-cols-2">
            <DiscordAppCard
              savedClientId={form.getValue("discord.client_id")}
              sensitiveConfigured={form.sensitiveConfigured}
              restartKeys={restartKeys}
            />
          </div>
        </FieldGroup>

        <FieldGroup label="AI services">
          <div className="grid gap-4 xl:grid-cols-2">
            <TextModelCard
              baseURL={textBaseURL}
              chatModel={chatModel}
              apiKeyValue={value("ai.api_key")}
              apiKeyConfigured={
                form.sensitiveConfigured.includes("ai.api_key") ||
                form.sensitiveConfigured.includes("subtitle_ai.api_key")
              }
              ready={textReady}
              dirty={TEXT_AI_KEYS.some((key) => form.isDirty(key))}
              restartKeys={restartKeys}
              onChange={setValue}
              onReset={form.resetValue}
              onTest={() => void checkTextConnection()}
              isTesting={textCheck.isPending}
              testResult={textResult}
            />
            <SpeechModelCard
              asrBaseURL={asrBaseURL}
              asrModel={asrModel}
              apiKeyValue={value("ai.asr_api_key")}
              apiKeyConfigured={form.sensitiveConfigured.includes("ai.asr_api_key")}
              usesTextEndpoint={speechUsesTextEndpoint}
              compatible={speechCompatible}
              ready={speechReady}
              checkable={speechCheckable}
              dirty={SPEECH_AI_KEYS.some((key) => form.isDirty(key))}
              restartKeys={restartKeys}
              onChange={setValue}
              onReset={form.resetValue}
              onTest={() => void checkSpeechConnection()}
              isTesting={speechCheck.isPending}
              testResult={speechResult}
            />
          </div>
        </FieldGroup>

        <FieldGroup label="AI features">
          <div>
            <SettingField
              label="Translate subtitles"
              type="toggle"
              value={value("subtitle_ai.enabled", "false")}
              onChange={(next) => setValue("subtitle_ai.enabled", next)}
              hint="Turns an existing text subtitle track into another language."
              restartRequired={restartKeys.has("subtitle_ai.enabled")}
            />
            <RequirementNote
              label="Needs the text model"
              ready={textReady}
              detail="Uses the text model card above."
            />
          </div>
          <div>
            <SettingField
              label="Create subtitles from audio"
              type="toggle"
              value={value("subtitle_ai.transcribe_enabled", "false")}
              onChange={(next) => setValue("subtitle_ai.transcribe_enabled", next)}
              hint="Listens to the selected audio track and writes timed subtitles for it."
              restartRequired={restartKeys.has("subtitle_ai.transcribe_enabled")}
            />
            <RequirementNote
              label="Needs speech-to-text"
              ready={speechReady}
              detail="Also uses the text model when the requested subtitle language differs from the audio language."
            />
          </div>
          <div>
            <SettingField
              label="Translate descriptions"
              type="toggle"
              value={value("metadata_ai.enabled", "false")}
              onChange={(next) => setValue("metadata_ai.enabled", next)}
              hint="Translates overviews and taglines from the metadata editor or a library refresh."
              restartRequired={restartKeys.has("metadata_ai.enabled")}
            />
            <RequirementNote
              label="Needs the text model"
              ready={textReady}
              detail="Speech-to-text is not used."
            />
          </div>
          <SettingField
            label="Description translation for viewers"
            type="select"
            value={value("metadata_ai.on_view", "off")}
            onChange={(next) => setValue("metadata_ai.on_view", next)}
            disabled={!descriptionEnabled}
            options={[
              { value: "off", label: "Off" },
              { value: "button", label: "Translate button on detail pages" },
              { value: "auto", label: "Automatic on view" },
            ]}
            hint={
              descriptionEnabled
                ? "Whether viewers can trigger a translation from a detail page."
                : "Inactive until Translate descriptions is turned on."
            }
            restartRequired={restartKeys.has("metadata_ai.on_view")}
          />
          <div className="pt-3">
            <AdvancedSection
              id="integrations.ai"
              count={AI_ADVANCED_KEYS.length}
              forceOpen={advancedDirty}
            >
              <SettingField
                label="Jobs running at once"
                type="number"
                value={effectiveValue(
                  "ai.max_concurrent_jobs",
                  "subtitle_ai.max_concurrent_jobs",
                  "2",
                )}
                onChange={(next) => setValue("ai.max_concurrent_jobs", next)}
                hint="Shared by subtitle translation, speech-to-text, and description translation."
                restartRequired={restartKeys.has("ai.max_concurrent_jobs")}
              />
              <SettingField
                label="Subtitle lines per request"
                type="number"
                value={value("subtitle_ai.batch_size", "40")}
                onChange={(next) => setValue("subtitle_ai.batch_size", next)}
                hint="How many subtitle lines are translated in one request."
                restartRequired={restartKeys.has("subtitle_ai.batch_size")}
              />
              <SettingField
                label="Surrounding lines sent for context"
                type="number"
                value={value("subtitle_ai.context_neighbors", "2")}
                onChange={(next) => setValue("subtitle_ai.context_neighbors", next)}
                hint="Earlier lines included so a scene keeps its meaning."
                restartRequired={restartKeys.has("subtitle_ai.context_neighbors")}
              />
              <SettingField
                label="Audio sent per request (seconds)"
                type="number"
                value={value("subtitle_ai.asr_chunk_seconds", "600")}
                onChange={(next) => setValue("subtitle_ai.asr_chunk_seconds", next)}
                hint="Between 60 and 600. Shorter pieces keep timings tighter but make more requests."
                restartRequired={restartKeys.has("subtitle_ai.asr_chunk_seconds")}
              />
              <LimitField
                label="Transcriptions per account"
                value={value("subtitle_ai.transcribe_quota_jobs", "0")}
                onChange={(next) => setValue("subtitle_ai.transcribe_quota_jobs", next)}
                fallbackValue="10"
                hint="Every profile on an account shares the account's allowance."
                restartRequired={restartKeys.has("subtitle_ai.transcribe_quota_jobs")}
              />
              <SettingField
                label="Allowance resets"
                type="select"
                value={value("subtitle_ai.transcribe_quota_period", "day")}
                onChange={(next) => setValue("subtitle_ai.transcribe_quota_period", next)}
                options={QUOTA_PERIODS.map((period) => ({
                  value: period,
                  label: `Per ${period} (rolling ${QUOTA_PERIOD_WINDOW_LABELS[period]})`,
                }))}
                hint="The rolling window the allowance above is counted over."
                restartRequired={restartKeys.has("subtitle_ai.transcribe_quota_period")}
              />
            </AdvancedSection>
          </div>
        </FieldGroup>
      </div>

      <div className="bg-muted/30 mt-6 flex flex-col gap-3 rounded-md px-4 py-3 text-sm sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="font-medium">Recommendation embeddings are configured separately</p>
          <p className="text-muted-foreground mt-0.5 text-xs">
            Search vectors and recommendations do not use the models above.
          </p>
        </div>
        <a
          href="/admin/recommendations"
          className="text-primary inline-flex shrink-0 items-center gap-1 text-xs font-medium hover:underline"
        >
          Open Recommendations
          <ExternalLink className="size-3.5" />
        </a>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={() => void save()}
        onDiscard={discard}
        isSaving={form.isSaving}
        restartRequired={form.restartRequired}
      />
    </div>
  );
}

import { useState } from "react";
import { AudioLines, CircleAlert, ExternalLink, Languages } from "lucide-react";
import { toast } from "sonner";

import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { LimitField } from "@/components/settings/LimitField";
import { ProviderTile, ProviderTileGrid } from "@/components/settings/ProviderTile";
import type { ProviderTileState } from "@/components/settings/ProviderTile";
import { SecretField } from "@/components/settings/SecretField";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { StatusStrip } from "@/components/settings/StatusStrip";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useCheckAdminSettingsConnection } from "@/hooks/queries/admin/settings";
import { useRestartKeys, type RestartKeyMatcher } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { QUOTA_PERIODS, QUOTA_PERIOD_WINDOW_LABELS } from "@/lib/quotaPeriods";
import { cn } from "@/lib/utils";

import { FieldGroup } from "./FieldGroup";
import { SaveBar } from "./SaveBar";
import { SettingField, SettingFieldStatus } from "./SettingField";

// ---------------------------------------------------------------------------
// Setting keys
// ---------------------------------------------------------------------------

const TEXT_AI_KEYS = ["ai.base_url", "ai.chat_model", "ai.api_key"] as const;
/**
 * What the transcription connection check has to send: the speech endpoint
 * plus the text endpoint it falls back to when no ASR base URL is set.
 */
const SPEECH_AI_KEYS = [
  "ai.base_url",
  "ai.api_key",
  "ai.asr_base_url",
  "ai.asr_model",
  "ai.asr_api_key",
] as const;
/**
 * The keys the speech tile actually renders. Only these decide whether it is
 * held open by a staged edit — the shared text keys are edited in the text
 * tile, so counting them here would expand both tiles at once.
 */
const SPEECH_ONLY_KEYS = ["ai.asr_base_url", "ai.asr_model", "ai.asr_api_key"] as const;
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

const KEYS: string[] = Array.from(
  new Set([
    ...TEXT_AI_KEYS,
    ...SPEECH_AI_KEYS,
    ...LEGACY_AI_KEYS,
    ...AI_FEATURE_KEYS,
    ...AI_ADVANCED_KEYS,
  ]),
);

// ---------------------------------------------------------------------------
// Helpers
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

function hostLabel(rawURL: string): string {
  const trimmed = rawURL.trim();
  if (!trimmed) return "";
  try {
    return new URL(trimmed.includes("://") ? trimmed : `https://${trimmed}`).host;
  } catch {
    return trimmed;
  }
}

function parseStrictInteger(rawValue: string): number | null {
  const trimmed = rawValue.trim();
  if (!/^-?\d+$/.test(trimmed)) return null;
  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) ? parsed : null;
}

/** Result of the last connection check, kept in memory for the tile and strip. */
interface AITestState {
  ok: boolean;
  message: string;
  at: number;
  durationMs: number;
}

function testedLabel(test: AITestState): string {
  const seconds = Math.max(0, Math.round((Date.now() - test.at) / 1000));
  const ago = seconds < 60 ? `${seconds}s ago` : `${Math.round(seconds / 60)}m ago`;
  return `Tested ${ago} · ${test.durationMs} ms`;
}

/** The action row shared by both model tiles. */
function ModelPanelActions({
  testLabel,
  pendingLabel,
  onTest,
  isTesting,
  testDisabled,
  onCollapse,
  canCollapse,
  test,
}: {
  testLabel: string;
  pendingLabel: string;
  onTest: () => void;
  isTesting: boolean;
  testDisabled: boolean;
  onCollapse: () => void;
  /**
   * False while a staged edit holds the tile open. Collapsing then does
   * nothing, so the button is left out rather than shown as a dead control.
   */
  canCollapse: boolean;
  test: AITestState | undefined;
}) {
  return (
    <div className="mt-3.5 flex flex-wrap items-center gap-2">
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={onTest}
        disabled={testDisabled || isTesting}
      >
        {isTesting ? pendingLabel : testLabel}
      </Button>
      {canCollapse ? (
        <Button type="button" size="sm" variant="ghost" onClick={onCollapse}>
          Close
        </Button>
      ) : null}
      {test ? (
        <span
          role="status"
          aria-live="polite"
          className={cn(
            "ml-auto text-[11.5px]",
            test.ok ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400",
          )}
        >
          {test.ok ? `${test.message} · ${testedLabel(test)}` : test.message}
        </span>
      ) : null}
    </div>
  );
}

/** Note shown on a model tile whose values are staged in the page's save bar. */
function PendingSaveNote({ dirty }: { dirty: boolean }) {
  if (!dirty) return null;
  return (
    <p className="text-muted-foreground mt-2 text-xs">
      Unsaved. Test works on what is typed here; use Save Changes below to store it.
    </p>
  );
}

// ---------------------------------------------------------------------------
// Model tiles
// ---------------------------------------------------------------------------

function TextModelTile({
  baseURL,
  chatModel,
  apiKeyValue,
  apiKeyConfigured,
  ready,
  dirty,
  isDirty,
  restartKeys,
  onChange,
  onReset,
  onTest,
  isTesting,
  test,
  expanded,
  onExpand,
  onCollapse,
}: {
  baseURL: string;
  chatModel: string;
  apiKeyValue: string;
  apiKeyConfigured: boolean;
  ready: boolean;
  dirty: boolean;
  isDirty: (key: string) => boolean;
  restartKeys: RestartKeyMatcher;
  onChange: (key: string, value: string) => void;
  onReset: (key: string) => void;
  onTest: () => void;
  isTesting: boolean;
  test: AITestState | undefined;
  expanded: boolean;
  onExpand: () => void;
  onCollapse: () => void;
}) {
  const failed = test != null && !test.ok;
  const state: ProviderTileState = expanded
    ? "editing"
    : failed
      ? "error"
      : ready
        ? "connected"
        : "not_connected";

  return (
    <ProviderTile
      name="Text model"
      tagline="Translates subtitle text, descriptions, and taglines"
      logo={<Languages className="text-muted-foreground size-4" aria-hidden="true" />}
      state={state}
      statePill={!expanded && ready && !test?.ok ? "Configured" : undefined}
      meta={
        expanded
          ? undefined
          : failed
            ? test.message
            : ready
              ? `${chatModel} · ${hostLabel(baseURL)}`
              : "Base URL and model required"
      }
      expanded={expanded}
      primaryAction={{ label: ready ? "Manage" : "Connect", onClick: onExpand }}
    >
      <p className="text-muted-foreground mb-1 text-xs leading-relaxed">
        A chat endpoint that speaks the OpenAI API. Silo uses it to translate existing subtitle
        text, descriptions, and taglines.
      </p>
      <SettingField
        label="Base URL"
        value={baseURL}
        onChange={(next) => onChange("ai.base_url", next)}
        hint="https://api.openai.com"
        restartRequired={restartKeys.has("ai.base_url")}
        dirty={isDirty("ai.base_url")}
      />
      <SettingField
        label="Model"
        value={chatModel}
        onChange={(next) => onChange("ai.chat_model", next)}
        hint="gpt-4o-mini, gemini-flash-latest, llama3.1"
        restartRequired={restartKeys.has("ai.chat_model")}
        dirty={isDirty("ai.chat_model")}
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
        dirty={isDirty("ai.api_key")}
      />
      <ModelPanelActions
        testLabel="Test text model"
        pendingLabel="Testing text model..."
        onTest={onTest}
        isTesting={isTesting}
        testDisabled={!ready}
        onCollapse={onCollapse}
        canCollapse={!dirty}
        test={test}
      />
      <PendingSaveNote dirty={dirty} />
    </ProviderTile>
  );
}

function SpeechModelTile({
  asrBaseURL,
  asrModel,
  apiKeyValue,
  apiKeyConfigured,
  usesTextEndpoint,
  compatible,
  ready,
  checkable,
  dirty,
  isDirty,
  restartKeys,
  onChange,
  onReset,
  onTest,
  isTesting,
  test,
  expanded,
  onExpand,
  onCollapse,
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
  isDirty: (key: string) => boolean;
  restartKeys: RestartKeyMatcher;
  onChange: (key: string, value: string) => void;
  onReset: (key: string) => void;
  onTest: () => void;
  isTesting: boolean;
  test: AITestState | undefined;
  expanded: boolean;
  onExpand: () => void;
  onCollapse: () => void;
}) {
  const failed = test != null && !test.ok;
  const statePill = !compatible
    ? "Endpoint cannot transcribe"
    : test?.ok
      ? "Verified"
      : usesTextEndpoint
        ? "Using the text model endpoint"
        : ready
          ? "Configured"
          : undefined;
  const state: ProviderTileState = expanded
    ? "editing"
    : !compatible || failed
      ? "error"
      : ready && !usesTextEndpoint
        ? "connected"
        : "not_connected";

  return (
    <ProviderTile
      name="Speech-to-text"
      tagline="Writes subtitles from an audio track"
      logo={<AudioLines className="text-muted-foreground size-4" aria-hidden="true" />}
      state={state}
      statePill={expanded ? undefined : statePill}
      meta={
        expanded
          ? undefined
          : !compatible
            ? "This endpoint only serves chat completions."
            : failed
              ? test.message
              : asrBaseURL.trim() !== ""
                ? `${asrModel} · ${hostLabel(asrBaseURL)}`
                : "Falls back to the text model endpoint"
      }
      expanded={expanded}
      primaryAction={{ label: ready ? "Manage" : "Connect", onClick: onExpand }}
    >
      <p className="text-muted-foreground mb-1 text-xs leading-relaxed">
        A Whisper-compatible endpoint that returns timestamps. Only needed when Silo creates
        subtitles from an audio track.
      </p>
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
        dirty={isDirty("ai.asr_base_url")}
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
        dirty={isDirty("ai.asr_model")}
      />
      <SecretField
        label="API key"
        value={apiKeyValue}
        configured={apiKeyConfigured}
        onChange={(next) => onChange("ai.asr_api_key", next)}
        onKeep={() => onReset("ai.asr_api_key")}
        hint="Leave empty to reuse the text model key."
        restartRequired={restartKeys.has("ai.asr_api_key")}
        dirty={isDirty("ai.asr_api_key")}
      />
      <ModelPanelActions
        testLabel="Test speech-to-text"
        pendingLabel="Testing speech-to-text..."
        onTest={onTest}
        isTesting={isTesting}
        testDisabled={!checkable}
        onCollapse={onCollapse}
        canCollapse={!dirty}
        test={test}
      />
      <p className="text-muted-foreground mt-2 text-xs leading-relaxed">
        For a service on your own network, use a hostname or IP the Silo container can reach.
        <code className="mx-1">localhost</code>
        points back at Silo itself.
      </p>
      <PendingSaveNote dirty={dirty} />
    </ProviderTile>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export default function AISettings() {
  const form = useSettingsForm({ keys: KEYS });
  const restartKeys = useRestartKeys();
  const textCheck = useCheckAdminSettingsConnection();
  const speechCheck = useCheckAdminSettingsConnection();
  const [textResult, setTextResult] = useState<AITestState | undefined>(undefined);
  const [speechResult, setSpeechResult] = useState<AITestState | undefined>(undefined);
  const [expandedTile, setExpandedTile] = useState<string | null>(null);

  if (form.isLoading) {
    return (
      <div className="max-w-5xl space-y-6" role="status" aria-label="Loading AI settings">
        <Skeleton className="h-9 w-48" />
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-64 w-full" />
        <span className="sr-only">Loading AI settings</span>
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
  const textDirty = TEXT_AI_KEYS.some((key) => form.isDirty(key));
  const speechDirty = SPEECH_ONLY_KEYS.some((key) => form.isDirty(key));
  const advancedChangedCount = AI_ADVANCED_KEYS.filter((key) => form.isDirty(key)).length;

  const featureCount = [
    value("subtitle_ai.enabled", "false") === "true",
    value("subtitle_ai.transcribe_enabled", "false") === "true",
    descriptionEnabled,
  ].filter(Boolean).length;

  function setValue(key: string, nextValue: string) {
    form.setValue(key, nextValue);
    if (TEXT_AI_KEYS.includes(key as (typeof TEXT_AI_KEYS)[number])) {
      setTextResult(undefined);
    }
    if (SPEECH_AI_KEYS.includes(key as (typeof SPEECH_AI_KEYS)[number])) {
      setSpeechResult(undefined);
    }
  }

  async function checkTextConnection() {
    const started = Date.now();
    try {
      const result = await textCheck.mutateAsync({
        kind: "ai_chat",
        body: form.buildConnectionCheckRequest([...TEXT_AI_KEYS]),
      });
      setTextResult({
        ok: result.success,
        message: result.message,
        at: Date.now(),
        durationMs: Date.now() - started,
      });
    } catch (error) {
      setTextResult({
        ok: false,
        message: error instanceof Error ? error.message : "Text model connection check failed.",
        at: Date.now(),
        durationMs: Date.now() - started,
      });
    }
  }

  async function checkSpeechConnection() {
    const started = Date.now();
    try {
      const result = await speechCheck.mutateAsync({
        kind: "ai_transcription",
        body: form.buildConnectionCheckRequest([...SPEECH_AI_KEYS]),
      });
      setSpeechResult({
        ok: result.success,
        message: result.message,
        at: Date.now(),
        durationMs: Date.now() - started,
      });
    } catch (error) {
      setSpeechResult({
        ok: false,
        message: error instanceof Error ? error.message : "Speech-to-text connection check failed.",
        at: Date.now(),
        durationMs: Date.now() - started,
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
    setTextResult(undefined);
    setSpeechResult(undefined);
  }

  return (
    <div className="flex h-full max-w-5xl flex-col gap-7">
      <SettingsPageHeader
        title="AI"
        description="Optional language models for subtitle translation, transcription, and descriptions."
        strip={
          <StatusStrip
            items={[
              {
                tone: textReady ? "ok" : "muted",
                label: textReady ? "Text model configured" : "Text model not configured",
              },
              {
                tone: speechReady ? "ok" : speechCompatible ? "muted" : "warn",
                label: !speechCompatible
                  ? "Speech endpoint cannot transcribe"
                  : speechReady
                    ? "Speech-to-text configured"
                    : "Speech-to-text not configured",
              },
              {
                tone: featureCount > 0 ? "info" : "muted",
                label: `${featureCount} of 3 features on`,
              },
            ]}
          />
        }
      />

      <FieldGroup label="Models" clarifier="Any endpoint that speaks the OpenAI API">
        <div className="py-3.5">
          <ProviderTileGrid className="xl:grid-cols-2">
            <TextModelTile
              baseURL={textBaseURL}
              chatModel={chatModel}
              apiKeyValue={value("ai.api_key")}
              apiKeyConfigured={
                form.sensitiveConfigured.includes("ai.api_key") ||
                form.sensitiveConfigured.includes("subtitle_ai.api_key")
              }
              ready={textReady}
              dirty={textDirty}
              isDirty={form.isDirty}
              restartKeys={restartKeys}
              onChange={setValue}
              onReset={form.resetValue}
              onTest={() => void checkTextConnection()}
              isTesting={textCheck.isPending}
              test={textResult}
              // A staged edit forces its tile open: the save bar must never
              // block on a field the admin cannot see.
              expanded={expandedTile === "text" || textDirty}
              onExpand={() => setExpandedTile("text")}
              onCollapse={() => setExpandedTile(null)}
            />
            <SpeechModelTile
              asrBaseURL={asrBaseURL}
              asrModel={asrModel}
              apiKeyValue={value("ai.asr_api_key")}
              apiKeyConfigured={form.sensitiveConfigured.includes("ai.asr_api_key")}
              usesTextEndpoint={speechUsesTextEndpoint}
              compatible={speechCompatible}
              ready={speechReady}
              checkable={speechCheckable}
              dirty={speechDirty}
              isDirty={form.isDirty}
              restartKeys={restartKeys}
              onChange={setValue}
              onReset={form.resetValue}
              onTest={() => void checkSpeechConnection()}
              isTesting={speechCheck.isPending}
              test={speechResult}
              expanded={expandedTile === "speech" || speechDirty}
              onExpand={() => setExpandedTile("speech")}
              onCollapse={() => setExpandedTile(null)}
            />
          </ProviderTileGrid>
        </div>
      </FieldGroup>

      <FieldGroup label="Features" clarifier="What Silo is allowed to use the models for">
        <SettingField
          label="Translate subtitles"
          type="toggle"
          value={value("subtitle_ai.enabled", "false")}
          onChange={(next) => setValue("subtitle_ai.enabled", next)}
          description="Turns an existing text subtitle track into another language."
          status={
            <SettingFieldStatus tone={textReady ? "ok" : "warn"}>
              Needs the text model — uses the text model tile above.
            </SettingFieldStatus>
          }
          restartRequired={restartKeys.has("subtitle_ai.enabled")}
          dirty={form.isDirty("subtitle_ai.enabled")}
        />
        <SettingField
          label="Create subtitles from audio"
          type="toggle"
          value={value("subtitle_ai.transcribe_enabled", "false")}
          onChange={(next) => setValue("subtitle_ai.transcribe_enabled", next)}
          description="Listens to the selected audio track and writes timed subtitles for it."
          status={
            <SettingFieldStatus tone={speechReady ? "ok" : "warn"}>
              Needs speech-to-text — also uses the text model when the requested subtitle language
              differs from the audio language.
            </SettingFieldStatus>
          }
          restartRequired={restartKeys.has("subtitle_ai.transcribe_enabled")}
          dirty={form.isDirty("subtitle_ai.transcribe_enabled")}
        />
        <SettingField
          label="Translate descriptions"
          type="toggle"
          value={value("metadata_ai.enabled", "false")}
          onChange={(next) => setValue("metadata_ai.enabled", next)}
          description="Translates overviews and taglines from the metadata editor or a library refresh."
          status={
            <SettingFieldStatus tone={textReady ? "ok" : "warn"}>
              Needs the text model — speech-to-text is not used.
            </SettingFieldStatus>
          }
          restartRequired={restartKeys.has("metadata_ai.enabled")}
          dirty={form.isDirty("metadata_ai.enabled")}
        />
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
          description={
            descriptionEnabled
              ? "Whether viewers can trigger a translation from a detail page."
              : "Inactive until Translate descriptions is turned on."
          }
          restartRequired={restartKeys.has("metadata_ai.on_view")}
          dirty={form.isDirty("metadata_ai.on_view")}
        />
        <AdvancedSection
          id="ai.tuning"
          count={AI_ADVANCED_KEYS.length}
          changedCount={advancedChangedCount}
          forceOpen={advancedChangedCount > 0}
        >
          <SettingField
            label="Jobs running at once"
            type="number"
            value={effectiveValue("ai.max_concurrent_jobs", "subtitle_ai.max_concurrent_jobs", "2")}
            onChange={(next) => setValue("ai.max_concurrent_jobs", next)}
            description="Shared by subtitle translation, speech-to-text, and description translation."
            restartRequired={restartKeys.has("ai.max_concurrent_jobs")}
            dirty={form.isDirty("ai.max_concurrent_jobs")}
          />
          <SettingField
            label="Subtitle lines per request"
            type="number"
            value={value("subtitle_ai.batch_size", "40")}
            onChange={(next) => setValue("subtitle_ai.batch_size", next)}
            description="How many subtitle lines are translated in one request."
            restartRequired={restartKeys.has("subtitle_ai.batch_size")}
            dirty={form.isDirty("subtitle_ai.batch_size")}
          />
          <SettingField
            label="Surrounding lines sent for context"
            type="number"
            value={value("subtitle_ai.context_neighbors", "2")}
            onChange={(next) => setValue("subtitle_ai.context_neighbors", next)}
            description="Earlier lines included so a scene keeps its meaning."
            restartRequired={restartKeys.has("subtitle_ai.context_neighbors")}
            dirty={form.isDirty("subtitle_ai.context_neighbors")}
          />
          <SettingField
            label="Audio sent per request (seconds)"
            type="number"
            value={value("subtitle_ai.asr_chunk_seconds", "600")}
            onChange={(next) => setValue("subtitle_ai.asr_chunk_seconds", next)}
            description="Between 60 and 600. Shorter pieces keep timings tighter but make more requests."
            restartRequired={restartKeys.has("subtitle_ai.asr_chunk_seconds")}
            dirty={form.isDirty("subtitle_ai.asr_chunk_seconds")}
          />
          <LimitField
            label="Transcriptions per account"
            value={value("subtitle_ai.transcribe_quota_jobs", "0")}
            onChange={(next) => setValue("subtitle_ai.transcribe_quota_jobs", next)}
            fallbackValue="10"
            hint="Every profile on an account shares the account's allowance."
            restartRequired={restartKeys.has("subtitle_ai.transcribe_quota_jobs")}
            dirty={form.isDirty("subtitle_ai.transcribe_quota_jobs")}
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
            description="The rolling window the allowance above is counted over."
            restartRequired={restartKeys.has("subtitle_ai.transcribe_quota_period")}
            dirty={form.isDirty("subtitle_ai.transcribe_quota_period")}
          />
        </AdvancedSection>
      </FieldGroup>

      <div className="bg-muted/30 flex flex-col gap-3 rounded-md px-4 py-3 text-sm sm:flex-row sm:items-center sm:justify-between">
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

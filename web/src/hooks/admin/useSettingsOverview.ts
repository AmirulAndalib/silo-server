import { useMemo } from "react";

import type {
  AdminServerStatus,
  JellyfinCompatStatus,
  RateLimitConfig,
  ServerNotificationChannel,
  SubtitleProviderConfig,
} from "@/api/types";
import { useBranding } from "@/hooks/useBranding";
import {
  useAdminSensitiveStatus,
  useAdminServerSettings,
  useAdminServerStatus,
  useCatalogSearchStatus,
  useJellyfinCompatStatus,
  type CatalogSearchStatus,
} from "@/hooks/queries/admin/settings";
import { useRateLimitConfig } from "@/hooks/queries/admin/rateLimits";
import { useServerNotificationChannels } from "@/hooks/queries/admin/serverNotificationChannels";
import { useSubtitleProviders } from "@/hooks/queries/admin/subtitles";
import { useHWAccelDetection, type HWAccelInfo } from "@/hooks/queries/admin/system";
import { THEME_IDS, THEMES, type ThemeId } from "@/lib/themes";

/**
 * The settings tabs the overview links to. These are URL state (`?tab=`), so
 * the ids here have to match the ones the settings layout mounts.
 */
export const SETTINGS_OVERVIEW_TAB_IDS = [
  "general",
  "appearance",
  "security",
  "library",
  "playback",
  "providers",
  "watch-sync",
  "ai",
  "notifications",
  "compatibility",
  "infrastructure",
] as const;

export type SettingsOverviewTabID = (typeof SETTINGS_OVERVIEW_TAB_IDS)[number];

/** Colour band a health tile reads in: green, amber, dimmed, or neutral blue. */
export type OverviewState = "ok" | "warn" | "off" | "info";

export interface OverviewTileAction {
  label: string;
  tab: SettingsOverviewTabID;
}

export interface OverviewTile {
  id: string;
  label: string;
  state: OverviewState;
  /** One or two words, e.g. "Healthy" or "Restart pending". */
  stateText: string;
  /** Single supporting line under the state. */
  detail: string;
  action?: OverviewTileAction;
}

export interface OverviewCard {
  id: SettingsOverviewTabID;
  title: string;
  /** One phrase saying what the section is doing right now. */
  summary: string;
  /** True when something in the section needs the admin; reads amber. */
  attention: boolean;
  /** True when nothing in the section is set up yet. */
  inactive: boolean;
}

export type SectionStatus = "ok" | "warn" | "off";

export interface SettingsOverviewModel {
  /** True until the settings map has arrived; the page shows skeletons. */
  isLoading: boolean;
  tiles: OverviewTile[];
  cards: OverviewCard[];
  sectionStatus: Record<SettingsOverviewTabID, SectionStatus>;
}

/**
 * Everything the model is derived from. Kept as plain data (rather than read
 * off hooks inside the builder) so the derivation is testable without a query
 * client, and so a missing/failed query is just `undefined` here.
 */
export interface SettingsOverviewInput {
  settings?: Record<string, string>;
  sensitiveConfigured?: readonly string[];
  storageAvailable?: boolean;
  serverStatus?: AdminServerStatus;
  search?: CatalogSearchStatus;
  hwAccel?: HWAccelInfo;
  jellyfin?: JellyfinCompatStatus;
  rateLimits?: RateLimitConfig;
  subtitleProviders?: readonly SubtitleProviderConfig[];
  serverChannels?: readonly ServerNotificationChannel[];
}

/** Deep link to one settings tab. */
export function settingsTabHref(tab: string): string {
  return `/admin/settings?tab=${encodeURIComponent(tab)}`;
}

/** Rendered wherever a value exists as a concept but not as data yet. */
export const UNKNOWN_VALUE = "—";

const TRUE_VALUES = new Set(["true", "1", "yes", "on"]);

function readText(settings: Record<string, string> | undefined, key: string): string {
  return (settings?.[key] ?? "").trim();
}

function readBool(settings: Record<string, string> | undefined, key: string): boolean {
  return TRUE_VALUES.has(readText(settings, key).toLowerCase());
}

function readInt(settings: Record<string, string> | undefined, key: string): number | null {
  const parsed = Number.parseInt(readText(settings, key), 10);
  return Number.isFinite(parsed) ? parsed : null;
}

function join(parts: Array<string | null | undefined>): string {
  return parts.filter((part): part is string => Boolean(part && part.trim())).join(" · ");
}

function sentenceCase(value: string): string {
  if (!value) return "";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

/**
 * Turns a Go duration string into something an admin reads at a glance.
 * Anything it does not recognise is passed through untouched rather than
 * guessed at.
 */
export function formatDurationSetting(raw: string): string {
  const value = raw.trim();
  const match = /^(\d+(?:\.\d+)?)(h|m|s)$/.exec(value);
  if (!match) return value;
  const amount = Number(match[1]);
  if (!Number.isFinite(amount)) return value;
  if (match[2] === "h") {
    if (amount >= 24 && amount % 24 === 0) return plural(amount / 24, "day");
    return plural(amount, "hour");
  }
  if (match[2] === "m") return plural(amount, "minute");
  return plural(amount, "second");
}

function plural(amount: number, unit: string): string {
  const rounded = Number.isInteger(amount) ? amount : Number(amount.toFixed(1));
  return `${rounded} ${unit}${rounded === 1 ? "" : "s"}`;
}

const HW_ACCEL_LABELS: Record<string, string> = {
  auto: "Auto",
  qsv: "Quick Sync",
  vaapi: "VA-API",
  nvenc: "NVENC",
  videotoolbox: "VideoToolbox",
  none: "Software",
};

function hwAccelLabel(value: string): string {
  return HW_ACCEL_LABELS[value] ?? value.toUpperCase();
}

/**
 * How transcoding is set up, in one phrase: the configured mode, and for
 * "auto" the mode detection actually resolved to.
 */
function transcodeModeLabel(configured: string, detection: HWAccelInfo | undefined): string {
  const mode = configured || "auto";
  if (mode !== "auto") return hwAccelLabel(mode);
  const resolved = detection?.resolved;
  if (!resolved) return "Auto";
  if (resolved === "none") return "Auto · software";
  return `Auto · ${hwAccelLabel(resolved)}`;
}

const SEARCH_PROVIDER_LABELS: Record<string, string> = {
  postgres: "Postgres",
  meilisearch: "Meilisearch",
};

function searchProviderLabel(value: string): string {
  return SEARCH_PROVIDER_LABELS[value] ?? sentenceCase(value);
}

const SUBTITLE_PROVIDER_LABELS: Record<string, string> = {
  opensubtitles: "OpenSubtitles",
  subdl: "SubDL",
  subsource: "SubSource",
};

const AI_FEATURE_KEYS = [
  "subtitle_ai.enabled",
  "subtitle_ai.transcribe_enabled",
  "metadata_ai.enabled",
  "metadata_ai.on_view",
];

/**
 * Restart reasons that name a subsystem other than the settings batch. The
 * tracker only records a coarse reason (`internal/api/handlers`), so a
 * "server_settings" restart is the closest signal there is to "a saved
 * playback key is waiting for a restart".
 */
const NON_SETTINGS_RESTART_REASONS = new Set([
  "jellyfin_compat",
  "ratelimit_backend",
  "plugin_auth_binding",
  "plugin_task_binding",
]);

function settingsRestartPending(status: AdminServerStatus | undefined): boolean {
  if (!status?.restart_required) return false;
  const reason = (status.restart_required_reason ?? "").trim();
  return reason === "" || !NON_SETTINGS_RESTART_REASONS.has(reason);
}

/** Host portion of a configured URL, or the raw value when it will not parse. */
function urlHost(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "";
  try {
    return new URL(trimmed).host;
  } catch {
    return trimmed;
  }
}

// ---------------------------------------------------------------------------
// Health tiles
// ---------------------------------------------------------------------------

/**
 * The link a tile carries. Only a tile the admin has to do something about
 * gets one — a healthy tile is a fact, not a task.
 */
function tileAction(
  state: OverviewState,
  tab: SettingsOverviewTabID,
): OverviewTileAction | undefined {
  if (state === "warn") return { label: "Fix", tab };
  if (state === "off") return { label: "Set up", tab };
  return undefined;
}

function buildTiles(input: SettingsOverviewInput): OverviewTile[] {
  const { settings } = input;
  const configured = new Set(input.sensitiveConfigured ?? []);

  const publicBucket = readText(settings, "s3.public_bucket");
  const privateBucket = readText(settings, "s3.private_bucket");
  const storageReady = input.storageAvailable === true;
  const bucketDetail = privateBucket
    ? "S3 · public + private"
    : publicBucket
      ? `S3 · ${publicBucket}`
      : "No bucket configured";
  const storageState: OverviewState = storageReady ? "ok" : "off";

  const maxConnections = readInt(settings, "database.max_connections");
  const redisConfigured = configured.has("redis.url") || readText(settings, "redis.url") !== "";

  const transcodeEnabled = readBool(settings, "playback.transcode_enabled");
  const restartPending = settingsRestartPending(input.serverStatus);
  const transcodeMode = transcodeModeLabel(readText(settings, "playback.hw_accel"), input.hwAccel);
  const renderDevice = input.hwAccel?.render_devices?.[0] ?? "";
  const transcodeState: OverviewState = restartPending ? "warn" : transcodeEnabled ? "ok" : "off";

  const activeSearch =
    input.search?.active_provider || readText(settings, "catalog.search.provider") || "postgres";
  const meiliConfigured = input.search?.meilisearch.configured ?? false;
  const meiliHealthy = input.search?.meilisearch.healthy ?? false;
  const searchState: OverviewState =
    activeSearch === "meilisearch"
      ? meiliHealthy
        ? "ok"
        : "warn"
      : meiliConfigured
        ? "warn"
        : "info";

  const emailHost = readText(settings, "email.smtp_host");
  const emailReady = readBool(settings, "email.enabled") && emailHost !== "";
  const emailState: OverviewState = emailReady ? "ok" : "off";

  return [
    {
      id: "storage",
      label: "Storage",
      state: storageState,
      stateText: storageReady ? "Healthy" : "Not set up",
      detail: storageReady ? bucketDetail : "Artwork and uploads have nowhere to go",
      action: tileAction(storageState, "infrastructure"),
    },
    {
      id: "database",
      label: "Database",
      state: "ok",
      stateText: "Healthy",
      detail: join([
        "Postgres",
        maxConnections ? `max ${maxConnections} connections` : null,
        redisConfigured ? "Redis" : null,
      ]),
    },
    {
      id: "transcoding",
      label: "Transcoding",
      state: transcodeState,
      stateText: restartPending ? "Restart pending" : transcodeEnabled ? "Ready" : "Off",
      detail: restartPending
        ? "Saved changes apply after a restart"
        : transcodeEnabled
          ? join([transcodeMode, renderDevice])
          : "Clients only get what they can already play",
      action: tileAction(transcodeState, "playback"),
    },
    {
      id: "search",
      label: "Search",
      state: searchState,
      stateText: searchProviderLabel(activeSearch),
      detail:
        activeSearch === "meilisearch"
          ? meiliHealthy
            ? "Meilisearch connected"
            : "Meilisearch not reachable"
          : meiliConfigured
            ? "Meilisearch configured but not active"
            : "Meilisearch not connected",
      action: tileAction(searchState, "library"),
    },
    {
      id: "email",
      label: "Email",
      state: emailState,
      stateText: emailReady ? "Ready" : "Not set up",
      detail: emailReady ? `SMTP · ${emailHost}` : "Invites and resets can't send",
      action: tileAction(emailState, "notifications"),
    },
  ];
}

// ---------------------------------------------------------------------------
// Section cards
// ---------------------------------------------------------------------------

function themeLabel(id: string | null | undefined): string | null {
  if (!id) return null;
  return THEME_IDS.includes(id as ThemeId) ? THEMES[id as ThemeId].label : id;
}

function buildCards(input: SettingsOverviewInput): OverviewCard[] {
  const { settings } = input;
  const configured = new Set(input.sensitiveConfigured ?? []);

  const cards: OverviewCard[] = [];
  const push = (
    id: SettingsOverviewTabID,
    title: string,
    summary: string,
    flags: { attention?: boolean; inactive?: boolean } = {},
  ) => {
    cards.push({
      id,
      title,
      summary: summary || UNKNOWN_VALUE,
      attention: flags.attention ?? false,
      inactive: flags.inactive ?? false,
    });
  };

  // General ---------------------------------------------------------------
  const serverName = readText(settings, "branding.server_name");
  push(
    "general",
    "General",
    join([serverName || "Silo", readBool(settings, "signup.enabled") ? "Signups on" : null]),
  );

  // Appearance ------------------------------------------------------------
  const theme = themeLabel(readText(settings, "branding.default_theme"));
  push(
    "appearance",
    "Appearance",
    join([theme ?? "Viewer's choice", readBool(settings, "overlays.enabled") ? "Overlays" : null]),
  );

  // Security & access -----------------------------------------------------
  const accessExpiry = readText(settings, "auth.access_token_expiry");
  // A missing config is "not loaded", not "disabled": defaulting it to off used
  // to paint an amber dot on the rail for every section while the page booted.
  const rateLimits = input.rateLimits;
  push(
    "security",
    "Security & Access",
    join([
      accessExpiry ? `Sign-in ${formatDurationSetting(accessExpiry)}` : null,
      rateLimits ? (rateLimits.enabled ? "Rate limiting on" : "Rate limiting off") : null,
    ]),
  );

  // Library & metadata ----------------------------------------------------
  const cacheImages = readBool(settings, "metadata.cache_images");
  // Caching to a bucket that is not there is the one library setting that
  // fails silently, so it is called out rather than left as a fact.
  const cacheBroken = cacheImages && input.storageAvailable === false;
  const searchProvider =
    input.search?.active_provider || readText(settings, "catalog.search.provider") || "postgres";
  push(
    "library",
    "Library & Metadata",
    cacheBroken
      ? "Image cache has no bucket"
      : join([
          `${searchProviderLabel(searchProvider)} search`,
          cacheImages ? "Images cached" : null,
        ]),
    { attention: cacheBroken },
  );

  // Playback --------------------------------------------------------------
  const transcodeEnabled = readBool(settings, "playback.transcode_enabled");
  push(
    "playback",
    "Playback",
    transcodeEnabled
      ? `Transcoding on · ${transcodeModeLabel(readText(settings, "playback.hw_accel"), input.hwAccel)}`
      : "Transcoding off",
  );

  // Subtitles & Metadata ---------------------------------------------------
  const providers = input.subtitleProviders ?? [];
  const connectedProviders = providers.filter(
    (provider) => provider.has_api_key || provider.has_credentials,
  );
  const needsKey = providers.filter(
    (provider) => provider.enabled && !provider.has_api_key && !provider.has_credentials,
  );
  const mdblistConfigured =
    configured.has("mdblist.api_key") || readText(settings, "mdblist.api_key") !== "";
  push(
    "providers",
    "Subtitles & Metadata",
    needsKey.length
      ? `${needsKey
          .map(
            (provider) =>
              SUBTITLE_PROVIDER_LABELS[provider.provider_name] ?? provider.provider_name,
          )
          .join(", ")} needs a key`
      : providers.length
        ? join([
            `${connectedProviders.length} of ${providers.length} connected`,
            mdblistConfigured ? "MDBList" : null,
          ])
        : mdblistConfigured
          ? "MDBList connected"
          : "Not set up",
    {
      attention: needsKey.length > 0,
      inactive: connectedProviders.length === 0 && !mdblistConfigured,
    },
  );

  // Watch sync ------------------------------------------------------------
  const traktConfigured =
    configured.has("watchsync.trakt.client_secret") ||
    readText(settings, "watchsync.trakt.client_id") !== "";
  const simklConfigured =
    configured.has("watchsync.simkl.client_secret") ||
    readText(settings, "watchsync.simkl.client_id") !== "";
  const watchSyncNames = [
    traktConfigured ? "Trakt" : null,
    simklConfigured ? "Simkl" : null,
  ].filter((name): name is string => name !== null);
  push(
    "watch-sync",
    "Watch sync",
    watchSyncNames.length ? `${watchSyncNames.join(" and ")} connected` : "Not set up",
    { inactive: watchSyncNames.length === 0 },
  );

  // AI ------------------------------------------------------------------
  const textModel = readText(settings, "ai.chat_model");
  const textKeyConfigured = configured.has("ai.api_key") || configured.has("subtitle_ai.api_key");
  const speechConfigured =
    configured.has("ai.asr_api_key") || readText(settings, "ai.asr_base_url") !== "";
  const featuresLive = AI_FEATURE_KEYS.filter((key) => readBool(settings, key)).length;
  const aiKeyMissing = textModel !== "" && !textKeyConfigured;
  push(
    "ai",
    "AI Services",
    aiKeyMissing
      ? "Model set, no API key"
      : textModel
        ? join([textModel, `${featuresLive} of ${AI_FEATURE_KEYS.length} features on`])
        : "Not set up",
    {
      attention: aiKeyMissing,
      inactive: textModel === "" && !speechConfigured && featuresLive === 0,
    },
  );

  // Notifications ---------------------------------------------------------
  const inApp = readBool(settings, "notifications.ui_enabled");
  const webPush = readBool(settings, "notifications.web_push_enabled");
  const emailChannel = readBool(settings, "notifications.email_enabled");
  const smtpReady =
    readBool(settings, "email.enabled") && readText(settings, "email.smtp_host") !== "";
  const discordChannel = readBool(settings, "notifications.discord_enabled");
  const emailBroken = emailChannel && !smtpReady;
  const channels = [
    inApp ? "In-app" : null,
    webPush ? "Web push" : null,
    emailChannel ? "Email" : null,
    discordChannel ? "Discord" : null,
  ].filter((name): name is string => name !== null);
  push(
    "notifications",
    "Notifications",
    emailBroken ? "Email channel has no SMTP" : channels.length ? join(channels) : "All off",
    { attention: emailBroken, inactive: channels.length === 0 },
  );

  // Compatibility ---------------------------------------------------------
  const jellyfinEnabled = input.jellyfin?.enabled ?? readBool(settings, "jellyfin_compat.enabled");
  const jellyfinHost = urlHost(
    input.jellyfin?.public_url ?? readText(settings, "jellyfin_compat.public_url"),
  );
  const jellyfinBroken = jellyfinEnabled && input.jellyfin?.api_state === "error";
  const absEnabled = readBool(settings, "audiobookshelf_compat.enabled");
  push(
    "compatibility",
    "Compatibility",
    jellyfinBroken
      ? "Jellyfin API not responding"
      : join([
          jellyfinEnabled ? join(["Jellyfin on", jellyfinHost || null]) : null,
          absEnabled ? "Audiobookshelf on" : null,
        ]) || "Off",
    { attention: jellyfinBroken, inactive: !jellyfinEnabled && !absEnabled },
  );

  // Storage & Database ---------------------------------------------------
  const redisConfigured = configured.has("redis.url") || readText(settings, "redis.url") !== "";
  const publicBucket = readText(settings, "s3.public_bucket");
  const privateBucket = readText(settings, "s3.private_bucket");
  push(
    "infrastructure",
    "Storage & Database",
    publicBucket
      ? join([
          privateBucket ? "Public and private buckets" : "Public bucket",
          redisConfigured ? "Redis" : null,
        ])
      : "No bucket configured",
    { attention: publicBucket === "" },
  );

  return cards;
}

function sectionStatusFor(card: OverviewCard): SectionStatus {
  if (card.attention) return "warn";
  return card.inactive ? "off" : "ok";
}

/** Derives the whole overview model from already-fetched data. */
export function buildSettingsOverview(input: SettingsOverviewInput): SettingsOverviewModel {
  const tiles = buildTiles(input);
  const cards = buildCards(input);

  const sectionStatus = Object.fromEntries(
    cards.map((card) => [card.id, sectionStatusFor(card)]),
  ) as Record<SettingsOverviewTabID, SectionStatus>;

  return { isLoading: false, tiles, cards, sectionStatus };
}

/**
 * Live settings state for the admin settings landing page. Every query it
 * reads is one an individual tab already loads, so opening the overview warms
 * the caches the tabs go on to use.
 */
export function useSettingsOverview(): SettingsOverviewModel {
  const { data: settings, isLoading: settingsLoading } = useAdminServerSettings();
  const { data: sensitive } = useAdminSensitiveStatus();
  const { data: serverStatus } = useAdminServerStatus();
  const { data: search } = useCatalogSearchStatus();
  const { data: jellyfin } = useJellyfinCompatStatus();
  const { data: rateLimits } = useRateLimitConfig();
  const { data: subtitleProviders } = useSubtitleProviders();
  const { data: serverChannels } = useServerNotificationChannels();
  const branding = useBranding();

  // Hardware detection shells out to ffmpeg on the transcode host, so it is
  // only asked for when transcoding could actually use it.
  const hwAccelMode = (settings?.["playback.hw_accel"] ?? "").trim();
  const transcodeEnabled = TRUE_VALUES.has(
    (settings?.["playback.transcode_enabled"] ?? "").trim().toLowerCase(),
  );
  const { data: hwAccel } = useHWAccelDetection(
    settings != null && transcodeEnabled && hwAccelMode !== "none",
  );

  return useMemo(() => {
    const model = buildSettingsOverview({
      settings,
      sensitiveConfigured: sensitive?.configured,
      storageAvailable: branding.storageAvailable,
      serverStatus,
      search,
      hwAccel,
      jellyfin,
      rateLimits,
      subtitleProviders: subtitleProviders?.providers,
      serverChannels,
    });
    return { ...model, isLoading: settingsLoading && settings == null };
  }, [
    branding.storageAvailable,
    hwAccel,
    jellyfin,
    rateLimits,
    search,
    sensitive?.configured,
    serverChannels,
    serverStatus,
    settings,
    settingsLoading,
    subtitleProviders?.providers,
  ]);
}

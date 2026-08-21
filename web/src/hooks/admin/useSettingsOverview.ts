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

/** Colour band a single card row's value reads in. */
export type OverviewTone = "default" | "muted" | "ok" | "warn";

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

export interface OverviewRow {
  label: string;
  value: string;
  tone?: OverviewTone;
}

export interface OverviewCard {
  id: SettingsOverviewTabID;
  title: string;
  rows: OverviewRow[];
  /** True when any row is in a warn state; drives the amber card treatment. */
  attention: boolean;
}

export type SectionStatus = "ok" | "warn" | "off";

export interface SettingsOverviewModel {
  /** True until the settings map has arrived; the page shows skeletons. */
  isLoading: boolean;
  tiles: OverviewTile[];
  cards: OverviewCard[];
  sectionStatus: Record<SettingsOverviewTabID, SectionStatus>;
  /** Number of tiles plus cards asking for attention, for the strip caption. */
  attentionCount: number;
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

function onOff(value: boolean): OverviewRow["value"] {
  return value ? "On" : "Off";
}

function toneFor(value: boolean): OverviewTone {
  return value ? "ok" : "muted";
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

const MARKER_MODE_LABELS: Record<string, string> = {
  off: "Off",
  local: "On this server",
  both: "Server, then online",
  online: "Online only",
};

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

function countList(value: string): number {
  return value
    .split(/[\s,]+/)
    .map((entry) => entry.trim())
    .filter(Boolean).length;
}

// ---------------------------------------------------------------------------
// Health tiles
// ---------------------------------------------------------------------------

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

  const maxConnections = readInt(settings, "database.max_connections");
  const redisConfigured = configured.has("redis.url") || readText(settings, "redis.url") !== "";

  const transcodeEnabled = readBool(settings, "playback.transcode_enabled");
  const restartPending = settingsRestartPending(input.serverStatus);
  const transcodeMode = transcodeModeLabel(readText(settings, "playback.hw_accel"), input.hwAccel);
  const renderDevice = input.hwAccel?.render_devices?.[0] ?? "";

  const activeSearch =
    input.search?.active_provider || readText(settings, "catalog.search.provider") || "postgres";
  const meiliConfigured = input.search?.meilisearch.configured ?? false;
  const meiliHealthy = input.search?.meilisearch.healthy ?? false;

  const emailHost = readText(settings, "email.smtp_host");
  const emailReady = readBool(settings, "email.enabled") && emailHost !== "";

  return [
    {
      id: "storage",
      label: "Storage",
      state: storageReady ? "ok" : "off",
      stateText: storageReady ? "Healthy" : "Not set up",
      detail: storageReady ? bucketDetail : "Artwork and uploads have nowhere to go",
      action: { label: storageReady ? "Review" : "Set up", tab: "infrastructure" },
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
      action: { label: "Review", tab: "infrastructure" },
    },
    {
      id: "transcoding",
      label: "Transcoding",
      state: restartPending ? "warn" : transcodeEnabled ? "ok" : "off",
      stateText: restartPending ? "Restart pending" : transcodeEnabled ? "Ready" : "Off",
      detail: restartPending
        ? "Saved changes apply after a restart"
        : transcodeEnabled
          ? join([transcodeMode, renderDevice])
          : "Clients only get what they can already play",
      action: { label: restartPending ? "Fix" : "Review", tab: "playback" },
    },
    {
      id: "search",
      label: "Search",
      state:
        activeSearch === "meilisearch"
          ? meiliHealthy
            ? "ok"
            : "warn"
          : meiliConfigured
            ? "warn"
            : "info",
      stateText: searchProviderLabel(activeSearch),
      detail:
        activeSearch === "meilisearch"
          ? meiliHealthy
            ? "Meilisearch connected"
            : "Meilisearch not reachable"
          : meiliConfigured
            ? "Meilisearch configured but not active"
            : "Meilisearch not connected",
      action: { label: "Switch engine", tab: "library" },
    },
    {
      id: "email",
      label: "Email",
      state: emailReady ? "ok" : "off",
      stateText: emailReady ? "Ready" : "Not set up",
      detail: emailReady ? `SMTP · ${emailHost}` : "Invites and resets can't send",
      action: { label: emailReady ? "Review" : "Set up", tab: "notifications" },
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

  const cards: Array<Omit<OverviewCard, "attention">> = [];

  // General ---------------------------------------------------------------
  const serverName = readText(settings, "branding.server_name");
  cards.push({
    id: "general",
    title: "General",
    rows: [
      { label: "Server name", value: serverName || "Silo", tone: serverName ? "default" : "muted" },
      {
        label: "Public signups",
        value: onOff(readBool(settings, "signup.enabled")),
        tone: readBool(settings, "signup.enabled") ? "default" : "muted",
      },
      {
        label: "Log level",
        value: sentenceCase(readText(settings, "server.log_level")) || UNKNOWN_VALUE,
      },
    ],
  });

  // Appearance ------------------------------------------------------------
  const theme = themeLabel(readText(settings, "branding.default_theme"));
  const accent = readText(settings, "branding.accent_color");
  const overlaysOn = readBool(settings, "overlays.enabled");
  cards.push({
    id: "appearance",
    title: "Appearance",
    rows: [
      {
        label: "Default theme",
        value: theme ?? "Viewer's choice",
        tone: theme ? "default" : "muted",
      },
      {
        label: "Accent",
        value: accent ? accent.toUpperCase() : "Theme default",
        tone: accent ? "default" : "muted",
      },
      { label: "Card overlays", value: onOff(overlaysOn), tone: toneFor(overlaysOn) },
    ],
  });

  // Security & access -----------------------------------------------------
  const accessExpiry = readText(settings, "auth.access_token_expiry");
  const proxyCount = countList(readText(settings, "clientip.trusted_proxies"));
  // A missing config is "not loaded", not "disabled": defaulting it to off used
  // to paint an amber dot on the rail for every section while the page booted.
  const rateLimits = input.rateLimits;
  const rateLimitBackend = rateLimits?.backend === "redis" ? "Redis" : "In memory";
  cards.push({
    id: "security",
    title: "Security & Access",
    rows: [
      {
        label: "Sign-in lifetime",
        value: accessExpiry ? formatDurationSetting(accessExpiry) : UNKNOWN_VALUE,
      },
      {
        label: "Rate limiting",
        value: rateLimits
          ? rateLimits.enabled
            ? `On · ${rateLimitBackend}`
            : "Off"
          : UNKNOWN_VALUE,
        tone: rateLimits ? (rateLimits.enabled ? "ok" : "warn") : "muted",
      },
      {
        label: "Trusted proxies",
        value: proxyCount ? `${proxyCount} range${proxyCount === 1 ? "" : "s"}` : "None",
        tone: proxyCount ? "default" : "muted",
      },
    ],
  });

  // Library & metadata ----------------------------------------------------
  const cacheImages = readBool(settings, "metadata.cache_images");
  const markerMode = readText(settings, "markers.mode") || "local";
  const searchProvider =
    input.search?.active_provider || readText(settings, "catalog.search.provider") || "postgres";
  cards.push({
    id: "library",
    title: "Library & Metadata",
    rows: [
      {
        label: "Image cache",
        value: cacheImages ? "S3" : "Off",
        // Caching to a bucket that is not there is the one library setting
        // that silently fails, so it is called out rather than shown green.
        tone: cacheImages ? (input.storageAvailable === false ? "warn" : "ok") : "muted",
      },
      {
        label: "Intro markers",
        value: MARKER_MODE_LABELS[markerMode] ?? sentenceCase(markerMode),
        tone: markerMode === "off" ? "muted" : "default",
      },
      { label: "Search engine", value: searchProviderLabel(searchProvider) },
    ],
  });

  // Playback --------------------------------------------------------------
  const transcodeEnabled = readBool(settings, "playback.transcode_enabled");
  const allow4k = readBool(settings, "allow_4k_transcode");
  const downloadsOn = readBool(settings, "download.enabled");
  const downloadMbps = readInt(settings, "download.user_bandwidth_mbps");
  cards.push({
    id: "playback",
    title: "Playback",
    rows: [
      {
        label: "Transcoding",
        value: transcodeEnabled
          ? `On · ${transcodeModeLabel(readText(settings, "playback.hw_accel"), input.hwAccel)}`
          : "Off",
        tone: toneFor(transcodeEnabled),
      },
      { label: "4K transcode", value: onOff(allow4k), tone: allow4k ? "default" : "muted" },
      {
        label: "Downloads",
        value: downloadsOn ? join(["On", downloadMbps ? `${downloadMbps} Mbps` : null]) : "Off",
        tone: toneFor(downloadsOn),
      },
    ],
  });

  // Metadata & subtitle providers -----------------------------------------
  const providers = input.subtitleProviders ?? [];
  const connectedProviders = providers.filter(
    (provider) => provider.has_api_key || provider.has_credentials,
  );
  const needsKey = providers.filter(
    (provider) => provider.enabled && !provider.has_api_key && !provider.has_credentials,
  );
  const mdblistConfigured =
    configured.has("mdblist.api_key") || readText(settings, "mdblist.api_key") !== "";
  const providerRows: OverviewRow[] = [
    {
      label: "Subtitle providers",
      value: providers.length
        ? `${connectedProviders.length} of ${providers.length} connected`
        : UNKNOWN_VALUE,
      tone: connectedProviders.length ? "ok" : "muted",
    },
    {
      label: "MDBList",
      value: mdblistConfigured ? "Connected" : "Not set up",
      tone: mdblistConfigured ? "ok" : "muted",
    },
  ];
  if (needsKey.length) {
    providerRows.push({
      label: "Needs a key",
      value: needsKey
        .map(
          (provider) => SUBTITLE_PROVIDER_LABELS[provider.provider_name] ?? provider.provider_name,
        )
        .join(", "),
      tone: "warn",
    });
  }
  cards.push({ id: "providers", title: "Metadata & subtitle providers", rows: providerRows });

  // Watch sync ------------------------------------------------------------
  const traktConfigured =
    configured.has("watchsync.trakt.client_secret") ||
    readText(settings, "watchsync.trakt.client_id") !== "";
  const simklConfigured =
    configured.has("watchsync.simkl.client_secret") ||
    readText(settings, "watchsync.simkl.client_id") !== "";
  cards.push({
    id: "watch-sync",
    title: "Watch sync",
    rows: [
      {
        label: "Trakt",
        value: traktConfigured ? "Connected" : "Not set up",
        tone: traktConfigured ? "ok" : "muted",
      },
      {
        label: "Simkl",
        value: simklConfigured ? "Connected" : "Not set up",
        tone: simklConfigured ? "ok" : "muted",
      },
    ],
  });

  // AI services -----------------------------------------------------------
  const textModel = readText(settings, "ai.chat_model");
  const textKeyConfigured = configured.has("ai.api_key") || configured.has("subtitle_ai.api_key");
  const speechConfigured =
    configured.has("ai.asr_api_key") || readText(settings, "ai.asr_base_url") !== "";
  const featuresLive = AI_FEATURE_KEYS.filter((key) => readBool(settings, key)).length;
  cards.push({
    id: "ai",
    title: "AI services",
    rows: [
      {
        label: "Text model",
        value: textModel || "Not set up",
        tone: textModel ? (textKeyConfigured ? "ok" : "warn") : "muted",
      },
      {
        label: "Speech-to-text",
        value: speechConfigured ? "Ready" : "Not set up",
        tone: speechConfigured ? "ok" : "muted",
      },
      {
        label: "Features live",
        value: `${featuresLive} of ${AI_FEATURE_KEYS.length}`,
        tone: featuresLive ? "default" : "muted",
      },
    ],
  });

  // Notifications ---------------------------------------------------------
  const inApp = readBool(settings, "notifications.ui_enabled");
  const webPush = readBool(settings, "notifications.web_push_enabled");
  const emailChannel = readBool(settings, "notifications.email_enabled");
  const smtpReady =
    readBool(settings, "email.enabled") && readText(settings, "email.smtp_host") !== "";
  const discordChannel = readBool(settings, "notifications.discord_enabled");
  const discordWebhooks = (input.serverChannels ?? []).filter(
    (channel) => channel.type === "discord" && channel.enabled,
  ).length;
  cards.push({
    id: "notifications",
    title: "Notifications",
    rows: [
      {
        label: "In-app · Web push",
        value: inApp && webPush ? "On" : inApp ? "In-app only" : webPush ? "Web push only" : "Off",
        tone: inApp || webPush ? "ok" : "muted",
      },
      {
        label: "Email",
        value: emailChannel ? (smtpReady ? "On" : "No SMTP") : "Off",
        tone: emailChannel ? (smtpReady ? "ok" : "warn") : "muted",
      },
      {
        label: "Discord",
        value: discordChannel
          ? discordWebhooks
            ? `${discordWebhooks} webhook${discordWebhooks === 1 ? "" : "s"}`
            : "On"
          : "Off",
        tone: toneFor(discordChannel),
      },
    ],
  });

  // Compatibility ---------------------------------------------------------
  const jellyfinEnabled = input.jellyfin?.enabled ?? readBool(settings, "jellyfin_compat.enabled");
  const jellyfinHost = urlHost(
    input.jellyfin?.public_url ?? readText(settings, "jellyfin_compat.public_url"),
  );
  const jellyfinBroken = jellyfinEnabled && input.jellyfin?.api_state === "error";
  const webState = input.jellyfin?.web_state;
  const absEnabled = readBool(settings, "audiobookshelf_compat.enabled");
  cards.push({
    id: "compatibility",
    title: "Compatibility",
    rows: [
      {
        label: "Jellyfin API",
        value: jellyfinEnabled ? join(["On", jellyfinHost || null]) : "Off",
        tone: jellyfinBroken ? "warn" : toneFor(jellyfinEnabled),
      },
      {
        label: "Jellyfin web UI",
        value:
          webState === "installed"
            ? "Installed"
            : webState === "update_available"
              ? "Update available"
              : webState === "installing"
                ? "Installing"
                : webState === "failed"
                  ? "Install failed"
                  : "Not installed",
        tone:
          webState === "installed"
            ? "ok"
            : webState === "failed"
              ? "warn"
              : webState === "update_available"
                ? "default"
                : "muted",
      },
      { label: "Audiobookshelf", value: onOff(absEnabled), tone: toneFor(absEnabled) },
    ],
  });

  // Infrastructure --------------------------------------------------------
  const redisConfigured = configured.has("redis.url") || readText(settings, "redis.url") !== "";
  const publicBucket = readText(settings, "s3.public_bucket");
  const privateBucket = readText(settings, "s3.private_bucket");
  const opsLogDays = readInt(settings, "opslog.retention_days");
  cards.push({
    id: "infrastructure",
    title: "Infrastructure",
    rows: [
      {
        label: "Redis",
        value: redisConfigured ? "Configured" : "Not configured",
        tone: redisConfigured ? "ok" : "muted",
      },
      {
        label: "Buckets",
        value: privateBucket ? "Public + private" : publicBucket ? "Public only" : "None",
        tone: publicBucket ? "default" : "warn",
      },
      {
        label: "Ops log",
        value: opsLogDays ? `${opsLogDays} day${opsLogDays === 1 ? "" : "s"}` : UNKNOWN_VALUE,
      },
    ],
  });

  return cards.map((card) => ({
    ...card,
    attention: card.rows.some((row) => row.tone === "warn"),
  }));
}

function sectionStatusFor(card: OverviewCard): SectionStatus {
  if (card.attention) return "warn";
  return card.rows.every((row) => row.tone === "muted") ? "off" : "ok";
}

/** Derives the whole overview model from already-fetched data. */
export function buildSettingsOverview(input: SettingsOverviewInput): SettingsOverviewModel {
  const tiles = buildTiles(input);
  const cards = buildCards(input);

  const sectionStatus = Object.fromEntries(
    cards.map((card) => [card.id, sectionStatusFor(card)]),
  ) as Record<SettingsOverviewTabID, SectionStatus>;

  const attentionCount =
    tiles.filter((tile) => tile.state === "warn" || tile.state === "off").length +
    cards.filter((card) => card.attention).length;

  return { isLoading: false, tiles, cards, sectionStatus, attentionCount };
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

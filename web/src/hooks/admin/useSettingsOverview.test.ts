import { describe, expect, it } from "vitest";

import { ADMIN_SETTINGS_NAV } from "@/lib/adminSettingsSearch";
import {
  buildSettingsOverview,
  formatDurationSetting,
  type OverviewCard,
  type OverviewTile,
  type SettingsOverviewInput,
} from "./useSettingsOverview";

function tile(input: SettingsOverviewInput, id: string): OverviewTile {
  const found = buildSettingsOverview(input).tiles.find((entry) => entry.id === id);
  if (!found) throw new Error(`no tile ${id}`);
  return found;
}

function card(input: SettingsOverviewInput, id: string): OverviewCard {
  const found = buildSettingsOverview(input).cards.find((entry) => entry.id === id);
  if (!found) throw new Error(`no card ${id}`);
  return found;
}

function summary(input: SettingsOverviewInput, id: string): string {
  return card(input, id).summary;
}

describe("formatDurationSetting", () => {
  it("reads whole days, hours, and minutes back in words", () => {
    expect(formatDurationSetting("720h")).toBe("30 days");
    expect(formatDurationSetting("24h")).toBe("1 day");
    expect(formatDurationSetting("36h")).toBe("36 hours");
    expect(formatDurationSetting("15m")).toBe("15 minutes");
  });

  it("passes anything it does not recognise through untouched", () => {
    expect(formatDurationSetting("1h30m")).toBe("1h30m");
    expect(formatDurationSetting("")).toBe("");
  });
});

describe("buildSettingsOverview health tiles", () => {
  it("degrades to placeholders rather than throwing when nothing has loaded", () => {
    const model = buildSettingsOverview({});

    expect(model.tiles).toHaveLength(5);
    expect(model.cards).toHaveLength(11);
    expect(tile({}, "storage").stateText).toBe("Not set up");
    expect(summary({}, "general")).toBe("Silo");
  });

  it("names the bucket when only public storage is configured", () => {
    const storage = tile(
      { storageAvailable: true, settings: { "s3.public_bucket": "silo-art" } },
      "storage",
    );

    expect(storage.state).toBe("ok");
    expect(storage.detail).toBe("S3 · silo-art");
    // A healthy tile is a fact, not a task: it carries no link.
    expect(storage.action).toBeUndefined();
  });

  it("summarises both buckets when private storage is configured too", () => {
    const storage = tile(
      {
        storageAvailable: true,
        settings: { "s3.public_bucket": "silo-art", "s3.private_bucket": "silo-private" },
      },
      "storage",
    );

    expect(storage.detail).toBe("S3 · public + private");
  });

  it("reports the detected accelerator on the transcoding tile", () => {
    const transcoding = tile(
      {
        settings: { "playback.transcode_enabled": "true", "playback.hw_accel": "auto" },
        hwAccel: {
          resolved: "vaapi",
          render_devices: ["/dev/dri/renderD128"],
          intel_detected: true,
          source: "local",
        },
      },
      "transcoding",
    );

    expect(transcoding.state).toBe("ok");
    expect(transcoding.stateText).toBe("Ready");
    expect(transcoding.detail).toBe("Auto · VA-API · /dev/dri/renderD128");
  });

  it("turns the transcoding tile amber while a settings restart is pending", () => {
    const transcoding = tile(
      {
        settings: { "playback.transcode_enabled": "true" },
        serverStatus: {
          started_at: "2026-01-01T00:00:00Z",
          restart_required: true,
          restart_required_reason: "server_settings",
          restart_requested: false,
        },
      },
      "transcoding",
    );

    expect(transcoding.state).toBe("warn");
    expect(transcoding.stateText).toBe("Restart pending");
    expect(transcoding.action).toEqual({ label: "Fix", tab: "playback" });
  });

  it("leaves transcoding alone for a restart another subsystem asked for", () => {
    const transcoding = tile(
      {
        settings: { "playback.transcode_enabled": "true" },
        serverStatus: {
          started_at: "2026-01-01T00:00:00Z",
          restart_required: true,
          restart_required_reason: "jellyfin_compat",
          restart_requested: false,
        },
      },
      "transcoding",
    );

    expect(transcoding.state).toBe("ok");
  });

  it("marks search as informational on Postgres and healthy on Meilisearch", () => {
    const postgres = tile({ settings: { "catalog.search.provider": "postgres" } }, "search");
    expect(postgres.state).toBe("info");
    expect(postgres.stateText).toBe("Postgres");
    expect(postgres.detail).toBe("Meilisearch not connected");

    const meili = tile(
      {
        search: {
          active_provider: "meilisearch",
          meilisearch: { configured: true, healthy: true },
        } as SettingsOverviewInput["search"],
      },
      "search",
    );
    expect(meili.state).toBe("ok");
    expect(meili.stateText).toBe("Meilisearch");
  });

  it("only calls email ready when it is on and has a host", () => {
    expect(tile({ settings: { "email.enabled": "true" } }, "email").stateText).toBe("Not set up");
    expect(
      tile(
        { settings: { "email.enabled": "true", "email.smtp_host": "smtp.example.com" } },
        "email",
      ).stateText,
    ).toBe("Ready");
  });
});

describe("buildSettingsOverview section cards", () => {
  it("emits one card per settings tab id", () => {
    expect(buildSettingsOverview({}).cards.map((entry) => entry.id)).toEqual([
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
    ]);
  });

  it("titles every card exactly like its settings rail label", () => {
    const railLabels = Object.fromEntries(ADMIN_SETTINGS_NAV.map((item) => [item.id, item.label]));

    for (const card of buildSettingsOverview({}).cards) {
      expect(card.title).toBe(railLabels[card.id]);
    }
  });

  it("summarises playback in one phrase", () => {
    const playback = card(
      {
        settings: {
          "playback.transcode_enabled": "true",
          "playback.hw_accel": "qsv",
        },
      },
      "playback",
    );

    expect(playback.summary).toBe("Transcoding on · Quick Sync");
    expect(playback.attention).toBe(false);
    expect(summary({}, "playback")).toBe("Transcoding off");
  });

  it("flags a notifications email channel with no SMTP host", () => {
    const notifications = card(
      { settings: { "notifications.email_enabled": "true", "email.enabled": "false" } },
      "notifications",
    );

    expect(notifications.summary).toBe("Email channel has no SMTP");
    expect(notifications.attention).toBe(true);
    // With nothing configured at all the section is merely dormant, not broken.
    expect(buildSettingsOverview({}).sectionStatus.notifications).toBe("off");
  });

  it("counts connected subtitle providers and calls out the ones missing a key", () => {
    const providers = card(
      {
        sensitiveConfigured: ["mdblist.api_key"],
        subtitleProviders: [
          {
            provider_name: "opensubtitles",
            enabled: true,
            has_api_key: false,
            has_credentials: true,
            updated_at: "",
          },
          {
            provider_name: "subdl",
            enabled: true,
            has_api_key: false,
            has_credentials: false,
            updated_at: "",
          },
          {
            provider_name: "subsource",
            enabled: false,
            has_api_key: false,
            has_credentials: false,
            updated_at: "",
          },
        ],
      },
      "providers",
    );

    expect(providers.summary).toBe("SubDL needs a key");
    expect(providers.attention).toBe(true);
  });

  it("reads watch sync and AI state out of the sensitive-status list", () => {
    const input: SettingsOverviewInput = {
      sensitiveConfigured: ["watchsync.trakt.client_secret", "ai.api_key"],
      settings: {
        "ai.chat_model": "gpt-4o-mini",
        "subtitle_ai.enabled": "true",
        "metadata_ai.enabled": "true",
      },
    };

    expect(card(input, "watch-sync").summary).toBe("Trakt connected");

    const ai = card(input, "ai");
    expect(ai.summary).toBe("gpt-4o-mini · 2 of 4 features on");
    expect(ai.attention).toBe(false);
  });

  it("flags a text model saved without an API key", () => {
    const ai = card({ settings: { "ai.chat_model": "gpt-4o-mini" } }, "ai");

    expect(ai.summary).toBe("Model set, no API key");
    expect(ai.attention).toBe(true);
  });

  it("describes the Jellyfin surface from the compat status", () => {
    const compatibility = card(
      {
        jellyfin: {
          enabled: true,
          api_state: "enabled",
          public_url: "https://media.example.tv",
          web_state: "installed",
        } as SettingsOverviewInput["jellyfin"],
        settings: { "audiobookshelf_compat.enabled": "false" },
      },
      "compatibility",
    );

    expect(compatibility.summary).toBe("Jellyfin on · media.example.tv");
    expect(compatibility.attention).toBe(false);
  });

  it("warns when there is no public bucket at all", () => {
    const infrastructure = card({ settings: { "opslog.retention_days": "30" } }, "infrastructure");

    expect(infrastructure.summary).toBe("No bucket configured");
    expect(infrastructure.attention).toBe(true);
  });

  it("reports rate limiting and trusted proxies on the security card", () => {
    const security = card(
      {
        settings: {
          "auth.access_token_expiry": "720h",
          "clientip.trusted_proxies": "10.0.0.0/8, 192.168.0.0/16",
        },
        rateLimits: { enabled: true, backend: "redis" } as SettingsOverviewInput["rateLimits"],
      },
      "security",
    );

    expect(security.summary).toBe("Sign-in 30 days · Rate limiting on");
    expect(security.attention).toBe(false);
  });

  it("does not read a missing rate-limit config as rate limiting being off", () => {
    const security = card({ settings: { "auth.access_token_expiry": "720h" } }, "security");

    expect(security.summary).toBe("Sign-in 30 days");
    expect(security.attention).toBe(false);
  });

  it("exposes a per-section status for the sidebar rail", () => {
    const model = buildSettingsOverview({
      settings: { "s3.public_bucket": "silo-art", "notifications.email_enabled": "true" },
    });

    expect(model.sectionStatus.notifications).toBe("warn");
    expect(model.sectionStatus.infrastructure).toBe("ok");
    expect(model.sectionStatus["watch-sync"]).toBe("off");
  });
});

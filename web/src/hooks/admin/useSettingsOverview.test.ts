import { describe, expect, it } from "vitest";

import { ADMIN_SETTINGS_NAV } from "@/lib/adminSettingsSearch";
import {
  buildSettingsOverview,
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

describe("buildSettingsOverview health tiles", () => {
  it("degrades to placeholders rather than throwing when nothing has loaded", () => {
    const model = buildSettingsOverview({});

    expect(model.tiles).toHaveLength(5);
    expect(model.cards).toHaveLength(11);
    expect(tile({}, "storage").stateText).toBe("Not set up");
    expect(card({}, "general")).toEqual({ id: "general" });
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

describe("buildSettingsOverview groups and rail status", () => {
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

  it("keeps the group manifest aligned with the overview ids", () => {
    expect(buildSettingsOverview({}).cards.map((entry) => entry.id)).toEqual(
      ADMIN_SETTINGS_NAV.map((item) => item.id),
    );
  });

  it("flags a notifications email channel with no SMTP host", () => {
    const model = buildSettingsOverview({
      settings: { "notifications.email_enabled": "true", "email.enabled": "false" },
    });

    expect(model.sectionStatus.notifications).toBe("warn");
    expect(
      buildSettingsOverview({
        settings: { "notifications.email_enabled": "true", "email.enabled": "true" },
      }).sectionStatus.notifications,
    ).toBe("warn");
    // With nothing configured at all the section is merely dormant, not broken.
    expect(buildSettingsOverview({}).sectionStatus.notifications).toBe("off");
  });

  it("flags subtitle providers that are enabled without credentials", () => {
    const model = buildSettingsOverview({
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
    });

    expect(model.sectionStatus.providers).toBe("warn");
  });

  it("reads watch sync and AI status out of the sensitive-status list", () => {
    const model = buildSettingsOverview({
      sensitiveConfigured: ["watchsync.trakt.client_secret", "ai.api_key"],
      settings: {
        "ai.chat_model": "gpt-4o-mini",
        "subtitle_ai.enabled": "true",
        "metadata_ai.enabled": "true",
      },
    });

    expect(model.sectionStatus["watch-sync"]).toBe("ok");
    expect(model.sectionStatus.ai).toBe("ok");
  });

  it("flags a text model saved without an API key", () => {
    const model = buildSettingsOverview({ settings: { "ai.chat_model": "gpt-4o-mini" } });

    expect(model.sectionStatus.ai).toBe("warn");
  });

  it("tracks compatibility health without putting it on the group card", () => {
    const healthy = buildSettingsOverview({
      jellyfin: {
        enabled: true,
        api_state: "enabled",
        public_url: "https://media.example.tv",
        web_state: "installed",
      } as SettingsOverviewInput["jellyfin"],
      settings: { "audiobookshelf_compat.enabled": "false" },
    });
    const broken = buildSettingsOverview({
      jellyfin: {
        enabled: true,
        api_state: "error",
        public_url: "https://media.example.tv",
        web_state: "installed",
      } as SettingsOverviewInput["jellyfin"],
    });

    expect(healthy.sectionStatus.compatibility).toBe("ok");
    expect(broken.sectionStatus.compatibility).toBe("warn");
  });

  it("warns when there is no public bucket at all", () => {
    const model = buildSettingsOverview({
      settings: { "opslog.retention_days": "30" },
    });

    expect(model.sectionStatus.infrastructure).toBe("warn");
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

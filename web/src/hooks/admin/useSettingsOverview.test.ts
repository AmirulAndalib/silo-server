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
    expect(model.cards).toHaveLength(12);
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
    expect(transcoding.action).toEqual({ label: "Fix", page: "playback" });
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

  it("does not report Meilisearch as broken before its status resolves", () => {
    const pending = tile({ settings: { "catalog.search.provider": "meilisearch" } }, "search");

    expect(pending.state).toBe("info");
    expect(pending.detail).toBe("Checking connection");
    expect(pending.action).toBeUndefined();
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

describe("buildSettingsOverview groups", () => {
  it("emits one card per settings page id", () => {
    expect(buildSettingsOverview({}).cards.map((entry) => entry.id)).toEqual([
      "general",
      "infrastructure",
      "appearance",
      "security",
      "library",
      "playback",
      "downloads",
      "providers",
      "watch-sync",
      "ai",
      "notifications",
      "compatibility",
    ]);
  });

  it("keeps the group manifest aligned with the overview ids", () => {
    expect(buildSettingsOverview({}).cards.map((entry) => entry.id)).toEqual(
      ADMIN_SETTINGS_NAV.map((item) => item.id),
    );
  });
});

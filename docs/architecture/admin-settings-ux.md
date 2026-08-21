# Admin settings UX

Admin settings are organized by admin intent ("I want subtitles to download
automatically"), not by subsystem, and collapse into 9 tabs: General,
Appearance, Security & Access, Library & Metadata, Playback, Integrations,
Notifications, Compatibility, and Infrastructure. Settings promoted to its own
sidebar group, with the 9 tabs listed inline; old `?tab=` ids from the
previous 20-tab layout redirect to their new tab rather than 404ing. `⌘K`
(`AdminSectionCommandDialog`) is mounted in `AdminLayout` so search works from
every admin page, not just the Dashboard.

## Three tiers, and how to pick one for a new setting

Every admin setting is one of:

- **Essential** — shown by default, no disclosure needed. Target at most ~8
  essential controls per tab above the fold. A setting is Essential only if a
  household admin on a single-node install would plausibly need it without
  being told to look for it (on/off toggles for a whole feature, the handful
  of values that make the feature usable at all).
- **Advanced** — correct but not essential; collapsed by default behind one
  `AdvancedSection` disclosure per tab (or per `FieldGroup` on a dense tab).
  Open state persists in `localStorage` and auto-expands when a search match
  or a dirty/invalid field lives inside it. Tuning knobs, alternate backends,
  and anything whose default is good enough that most admins never touch it
  belong here.
- **Hidden** — no UI at all, on any tab. The setting is still a normal
  `server_settings` row: readable and writable through the admin settings API
  and environment configuration exactly as before this reorganization. Use
  Hidden for legacy key families kept for compatibility, settings that only
  make sense with expert knowledge of the codebase, or values better derived
  automatically (e.g. from node pool capacity) than hand-set.

The tier is a UI-only decision. It must never change a key's validation,
default resolution, or API visibility — moving a setting to Hidden is
reversible by adding UI back, not by a data migration.

## Shared primitives

Reuse these instead of adding a bespoke variant per tab:

- `SettingField` / `FieldGroup` / `SaveBar` (`web/src/pages/admin-settings/`)
  and `useSettingsForm` (`web/src/hooks/`) — the one save model. Every tab
  batches edits and commits them through one `SaveBar` with Discard; provider
  credential cards are the only exception, and only because they need
  Test-before-commit, which is itself one shared card component rather than a
  bespoke one per provider.
- `AdvancedSection` — the one collapsible-disclosure primitive for the
  Advanced tier. Do not add another `<details>`, another bespoke collapsible
  component, or a per-page expand/collapse toggle.
- `SecretField` — the one "configured · Replace / Keep" credential control.
- `LimitField` — the one "Unlimited" checkbox pattern, replacing "0 = unlimited"
  hint text conventions.
- A restart badge on `SettingField` itself, sourced from
  `config.RestartRequired` (`internal/config/restart_keys.go`), not hand-copied
  into hint text or inferred by a page-local heuristic. `RestartRequired` is
  the single source of truth for which keys need a process restart to take
  effect; a new field's badge must read that function (directly, or via a
  manifest/meta endpoint built on top of it) rather than duplicating its
  judgment.

## Deferred out of this reorganization

These were identified during the review but deliberately left for later work,
not folded into this pass:

- Key renames (e.g. un-namespaced `allow_4k_transcode`,
  `enable_transcode_throttle`, `transcode_throttle_seconds` moving under
  `playback.*`).
- Introducing `server.public_url` as one canonical public URL that
  `jellyfin_compat.public_url` and friends would derive from.
- Deleting the legacy `s3.operational_*` rows that a past migration copied and
  never removed.

Each still applies via its existing key and behavior; only the UI
reorganization and tiering in this document shipped now.

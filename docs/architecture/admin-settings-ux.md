# Admin settings UX

Admin settings are organized by admin intent ("I want subtitles to download
automatically"), not by subsystem. `/admin/settings` with no `?tab=` is the
**Overview**: server health across the top and one live card per settings group.
Eleven groups hang off it, in rail order: General, Appearance, Security &
Access, Library & Metadata, Playback, Subtitles & Metadata, Watch sync, AI Services,
Notifications, Compatibility, and Storage & Database. The global admin sidebar
has one Settings destination; the Overview and its section rail own the
settings information architecture. Old `?tab=` ids from earlier layouts
(including `integrations`, now split into Subtitles & Metadata, Watch sync, and
AI Services) redirect to the section that absorbed them rather than 404ing.
`⌘K` (`AdminSectionCommandDialog`) is mounted in `AdminLayout` so search works
from every admin page, not just the Dashboard.

## Visual system

One section is on screen at a time, and each thing on screen carries one
signal. The left rail lists the sections; a section only gets a dot when
`useSettingsOverview().sectionStatus` says `warn`, and it is amber. Healthy and
not-set-up sections get nothing. The open item is marked with a 2px accent bar
and no fill; the rail collapses on mobile, where the Overview is the section
list. The Overview itself shows a health tile only for a tile in `warn` or
`off` — a server with nothing to fix reads "Everything is configured." — and
one large card per settings group. Each card explains the group's scope and
names the sections inside it. Live state stays in the health area and settings
rail instead of reducing a multi-provider group to one misleading summary.

A section opens with `SettingsPageHeader`: the title, and page actions if it
has any. No breadcrumb, no lede, no status strip. Below it, settings are rows
in hairline-ruled `FieldGroup`s, not nested cards, with the Advanced tier
inline as one disclosure row per group. A description under a field label is
the exception, not the rule: one short sentence, and only when the label alone
is ambiguous. Units live beside the control (`SettingField`'s `unit`), not in
the label. When every field in a group needs a restart, the group says so once
(`FieldGroup restartAll`) and the fields inside drop their chips. Provider
credentials are `ProviderTile`s that expand in place to Test before saving;
their border is neutral in every state and the state is a dot plus a word in
the header. Staged edits raise one floating save pill (`SaveBar`); the restart
prompt is a single `RestartBanner` rendered by the settings shell, never per
tab.

## Three tiers, and how to pick one for a new setting

Every admin setting is one of:

- **Essential** — shown by default, no disclosure needed. Target at most ~8
  essential controls per tab above the fold. A setting is Essential only if a
  household admin on a single-node install would plausibly need it without
  being told to look for it (on/off toggles for a whole feature, the handful
  of values that make the feature usable at all).
- **Advanced** — correct but not essential; collapsed by default behind one
  `AdvancedSection` disclosure per tab (or per `FieldGroup` on a dense tab).
  Open state persists in `localStorage` and auto-expands when a dirty or
  invalid field lives inside it. Tuning knobs, alternate backends,
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
  credentials are the only exception, and only because they need
  Test-before-commit, which is `ProviderTile` rather than a bespoke card per
  provider.
- `SettingsPageHeader` (`web/src/components/settings/`) — the one way a
  section names itself. Live state belongs on the Overview, not repeated as a
  strip on every tab.
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

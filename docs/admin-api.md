# Admin API

Server-administration endpoints under `/api/v1/admin`. Every route here requires
an authenticated account with the server-wide `admin` role — the same
authorization as `/api/v1/admin/sessions` — and none of them are part of the
client-facing contract that third-party apps build against.

This document is new and covers only the routes listed below. The rest of the
admin surface predates it and is currently documented by the code and by the
design documents under `docs/design/`.

## `GET /api/v1/admin/stream-telemetry/parity`

Returns the merged stream-telemetry view beside the two legacy live-session
projections an admin reads today, plus the diff between them.

It is a diagnostic: it compares and does not cut over. No existing admin read has
been repointed onto telemetry, and nothing here blocks, throttles or ends a
session. Design: [`docs/design/2026-08-17-stream-telemetry.md`](design/2026-08-17-stream-telemetry.md).

The view is served from a bounded-staleness cache with single-flight refresh, so
several admins polling this route pay at most one rebuild per TTL.

Stream telemetry runs by default, so this route reports on an unconfigured
server. An `enabled: false` body means this process was switched off with
`SILO_STREAM_TELEMETRY_ENABLED=false`, or that a bad core setting disabled it —
the startup log names the variable in that case.

### Response

Always `200 OK`. "Nothing to compare" is expressed in the body rather than as an
error status, because an empty report with a success status would read as
agreement.

| Field | Type | Meaning |
|---|---|---|
| `enabled` | bool | Stream telemetry is running in this process. |
| `reason` | string | Present when there is nothing to compare (telemetry disabled, or no view built yet). |
| `view` | object | State of the merged view the comparison was built from. |
| `sources` | array | One report per legacy projection. Empty when `enabled` is false. |

`view`:

| Field | Type | Meaning |
|---|---|---|
| `available` | bool | A merged view exists. |
| `built_at` | RFC3339 string | When it was built. Omitted if never. |
| `age_ms`, `stale` | int, bool | Age of the cached view, and whether it exceeded the TTL. |
| `build_took_ms` | int | Cost of the last rebuild. |
| `refreshes`, `failures`, `last_error` | int, int, string | Cache counters since process start. |
| `complete` | bool | No publisher was stale, degraded or truncated. |
| `incomplete_reasons` | string[] | Why `complete` is false — e.g. `missing_publisher`, `publisher_truncated`, `decode_errors`, `truncated`. |
| `missing_publishers` | string[] | Publisher ids present in the roster but with no usable snapshot. |
| `clock_skew_suspected` | bool | A publisher stamped a time in the future. A clock running *behind* is indistinguishable from a stalled publisher in one sample; compare `publishers` sequence across two reads to tell them apart. |
| `publishers` | string[] | `<publisher-id>=<state>`, where state is `fresh`, `degraded`, `stale` or `departed`. |
| `session_count`, `transfer_count` | int | Sizes of the merged view. |

Each entry in `sources`:

| Field | Type | Meaning |
|---|---|---|
| `source` | string | `playback_sessions_sync` or `node_sessions`. |
| `available` | bool | The projection could be read. |
| `error` | string | Why it could not. |
| `notes` | string[] | Caveats that apply to this comparison. |
| `report` | object | The diff, when available. |

`report`:

| Field | Type | Meaning |
|---|---|---|
| `telemetry_count`, `legacy_count`, `in_both` | int | Session counts on each side and their intersection. |
| `agrees` | bool | Same session set, and no field both sides express disagrees. Read `fields_absent` before treating this as clearance to cut over. |
| `telemetry_only`, `legacy_only` | string[] | Session ids present on one side only, capped. |
| `telemetry_only_truncated`, `legacy_only_truncated` | int | How many ids the cap dropped. |
| `mismatches` | object[] | Per-session field disagreements, capped. |
| `mismatches_truncated` | int | How many the cap dropped. |
| `fields_absent` | object | Per field, sessions both sides know where one side carries no value. A gap in a projection, not a disagreement. |

A single report samples three independently updated stores, so one-sided
differences are normal and are not on their own evidence of a defect. Repeated
agreement over time is what the legacy-retirement project is gated on.

## `/api/v1/admin/dashboard/layout`

The admin dashboard is a widget grid each admin arranges for themselves. The
arrangement is stored per **account** (`users.id`), not per household profile,
so the same admin sees the same dashboard in every browser they log in from.

The server stores the document verbatim and validates only that the body is at
most 16 KiB and that `layout` is a JSON object. Widget ids, column spans and row
heights are the admin web client's vocabulary: it already sanitizes what it
loads — dropping widgets it does not know, clamping each axis to that widget's
range, and filling in the default height for an entry saved before row heights
existed — so a second copy of that schema on the server would only be another
place to update whenever a widget is added. That also means a layout written by
a newer build degrades gracefully on an older one instead of being rejected.

Writes are last-write-wins. The layout is one admin's own blob, so a race
between two of their tabs can cost only the older arrangement; `updated_at` is
returned so a compare-and-set could be layered on later without a contract
change.

The web client keeps a copy in `localStorage` for instant paint and offline use,
adopts the server document when it arrives, and — the first time it finds no
server document but does have a local one — uploads that local layout once.

### `GET /api/v1/admin/dashboard/layout`

`200 OK`. Both fields are `null` when this admin has never saved a layout; that
is the normal first-load answer, not an error.

| Field | Type | Meaning |
|---|---|---|
| `layout` | object \| null | The stored document, exactly as it was written. |
| `updated_at` | RFC3339 string \| null | When it was last written. |

```json
{
  "layout": {
    "version": 1,
    "entries": [{ "id": "libraries", "span": 7, "rows": 4 }]
  },
  "updated_at": "2026-08-26T10:00:00Z"
}
```

### `PUT /api/v1/admin/dashboard/layout`

Body: `{"layout": {…}}`. Responds `204 No Content` on success, and
`400 bad_request` when the body is not valid JSON, when `layout` is absent or
`null`, when `layout` is not a JSON object, or when the body exceeds 16 KiB.

### `DELETE /api/v1/admin/dashboard/layout`

Resets this admin to the default arrangement. `204 No Content`, and idempotent:
deleting a layout that is not there succeeds.

## `GET /api/v1/admin/stats/timeseries`

Sampled history for the concurrent-streams and egress charts. Cached in-process
for 30s, dropped early on playback or admin activity, and bypassed with
`?refresh=1`.

| Parameter | Type | Meaning |
|---|---|---|
| `hours` | int | Window length. Default 24, clamped to 1..744 (31 days, the retention window). A non-numeric value is `400 bad_request`. |
| `refresh` | bool | Bypass the cache for this read. |

Neither series can be reconstructed after the fact — live sessions leave no
per-minute trace once they end, and node egress is a rolling average that each
health check overwrites — so a sampler (`internal/dashmetrics`) writes them as
they happen, once a minute, into `dashboard_metric_samples`. Samples older than
31 days are deleted.

Reads bucket those minutes down so a response stays under ~750 points at any
window. `resolution_seconds` reports the bucket that was used — read it rather
than assuming the sampler's minute:

| Requested window | `resolution_seconds` |
|---|---|
| ≤ 2 hours | 60 |
| ≤ 48 hours | 300 |
| ≤ 336 hours (14 days) | 1800 |
| wider | 7200 |

A bucket wider than a minute reports the **peak** minute of each column, never
an average: these charts are read to answer "how bad did it get", and a mean
would erase exactly that. Stream counts and egress are maxed independently, so
a bucket's columns may come from different minutes within it.

Each minute holds up to two kinds of row. The `shared` row is the cluster-wide
snapshot: stream counts by play method, plus the egress reported by enabled,
healthy stream nodes. Every replica tries to write it and the first one to land
wins, so the values for a minute come from whichever replica got there first —
they differ only by sub-second timing. A `proc:<node_id>` row per API process
carries the viewer egress that process served, measured from stream telemetry;
without it a deployment with no stream nodes would chart zero egress forever.
Relay traffic is excluded, so bytes a proxy node passes through the API node are
not counted twice.

Stream counts in a point therefore come from the shared row, while `egress_kbps`
sums every source for a minute before the peak minute of the bucket is taken.
Precision is mixed by design: node egress is a 30-second rolling average and
process egress is an exact byte delta.

A bucket with no sample in it is absent from `points` rather than zero — a gap
(a restart, a stopped server) and an idle bucket are different facts. Stream
telemetry being disabled means no `proc:` rows, not an error.
`oldest_sample_at` is `null` until the first sample exists, which is how a
fresh install renders "collecting data" instead of an empty chart.

```json
{
  "resolution_seconds": 300,
  "from": "2026-08-25T12:00:00Z",
  "to": "2026-08-26T12:00:00Z",
  "oldest_sample_at": "2026-08-24T09:31:00Z",
  "points": [
    {
      "t": "2026-08-26T11:55:00Z",
      "streams": 3,
      "direct": 1,
      "remux": 0,
      "transcode": 2,
      "egress_kbps": 48211
    }
  ]
}
```

## `GET /api/v1/admin/stats/playback-activity`

Bucketed playback starts split by play method, plus reliability scalars, for the
admin dashboard. Answers are cached in-process for 60s and dropped early when
the shared event bus reports playback or admin activity; `?refresh=1` drops the
cache before reading.

| Parameter | Type | Meaning |
|---|---|---|
| `hours` | int | Window length. Default 24, clamped to 1..744. A non-numeric value is `400 bad_request`. |
| `refresh` | bool | Bypass the cache for this read. |

Buckets are hourly up to a 48-hour window and daily beyond it; `bucket_seconds`
is `3600` or `86400` accordingly. A bucket's `hour` field is its start instant
at either width — it keeps that name because it is the same fact, and
`bucket_seconds` already says how wide the bucket is.

Sessions come from `playback_history_admin` (which only gains a row when a
session finalizes) unioned with the live sessions table, so the current hour is
not under-counted. A live session cannot already be in history, so nothing is
counted twice. Live sessions with no recorded start — reconstructed after a
restart — are dated by their last update instead.

`buckets` contains only buckets that saw a session; the client zero-fills the
window on the `bucket_seconds` grid so a quiet server draws empty columns rather
than a shorter chart. Everything in `reliability` is computed over the whole
requested window. `completion_rate` is
`completed_sessions / finalized_sessions`: live sessions are excluded from both
sides, because a session that is still playing has not failed to complete.

`profiles_active_24h` is a fixed rolling-24h figure that ignores `hours` — it
answers "who watched today" whatever window the chart beside it is showing. It
counts distinct (account, profile) pairs in
`user_watch_history` over a rolling 24 hours, excluding history that was
imported or synced from a watch provider (`import`, `trakt`, `simkl`,
`mdblist`), so it means "watched on this server". Marked-watched (`manual`)
rows are counted: they are on-server actions.

**Not reported:** time-to-first-frame and failed-start counts. Nothing records
a playback *start* event today, so both would have to be inferred from log
parsing. They need start-event capture in playback first, and are deliberately
absent rather than approximated.

```json
{
  "hours": 24,
  "bucket_seconds": 3600,
  "buckets": [{ "hour": "2026-08-26T10:00:00Z", "direct": 4, "remux": 1, "transcode": 2 }],
  "reliability": {
    "sessions_started": 42,
    "transcode_starts": 11,
    "finalized_sessions": 38,
    "completed_sessions": 27,
    "completion_rate": 0.7105,
    "unique_profiles": 9
  },
  "profiles_active_24h": 9
}
```

## `GET /api/v1/admin/stats/top-activity`

Most-watched titles and most-active profiles over a multi-day window. Cached
for 5 minutes — a seven-day ranking barely moves within minutes — with the same
`?refresh=1` escape hatch.

| Parameter | Type | Meaning |
|---|---|---|
| `days` | int | Window length. Default 7, clamped to 1..30. |
| `limit` | int | Rows per list. Default 10, clamped to 1..25. |
| `refresh` | bool | Bypass the cache for this read. |

Both lists read `user_watch_history` with the same source exclusions as
`profiles_active_24h` above. Episodes are rolled up to their series, so a
season binge reads as one show and a title's `media_item_id` is a series
content id for TV. Profile display names live in the per-user stores rather
than in watch history, so they are read back from that profile's most recent
`playback_history_admin` row; a profile that has only ever marked things
watched falls back to its profile id. No poster URLs are returned — the
bar-list widgets do not need them, and it keeps the query cheap.

Both lists are `[]` on a server with no history, never `null`.

```json
{
  "days": 7,
  "limit": 10,
  "titles": [
    {
      "media_item_id": "…",
      "title": "…",
      "media_type": "series",
      "plays": 18,
      "total_seconds": 54120
    }
  ],
  "profiles": [
    {
      "user_id": 3,
      "username": "quick",
      "profile_id": "p1",
      "profile_name": "Quick",
      "plays": 12,
      "total_seconds": 40100
    }
  ]
}
```

## `GET /api/v1/admin/server/status` — `health`

The status route carries an additive `health` object for the dashboard health
strip. Every field the route already returned is unchanged; only `health` is
new, and the example below is trimmed to the fields it discusses:

```json
{
  "started_at": "2026-08-26T09:00:00Z",
  "restart_required": false,
  "health": {
    "postgres": { "configured": true, "ok": true, "latency_ms": 1.42 },
    "redis": { "configured": true, "ok": true, "latency_ms": 0.31 },
    "errors_24h": 4,
    "warnings_24h": 12
  }
}
```

Each component reports `configured` first: `false` means this deployment runs
without that service — a supported single-node shape for Redis — and `ok` and
`latency_ms` are then absent, so "not present" and "present but broken" do not
look the same on the strip. Latency is the round trip of one ping, in
milliseconds with two decimals, bounded by a 2s timeout: a wedged dependency is
reported as `ok: false` rather than holding the route open.

`errors_24h` / `warnings_24h` count `operational_logs` rows at those levels over
a rolling 24 hours, cached for 30s. A server with operational logging disabled
reports zeros and logs a warning; this route never fails over a secondary
number.

Version, uptime and node health are not repeated here. The client composes them
from `GET /admin/system/build`, `started_at` above, and `GET /admin/nodes`.

## `GET /api/v1/admin/logs/app` — `level`

`level` accepts a comma-separated list, so one request can ask for several
levels at once (`?level=error,warn`). Values are trimmed, lowercased and
de-duplicated; a single value behaves exactly as before. The same parsing
applies to the log-stream WebSocket, so a stream filtered on two levels
delivers both.

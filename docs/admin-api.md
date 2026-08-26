# Admin API

Server-administration endpoints under `/api/v1/admin`. Every route here requires
an authenticated account with the server-wide `admin` role — the same
authorization as `/api/v1/admin/sessions` — and none of them are part of the
client-facing contract that third-party apps build against.

This document is new and covers only the routes listed below. The rest of the
admin surface predates it and is currently documented by the code and by the
design documents under `docs/design/`.

## `GET /api/v1/admin/nodes`

Lists every registered stream node — proxy and transcode alike — with its
configuration, last health result, and last stored hardware inventory.

Always `200 OK` with a JSON array.

| Field | Type | Meaning |
|---|---|---|
| `id`, `name`, `type`, `url` | int, string, string, string | Identity. `type` is `proxy` or `transcode`. |
| `enabled` | bool | Whether the node is eligible for selection at all. |
| `healthy` | bool | Result of the last health check. |
| `active_jobs`, `egress_kbps` | int | Last health-reported load. `egress_kbps` is a rolling average and is currently non-zero for proxy nodes only. |
| `group` | string \| null | Co-location group. A group is only eligible while every enabled member is healthy. |
| `max_jobs`, `max_bandwidth_kbps` | int \| null | Capacity caps. `null` means unlimited. |
| `last_health_check` | RFC3339 string \| null | When the node was last checked. |
| `created_at` | RFC3339 string | When the node was registered. |
| `capabilities` | object | The node's last stored capability report — the same body `GET /hw-capabilities` returns on the node. Omitted until one has been stored. |
| `capabilities_hash` | string | Identity of that report, as computed by the node. Omitted with `capabilities`. |
| `capabilities_refreshed_at` | RFC3339 string | When the report was fetched. This is the age of the *inventory*, not of the health check: an unchanged node keeps a report from hours ago. |
| `physical_gpu_keys` | string[] | Stable identities of the GPUs behind this node, derived from `capabilities` (see below). Omitted when the node reports no identifiable GPU. |

Capability reports are refreshed by the background health sweep, not by this
read: a node advertises a `capabilities_hash` in its own health response, and
only a hash that differs from the stored one triggers a refetch. A node running
a build from before capability snapshots advertises no hash and therefore
carries none of the four fields above. A failed refetch keeps the previous
report rather than clearing it — a node that cannot be reached is not evidence
that its hardware changed. The refetch itself runs outside the sweep's own wait,
one at a time per node, so a slow capability probe cannot delay the health
cadence of the other nodes; a new report can therefore land shortly after the
check that noticed the change rather than with it.

### `physical_gpu_keys`

One key per render device in the stored report, deduplicated and sorted:

- the device's `gpu_uuid` when present (NVIDIA's permanent GPU identity, which
  follows the card between slots and hosts), otherwise
- `<boot_id>|<pci_address>`, because a PCI slot only means the same hardware
  within one boot of one kernel.

A device with neither contributes no key rather than a synthetic one. Two nodes
sharing a key are backed by the same physical GPU — the case that makes
per-node capacity accounting wrong, and which no single node's report can
express.

## `POST /api/v1/admin/nodes`

Registers a node. Body: `name`, `type` (`proxy` or `transcode`), `url`, and the
optional `group`, `max_jobs`, `max_bandwidth_kbps`. A non-positive cap and an
empty group mean "unlimited" and "ungrouped".

`201 Created` with the created node in the same shape as one list entry (with
no capability fields yet — nothing has been fetched). `400 Bad Request` when a
required field is missing or `type` is not one of the two allowed values. The
node pools are reloaded afterwards.

## `PUT /api/v1/admin/nodes/{id}`

Updates a node's mutable fields. Every field is optional; an omitted field is
left unchanged. An empty-string `group` clears the group, and a non-positive
`max_jobs` or `max_bandwidth_kbps` clears that cap.

`200 OK` with the updated node, `404 Not Found` for an unknown id. The node
pools are reloaded afterwards.

Capability fields are not writable here. They are owned by the health sweep,
because only the node can say what hardware it has.

## `DELETE /api/v1/admin/nodes/{id}`

Removes a node. `204 No Content`, or `404 Not Found` for an unknown id. The
node pools are reloaded afterwards. Sessions already streaming from the node
are not torn down by this call.

## `POST /api/v1/admin/nodes/{id}/check`

Runs one health check against a node immediately and persists the result, for
an admin who does not want to wait for the next 30-second sweep.

Always `200 OK`; an unreachable node is reported as `healthy: false` rather
than as an error status. `404 Not Found` for an unknown id.

| Field | Type | Meaning |
|---|---|---|
| `healthy` | bool | The node answered its health endpoint. |
| `active_jobs`, `egress_kbps` | int | What it reported. Zero when unhealthy. |
| `capabilities_hash` | string | The hash the node advertised on this check. Omitted when the node reports none. |

This is the node's *current* hash, not the stored one. A value here that
differs from the `capabilities_hash` in the list response means the background
sweep has a refetch pending; this route does not fetch capabilities itself.

## `GET /api/v1/admin/system/hw-accel`

Reports GPU hardware and acceleration capability. With healthy transcode nodes
registered it probes each of them; with none it probes this host. The top-level
fields are the first node that answered (or the local probe), and `nodes`
carries one entry per healthy node.

`playback.hw_device` is one cluster-wide value, so the per-node inventories are
what an operator needs to see that a device path exists on every node before
pinning one.

Always `200 OK`. A node that failed its probe is reported in `nodes` with an
`error` rather than dropped, so a hardware problem is visible instead of silent.

Top-level (and each node's own report):

| Field | Type | Meaning |
|---|---|---|
| `resolved` | string | The backend that would actually be used: `nvenc`, `qsv`, `vaapi`, or `none`. An explicitly configured backend wins even when its probe failed — read `detected_backends` for why. |
| `render_devices` | string[] | Every accessible `/dev/dri/renderD*` path. |
| `render_device_details` | object[] | One entry per device (see below). |
| `intel_detected` | bool | An Intel GPU is present in the inventory. |
| `detected_backends` | object[] | One entry per backend that had candidate hardware, with the outcome of its FFmpeg verification (see below). |
| `boot_id` | string | The host's kernel boot identity (Linux only). Pairs with a device's `pci_address` to distinguish the same GPU from the same slot after a reboot. |
| `capability_hash` | string | `sha256:<hex>` over this report's hardware identity and capability fields — not over `source`, `node_url`, the probe budget, or itself. Two reports of unchanged hardware hash identically regardless of probe order. |
| `source` | string | `local` for a probe of this host. |
| `node_url` | string | Set on a node's report. |
| `transformations`, `tone_map_capabilities` | object[] | What this host can execute, as advertised to the planner. |

`render_device_details` entries:

| Field | Type | Meaning |
|---|---|---|
| `path` | string | The `/dev/dri` path. Assigned by enumeration order, so it moves when hardware is added or removed. |
| `pci_address` | string | The device's PCI slot (e.g. `0000:03:00.0`), read from sysfs. Omitted when the device has no PCI identity. |
| `gpu_uuid` | string | NVIDIA's permanent GPU identity. Reported only for NVIDIA devices on hosts with `nvidia-smi` installed; omitted otherwise. |
| `description` | string | Short human label, e.g. `NVIDIA GPU (0x2204)`. |

`detected_backends` entries:

| Field | Type | Meaning |
|---|---|---|
| `backend` | string | `nvenc`, `qsv`, or `vaapi`. |
| `verified` | bool | At least one candidate device passed a real single-frame encode, not just an FFmpeg build-flag listing. |
| `devices` | string[] | Every candidate considered for this backend. |
| `device` | string | The candidate whose probe passed. Empty for NVENC, which addresses its GPU through CUDA rather than a render node. |
| `reason` | string | Why verification failed, attributed per device when several were tried. |

Each entry in `nodes` carries `node_url` and `node_name` plus either that
node's `resolved`, `render_devices` and `render_device_details`, or an `error`
explaining why it could not be probed. The full report for one node — including
`detected_backends`, `boot_id` and `capability_hash` — is what
`GET /api/v1/admin/nodes` stores per node in `capabilities`.

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

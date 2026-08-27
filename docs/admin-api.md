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
| `last_stats` | object | The node's most recent host resource sample — `{"system": …, "gpu": […]}` in the shape below. Omitted when the node reported none. |
| `hw_accel_override`, `hw_device_override` | string | This node's own acceleration policy (see below). Omitted when the node inherits the cluster-wide settings, which is the normal case. |

### Acceleration overrides

`hw_accel_override` and `hw_device_override` override the cluster-wide
`playback.hw_accel` and `playback.hw_device` settings for one node.
`hw_accel_override` takes the same values as the cluster setting — `auto`,
`qsv`, `vaapi`, `nvenc`, `none`. Absent means inherit; there is no separate
"inherit" value to set.

They exist for a heterogeneous deployment: one CPU-only node in a QSV cluster
sets `none` for itself instead of forcing every node onto the lowest common
denominator. A homogeneous deployment should leave both unset and configure
`playback.hw_accel` once.

A node finds its own row by URL: `NODE_URL` on the node is matched against
`stream_nodes.url`, ignoring a trailing slash on either side. Set `NODE_URL`
explicitly on every node. Without it a node guesses `http://localhost:<port>`
and adopts whatever row carries that URL, which on a multi-node deployment can
be a different machine's policy.

The node overlays its row onto the cluster-wide playback settings on every
config reload, so the override is what that node probes with, advertises in
`capabilities.resolved`, and falls back to when a start request names no
backend. The API dispatches remote transcodes with the node's
`hw_accel_override` in preference to its own cluster setting, so the request
agrees with what the node would have run anyway. Dispatch reads the override
column, not `capabilities.resolved`: a node inheriting `auto` is dispatched
`auto` so it resolves against live hardware at session start rather than
against a snapshot.

**A changed override applies without a restart, but not all at once.** Dispatch
picks it up as soon as the update returns, because the pools are reloaded. The
node applies it to new transcodes on its next config reload (within 60 seconds)
and re-advertises `capabilities.resolved` at its next capability snapshot
(every 15 minutes). Two things do wait for a restart: the hardware encoder
warmup that ran at boot, which stays primed for the old backend, and sessions
already transcoding, which keep the backend they started with. Restart the node
when you want all four in agreement immediately.

### `last_stats`

Written by the same 30-second health check that writes `active_jobs`, so it is
exactly as old as `last_health_check` and never fresher. It is the current
sample only: nothing here is a time series, and operators who want history
scrape the node's own `GET /metrics` (unauthenticated, on the node's listener,
same `streamapp_node_*` gauges, with disk series labeled by role rather than by
path). A sample larger than 32 KiB is dropped rather than stored — the health
verdict is what routes streams, and no honest sample comes close to that.

`last_stats.system`:

| Field | Type | Meaning |
|---|---|---|
| `cpu_pct` | int | Aggregate busy percentage across all cores over the last sampling interval (5s), 0-100. Idle and iowait both count as not busy. Under a cgroup this is the container's own consumption against its own quota, not the host's. |
| `load1` | float | 1-minute load average. Unlike `cpu_pct` it also counts tasks blocked on storage, so a node stuck on I/O looks idle in one and busy in the other. Always host-wide: the kernel keeps no per-cgroup load average. |
| `cores` | int | CPUs this process may run on — the cgroup's CPU quota rounded up where one is set, otherwise every CPU the kernel reports. This is what `cpu_pct` is normalized against and what `load1` must be read relative to. |
| `mem_used_mb`, `mem_total_mb` | int | Memory. Under a cgroup these are the cgroup's limit and working set (page cache excluded), not the host's. |
| `disks` | object[] | Sampled mounts, transcode scratch first, deduplicated by filesystem and capped at 8 — unmeasurable paths included, so the array never grows with the library count. |
| `net_rx_bps`, `net_tx_bps` | int | Aggregate throughput in **bits** per second, loopback excluded. In a container this is the container's own network namespace. |

Each entry in `disks`:

| Field | Type | Meaning |
|---|---|---|
| `path` | string | The sampled path. |
| `used_gb`, `total_gb` | float | Capacity in GiB. Used counts filesystem-reserved blocks, matching `df`. |
| `stale` | bool | The numbers are real but carried over from an earlier pass because the current probe has not returned — the normal reading for a network mount whose server went away. Omitted when false. |
| `unavailable` | bool | The path has never been measured on this node (it does not exist here, or the first probe is still hanging). `used_gb`/`total_gb` are meaningless. Omitted when false. |

Each entry in `last_stats.gpu`:

| Field | Type | Meaning |
|---|---|---|
| `device` | string | The render node path (`/dev/dri/renderD128`), or `cuda:N` for an NVIDIA GPU with no readable DRM node. |
| `vendor` | string | `intel`, `nvidia` or `amd`. Omitted when sysfs names a vendor we do not recognize. |
| `sessions` | int | GPU workloads this node currently has pinned to the device. It comes from the playback device balancer, so it is exact for Silo's own work and blind to any other tenant's. A workload started with no `playback.hw_device` configured under QSV/VAAPI has no device name until ffmpeg picks one, and is the one case not counted here. |
| `video_busy_pct`, `render_busy_pct` | int | Engine busy percentages over the sampling interval. |
| `total_busy_pct` | int | Whole-GPU utilization *including other tenants*. Present only with an enrichment source — absent is not zero, and must not be rendered as an idle GPU. |
| `vram_used_mb`, `vram_total_mb` | int | GPU memory, on the same terms as `total_busy_pct`. |
| `source` | string | What produced the numbers: `fdinfo`, `nvidia-smi`, `fdinfo+nvidia-smi`, or `unavailable`. |

`source` is what tells an operator how far to trust the busy percentages.
`fdinfo` is the unprivileged DRM baseline and covers **only this node's own
ffmpeg children** — a GPU shared with anything outside Silo reads as less busy
than it is. `nvidia-smi` is whole-GPU. `unavailable` means nothing could measure
the device this interval; its percentages are zeros with no measurement behind
them.

A node reports these fields in its own `/health` and `/status`; the API stores
them opaquely and never routes on them. Nothing in node selection reads
`last_stats`.

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

A device with neither contributes no key rather than a synthetic one, and so
does a slot on a host that reported no `boot_id`: `boot_id` detection is
best-effort, and an unscoped slot is not an identity, since every host with an
Intel iGPU has one at `0000:00:02.0`. Two nodes sharing a key are backed by the
same physical GPU — the case that makes per-node capacity accounting wrong, and
which no single node's report can express. The keys are derived from the stored report on every read, so they are
present as soon as a report is, including immediately after an API restart.

Caveats on what a key can prove:

- A key is only stable within one boot of the host it came from. `boot_id`
  changes on reboot, so a fallback key does too, and the same card looks like a
  different GPU until every node on that host has re-reported. An NVIDIA
  `gpu_uuid` has no such limit.
- Intel and AMD GPUs passed through to separate VMs cannot be correlated at
  all: each guest reports its own `boot_id` and its own PCI topology, so two
  guests on one card produce two unrelated keys. Sharing there is invisible to
  the server, and stays a matter for how the host is partitioned.

Node selection uses the same keys as a tie-breaker: among transcode nodes that
are otherwise level on effective job count, the one whose physical GPU group —
itself plus every pooled transcode node sharing a key with it — carries the
fewest jobs wins. It never overrides the job count itself or the soft affinity
that keeps a session on its current node, and it does not apply to proxy
selection, which is round-robin and does no GPU work.

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

`hw_accel_override` and `hw_device_override` are writable here. Either `null`
or an empty string clears one, restoring inheritance of the cluster-wide
setting; an omitted field leaves it alone. Clearing an override is a real
change with a real effect, so it is deliberately expressible rather than being
indistinguishable from omission.

`200 OK` with the updated node, `404 Not Found` for an unknown id,
`400 Bad Request` when `hw_accel_override` is not one of `auto`, `qsv`,
`vaapi`, `nvenc`, `none` (matched case-insensitively and stored lowercase, as
`playback.hw_accel` is). The node pools are reloaded afterwards, so remote
dispatch honors a new override immediately; the target node itself picks it up
on its next config reload — see "Acceleration overrides" above for what waits
for a restart.

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

The check also persists the node's resource sample, so `last_stats` on the list
response reflects this check immediately. The sample itself is not echoed here.

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

## `GET /api/v1/admin/system/resources`

Reports the **API host's own** current resource sample — the counterpart to the
per-node `last_stats` above.

The API host is not a registered stream node, so without this route the one
machine an operator cannot see is the machine serving the request (and, in
integrated mode, doing the transcoding). Unlike a node, this host also samples
the configured library roots: it is the process that knows what the library is,
and its view of a media mount is the authoritative one.

Always `200 OK`. It reads a snapshot the sampler already published, so it costs
nothing and cannot hang regardless of what a mount or a GPU query is doing.

| Field | Type | Meaning |
|---|---|---|
| `available` | bool | This host can be sampled. False on a non-Linux host, before the first sample lands, or when no sampler is running — in which case the fields below are absent. |
| `sampled_at` | RFC3339 string | When the sample was taken. Omitted when there is none. |
| `system` | object | Same shape as `last_stats.system` above. |
| `gpu` | object[] | Same shape as `last_stats.gpu` above. |

Sampling is Linux-only: `available: false` on macOS or Windows is expected and
is not an error. History and alerting are Prometheus's job — the same numbers
are exposed as `streamapp_node_*` gauges on this process's existing `/metrics`
endpoint, with one deliberate difference: `/metrics` is unauthenticated, so its
disk series are labeled `mount="scratch"` / `mount="library-N"` and the library
paths themselves appear only here, behind admin auth.

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

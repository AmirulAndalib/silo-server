# HDR Tone-Map Reliability Design

**Date:** 2026-08-14

**Status:** Implemented in pull request #634

**Scope:** Native protocol-v3 playback, Jellyfin-compatible playback, prepared
downloads, transcode-node placement, and Dolby Vision probe metadata

## Problem

HDR-to-SDR tone mapping crosses several boundaries: media probing, source
classification, playback planning, transcode-node placement, executable recipe
freezing, seek/replan reconstruction, and prepared-download identity. Each
boundary must agree on both the source facts and the selected execution mode.

Three failure classes must be prevented:

1. A seek or replan must not rebuild a different FFmpeg graph because source
   profile or bit depth was omitted from the frozen recipe.
2. A hardware-capable deployment must not reject work solely because hardware
   executors are full when policy permits an idle software executor.
3. Legacy Dolby Vision metadata must not be interpreted as ordinary PQ when
   the persisted row cannot prove that an SDR-compatible base layer exists.

The reliability model applies equally to native playback, Jellyfin-compatible
playback, and prepared downloads. Direct HDR playback remains unchanged.

## Goals

- Preserve every source fact that can affect executable transcode arguments
  across recipe freeze, thaw, seek, restart, and replan.
- Prefer hardware tone mapping when eligible capacity exists, then use software
  capacity when policy permits it.
- Select the prepared-download tone-map mode before computing artifact identity
  and never mutate that mode after insertion.
- Distinguish temporary executor saturation from permanent capability or
  quality unavailability.
- Fail closed for Dolby Vision sources whose persisted base-layer provenance is
  incomplete, then restore eligibility through authoritative reprobe.
- Keep capability discovery outside scheduler locks and avoid leaking planning
  reservations.

## Non-goals

- Changing direct-play or direct-stream HDR behavior.
- Enabling hardware or software tone mapping by default.
- Making hardware and software encodes byte-identical.
- Holding a scheduler reservation while a prepared artifact waits in the job
  queue.
- Inferring missing Dolby Vision provenance in SQL without reading the media.
- Replacing the node scheduler, executable recipe format, or prepared-artifact
  model.

## Reliability invariants

The implementation maintains these invariants:

- A frozen executable recipe contains every catalog-derived fact used to build
  its FFmpeg graph.
- A sidecar-only replan may reuse existing audio/video bytes only when all
  byte-affecting recipe facts are equal.
- `hardware_only` and `software_only` never cross execution modes.
- `hardware_then_software` chooses the first policy-allowed mode with current
  eligible capacity.
- Prepared-artifact mode, parameter hash, ID, and output path are immutable
  after insertion.
- Capacity exhaustion is retryable; missing capability or disallowed quality
  is a permanent request outcome.
- Ambiguous Dolby Vision metadata cannot enter HDR-to-SDR processing.

## Frozen playback recipe fidelity

`SourceExecutionMetadataV3` and `ExecutableRecipeV3` persist source video
profile and bit depth alongside codec, decode mode, duration, tone-map source
kind, source revision, and Dolby Vision provenance. Freezing copies these facts
from the accepted media snapshot. Thawing restores them without consulting a
later catalog row.

This matters for hardware pipelines whose filter graph depends on the decoded
pixel format. A 10-bit NVENC SDR-base recipe must retain its
`hwdownload,format=p010le` stage after seek or restart instead of degrading to
an 8-bit `nv12` assumption.

Sidecar-only HLS replans compare profile and bit depth as part of executable
audio/video equality. If either changes, the server creates a new audio/video
transport instead of retaining old bytes while persisting a different frozen
recipe.

The recipe extension is backward-compatible. Older recipes omit the new JSON
fields and thaw to their existing zero values; recipe validity and versioning
are unchanged.

## Capacity-aware playback placement

Capability discovery produces a per-node, validated inventory before scheduler
selection. Scheduler predicates use only in-memory capability maps, so network
I/O never occurs while the planner lock is held.

For native protocol-v3 playback, the server first attempts placement for the
planned hardware recipe. When no hardware executor has capacity and the stored
policy permits software, it reuses the software fallback path to build and
place a software recipe. The selected mode is applied before the durable recipe
is frozen.

Jellyfin-compatible playback uses the same ordered placement rule. It attempts
hardware first and appends a software attempt only when policy and the validated
inventory both permit software for the resolved source kind.

A failed placement creates no reservation. A successful placement creates the
ordinary session reservation exactly once. Hardware-only, software-only, and
ordinary non-tone-mapped transcodes retain their existing selection behavior.

## Capacity-aware prepared downloads

Prepared downloads must choose their execution mode before artifact hashing.
The optional capacity-provider interface lets a node-aware preparer answer
whether a specific mode and source kind currently has an eligible executor.

The preparer collects per-node capabilities first, constructs an in-memory set
of eligible node URLs, then briefly reserves and releases matching transcode
capacity. The availability check is a planning snapshot, not a durable claim.
If capacity changes after queuing, the immutable recipe follows the normal job
retry lifecycle.

Target resolution considers local and remote execution separately:

- local capability counts only when local fallback is enabled;
- remote capability must match the requested mode and source kind;
- hardware is considered before software;
- only modes allowed by policy are candidates.

Once selected, the mode is included in the parameter hash before the artifact
ID and output path are created. No later worker or retry changes that identity.
Preparers that do not implement capacity reporting retain the legacy
capability-union selection path.

## Error semantics

The server distinguishes executor saturation from permanent unavailability:

- compatible, policy-allowed executors with no current capacity return
  `capacity_unavailable` with HTTP 503;
- unsupported, disabled, or disallowed tone-map quality remains
  `quality_unavailable` with HTTP 501;
- capability-probe errors take precedence over a definite saturation result
  when the inventory is incomplete.

Capacity failure occurs before artifact hashing, ID generation, output-path
construction, or queue insertion. A failed request therefore cannot create or
mutate a prepared artifact.

## Legacy Dolby Vision safety

Dolby Vision source resolution requires authoritative evidence that the
configuration was present, the base-layer compatibility ID was present and
nonzero, and a base layer exists. Profile 5 and missing provenance fail closed.
Supported compatibility IDs remain eligible when all required facts are
authoritative.

A Goose data migration clears `media_files.probe_updated_at` for array-shaped
video-track metadata that identifies Dolby Vision but lacks either new
provenance key. Existing scan and playback repair paths then reprobe the media
and persist facts derived from the bytes.

The migration treats SQL `NULL`, JSON `null`, objects, and scalar JSON values as
empty track collections before array expansion. This prevents malformed or
legacy JSONB shapes from aborting an upgrade. Its down section performs no data
reconstruction because previous probe timestamps cannot be recovered safely.

## Compatibility and rollout

- Direct HDR paths and default-disabled settings are unchanged.
- Existing protocol-v3 recipes and non-tone-mapped artifact hashes remain
  valid.
- Existing preparers without capacity reporting retain their prior behavior.
- Operators may temporarily see tone mapping unavailable for affected legacy
  Dolby Vision files until normal reprobe succeeds.
- No migration-time FFprobe process or blocking full-library scan is required.

## Verification strategy

Behavioral coverage establishes:

1. Initial and frozen/thawed 10-bit NVENC SDR-base launches build equivalent
   stable FFmpeg arguments and retain `p010le`.
2. Sidecar-only replans reject source profile or bit-depth drift.
3. Native and Jellyfin-compatible playback select idle software capacity after
   hardware saturation when policy permits it.
4. Prepared downloads select and hash the software target under the same
   capacity shape.
5. Complete executor saturation returns retryable capacity semantics without
   queuing an artifact.
6. Legacy Dolby Vision metadata fails closed, while authoritative supported
   sources remain eligible.
7. The real PostgreSQL migration invalidates only affected array-shaped rows
   and safely ignores SQL/JSON null and non-array JSONB shapes.

Focused race coverage protects playback placement, prepared-download capacity
selection, tone-map classification, transcode nodes, Jellyfin compatibility,
and scanner repair paths.

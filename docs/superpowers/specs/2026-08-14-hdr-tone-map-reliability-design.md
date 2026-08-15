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

The persisted recipe extension is backward-compatible. Older recipes omit the
new JSON fields and thaw to their existing zero values. Tone-map reconstruction
tokens deliberately use the `transcode_tonemap_v1` play-method discriminator,
however. Current readers map that discriminator back to an ordinary transcode,
while older readers reject it instead of silently reconstructing an HDR recipe
without tone mapping during a rolling deployment.

### Executor-side source verification

Catalog size, modification time, and the OpenSubtitles hash are useful change
signals but do not prove that a later executor sees the same video metadata.
Filesystem identity tokens were rejected because their representation is not
portable across Linux, macOS, Windows, NFS, and SMB mounts, and because a
pathname can still change after a token is checked.

Every tone-map execution attempt therefore runs a bounded FFprobe on the
executor immediately before representative-frame preflight or FFmpeg startup.
Scanner persistence and execution use the same `mediaprobe` normalization for
the primary video stream, including profile, bit depth, color fields, and Dolby
Vision provenance. The live stream signature must exactly match the signature
frozen in the recipe. A successful mismatch is a stale recipe; an unavailable,
malformed, cancelled, or timed-out probe is a transient validation failure.

The validation runs for initial starts, restarts, reconstructed sessions, and
prepared downloads, but never for direct play, direct stream, or ordinary
non-tone-mapped transcodes. Reconstruction preserves stale and transient
validation errors through local, compatibility, and transcode-node boundaries;
an existing frozen tone-map recipe is never replaced by a fresh plan merely
because its reconstruction failed. Existing size, modification-time, and
edge-hash checks remain defense in depth. Adversarial replacement of a pathname
after validation begins is outside the supported threat model; eliminating
that race portably would require a full-file digest and rehash or passing one
open file handle through the complete probe and FFmpeg lifecycle.

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
of eligible node URLs, then performs a non-reserving capacity check. The
availability check is a planning snapshot, not a durable claim. If capacity
changes after queuing, the immutable recipe follows the normal job retry
lifecycle.

Target resolution considers local and remote execution separately:

- local capability counts only when local fallback is enabled;
- remote capability must match the requested mode and source kind;
- hardware is considered before software;
- only modes allowed by policy are candidates.

Once selected, the mode is included in the parameter hash before the artifact
ID and output path are created. No later worker or retry changes that identity.
Preparers that do not implement capacity reporting retain the legacy
capability-union selection path.

### Remote execution attestation

Capability discovery and job execution can observe different node versions. A
node may advertise tone-map support, then be replaced by an older binary before
the queued request starts. Older JSON decoders ignore the frozen recipe fields,
so artifact ID and size alone cannot prove that an HDR source was actually
tone-mapped.

After a successful tone-map encode, the node publishes a small receipt beside
the artifact. It records the confirmed execution mode, output size, and a
canonical execution fingerprint over every transported byte-affecting request
field. The artifact ID is excluded because it is the idempotency handle rather
than an encoding input. Prepare responses and artifact status responses return
the same attestation.

Publication is crash-ordered: the completed output and artifact directory are
synced before the receipt file and directory are synced. The receipt is
therefore the commit record for the already-published bytes. The central
preparer accepts an artifact only when every attested value exactly matches its
frozen request, including during lost-response recovery and later maintenance.
Expected size and fingerprint also travel through direct file targets and
signed proxy claims, so a missing or mismatched receipt fails closed at delivery
rather than serving unverified bytes. Invalid outputs are requeued or removed
before retry or local fallback; indeterminate cleanup never masquerades as a
successful fallback. Ordinary non-tone-mapped prepared artifacts retain their
existing wire behavior.

## Compatibility runtime ownership

Jellyfin-compatible fallback may replace a hardware runtime with a software
runtime on the same session ID. Remote and local readiness, durable recipe
persistence, and rollback are fenced to the exact route and runtime generation
that still owns the session. A delayed successful remote start cannot overwrite
the winning local recipe, leak its remote runtime, or close the replacement
runtime.

An empty or mismatched node-confirmed hardware mode is handled as a failed
hardware attempt, so `hardware_then_software` continues through validated
software nodes and then validated local software. When a remote recipe falls
back to local software, the old remote runtime is stopped and its durable node
recipe is removed as part of the bounded transition; a failed central-store
update restores the previous recipe or reports the failed compensation.

## Error semantics

The server distinguishes executor saturation from permanent unavailability:

- compatible, policy-allowed executors with no current capacity return
  `capacity_unavailable` with HTTP 503;
- unsupported, disabled, or disallowed tone-map quality remains
  `quality_unavailable` with HTTP 501;
- capability-probe errors take precedence over a definite saturation result
  when the inventory is incomplete.

Internal transcode-node responses distinguish live source mismatches from
transient live-probe failures with a bounded machine-readable header. Central
V3 and Jelly handlers require an exact status-and-code pair, so unrelated 422
recipe errors and 503 node-configuration errors retain their previous meaning.
Fallback order is unchanged: other validated software executors are attempted
before the final permanent stale-source or retryable transient outcome is
returned.

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

New scanner writes always persist both provenance keys, including explicit
false values. Repair eligibility retains whether those raw keys were present,
so a legacy writer running after the one-shot migration cannot make an
incomplete row look current during a rolling upgrade. If a repair probe fails,
the scanner preserves the previous technical metadata and leaves the probe
timestamp empty for a later bounded retry.

The migration treats SQL `NULL`, JSON `null`, objects, and scalar JSON values as
empty track collections before array expansion. This prevents malformed or
legacy JSONB shapes from aborting an upgrade. Its down section performs no data
reconstruction because previous probe timestamps cannot be recovered safely.

## Compatibility and rollout

- Direct HDR paths and default-disabled settings are unchanged.
- Existing protocol-v3 recipes and non-tone-mapped artifact hashes remain
  valid. Older processes reject newly minted tone-map reconstruction tokens
  instead of executing them incompletely.
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

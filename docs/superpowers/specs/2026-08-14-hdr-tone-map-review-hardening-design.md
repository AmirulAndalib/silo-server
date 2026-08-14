# HDR Tone-Map Review Hardening Design

## Context

Pull request #634 adds opt-in HDR-to-SDR tone mapping for native playback,
Jellyfin-compatible playback, and prepared downloads. Review found three gaps:

1. A protocol-v3 seek or replan thaws a frozen recipe without the source video
   profile and bit depth, so the resumed FFmpeg graph can differ from the graph
   used at initial playback.
2. `hardware_then_software` selects hardware from the deployment capability
   union before node capacity is considered, so saturated hardware capacity can
   hide an idle software executor.
3. Catalog rows probed before Dolby Vision presence flags were persisted can
   treat an explicit base-layer compatibility ID of zero as ambiguous PQ and
   pass a preflight that cannot identify proprietary Dolby base-layer pixels.

This design closes those gaps without changing direct HDR playback or enabling
tone mapping by default.

## Goals

- Keep protocol-v3 transport arguments stable across seek and replan.
- Prefer hardware tone mapping when an eligible executor has capacity, then use
  software when policy permits it.
- Preserve prepared-artifact identity: the selected executor mode remains part
  of the artifact hash and is never mutated after the artifact is created.
- Reprobe pre-existing Dolby Vision catalog metadata once and reject ambiguous
  Dolby sources until authoritative configuration data is available.
- Cover the three reported failures with regression tests that fail before the
  production changes are applied.

## Non-goals

- Changing direct-play or direct-stream HDR behavior.
- Making hardware and software encodes byte-identical to one another.
- Replacing the node scheduler or prepared-download artifact model.
- Backfilling Dolby Vision metadata in SQL without reading the source media.

## Design

### Frozen protocol-v3 source facts

`SourceExecutionMetadataV3` and `ExecutableRecipeV3` will carry the source video
profile and bit depth alongside the already frozen codec, software-decode
decision, duration, and tone-map provenance. Recipe freezing copies the values
from the accepted media snapshot; thawing restores them; transport construction
uses the frozen values instead of returning empty placeholders.

The tone-map recipe version introduced by pull request #634 can be extended in
place because it has not shipped on the base branch. Version-1 recipes remain
valid and unchanged. Tests will compare initial and thawed NVENC SDR-base
arguments for a 10-bit source and verify that both select `p010le`.

### Capacity-aware playback placement

Capability discovery remains separate from scheduler locking: remote capability
maps are collected first, and scheduler predicates remain in-memory lookups.

For native protocol-v3 playback, placement first reserves a node supporting the
planned hardware recipe. If none has capacity and the frozen policy permits
software, placement retries with a software recipe. The selected transport mode
is applied to the planner result before the durable recipe is frozen, matching
the existing startup-failure fallback behavior.

For Jellyfin-compatible playback, node placement performs the same ordered
hardware-then-software reservation. Recipe resolution remains node-specific, so
the selected software-only node naturally freezes a software recipe before the
remote request is sent.

No retry occurs for `hardware_only` or `software_only`, and ordinary transcodes
continue using the existing unfiltered scheduler path.

### Capacity-aware prepared-download identity

Prepared-download target resolution will distinguish executor capability from
currently reservable capacity. The node-aware preparer will expose a read-only
mode-availability check implemented by briefly reserving and releasing an
eligible node after its capability map has been collected. Local capability is
included only when local fallback is enabled.

Target resolution chooses hardware when hardware is currently reservable,
otherwise software when policy permits and software is reservable. It then
computes the existing mode-specific artifact hash. The selected mode is
immutable after `EnsureQueued`; a later capacity change causes normal job retry
rather than changing the recipe or writing software output under a hardware
artifact identity.

This is intentionally a capacity snapshot, not a reservation held across the
queue. Holding a scheduler reservation until an asynchronous worker claims the
job would leak capacity across deduplicated or already-ready artifacts and
would require a separate reservation lifecycle.

### Legacy Dolby Vision repair

A Goose migration will set `media_files.probe_updated_at` to `NULL` for rows
whose `video_tracks` contain Dolby Vision indicators but lack the new Dolby
configuration-presence fields. The existing critical-probe repair path will
then reprobe those files on scan or playback and persist authoritative presence
flags derived from the media bytes.

Source resolution will no longer accept a Dolby Vision source whose base-layer
configuration or compatibility-ID presence is unknown. Such a source remains
unavailable for HDR-to-SDR transcoding until reprobe succeeds; direct HDR
playback is unaffected. This conservative failure is preferable to producing
visibly corrupted SDR output.

The migration is reversible only in the sense that a successful reprobe writes
a fresh timestamp; its down section performs no data reconstruction because the
previous probe timestamp cannot be recovered safely.

## Error handling and compatibility

- Capacity exhaustion remains retryable and does not masquerade as an executor
  capability failure.
- Software fallback is attempted only when allowed by the stored policy.
- Existing version-1 protocol-v3 recipes and non-tone-mapped artifact hashes do
  not change.
- Legacy Dolby Vision rows fail closed until their media is reprobed.
- A download artifact never changes its mode, hash, or output path after it is
  inserted.

## Testing

Tests will be added in this order:

1. A frozen-recipe round trip preserves profile and bit depth, and a thawed
   10-bit NVENC SDR-base recipe builds the same pixel-format stage as the
   initial recipe.
2. Native and Jellyfin-compatible placement choose an idle software node when
   all eligible hardware nodes are at capacity and local fallback is disabled.
3. Prepared-download target resolution selects a software-specific target and
   hash under the same capacity shape.
4. A legacy Dolby Vision track without presence fields requires critical probe
   repair and cannot resolve a tone-map source before reprobe.
5. Migration validation, focused Go tests, race tests, the web tests touched by
   pull request #634, and the repository verification commands are run from the
   repository root.

## Rollout

The migration runs before the updated server begins serving requests. Legacy
Dolby Vision files are repaired lazily through existing scan and playback probe
paths, avoiding a migration-time FFprobe dependency or a blocking full-library
scan. Operators may temporarily see tone-mapped playback unavailable for an
affected file until its reprobe completes.

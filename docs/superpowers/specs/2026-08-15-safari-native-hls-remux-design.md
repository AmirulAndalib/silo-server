# Safari Native-HLS Remux Design

## Context

Issue #648 isolates a Safari playback failure to HTTP transport rather than to the
remuxed media. Safari plays the completed MP4 when it is served with a known
`Content-Length`, `Accept-Ranges: bytes`, and correct `206 Partial Content`
responses. It rejects the same bytes when Silo streams them as an unbounded
chunked `200 OK` response.

The v3 planner currently prefers `server_remux_progressive` for container
normalization. The web client scopes its verified `dvh1` Dolby Vision and `hvc1`
HDR10 media-element evidence to that progressive delivery. The resulting plan is
internally consistent about codecs but selects a transport Safari cannot use for
MP4 media.

Silo already has a copy-codec `server_remux_hls` path that emits bounded fMP4 init
and media segments. The fix will make native-HLS capability advertisement, plan
selection, player execution, and HLS sample-entry signaling agree on that route.

## Goals

- Route native-HLS web clients with verified Dolby Vision or HDR10 capability to
  copy-codec HLS for container normalization.
- Preserve Dolby Vision Profile 5/8 signaling in HLS with a `dvh1` sample entry
  and DOVI configuration record.
- Label a Dolby Vision-to-HDR10 copy-HLS result `hvc1`, matching the capability
  shape the web client probes.
- Keep the recipe stable across local execution, remote transcode nodes,
  reconstruction, restarts, and reconnects.
- Add regression coverage for capability scoping, plan selection, native-HLS
  playback, packaging arguments, and init/media segment transport.

## Non-goals

- Do not add fake range headers to the growing progressive FFmpeg pipe.
- Do not add Safari user-agent matching to the planner.
- Do not build a completed-remux cache or seekable progressive-remux store.
- Do not change legacy progressive remux or Jellyfin compatibility behavior.
- Do not address decoder-error replan recovery, which is tracked separately.

## Chosen Approach

Use existing delivery-specific capabilities to move the verified media-element
HDR claim to the route that can honor it. This is narrower than adding a new v3
transport capability and avoids browser-name policy in the server.

The web probe will distinguish two facts:

- `hls`: some HLS engine is available, either hls.js/MSE or the media element.
- `nativeHLS`: the media element accepts the HLS MIME type.

When `nativeHLS` is true, the web client will advertise its verified `dvh1` and
`hvc1` structured HDR evidence on the HLS delivery. It will withhold that evidence
from progressive delivery. The HLS delivery will also receive the media-element
video codec evidence needed by those structured claims. The original HTTP
delivery will remain untrusted for normalized sample entries.

When `nativeHLS` is false, the existing progressive capability behavior remains
unchanged. This avoids moving working progressive routes on browsers that have no
native HLS implementation.

No planner-specific Safari branch is necessary. `PlanPlaybackV3` already builds a
progressive candidate first and validates it with `deliverySupportsPlanV3`. The
progressive candidate will fail its delivery-specific HDR validation, while the
same copy recipe rebuilt as `server_remux_hls` will pass the HLS delivery
validation.

## Player Execution

The player must execute the route whose capability it advertised. For an HLS plan
whose effective dynamic range is Dolby Vision or HDR10, it will prefer the native
media-element HLS path when `canPlayType("application/vnd.apple.mpegurl")` is
non-empty, even if hls.js reports MSE support. Other HLS plans retain the current
hls.js-first behavior.

This keeps the behavior capability-driven: the same native media-element fact
both earns the delivery claim and selects the playback engine. A browser without
native HLS cannot advertise or execute this route.

## HLS Packaging Contract

Copy-HLS execution needs an explicit sample-entry input instead of inferring it
from a browser or an input filename. Add an allowlisted `VideoSampleEntry` field
to the transcode options and the internal remote-node/reconstruction contracts.
The allowed values are empty, `dvh1`, and `hvc1`.

The v3 playback handler derives the value from the frozen plan:

- A copy-HLS plan that preserves Dolby Vision Profile 5 or 8 uses `dvh1`.
- A copy-HLS plan carrying the validated Dolby Vision RPU-strip transformation
  uses `hvc1`.
- Other plans leave the value empty.

FFmpeg argument generation applies the field only to copy-video HLS sessions:

- `dvh1` emits `-tag:v dvh1 -strict unofficial` so FFmpeg retains the DOVI
  configuration record.
- `hvc1` emits `-tag:v hvc1`.

Validation rejects unknown values and rejects a non-empty sample entry on a
video-encode session. This prevents an internal request from claiming a sample
entry the executor cannot produce.

The field is copied through `TranscodeOpts`, transcode-node start requests,
recipe cards, and signed reconstruction tokens. A restarted or remotely served
session therefore emits the same sample entry as the initially selected plan.

## Error Handling and Compatibility

- A native-HLS-negative browser receives no HLS DV/HDR claim, so the planner
  cannot select a recipe the player cannot execute.
- Invalid sample-entry values fail before FFmpeg starts and use the existing
  transport-start failure path.
- The new internal JSON fields are additive and optional. Older recipes decode
  with an empty field and preserve their current behavior.
- Legacy web/Jellyfin progressive remuxes do not set the field and retain their
  existing sample-entry behavior.

## Testing

Implementation follows red-green-refactor, with each behavior first represented
by a failing regression test.

1. Web capability tests prove native HLS receives the verified DV/HDR and HEVC
   facts while progressive does not, and prove non-native HLS keeps the existing
   progressive scoping.
2. A player test proves a Dolby Vision/HDR10 HLS plan takes the native path even
   when hls.js reports support; an SDR HLS plan keeps the current hls.js path.
3. A Go planner test models the Safari capability shape and asserts
   `server_remux_hls`, copy video, and preserved Dolby Vision instead of
   `server_remux_progressive`.
4. Transcode argument tests prove `dvh1` adds both required flags, `hvc1` adds
   only its tag, and invalid/non-copy uses are rejected.
5. Contract tests prove the sample entry survives local options, remote-node
   requests, recipe cards, signed tokens, and reconstruction.
6. A real-FFmpeg integration test verifies copy-HLS produces and serves a
   manifest, `init.mp4`, and at least one media segment. Focused argument tests
   cover the DV-specific command shape without checking in copyrighted media.
7. After deployment, the supplied real Dolby Vision fixture is used to verify
   the selected HLS plan, the init segment's `dvh1`/DOVI signaling, segment
   delivery, and first-frame playback in Safari.

Commands assume the repository root is the current working directory. Final
verification includes focused Go/web tests, formatting, linting, the repository
test suite, and `make verify-local-paths`.

## Alternatives Considered

### Add a generic byte-range transport requirement

A new protocol capability could describe clients that require range-capable MP4
delivery. It is more general, but it changes the cross-client contract and still
needs a concrete HLS execution choice. Delivery-specific native-HLS evidence
already expresses the required distinction with less protocol surface.

### Cache a completed remux and serve ranges

A completed-remux cache would allow progressive playback with correct lengths
and byte ranges. It also introduces storage quotas, cache invalidation, concurrent
producer coordination, startup delay, and partial-failure cleanup. It remains a
possible future optimization but is disproportionate to the existing working HLS
route.

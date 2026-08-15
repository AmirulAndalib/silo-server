# Jellycompat WebOS Anamorphic Condition Design

**Date:** 2026-08-15
**Status:** Approved
**Issue:** #580

## Problem

Jellyfin Web 10.11.6 sends optional `IsAnamorphic` codec-profile conditions for
H.264 and HEVC playback on webOS. Silo's codec-profile evaluator does not
include `IsAnamorphic` in its condition values. Its missing-property path also
returns `false` unconditionally instead of consulting `IsRequired`.

The missing value therefore makes the entire matching video codec profile fail,
even though Silo's jellycompat media-stream response already reports
`IsAnamorphic: false`. Otherwise compatible H.264 and HEVC sources lose direct
play and video-copy direct streaming. A compatible 2160p source can lose every
playback path when 4K transcoding is disabled.

## Goals

- Keep ordinary non-anamorphic H.264 and HEVC media eligible for direct play and
  video-copy direct streaming when it otherwise matches the webOS device
  profile.
- Match Jellyfin's behavior for condition properties Silo cannot evaluate:
  optional unknown conditions pass and required unknown conditions fail.
- Cover compatible 2160p media so the 4K-transcode policy does not turn this
  codec-condition bug into a no-playback result.
- Keep the change local to jellycompat playback negotiation.

## Non-goals

- Detecting whether media is anamorphic during scanning.
- Adding anamorphic state to catalog models or database storage.
- Changing the `MediaStream.IsAnamorphic` response contract or its existing
  `false` value.
- Expanding Silo's supported codec-profile property or operator set beyond what
  issue #580 requires.
- Changing transcoding policy, client profiles, or public API shapes.

## Decision

The codec-profile condition values will include a normalized `isanamorphic`
entry with the text value `false`. This makes playback negotiation consistent
with the media-stream DTO Silo already sends to Jellyfin clients.

When a normalized condition property is absent from the value map,
`conditionMatches` will return the inverse of `condition.IsRequired`. A missing
optional property is therefore treated as satisfied, while a missing required
property remains a hard failure. Conditions whose values are present continue
through the existing operator-specific evaluation without behavioral changes.

This is the smallest complete fix. Adding only the `IsAnamorphic` value would
repair the immediate webOS profile but leave Silo's parsed `IsRequired` field
unused. Persisting real anamorphic metadata would be more expansive than the
protocol bug and is not required to make Silo's evaluation agree with its
current response.

## Data Flow

1. Playback negotiation decodes the client-supplied `DeviceProfile`.
2. `buildConditionValues` derives condition values from the selected file
   version and adds Silo's protocol-level non-anamorphic declaration.
3. Each applicable codec profile evaluates its apply conditions and ordinary
   conditions.
4. Known values use the existing equality, set-membership, and numeric
   comparisons.
5. Unknown values use `IsRequired`: optional passes, required fails.
6. The resulting video and audio compatibility flags feed the existing direct
   play, direct stream, and transcode selection logic.

## Error and Compatibility Behavior

Unknown optional properties become permissive, matching Jellyfin 10.11.6.
Unknown required properties remain restrictive. A known property with an
unsupported condition operator still fails through the existing default case;
this design does not reinterpret malformed or unsupported comparisons.

The change does not alter stored data, response JSON, endpoint status codes, or
native `/api/v1` behavior. Rollback is a code-only revert with no data cleanup.

## Testing

Test-driven implementation will add focused regressions in
`internal/jellycompat/deviceprofile_conditions_test.go` before production code
changes:

1. A webOS-style optional `IsAnamorphic NotEquals true` condition permits a
   compatible non-anamorphic H.264 source.
2. The same condition permits a compatible non-anamorphic HEVC source.
3. A compatible 2160p source remains directly playable when 4K transcoding is
   disabled.
4. An unknown optional condition property is satisfied.
5. An unknown required condition property fails.

The focused jellycompat test package will be run after the red-green cycle,
followed by the repository's required broader verification before completion.

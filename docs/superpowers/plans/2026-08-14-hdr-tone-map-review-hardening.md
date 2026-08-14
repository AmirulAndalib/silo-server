# HDR Tone-Map Review Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make HDR-to-SDR recipes stable across seek/replan, capacity-aware across every executor pool, and safe for catalogs created before Dolby Vision provenance fields existed.

**Architecture:** Extend the existing frozen protocol-v3 recipe with the missing source facts, reuse the current ordered software-fallback path when hardware placement has no capacity, and add a capacity snapshot interface for prepared-download target selection before artifact hashing. Invalidate legacy Dolby Vision probe timestamps with a Goose data migration and fail closed until the normal probe-repair path has restored authoritative presence flags.

**Tech Stack:** Go 1.26, PostgreSQL JSONB, Goose SQL migrations, FFmpeg/FFprobe argument construction, existing `nodepool.Planner`, standard Go testing.

## Global Constraints

- Direct HDR playback and direct streaming remain unchanged.
- Hardware and software tone mapping remain disabled by default.
- `hardware_only` and `software_only` never cross modes.
- Capability network calls happen before scheduler predicates; predicates remain in-memory lookups.
- Prepared-artifact mode, hash, and output path are immutable after insertion.
- Existing version-1 executable recipes and non-tone-mapped artifact hashes remain valid.
- Commands assume the repository root is the current working directory.
- New migrations are single Goose SQL files created with `make migrate-create`; do not create paired up/down files or run `goose fix`.

---

### Task 1: Preserve source profile and bit depth in frozen protocol-v3 recipes

**Files:**
- Modify: `internal/playback/plan_v3.go:72-85`
- Modify: `internal/playback/executable_recipe_v3.go:11-169`
- Modify: `internal/playback/executable_recipe_v3_test.go`
- Modify: `internal/api/handlers/playback_v3.go:1900-1931`
- Modify: `internal/api/handlers/playback_v3_test.go`

**Interfaces:**
- Produces: `SourceExecutionMetadataV3.VideoProfile string`
- Produces: `SourceExecutionMetadataV3.VideoBitDepth int`
- Produces: `ExecutableRecipeV3.SourceVideoProfile string`
- Produces: `ExecutableRecipeV3.SourceVideoBitDepth int`
- Consumes: `playback.SourceVideoTranscodeFacts(*models.MediaFile) (codec string, profile string, bitDepth int)`

- [ ] **Step 1: Write failing frozen-recipe round-trip assertions**

Extend `TestExecutableRecipeV3RoundTrip` in `internal/playback/executable_recipe_v3_test.go` so its source snapshot contains and verifies both values:

```go
FrozenSourceMetadata: &SourceExecutionMetadataV3{
    VideoCodec: "hevc", VideoProfile: "Main 10", VideoBitDepth: 10,
    SoftwareVideoDecode: false, DurationSeconds: 7_201,
    ToneMapSourceKind: tonemap.SourceSDRBT709,
    ToneMapPreflightRequired: true, ToneMapSourceRevision: revision,
    ToneMapDVConfigPresent: true, ToneMapDVBLCompatIDPresent: true,
    ToneMapDVBLPresent: true, ToneMapDVRPUPresent: true,
},
```

Add these comparisons to the restored metadata assertion:

```go
got.FrozenSourceMetadata.VideoProfile != "Main 10" ||
    got.FrozenSourceMetadata.VideoBitDepth != 10
```

- [ ] **Step 2: Write a failing handler test for thawed source facts**

Add `TestSourceVideoTranscodeFactsV3UsesFrozenProfileAndBitDepth` to `internal/api/handlers/playback_v3_test.go`:

```go
func TestSourceVideoTranscodeFactsV3UsesFrozenProfileAndBitDepth(t *testing.T) {
    result := playback.PlannerResultV3{FrozenSourceMetadata: &playback.SourceExecutionMetadataV3{
        VideoProfile: "Main 10", VideoBitDepth: 10,
    }}
    profile, depth := sourceVideoTranscodeFactsV3(&models.MediaFile{}, result)
    if profile != "Main 10" || depth != 10 {
        t.Fatalf("frozen source facts = %q/%d, want Main 10/10", profile, depth)
    }
}
```

- [ ] **Step 3: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/playback ./internal/api/handlers -run 'TestExecutableRecipeV3RoundTrip|TestSourceVideoTranscodeFactsV3UsesFrozenProfileAndBitDepth' -count=1
```

Expected: compilation fails because the new fields do not exist, proving the regression tests exercise the missing recipe data.

- [ ] **Step 4: Add and propagate the frozen fields**

Add the fields to `SourceExecutionMetadataV3`:

```go
VideoCodec    string
VideoProfile  string
VideoBitDepth int
```

Add JSON fields to `ExecutableRecipeV3` beside `SourceVideoCodec`:

```go
SourceVideoProfile  string `json:"source_video_profile,omitempty"`
SourceVideoBitDepth int    `json:"source_video_bit_depth,omitempty"`
```

Copy them in both `FreezeExecutableRecipeV3` and `ExecutableRecipeV3.PlannerResult`. In `sourceExecutionMetadataV3`, populate them from `SourceVideoTranscodeFacts`. Replace the frozen branch of `sourceVideoTranscodeFactsV3` with:

```go
if result.FrozenSourceMetadata != nil {
    return result.FrozenSourceMetadata.VideoProfile, result.FrozenSourceMetadata.VideoBitDepth
}
```

When `freezeExecutableRecipeV3` refreshes source facts from the accepted file snapshot, copy profile and depth into the recipe alongside codec, software-decode, and duration.

- [ ] **Step 5: Run the focused tests and verify GREEN**

Run:

```bash
go test ./internal/playback ./internal/api/handlers -run 'TestExecutableRecipeV3RoundTrip|TestSourceVideoTranscodeFactsV3UsesFrozenProfileAndBitDepth|TestBuildTranscodeArgs.*NVENC' -count=1
```

Expected: PASS, including the existing NVENC argument coverage.

- [ ] **Step 6: Commit Task 1**

```bash
git add internal/playback/plan_v3.go internal/playback/executable_recipe_v3.go internal/playback/executable_recipe_v3_test.go internal/api/handlers/playback_v3.go internal/api/handlers/playback_v3_test.go
git commit -m "fix(playback): preserve frozen source video facts"
```

---

### Task 2: Fall back to software when hardware placement has no capacity

**Files:**
- Modify: `internal/api/handlers/playback_v3.go:1078-1192`
- Modify: `internal/api/handlers/playback_v3_union_test.go`
- Modify: `internal/jellycompat/handlers_playback.go:493-526`
- Modify: `internal/jellycompat/tone_map_inventory_test.go`

**Interfaces:**
- Consumes: `canRetrySoftwareToneMapV3(playback.PlannerResultV3) bool`
- Consumes: `prepareSoftwareToneMapFallbackV3(...) (preparedTransportV3, bool, *transportErrorV3)`
- Produces: ordered `hardware`, then `software` calls to `PlanSessionWith` when policy allows both

- [ ] **Step 1: Write a failing native placement test**

Add a test in `internal/api/handlers/playback_v3_union_test.go` using a real `nodepool.Planner` with two healthy transcode nodes:

```go
limit := 1
hardwareNode := &nodepool.Node{URL: hardware.URL, Enabled: true, Healthy: true, ActiveJobs: 1, MaxJobs: &limit}
softwareNode := &nodepool.Node{URL: software.URL, Enabled: true, Healthy: true}
```

Have the two `/hw-capabilities` servers advertise the required HLS transformations, with only hardware mode on the saturated node and only software mode on the idle node. Disable `playback.local_transcode_fallback`, call `prepareTransportV3` with `ToneMapPolicy: hardware_then_software` and initial `ToneMapMode: hardware`, and assert:

```go
if transportErr != nil || transport.nodeURL != software.URL || transport.toneMapMode != tonemap.ModeSoftware {
    t.Fatalf("transport = %+v error = %v, want software node fallback", transport, transportErr)
}
transport.rollback()
```

- [ ] **Step 2: Write a failing Jelly-compatible placement test**

Add `TestPlanCompatTranscodeSessionFallsBackToSoftwareCapacity` to `internal/jellycompat/tone_map_inventory_test.go`. Use the same two-node capacity shape and set both tone-map settings true with local fallback false. Call:

```go
plan, err := handler.planCompatTranscodeSession(
    context.Background(), &playback.Session{ID: "compat-capacity"}, file,
    file.Bitrate, true,
)
```

Assert that `err == nil` and `plan.TranscodeNode.URL` is the software node URL.

- [ ] **Step 3: Run the placement tests and verify RED**

Run:

```bash
go test ./internal/api/handlers ./internal/jellycompat -run 'FallsBackToSoftwareCapacity' -count=1
```

Expected: native playback returns `capacity_unavailable`, and Jelly-compatible placement returns no transcode node.

- [ ] **Step 4: Reuse native software fallback when the hardware plan is empty**

In `prepareTransportV3`, immediately after `plan := h.planNodeSessionV3(...)`, handle an empty hardware placement before the local-fallback error:

```go
if plan.TranscodeNode == nil {
    if fallback, attempted, fallbackErr := h.prepareSoftwareToneMapFallbackV3(
        r, session, file, result, timeline,
    ); attempted {
        return fallback, fallbackErr
    }
}
```

Keep the existing selected-node startup/capability fallback unchanged. `prepareSoftwareToneMapFallbackV3` already creates a result copy with software mode, reserves a software-capable node, and returns the actual mode through `preparedTransportV3`; `startPlannedPlaybackV3` applies that mode before freezing the recipe.

- [ ] **Step 5: Make Jelly-compatible placement try each permitted mode**

In `planCompatTranscodeSession`, build an ordered mode slice from the capability inventory:

```go
modes := []tonemap.Mode{available.PreferredMode(policy, kind)}
if modes[0] == tonemap.ModeHardware && policy.Allows(tonemap.ModeSoftware) && available.Supports(tonemap.ModeSoftware, kind) {
    modes = append(modes, tonemap.ModeSoftware)
}
for _, mode := range modes {
    plan := selector.PlanSessionWith(session.ID, session.TranscodeNodeURL, true, bitrateKbps, func(node *nodepool.Node) bool {
        return node != nil && nodeCapabilities[strings.TrimRight(node.URL, "/")].Supports(mode, kind)
    })
    if plan.TranscodeNode != nil {
        return plan, nil
    }
}
return nodepool.Plan{}, nil
```

Preserve the existing probe-error and unsupported-source branches before this loop.

- [ ] **Step 6: Run placement and package tests and verify GREEN**

Run:

```bash
go test ./internal/api/handlers ./internal/jellycompat -run 'FallsBackToSoftwareCapacity|ToneMap|TranscodeSession' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add internal/api/handlers/playback_v3.go internal/api/handlers/playback_v3_union_test.go internal/jellycompat/handlers_playback.go internal/jellycompat/tone_map_inventory_test.go
git commit -m "fix(playback): use software capacity after hardware saturation"
```

---

### Task 3: Select a capacity-aware prepared-download mode before hashing

**Files:**
- Modify: `internal/downloads/remote_preparer.go:50-60,184-238`
- Modify: `internal/downloads/remote_preparer_test.go`
- Modify: `internal/downloads/artifacts.go:106-110,334-418`
- Modify: `internal/downloads/artifact_test.go`

**Interfaces:**
- Produces: `toneMapCapacityProvider.ToneMapModeAvailable(context.Context, tonemap.Mode, tonemap.SourceKind) (bool, error)`
- Produces: `(*NodeAwarePreparer).ToneMapModeAvailable(context.Context, tonemap.Mode, tonemap.SourceKind) (bool, error)`
- Consumes: `eligibleTranscodeWorkPlanner.ReserveTranscodeWorkWith`

- [ ] **Step 1: Write a failing node-aware capacity test**

Add `TestNodeAwarePreparerReportsSoftwareCapacityWhenHardwareIsFull` to `internal/downloads/remote_preparer_test.go`. Configure a full hardware-only node and an idle software-only node, prime `preparer.capabilities` with unexpired per-node inventories, then assert:

```go
hardwareAvailable, err := preparer.ToneMapModeAvailable(context.Background(), tonemap.ModeHardware, tonemap.SourcePQ)
if err != nil || hardwareAvailable {
    t.Fatalf("hardware availability = %t, %v; want false", hardwareAvailable, err)
}
softwareAvailable, err := preparer.ToneMapModeAvailable(context.Background(), tonemap.ModeSoftware, tonemap.SourcePQ)
if err != nil || !softwareAvailable {
    t.Fatalf("software availability = %t, %v; want true", softwareAvailable, err)
}
```

- [ ] **Step 2: Write a failing target/hash test**

Add a fake pooled preparer in `internal/downloads/artifact_test.go`:

```go
type capacityAwareToneMapPreparer struct {
    capabilities tonemap.Capabilities
    available    map[tonemap.Mode]bool
}

func (p capacityAwareToneMapPreparer) PrepareFile(context.Context, string, playback.TranscodeOpts, string) (PreparedArtifact, error) {
    return PreparedArtifact{}, nil
}
func (p capacityAwareToneMapPreparer) ToneMapCapabilities(context.Context) (tonemap.Capabilities, error) {
    return p.capabilities, nil
}
func (p capacityAwareToneMapPreparer) LocalFallbackAllowed(context.Context) bool { return false }
func (p capacityAwareToneMapPreparer) ToneMapModeAvailable(_ context.Context, mode tonemap.Mode, _ tonemap.SourceKind) (bool, error) {
    return p.available[mode], nil
}
```

Add `TestResolveToneMapTargetHashesSoftwareWhenHardwareHasNoCapacity`, enable both policies, expose both capabilities, report only software capacity, and assert that the returned target uses software. Compare its `paramsHashWithToneMapRevision` value against hardware and software hashes, requiring equality only with the software hash.

- [ ] **Step 3: Run the download tests and verify RED**

Run:

```bash
go test ./internal/downloads -run 'ReportsSoftwareCapacity|HashesSoftwareWhenHardwareHasNoCapacity' -count=1
```

Expected: compilation fails because `ToneMapModeAvailable` is not implemented or consumed.

- [ ] **Step 4: Implement lock-safe capacity snapshots**

Add the optional interface in `internal/downloads/artifacts.go`:

```go
type toneMapCapacityProvider interface {
    ToneMapModeAvailable(context.Context, tonemap.Mode, tonemap.SourceKind) (bool, error)
}
```

Implement it on `NodeAwarePreparer` by collecting `toneMapCapabilitiesByNode` before scheduler locking, constructing a normalized capable-URL set, then briefly reserving and releasing an eligible node:

```go
func (p *NodeAwarePreparer) ToneMapModeAvailable(ctx context.Context, mode tonemap.Mode, kind tonemap.SourceKind) (bool, error) {
    selector, ok := p.planner.(eligibleTranscodeWorkPlanner)
    if !ok {
        return false, nil
    }
    byNode, probeErr := p.toneMapCapabilitiesByNode(ctx)
    capable := make(map[string]struct{})
    for nodeURL, capabilities := range byNode {
        if capabilities.Supports(mode, kind) {
            capable[strings.TrimRight(nodeURL, "/")] = struct{}{}
        }
    }
    node, release := selector.ReserveTranscodeWorkWith("download-tone-map-plan", func(candidate *nodepool.Node) bool {
        if candidate == nil {
            return false
        }
        _, supported := capable[strings.TrimRight(candidate.URL, "/")]
        return supported
    })
    if release != nil {
        release()
    }
    if node != nil {
        return true, nil
    }
    return false, probeErr
}
```

The planner creates a unique reservation ID internally, so the fixed planning work ID does not collide; release remains mandatory.

- [ ] **Step 5: Filter pooled capabilities by capacity before selecting the target mode**

Keep local and remote capability results separate in `resolveToneMapTarget`. When the preparer implements `toneMapCapacityProvider`, evaluate permitted modes in hardware/software order. A mode is available when either enabled local fallback supports it or the pooled capacity provider returns true:

```go
for _, candidate := range []tonemap.Mode{tonemap.ModeHardware, tonemap.ModeSoftware} {
    if !policy.Allows(candidate) {
        continue
    }
    remoteAvailable, capacityErr := capacityProvider.ToneMapModeAvailable(probeCtx, candidate, kind)
    capabilityErr = errors.Join(capabilityErr, capacityErr)
    if localCapabilities.Supports(candidate, kind) || remoteAvailable {
        mode = candidate
        break
    }
}
```

For preparers without the optional capacity interface, retain `capabilities.PreferredMode(policy, kind)` so existing test doubles and integrated-only deployments remain compatible. Do not mutate `target.ToneMapMode` after `Ensure` computes the hash.

- [ ] **Step 6: Run download tests and verify GREEN**

Run:

```bash
go test ./internal/downloads ./internal/downloadprepare -run 'ToneMap|Capacity|ParamsHash' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add internal/downloads/remote_preparer.go internal/downloads/remote_preparer_test.go internal/downloads/artifacts.go internal/downloads/artifact_test.go
git commit -m "fix(downloads): choose tone-map mode from available capacity"
```

---

### Task 4: Reprobe legacy Dolby Vision rows and fail closed until authoritative

**Files:**
- Modify: `internal/tonemap/tonemap.go:213-252`
- Modify: `internal/tonemap/tonemap_test.go`
- Create: `internal/database/dolby_vision_probe_migration_test.go`
- Create with `make migrate-create`: one `migrations/sql/*_invalidate_legacy_dolby_vision_probes.sql` file

**Interfaces:**
- Consumes: `tonemap.SourceMetadata.DVConfigPresent`
- Consumes: `tonemap.SourceMetadata.DVBLCompatIDPresent`
- Produces: fail-closed `ResolveSource` result for Dolby Vision metadata lacking either presence fact
- Produces: one-time `probe_updated_at = NULL` invalidation for affected JSONB rows

- [ ] **Step 1: Write a failing resolver test**

Change the `legacy row lacks presence flags` case in `TestResolveSourceRejectsDolbyOnlyAndPreflightsAmbiguousMetadata` to expect an empty kind and no preflight:

```go
{
    name: "legacy row lacks presence flags",
    mutate: func(source *SourceMetadata) {
        source.DVConfigPresent = false
        source.DVBLCompatIDPresent = false
    },
    want: "",
},
```

Add separate cases for only the config-presence flag missing and only the compatibility-ID-presence flag missing; both must return an empty resolution.

- [ ] **Step 2: Write a failing migration contract test**

Create `internal/database/dolby_vision_probe_migration_test.go`:

```go
package database

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestInvalidateLegacyDolbyVisionProbeMigration(t *testing.T) {
    matches, err := filepath.Glob("../../migrations/sql/*_invalidate_legacy_dolby_vision_probes.sql")
    if err != nil || len(matches) != 1 {
        t.Fatalf("migration matches = %v, %v; want exactly one", matches, err)
    }
    body, err := os.ReadFile(matches[0])
    if err != nil {
        t.Fatal(err)
    }
    normalized := strings.Join(strings.Fields(string(body)), " ")
    for _, fragment := range []string{
        "UPDATE public.media_files AS mf SET probe_updated_at = NULL",
        "jsonb_array_elements(COALESCE(mf.video_tracks, '[]'::jsonb)) AS track",
        "track ? 'dv_profile'",
        "NOT (track ? 'dv_config_present')",
        "NOT (track ? 'dv_bl_compat_id_present')",
        "-- +goose Down SELECT 1;",
    } {
        if !strings.Contains(normalized, fragment) {
            t.Fatalf("migration missing %q:\n%s", fragment, body)
        }
    }
}
```

- [ ] **Step 3: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/tonemap ./internal/database -run 'RejectsDolbyOnlyAndPreflightsAmbiguousMetadata|InvalidateLegacyDolbyVisionProbeMigration' -count=1
```

Expected: the resolver test fails because legacy missing-presence metadata is currently accepted as preflight PQ, and the migration test fails because no matching migration exists.

- [ ] **Step 4: Reject missing Dolby Vision provenance**

In the Dolby Vision branch of `ResolveSource`, require both presence flags before compatibility mapping:

```go
if source.DVProfile == 5 || !source.DVConfigPresent || !source.DVBLCompatIDPresent || source.DVBLCompatID == 0 {
    return SourceResolution{}
}
if !source.DVBLPresent {
    return SourceResolution{}
}
```

Keep the standard-profile compatibility check and all non-Dolby dynamic-range handling unchanged.

- [ ] **Step 5: Create the Goose migration and replace its generated body**

Run:

```bash
make migrate-create NAME=invalidate_legacy_dolby_vision_probes
```

In the single generated migration file, use:

```sql
-- +goose Up
UPDATE public.media_files AS mf
SET probe_updated_at = NULL
WHERE mf.probe_updated_at IS NOT NULL
  AND EXISTS (
      SELECT 1
      FROM jsonb_array_elements(COALESCE(mf.video_tracks, '[]'::jsonb)) AS track
      WHERE (
          track ? 'dv_profile'
          OR lower(COALESCE(track ->> 'video_range_type', '')) LIKE '%dovi%'
          OR lower(COALESCE(track ->> 'dolby_vision', '')) LIKE '%dolby%'
      )
        AND (
            NOT (track ? 'dv_config_present')
            OR NOT (track ? 'dv_bl_compat_id_present')
        )
  );

-- +goose Down
SELECT 1;
```

The down migration intentionally does not invent the previous timestamps.

- [ ] **Step 6: Validate migration and focused tests**

Run:

```bash
make migrate-validate
go test ./internal/tonemap ./internal/database -run 'RejectsDolbyOnlyAndPreflightsAmbiguousMetadata|InvalidateLegacyDolbyVisionProbeMigration' -count=1
```

Expected: migration validation and tests PASS.

- [ ] **Step 7: Commit Task 4**

```bash
git add internal/tonemap/tonemap.go internal/tonemap/tonemap_test.go internal/database/dolby_vision_probe_migration_test.go migrations/sql/*_invalidate_legacy_dolby_vision_probes.sql
git commit -m "fix(playback): reprobe legacy Dolby Vision metadata"
```

---

### Task 5: Verify the complete branch and obtain adversarial review

**Files:**
- Review only: all files changed since commit `311a0877`

**Interfaces:**
- Consumes: all regression tests and repository verification targets from Tasks 1-4
- Produces: a reviewed, verified branch ready to push

- [ ] **Step 1: Run formatting on modified Go files**

```bash
gofmt -w internal/playback/plan_v3.go internal/playback/executable_recipe_v3.go internal/playback/executable_recipe_v3_test.go internal/api/handlers/playback_v3.go internal/api/handlers/playback_v3_test.go internal/api/handlers/playback_v3_union_test.go internal/jellycompat/handlers_playback.go internal/jellycompat/tone_map_inventory_test.go internal/downloads/remote_preparer.go internal/downloads/remote_preparer_test.go internal/downloads/artifacts.go internal/downloads/artifact_test.go internal/tonemap/tonemap.go internal/tonemap/tonemap_test.go internal/database/dolby_vision_probe_migration_test.go
```

- [ ] **Step 2: Run the required verification suite**

```bash
make lint
make test
cd web && pnpm run lint && pnpm run format:check
cd ..
make verify-local-paths
make migrate-validate
make verify-playback-fixtures
make verify-settings-bindings
git diff --check 311a0877..HEAD
```

Record full-tree baseline lint findings separately from changed-line findings. Do not add any failure to `WEBTEST_KNOWN_FAILURES`.

- [ ] **Step 3: Run focused race tests**

```bash
go test -race ./internal/tonemap ./internal/playback ./internal/transcodenode ./internal/downloads ./internal/downloadprepare ./internal/jellycompat ./internal/scanner
```

Expected: PASS.

- [ ] **Step 4: Request independent code review**

Dispatch a reviewer with base `311a0877` and current `HEAD`. Require validation of:

- frozen profile/bit-depth fidelity;
- hardware saturation to software placement in native, Jelly-compatible, and download paths;
- immutable prepared-artifact identity;
- legacy Dolby Vision migration targeting and fail-closed behavior;
- migration safety, races, and backward compatibility.

Address every Critical and Important finding with a fresh red-green test cycle before continuing.

- [ ] **Step 5: Commit any review corrections**

If review corrections changed files, run the affected focused tests and commit only those corrections:

```bash
git add --update
git commit -m "fix(playback): address tone-map hardening review"
```

If review produces no code changes, do not create an empty commit.

- [ ] **Step 6: Re-run final verification and push**

Run the full commands from Steps 2 and 3 again on the exact tree being pushed. Then push without force:

```bash
git push origin feat/add-hdr-sdr-tonemapping
```

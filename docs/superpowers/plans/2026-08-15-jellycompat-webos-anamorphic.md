# Jellycompat WebOS Anamorphic Condition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore direct play and video-copy direct streaming for compatible webOS H.264 and HEVC media by evaluating Silo's non-anamorphic declaration and honoring optional unknown codec-profile conditions.

**Architecture:** Keep the correction inside the existing jellycompat codec-profile evaluator. The evaluator will use `IsRequired` when a property is absent and will expose the same `IsAnamorphic: false` value that Silo already reports in its media-stream DTO; all downstream playback-path selection remains unchanged.

**Tech Stack:** Go, table-driven unit tests, jellycompat playback-source negotiation.

## Global Constraints

- Keep the change local to jellycompat playback negotiation.
- Optional unknown conditions pass and required unknown conditions fail.
- Preserve Silo's existing `MediaStream.IsAnamorphic: false` response behavior.
- Do not add scanner, catalog-model, database, migration, client, or public API changes.
- Do not expand the supported codec-profile property or operator set beyond issue #580.
- Prove each production correction RED before writing its implementation.
- Commands assume the repository root is the current working directory.

---

## File Structure

- `internal/jellycompat/deviceprofile_conditions.go` owns condition-value construction and condition evaluation; both production corrections belong here.
- `internal/jellycompat/deviceprofile_conditions_test.go` owns codec-profile negotiation regressions and direct evaluator tests.
- `docs/superpowers/specs/2026-08-15-jellycompat-webos-anamorphic-design.md` is the approved behavioral contract and is not edited during implementation.
- No new runtime files, packages, or test-only production hooks are needed.

### Task 1: Honor `IsRequired` for Unknown Condition Properties

**Files:**
- Modify: `internal/jellycompat/deviceprofile_conditions.go:149`
- Test: `internal/jellycompat/deviceprofile_conditions_test.go`

**Interfaces:**
- Consumes: `ProfileCondition.IsRequired` and normalized property lookup in `conditionValues`.
- Produces: `conditionMatches(ProfileCondition, conditionValues) bool`, where an absent optional property returns `true` and an absent required property returns `false`.
- Preserves: operator-specific evaluation for every property present in `conditionValues`.

- [ ] **Step 1: Add the failing webOS negotiation and unknown-property tests**

Append these tests to `internal/jellycompat/deviceprofile_conditions_test.go`. The webOS fixtures copy the relevant H.264 and HEVC condition shapes from Jellyfin Web 10.11.6, including the optional `IsAnamorphic NotEquals true` condition.

```go
func TestBuildPlaybackSourceCodecProfiles_WebOSAnamorphicCondition(t *testing.T) {
	tests := []struct {
		name               string
		version            catalog.FileVersion
		directProfile      DirectPlayProfile
		codecProfile       CodecProfile
		allow4KTranscode   bool
		wantTranscoding    bool
	}{
		{
			name: "non-anamorphic h264 remains directly playable",
			version: catalog.FileVersion{
				FileID:      1,
				Resolution:  "1080p",
				Container:   "mp4",
				CodecVideo:  "h264",
				CodecAudio:  "aac",
				VideoTracks: []models.VideoTrack{{Codec: "h264", Profile: "High", Level: 42, Width: 1920, Height: 1080, VideoRangeType: "SDR"}},
				AudioTracks: []models.AudioTrack{{Codec: "aac", Channels: 2, Default: true}},
			},
			directProfile: DirectPlayProfile{Type: "Video", Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"},
			codecProfile: CodecProfile{
				Type:  "Video",
				Codec: "h264",
				Conditions: []ProfileCondition{
					{Condition: "NotEquals", Property: "IsAnamorphic", Value: "true", IsRequired: false},
					{Condition: "EqualsAny", Property: "VideoProfile", Value: "high|main|baseline|constrained baseline", IsRequired: false},
					{Condition: "EqualsAny", Property: "VideoRangeType", Value: "SDR", IsRequired: false},
					{Condition: "LessThanEqual", Property: "VideoLevel", Value: "51", IsRequired: false},
				},
			},
			allow4KTranscode: true,
			wantTranscoding:  true,
		},
		{
			name: "non-anamorphic 4k hevc mkv remains playable when 4k transcode is disabled",
			version: catalog.FileVersion{
				FileID:      2,
				Resolution:  "2160p",
				Container:   "mkv",
				CodecVideo:  "hevc",
				CodecAudio:  "aac",
				VideoTracks: []models.VideoTrack{{Codec: "hevc", Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, VideoRangeType: "SDR"}},
				AudioTracks: []models.AudioTrack{{Codec: "aac", Channels: 2, Default: true}},
			},
			directProfile: DirectPlayProfile{Type: "Video", Container: "mkv", VideoCodec: "hevc", AudioCodec: "aac"},
			codecProfile: CodecProfile{
				Type:  "Video",
				Codec: "hevc",
				Conditions: []ProfileCondition{
					{Condition: "NotEquals", Property: "IsAnamorphic", Value: "true", IsRequired: false},
					{Condition: "EqualsAny", Property: "VideoProfile", Value: "main|main 10", IsRequired: false},
					{Condition: "EqualsAny", Property: "VideoRangeType", Value: "SDR|HDR10|HLG|DOVI|DOVIWithHDR10|DOVIWithHLG|DOVIWithSDR", IsRequired: false},
					{Condition: "LessThanEqual", Property: "VideoLevel", Value: "183", IsRequired: false},
				},
			},
			allow4KTranscode: false,
			wantTranscoding:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := DeviceProfile{
				DirectPlayProfiles: []DirectPlayProfile{tt.directProfile},
				TranscodingProfiles: []TranscodingProfile{{
					Type: "Video", Protocol: "hls", Container: "ts", VideoCodec: "h264", AudioCodec: "aac",
				}},
				CodecProfiles: []CodecProfile{tt.codecProfile},
			}

			source := (&PlaybackHandler{codec: NewResourceIDCodec()}).buildPlaybackSource(
				"item", "play", tt.version, profile, playbackInfoRequest{}, tt.allow4KTranscode,
			)
			if !source.SupportsDirectPlay {
				t.Fatal("SupportsDirectPlay = false, want true")
			}
			if !source.SupportsDirectStream {
				t.Fatal("SupportsDirectStream = false, want true")
			}
			if source.SupportsTranscoding != tt.wantTranscoding {
				t.Fatalf("SupportsTranscoding = %v, want %v", source.SupportsTranscoding, tt.wantTranscoding)
			}
		})
	}
}

func TestConditionMatchesUnknownPropertyHonorsIsRequired(t *testing.T) {
	tests := []struct {
		name       string
		isRequired bool
		want       bool
	}{
		{name: "optional unknown property is satisfied", want: true},
		{name: "required unknown property fails", isRequired: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			condition := ProfileCondition{
				Condition:  "Equals",
				Property:   "UnsupportedProperty",
				Value:      "value",
				IsRequired: tt.isRequired,
			}
			if got := conditionMatches(condition, conditionValues{}); got != tt.want {
				t.Fatalf("conditionMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/jellycompat \
  -run 'TestBuildPlaybackSourceCodecProfiles_WebOSAnamorphicCondition|TestConditionMatchesUnknownPropertyHonorsIsRequired' \
  -count=1
```

Expected: FAIL. Both webOS cases report direct play/direct stream as false, and the optional unknown-property case reports false instead of true. The required unknown-property case already returns the expected false.

- [ ] **Step 3: Implement the minimal unknown-property behavior**

Change only the missing-value branch in `conditionMatches`:

```go
actual, ok := values[normalizeConditionToken(condition.Property)]
if !ok {
	return !condition.IsRequired
}
```

- [ ] **Step 4: Run the focused tests and verify GREEN**

Run the command from Step 2 again.

Expected: PASS. Known-property operator behavior remains untouched.

- [ ] **Step 5: Commit the independently testable behavior**

```bash
git add internal/jellycompat/deviceprofile_conditions.go internal/jellycompat/deviceprofile_conditions_test.go
git commit -m "fix(jellycompat): honor optional codec profile conditions"
```

### Task 2: Evaluate Silo's Known Non-Anamorphic Value

**Files:**
- Modify: `internal/jellycompat/deviceprofile_conditions.go:181`
- Test: `internal/jellycompat/deviceprofile_conditions_test.go`

**Interfaces:**
- Consumes: Silo's established protocol declaration that jellycompat video streams are non-anamorphic.
- Produces: a normalized `isanamorphic` `conditionValue` with text `false` from `buildConditionValues`.
- Preserves: scanner/catalog state and the existing media-stream DTO.

- [ ] **Step 1: Add a failing required-condition test**

Append this test. Making the condition required distinguishes a known `false` value from an optional unknown value and catches removal of the production mapping.

```go
func TestConditionMatchesRequiredIsAnamorphicUsesReportedFalseValue(t *testing.T) {
	condition := ProfileCondition{
		Condition:  "Equals",
		Property:   "IsAnamorphic",
		Value:      "false",
		IsRequired: true,
	}
	values := buildConditionValues(catalog.FileVersion{}, nil)

	if !conditionMatches(condition, values) {
		t.Fatal("required IsAnamorphic Equals false condition failed for Silo's reported false value")
	}
}
```

- [ ] **Step 2: Run the new test and verify RED**

Run:

```bash
go test ./internal/jellycompat \
  -run TestConditionMatchesRequiredIsAnamorphicUsesReportedFalseValue \
  -count=1
```

Expected: FAIL because `buildConditionValues` has no `isanamorphic` entry and a required unknown property is rejected.

- [ ] **Step 3: Add the protocol-local anamorphic value**

Add one entry to the existing `conditionValues` literal in `buildConditionValues`:

```go
"isanamorphic": {text: "false"},
```

Do not add scanner fields, model fields, DTO changes, or persistence.

- [ ] **Step 4: Run focused GREEN**

Run:

```bash
gofmt -w internal/jellycompat/deviceprofile_conditions.go internal/jellycompat/deviceprofile_conditions_test.go
go test ./internal/jellycompat \
  -run 'TestBuildPlaybackSourceCodecProfiles_WebOSAnamorphicCondition|TestConditionMatchesUnknownPropertyHonorsIsRequired|TestConditionMatchesRequiredIsAnamorphicUsesReportedFalseValue' \
  -count=1
```

Expected: PASS with formatted production and test files.

- [ ] **Step 5: Run the complete jellycompat package tests**

Run:

```bash
go test ./internal/jellycompat -count=1
go test ./internal/jellycompat -race -count=1
```

Expected: both commands exit 0 with no failures or race reports.

- [ ] **Step 6: Commit the known-value behavior**

```bash
git add internal/jellycompat/deviceprofile_conditions.go internal/jellycompat/deviceprofile_conditions_test.go
git commit -m "fix(jellycompat): evaluate non-anamorphic video"
```

### Task 3: Repository Verification and Handoff

**Files:**
- Review: `docs/superpowers/specs/2026-08-15-jellycompat-webos-anamorphic-design.md`
- Review: `docs/superpowers/plans/2026-08-15-jellycompat-webos-anamorphic.md`
- Review: `internal/jellycompat/deviceprofile_conditions.go`
- Review: `internal/jellycompat/deviceprofile_conditions_test.go`

**Interfaces:**
- Consumes: both RED/GREEN cycles and the approved design constraints.
- Produces: a verified, reviewable issue #580 change with no unrelated files.

- [ ] **Step 1: Review the final diff against the approved scope**

Confirm each invariant directly from the diff:

- `IsAnamorphic` evaluation agrees with the existing `false` media-stream response.
- Optional unknown properties pass and required unknown properties fail.
- Present values still use the existing operators.
- H.264, HEVC, and the 2160p no-transcode case are covered.
- No scanner, catalog, database, DTO, API, client, or unrelated refactor appears.

- [ ] **Step 2: Run full repository verification**

Run:

```bash
make lint
make test
cd web && pnpm run lint && pnpm run format:check
cd ..
make verify-local-paths
git diff --check
git status --short --branch
```

Expected: test, frontend, formatting, path-hygiene, and diff checks exit 0. If the repository-wide `make lint` reports pre-existing findings outside the changed lines, record them separately and verify no new finding points to either changed Go file.

- [ ] **Step 3: Commit the approved documentation if it is not already committed**

```bash
git add \
  docs/superpowers/specs/2026-08-15-jellycompat-webos-anamorphic-design.md \
  docs/superpowers/plans/2026-08-15-jellycompat-webos-anamorphic.md
git commit -m "docs(jellycompat): design webos anamorphic condition fix"
```

- [ ] **Step 4: Prepare the issue handoff**

Report the root cause, exact evaluator changes, RED/GREEN evidence, focused and full verification results, changed files, and the fact that scanner/catalog anamorphic detection remains explicitly out of scope. Do not deploy or change external systems as part of this plan.

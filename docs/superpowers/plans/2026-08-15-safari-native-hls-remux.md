# Safari Native-HLS Remux Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]` without spaces) syntax for tracking.

**Goal:** Route native-HLS web clients away from unbounded progressive Dolby Vision/HDR remuxes and emit copy-HLS fMP4 with the exact `dvh1` or `hvc1` sample entry the client validated.

**Architecture:** The web probe scopes media-element HDR evidence to native HLS, allowing existing delivery-specific planner validation to select `server_remux_hls` without user-agent policy. A pure helper selects the native player for HDR HLS plans. The server derives an allowlisted sample entry from the frozen plan and carries it through local/remote execution and reconstruction before FFmpeg writes fMP4 segments.

**Tech Stack:** Go, FFmpeg/ffprobe, React 19, TypeScript, Vitest, pnpm, playback protocol v3.

## Global Constraints

- Commands assume the repository root is the current working directory.
- Preserve additive-only `/api/v1` behavior; all new JSON fields are optional and additive.
- Do not add Safari user-agent matching or fake range headers.
- Do not change legacy progressive-remux or Jellyfin compatibility behavior.
- Do not include deployment hosts, media paths, credentials, or local absolute paths in committed files.
- Use red-green-refactor: every production behavior change starts with a failing test and its expected failure is observed.
- The Codex workspace may expose `.git` read-only. Run commit steps only when Git writes are available; otherwise leave a described working-tree handoff.

---

## File Map

- `web/src/player/client-context-v3.ts` and tests: detect generic/native HLS and scope delivery claims.
- `web/src/player/hooks/useCodecDetection.ts` and tests: publish the native-HLS fact.
- `web/src/player/utils/hlsEngine.ts` and tests: pure HLS engine selection.
- `web/src/player/components/VideoPlayer.tsx` and tests: execute native or hls.js playback.
- `internal/playback/transcode.go` and argument tests: validate and apply a copy-HLS sample entry.
- `internal/playback/recipecard.go`, `internal/streamtoken/token.go`, and tests: durable reconstruction.
- `internal/transcodenode/server.go` and tests: remote-node start contract.
- `internal/api/handlers/playback_v3.go` and tests: frozen-plan derivation and local/remote propagation.
- `internal/playback/protocol_v3_test.go`: Safari-shaped planner regression.
- `internal/playback/copy_seek_anchor_test.go`: real fMP4 init/media integration.
- `docs/architecture/playback-protocol-v3.md`: capability and packaging invariant.

---

### Task 1: Scope Web HDR Evidence to Native HLS

**Files:**
- Modify: `web/src/player/client-context-v3.ts`
- Modify: `web/src/player/client-context-v3.test.ts`
- Modify: `web/src/player/hooks/useCodecDetection.ts`
- Modify: `web/src/player/hooks/useCodecDetection.test.ts`

**Interfaces:**
- Produces: `detectHLSSupport(): { supported: boolean; native: boolean }`
- Produces: `WebCapabilityProbe.nativeHLS: boolean`
- Consumes: existing `hdrDetails` and `progressiveCodecsVideo` media-element evidence.

- [ ] **Step 1: Write failing detection tests**

Change the two existing `detectHLSSupport` expectations:

```ts
expect(detectHLSSupport()).toEqual({ supported: true, native: true });
expect(detectHLSSupport()).toEqual({ supported: true, native: false });
```

Add `nativeHLS: false` to ordinary `WebCapabilityProbe` literals. In the structured-HDR tests add:

```ts
it("scopes normalized HDR sample entries to native HLS", () => {
  const deliveries = buildDeliveriesV3({ ...probe, nativeHLS: true });

  expect(deliveries.progressive?.hdr_details?.dolby_vision_profiles).toEqual([]);
  expect(deliveries.hls?.hdr_details).toEqual(probe.hdrDetails);
  expect(deliveries.hls?.video_codecs).toContain("hevc");
});

it("keeps normalized HDR sample entries on progressive without native HLS", () => {
  const deliveries = buildDeliveriesV3({ ...probe, nativeHLS: false });

  expect(deliveries.progressive?.hdr_details).toEqual(probe.hdrDetails);
  expect(deliveries.hls?.hdr_details?.dolby_vision_profiles).toEqual([]);
});
```

In `useCodecDetection.test.ts`, make the Safari-shaped `canPlayType` stub accept both the HLS MIME and `dvh1.08.06`, then assert `probeWebCapabilities().nativeHLS` is true.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd web && pnpm exec vitest run src/player/client-context-v3.test.ts src/player/hooks/useCodecDetection.test.ts
```

Expected: FAIL because detection returns a boolean and `nativeHLS` does not exist.

- [ ] **Step 3: Implement structured HLS detection**

In `client-context-v3.ts`:

```ts
export interface HLSSupportProbe {
  supported: boolean;
  native: boolean;
}

export function detectHLSSupport(): HLSSupportProbe {
  if (typeof document !== "undefined") {
    try {
      const video = document.createElement("video");
      if (video.canPlayType("application/vnd.apple.mpegurl") !== "") {
        return { supported: true, native: true };
      }
    } catch {
      // Fall through to the hls.js/MSE probe.
    }
  }
  if (typeof MediaSource === "undefined") return { supported: false, native: false };
  try {
    return {
      supported: MediaSource.isTypeSupported('video/mp4; codecs="avc1.42E01E"'),
      native: false,
    };
  } catch {
    return { supported: false, native: false };
  }
}
```

Add `nativeHLS: boolean` to `WebCapabilityProbe`. In `probeWebCapabilities`, call detection once immediately before the return and replace the current boolean HLS assignment with:

```ts
const hlsSupport = detectHLSSupport();

hls: hlsSupport.supported,
nativeHLS: hlsSupport.native,
```

- [ ] **Step 4: Relocate delivery-specific claims**

In `buildDeliveriesV3` choose:

```ts
const progressiveHDRDetails = probe.nativeHLS ? nonProgressiveHDRDetails : probe.hdrDetails;
const hlsHDRDetails = probe.nativeHLS ? probe.hdrDetails : nonProgressiveHDRDetails;
const hlsVideoCodecs = probe.nativeHLS ? probe.progressiveCodecsVideo : probe.codecsVideo;
```

Use them explicitly:

```ts
progressive: buildDeliveryCapability(probe, {
  video_codecs: probe.progressiveCodecsVideo,
  hdr_details: progressiveHDRDetails,
}),
hls: buildDeliveryCapability(probe, {
  supported_on_device: probe.hls,
  ...(probe.hls ? {} : { failure_reason: "media_source_extensions_unavailable" }),
  containers: ["hls"],
  video_codecs: hlsVideoCodecs,
  hdr_details: hlsHDRDetails,
}),
```

Original HTTP keeps `nonProgressiveHDRDetails`.

- [ ] **Step 5: Run tests and verify GREEN**

```bash
cd web && pnpm exec vitest run src/player/client-context-v3.test.ts src/player/hooks/useCodecDetection.test.ts
```

Expected: PASS.

- [ ] **Step 6: Format and commit**

```bash
cd web && pnpm exec prettier --write src/player/client-context-v3.ts src/player/client-context-v3.test.ts src/player/hooks/useCodecDetection.ts src/player/hooks/useCodecDetection.test.ts
cd ..
git add web/src/player/client-context-v3.ts web/src/player/client-context-v3.test.ts web/src/player/hooks/useCodecDetection.ts web/src/player/hooks/useCodecDetection.test.ts
git commit -m "fix(web): scope HDR remux claims to native HLS"
```

---

### Task 2: Prefer Native HLS for HDR Plans

**Files:**
- Create: `web/src/player/utils/hlsEngine.ts`
- Create: `web/src/player/utils/hlsEngine.test.ts`
- Modify: `web/src/player/components/VideoPlayer.tsx`
- Modify: `web/src/player/components/VideoPlayer.test.tsx`

**Interfaces:**
- Produces: `selectHLSEngineV3(dynamicRange, nativeSupported, hlsJSSupported): "native" | "hlsjs" | "unsupported"`
- Consumes: `plan.effective_recipe.dynamic_range`.

- [ ] **Step 1: Write the failing pure policy test**

```ts
import { describe, expect, it } from "vitest";
import { selectHLSEngineV3 } from "./hlsEngine";

describe("selectHLSEngineV3", () => {
  it.each(["dolby_vision", "hdr10"])(
    "prefers native HLS for %s when both engines are available",
    (dynamicRange) => {
      expect(selectHLSEngineV3(dynamicRange, true, true)).toBe("native");
    },
  );

  it("keeps hls.js first for SDR", () => {
    expect(selectHLSEngineV3("sdr", true, true)).toBe("hlsjs");
  });

  it("falls back to native when hls.js is unavailable", () => {
    expect(selectHLSEngineV3("sdr", true, false)).toBe("native");
  });

  it("rejects an unavailable transport", () => {
    expect(selectHLSEngineV3("dolby_vision", false, false)).toBe("unsupported");
  });
});
```

- [ ] **Step 2: Run and verify RED**

```bash
cd web && pnpm exec vitest run src/player/utils/hlsEngine.test.ts
```

Expected: FAIL because the module does not exist.

- [ ] **Step 3: Implement the pure helper**

```ts
export type HLSEngineV3 = "native" | "hlsjs" | "unsupported";

export function selectHLSEngineV3(
  dynamicRange: string | undefined,
  nativeSupported: boolean,
  hlsJSSupported: boolean,
): HLSEngineV3 {
  const nativeHDR = dynamicRange === "dolby_vision" || dynamicRange === "hdr10";
  if (nativeHDR && nativeSupported) return "native";
  if (hlsJSSupported) return "hlsjs";
  if (nativeSupported) return "native";
  return "unsupported";
}
```

- [ ] **Step 4: Use the policy in `VideoPlayer`**

Import the helper. Extract the existing native assignment into a local `attachNativeHLS()` function that sets `video.src`, installs the one-shot metadata handler, restores `effectiveInitialPosition`, and calls `attemptAutoplayWhenReady`.

After importing Hls, compute:

```ts
const nativeSupported = video.canPlayType("application/vnd.apple.mpegurl") !== "";
const engine = selectHLSEngineV3(
  plan.effective_recipe.dynamic_range,
  nativeSupported,
  Hls.isSupported(),
);
```

Use native first when `engine === "native"`, retain the existing hls.js setup for `"hlsjs"`, and preserve unsupported-transport recovery for `"unsupported"`. Add this shape to the existing native-HLS component tests (using the current render helper and media-element spies):

```ts
const plan = fixturePlanV3({
  delivery: "server_remux_hls",
  stream: {
    url: "/playback/transcode/session-1/master.m3u8",
    protocol: "hls",
    headers: {},
    header_refresh: "none",
  },
  effective_recipe: {
    video_codec: "hevc",
    audio_codec: "eac3",
    dynamic_range: "dolby_vision",
  },
  timeline: {
    source_start_seconds: 42,
    stream_origin_seconds: 35,
    player_start_seconds: 7,
    timeline_offset_seconds: 0,
    can_seek_anywhere: false,
    seek_restoration: "source_position",
  },
});
const { container } = renderPlayer({ plan, initialPosition: 42 });
const video = container.querySelector("video");
if (!video) throw new Error("expected video element");

await waitFor(() => expect(video.src).toContain("/api/v1/stream/session-1"));
fireEvent.loadedMetadata(video);
expect(video.currentTime).toBe(7);
```

- [ ] **Step 5: Run and verify GREEN**

```bash
cd web && pnpm exec vitest run src/player/utils/hlsEngine.test.ts src/player/components/VideoPlayer.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Format and commit**

```bash
cd web && pnpm exec prettier --write src/player/utils/hlsEngine.ts src/player/utils/hlsEngine.test.ts src/player/components/VideoPlayer.tsx src/player/components/VideoPlayer.test.tsx
cd ..
git add web/src/player/utils/hlsEngine.ts web/src/player/utils/hlsEngine.test.ts web/src/player/components/VideoPlayer.tsx web/src/player/components/VideoPlayer.test.tsx
git commit -m "fix(web): prefer native HLS for HDR remuxes"
```

---

### Task 3: Enforce Copy-HLS Sample Entries in FFmpeg

**Files:**
- Modify: `internal/playback/transcode.go`
- Modify: `internal/playback/transcode_args_test.go`

**Interfaces:**
- Produces: `TranscodeOpts.VideoSampleEntry string`
- Produces: `VideoSampleEntryDVH1 = "dvh1"` and `VideoSampleEntryHVC1 = "hvc1"`
- Invariant: a non-empty sample entry requires copy video.

- [ ] **Step 1: Write failing argument tests**

```go
func TestBuildFFmpegArgsCopyVideoAppliesSampleEntry(t *testing.T) {
	tests := []struct {
		name, entry, want, not string
	}{
		{name: "Dolby Vision", entry: VideoSampleEntryDVH1, want: "-c:v copy -tag:v dvh1 -strict unofficial"},
		{name: "HDR10", entry: VideoSampleEntryHVC1, want: "-c:v copy -tag:v hvc1", not: "-strict unofficial"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := strings.Join(buildFFmpegArgs(TranscodeOpts{
				InputPath: "/media/movie.mkv", OutputDir: t.TempDir(),
				TargetCodecVideo: "copy", TargetCodecAudio: "copy",
				VideoSampleEntry: tc.entry, SegmentDuration: 2,
			}), " ")
			if !strings.Contains(args, tc.want) || tc.not != "" && strings.Contains(args, tc.not) {
				t.Fatalf("args = %s", args)
			}
		})
	}
}

func TestStartTranscodeRejectsInvalidVideoSampleEntry(t *testing.T) {
	for _, opts := range []TranscodeOpts{
		{TargetCodecVideo: "copy", VideoSampleEntry: "dvhe"},
		{TargetCodecVideo: "h264", VideoSampleEntry: VideoSampleEntryDVH1},
	} {
		if _, err := StartTranscode(context.Background(), opts); err == nil {
			t.Fatalf("invalid recipe accepted: %+v", opts)
		}
	}
}
```

- [ ] **Step 2: Run and verify RED**

```bash
go test ./internal/playback -run 'TestBuildFFmpegArgsCopyVideoAppliesSampleEntry|TestStartTranscodeRejectsInvalidVideoSampleEntry' -count=1
```

Expected: compile failure because the field and constants do not exist.

- [ ] **Step 3: Add option and validation**

Add beside `VideoBitstreamFilter`:

```go
VideoSampleEntry string // allowlisted copy-HLS sample entry: dvh1 or hvc1
```

Add:

```go
const (
	VideoSampleEntryDVH1 = "dvh1"
	VideoSampleEntryHVC1 = "hvc1"
)

func validVideoSampleEntry(value string) bool {
	return value == "" || value == VideoSampleEntryDVH1 || value == VideoSampleEntryHVC1
}
```

At the beginning of `StartTranscode`, before filesystem work:

```go
if !validVideoSampleEntry(opts.VideoSampleEntry) ||
	opts.VideoSampleEntry != "" && !strings.EqualFold(opts.TargetCodecVideo, "copy") {
	return nil, fmt.Errorf("unsupported video sample-entry recipe")
}
```

- [ ] **Step 4: Apply FFmpeg flags**

Immediately after copy codec and the optional bitstream filter:

```go
switch opts.VideoSampleEntry {
case VideoSampleEntryDVH1:
	args = append(args, "-tag:v", VideoSampleEntryDVH1, "-strict", "unofficial")
case VideoSampleEntryHVC1:
	args = append(args, "-tag:v", VideoSampleEntryHVC1)
}
```

- [ ] **Step 5: Run and verify GREEN**

```bash
go test ./internal/playback -run 'TestBuildFFmpegArgsCopyVideoAppliesSampleEntry|TestStartTranscodeRejectsInvalidVideoSampleEntry|TestBuildFFmpegArgs_CopyVideoAppliesValidatedBitstreamFilter' -count=1
go test ./internal/playback -count=1
```

Expected: PASS.

- [ ] **Step 6: Format and commit**

```bash
gofmt -w internal/playback/transcode.go internal/playback/transcode_args_test.go
git add internal/playback/transcode.go internal/playback/transcode_args_test.go
git commit -m "fix(playback): preserve HDR sample entries in copy HLS"
```

---

### Task 4: Preserve Sample Entries Across Reconstruction and Remote Nodes

**Files:**
- Modify: `internal/playback/recipecard.go`
- Modify: `internal/playback/recipecard_test.go`
- Modify: `internal/streamtoken/token.go`
- Modify: `internal/transcodenode/server.go`
- Modify: `internal/transcodenode/server_test.go`

**Interfaces:**
- Consumes: `TranscodeOpts.VideoSampleEntry`
- Produces: optional `VideoSampleEntry` fields in recipe cards, token claims, and remote start requests.

- [ ] **Step 1: Write failing round-trip tests**

Set `VideoSampleEntry: VideoSampleEntryDVH1` in both `TestRecipeCardRoundTripOpts` and `TestRecipeCardClaimsRoundTrip`. Assert the rebuilt options and card preserve it, and include it in the complete byte-affecting field comparison.

Add a transcode-node start test that sends:

```go
VideoSampleEntry: playback.VideoSampleEntryHVC1,
TargetCodecVideo: "copy",
TargetCodecAudio: "copy",
```

Use the same looping fake process used by `TestHandleStartUsesConfiguredHWDeviceList`, then assert the registered session option:

```go
ffmpegPath := filepath.Join(t.TempDir(), "looping-ffmpeg.sh")
if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nwhile :; do sleep 0.1; done\n"), 0o755); err != nil {
	t.Fatal(err)
}
server.tracker = nodesessions.NewTracker(nil, "http://node", "node", "transcode")
server.watcher.Config().Playback.FFmpegPath = ffmpegPath
requestBody, err := json.Marshal(TranscodeStartRequest{
	SessionID: "sample-entry-start-1", InputPath: "/media/movie.mkv",
	VideoSampleEntry: playback.VideoSampleEntryHVC1,
	TargetCodecVideo: "copy", TargetCodecAudio: "copy", SegmentDuration: 2,
})
if err != nil {
	t.Fatal(err)
}
req := httptest.NewRequest(http.MethodPost, "/transcode/start", bytes.NewReader(requestBody))
rr := httptest.NewRecorder()
server.handleStart(rr, req)
if rr.Code != http.StatusAccepted {
	t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
}
server.mu.RLock()
session := server.sessions["sample-entry-start-1"]
server.mu.RUnlock()
if session == nil {
	t.Fatal("session was not registered")
}
defer session.CloseProcess()
if got := session.Opts().VideoSampleEntry; got != playback.VideoSampleEntryHVC1 {
	t.Fatalf("VideoSampleEntry = %q", got)
}
```

- [ ] **Step 2: Run and verify RED**

```bash
go test ./internal/playback ./internal/transcodenode -run 'TestRecipeCardRoundTripOpts|TestRecipeCardClaimsRoundTrip|TestHandleStart.*SampleEntry' -count=1
```

Expected: FAIL because durable and remote structures drop the field.

- [ ] **Step 3: Implement durable propagation**

Add:

```go
// RecipeCard
VideoSampleEntry string `json:"video_sample_entry,omitempty"`

// streamtoken.Claims
VideoSampleEntry string `json:"vse,omitempty"`
```

Copy it in all four projections:

```go
// NewRecipeCard
VideoSampleEntry: opts.VideoSampleEntry,

// RecipeCard.TranscodeOpts
VideoSampleEntry: c.VideoSampleEntry,

// RecipeCard.ToClaims
VideoSampleEntry: c.VideoSampleEntry,

// RecipeCardFromClaims
VideoSampleEntry: c.VideoSampleEntry,
```

- [ ] **Step 4: Implement remote propagation**

Add to `TranscodeStartRequest`:

```go
VideoSampleEntry string `json:"video_sample_entry,omitempty"`
```

Copy it to `playback.TranscodeOpts` in `handleStart`. Task 3 remains the single executor allowlist.

- [ ] **Step 5: Run and verify GREEN**

```bash
go test ./internal/playback ./internal/transcodenode -run 'TestRecipeCardRoundTripOpts|TestRecipeCardClaimsRoundTrip|TestHandleStart.*SampleEntry' -count=1
```

Expected: PASS.

- [ ] **Step 6: Format and commit**

```bash
gofmt -w internal/playback/recipecard.go internal/playback/recipecard_test.go internal/streamtoken/token.go internal/transcodenode/server.go internal/transcodenode/server_test.go
git add internal/playback/recipecard.go internal/playback/recipecard_test.go internal/streamtoken/token.go internal/transcodenode/server.go internal/transcodenode/server_test.go
git commit -m "fix(playback): persist copy-HLS sample entries"
```

---

### Task 5: Derive the Sample Entry From the Frozen v3 Plan

**Files:**
- Modify: `internal/api/handlers/playback_v3.go`
- Modify: `internal/api/handlers/playback_v3_test.go`
- Modify: `internal/playback/protocol_v3_test.go`

**Interfaces:**
- Produces: `videoSampleEntryForPlanV3(plan *playback.PlanV3) string`
- Invariant: only `DeliveryRemuxHLSV3` receives a non-empty value.

- [ ] **Step 1: Write failing derivation tests**

Add this table to handler tests:

```go
func TestVideoSampleEntryForPlanV3(t *testing.T) {
	tests := []struct {
		name string
		plan *playback.PlanV3
		want string
	}{
		{
			name: "preserved profile 8",
			plan: &playback.PlanV3{Delivery: playback.DeliveryRemuxHLSV3,
				Source: playback.SourceDescriptorV3{DVProfile: 8},
				EffectiveRecipe: playback.EffectiveRecipeV3{DynamicRange: playback.DynamicRangeDolbyVisionV3}},
			want: playback.VideoSampleEntryDVH1,
		},
		{
			name: "stripped HDR10",
			plan: &playback.PlanV3{Delivery: playback.DeliveryRemuxHLSV3,
				Transformations: []playback.TransformationV3{{Name: playback.TransformationServerDV7HDR10V3}}},
			want: playback.VideoSampleEntryHVC1,
		},
		{
			name: "progressive unchanged",
			plan: &playback.PlanV3{Delivery: playback.DeliveryRemuxProgressiveV3,
				Source: playback.SourceDescriptorV3{DVProfile: 8},
				EffectiveRecipe: playback.EffectiveRecipeV3{DynamicRange: playback.DynamicRangeDolbyVisionV3}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := videoSampleEntryForPlanV3(tc.plan); got != tc.want {
				t.Fatalf("sample entry = %q, want %q", got, tc.want)
			}
		})
	}
}
```

Extend `TestPrepareTransportV3SendsResolvedCopyAnchorToRemoteExecutor` with a Profile 8 Dolby Vision HLS plan and assert `startRequest.VideoSampleEntry == playback.VideoSampleEntryDVH1`.

- [ ] **Step 2: Run and verify RED**

```bash
go test ./internal/api/handlers -run 'TestVideoSampleEntryForPlanV3|TestPrepareTransportV3SendsResolvedCopyAnchorToRemoteExecutor' -count=1
```

Expected: FAIL because the helper does not exist and the request field is empty.

- [ ] **Step 3: Implement derivation and propagation**

```go
func videoSampleEntryForPlanV3(plan *playback.PlanV3) string {
	if plan == nil || plan.Delivery != playback.DeliveryRemuxHLSV3 {
		return ""
	}
	for _, transformation := range plan.Transformations {
		if transformation.Name == playback.TransformationServerDV7HDR10V3 {
			return playback.VideoSampleEntryHVC1
		}
	}
	if plan.EffectiveRecipe.DynamicRange == playback.DynamicRangeDolbyVisionV3 &&
		(plan.Source.DVProfile == 5 || plan.Source.DVProfile == 8) {
		return playback.VideoSampleEntryDVH1
	}
	return ""
}
```

Set `VideoSampleEntry: videoSampleEntryForPlanV3(result.Plan)` in the local `TranscodeOpts`, remote `TranscodeStartRequest`, and the remote `NewRecipeCard` options reconstructed from the request.

- [ ] **Step 4: Add the planner regression**

Create `TestPlanPlaybackV3SafariNativeHLSAvoidsProgressiveDVRemux` using `detailedFixtureFileV3` with MKV Profile 8.1 Dolby Vision. Give top-level and output HDR details Profile 8, Level 6, BL compatibility 1. Give progressive delivery empty HDR details and HLS delivery the verified claim:

```go
progressive := req.ClientPlaybackContext.Deliveries[DeliveryClassProgressiveV3]
progressive.Containers = []string{"mp4"}
progressive.VideoCodecs = []string{"hevc"}
progressive.AudioDecodeCodecs = []string{"eac3"}
progressive.HDRDetails = &HDRCapabilitiesV3{}
req.ClientPlaybackContext.Deliveries[DeliveryClassProgressiveV3] = progressive

hls := progressive
hls.Containers = []string{"hls"}
hls.HDRDetails = hdr
req.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3] = hls
```

Assert copy-codec `DeliveryRemuxHLSV3`, `PlayRemux`, and a Dolby Vision video claim. This is a characterization of existing delivery-specific planner logic and may pass immediately; retain it to prevent later route-order regression.

- [ ] **Step 5: Run and verify GREEN**

```bash
go test ./internal/api/handlers -run 'TestVideoSampleEntryForPlanV3|TestPrepareTransportV3SendsResolvedCopyAnchorToRemoteExecutor' -count=1
go test ./internal/playback -run 'TestPlanPlaybackV3SafariNativeHLSAvoidsProgressiveDVRemux' -count=1
```

Expected: PASS.

- [ ] **Step 6: Format and commit**

```bash
gofmt -w internal/api/handlers/playback_v3.go internal/api/handlers/playback_v3_test.go internal/playback/protocol_v3_test.go
git add internal/api/handlers/playback_v3.go internal/api/handlers/playback_v3_test.go internal/playback/protocol_v3_test.go
git commit -m "fix(playback): route native HDR remuxes through copy HLS"
```

---

### Task 6: Verify Real fMP4 Init and Media Segments

**Files:**
- Modify: `internal/playback/copy_seek_anchor_test.go`
- Modify: `docs/architecture/playback-protocol-v3.md`
- Test: focused and repository-wide suites.

**Interfaces:**
- Consumes: `TranscodeOpts.VideoSampleEntry`
- Produces: real FFmpeg evidence for an init segment, media segment, timeline, and `hvc1` tag.

- [ ] **Step 1: Strengthen the real FFmpeg integration**

In `TestResolveCopySeekAnchorMatchesRealLongGOPHEVC`, set:

```go
VideoSampleEntry: VideoSampleEntryHVC1,
```

After reading the manifest, assert:

```go
if !strings.Contains(string(manifest), `#EXT-X-MAP:URI="init.mp4"`) {
	t.Fatalf("copy HLS manifest missing init map:\n%s", manifest)
}
initInfo, err := os.Stat(filepath.Join(outputDir, "init.mp4"))
if err != nil || initInfo.Size() == 0 {
	t.Fatalf("copy HLS init segment: info=%v err=%v", initInfo, err)
}
segments, err := filepath.Glob(filepath.Join(outputDir, "seg_*.m4s"))
if err != nil || len(segments) == 0 {
	t.Fatalf("copy HLS media segments = %v err=%v", segments, err)
}
```

Probe the manifest:

```go
tag, err := exec.Command(ffprobePathFromFFmpeg(ffmpegPath),
	"-v", "error", "-select_streams", "v:0",
	"-show_entries", "stream=codec_tag_string",
	"-of", "default=nw=1:nk=1", manifestPath,
).CombinedOutput()
if err != nil || strings.TrimSpace(string(tag)) != VideoSampleEntryHVC1 {
	t.Fatalf("copy HLS sample entry = %q err=%v", tag, err)
}
```

- [ ] **Step 2: Run the real integration**

```bash
go test ./internal/playback -run 'TestResolveCopySeekAnchorMatchesRealLongGOPHEVC' -count=1 -v
```

Expected: PASS, or SKIP only if local FFmpeg lacks `libx265`. Report a skip and supplement it with the deployment fixture.

- [ ] **Step 3: Document the invariant**

In `docs/architecture/playback-protocol-v3.md`, document:

```markdown
- Web media-element `dvh1`/`hvc1` evidence is scoped to native HLS when native
  HLS is available; progressive delivery does not inherit that evidence.
- A preserved Profile 5/8 copy-HLS plan emits `dvh1` with FFmpeg's unofficial
  strictness relaxation so the DOVI configuration record is retained. A
  validated Dolby Vision-to-HDR10 copy-HLS plan emits `hvc1`.
```

Use real Markdown backticks rather than tildes in the file.

- [ ] **Step 4: Run focused verification**

```bash
gofmt -w internal/playback/copy_seek_anchor_test.go
cd web && pnpm run format:check && pnpm run lint && pnpm run build
cd ..
go test ./internal/playback ./internal/api/handlers ./internal/transcodenode -count=1
make verify-local-paths
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 5: Run repository verification**

```bash
make lint
make test
make build
cd web && pnpm run lint && pnpm run format:check
cd .. && make verify-local-paths
```

Expected: all tests and the build pass. If full `make lint` reports known pre-existing findings, distinguish them from changed-line findings and add none.

- [ ] **Step 6: Verify the supplied deployment fixture**

Once `.silo-dev.env` is configured:

```bash
scripts/silo-dev doctor
scripts/silo-dev compose ps
scripts/silo-dev api /api/v1/ready
```

Deploy through the configured local target. Start the user-supplied Dolby Vision file in Safari and verify:

- planner diagnostics show `delivery=server_remux_hls`, `play_method=remux`, and preserved Dolby Vision;
- the manifest references `init.mp4` and one or more `.m4s` segments;
- ffprobe reports `codec_tag_string=dvh1` and a DOVI configuration record;
- Safari reaches first frame and can seek without the immediate decoder error;
- recent server logs contain no transport startup failure.

Do not record the host, fixture path, tokens, or credentials in repository files.

- [ ] **Step 7: Commit integration and docs**

```bash
git add internal/playback/copy_seek_anchor_test.go docs/architecture/playback-protocol-v3.md docs/superpowers/specs/2026-08-15-safari-native-hls-remux-design.md docs/superpowers/plans/2026-08-15-safari-native-hls-remux.md
git commit -m "test(playback): cover native-HLS HDR remux transport"
```

---

## Final Review Checklist

- [ ] Progressive Safari delivery has no DV/HDR claim when native HLS is available.
- [ ] HLS capability and player both select the native media element for DV/HDR.
- [ ] Planner output is copy-codec `server_remux_hls` for the Safari-shaped request.
- [ ] Preserved DV uses `dvh1 -strict unofficial`; stripped HDR10 uses `hvc1`.
- [ ] Sample-entry data survives local start, remote start, recipe card, signed token, and reconstruction.
- [ ] Legacy/Jellyfin routes remain unchanged.
- [ ] Unit, integration, lint, build, local-path, and live-fixture evidence is included in the handoff.

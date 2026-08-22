package playback_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func defaultCaps() playback.ClientCapabilities {
	return playback.ClientCapabilities{
		CodecsVideo:   []string{"h264"},
		CodecsAudio:   []string{"aac", "opus"},
		Containers:    []string{"mp4", "webm"},
		MaxResolution: "1080p",
		HDR:           false,
	}
}

func defaultSettings() playback.AdminSettings {
	return playback.AdminSettings{
		TranscodeEnabled: true,
		Allow4KTranscode: false,
	}
}

func TestResolver_DirectPlay(t *testing.T) {
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "aac", Container: "mp4",
		Resolution: "1080p", HDR: false,
	}
	decision := playback.Resolve(file, defaultCaps(), defaultSettings())

	if decision.Method != playback.PlayDirect {
		t.Errorf("method = %q, want direct", decision.Method)
	}
}

func TestResolver_Remux(t *testing.T) {
	// h264+aac in mkv — client supports codecs but not container.
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "aac", Container: "mkv",
		Resolution: "1080p", HDR: false,
	}
	decision := playback.Resolve(file, defaultCaps(), defaultSettings())

	if decision.Method != playback.PlayRemux {
		t.Errorf("method = %q, want remux", decision.Method)
	}
}

func TestResolver_RemuxWithAudioTranscode(t *testing.T) {
	// h264 video (supported) + dts audio (unsupported) → remux with audio transcode.
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "dts", Container: "mkv",
		Resolution: "1080p", HDR: false,
	}
	decision := playback.Resolve(file, defaultCaps(), defaultSettings())

	if decision.Method != playback.PlayRemux {
		t.Errorf("method = %q, want remux", decision.Method)
	}
	if !decision.TranscodeAudio {
		t.Error("TranscodeAudio = false, want true")
	}
}

func TestResolver_CopyUnsafeForcesTranscode(t *testing.T) {
	unsafe := true
	// h264+dts in mkv would normally remux with audio transcode (video copied),
	// but the source carries conflicting in-band PPS, so the video copy is unsafe
	// and it must fall through to a full transcode.
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "dts", Container: "mkv",
		Resolution: "1080p", HDR: false,
		VideoTracks: []models.VideoTrack{{Codec: "h264", MultiplePPS: &unsafe}},
	}
	decision := playback.Resolve(file, defaultCaps(), defaultSettings())

	if decision.Method != playback.PlayTranscode {
		t.Errorf("method = %q, want transcode", decision.Method)
	}
}

func TestResolver_UnknownCopySafetyForcesTranscode(t *testing.T) {
	// An inconclusive safety scan must not fail open to video stream-copy.
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "dts", Container: "mkv",
		Resolution: "1080p", HDR: false,
		VideoTracks: []models.VideoTrack{{
			Codec:           "h264",
			VideoCopyUnsafe: true,
		}},
	}
	decision := playback.Resolve(file, defaultCaps(), defaultSettings())

	if decision.Method != playback.PlayTranscode {
		t.Errorf("method = %q, want transcode", decision.Method)
	}
}

func TestResolver_CopySafeStillRemuxes(t *testing.T) {
	safe := false
	// The same shape with the copy-safety scan resolved to safe keeps remuxing.
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "dts", Container: "mkv",
		Resolution: "1080p", HDR: false,
		VideoTracks: []models.VideoTrack{{Codec: "h264", MultiplePPS: &safe}},
	}
	decision := playback.Resolve(file, defaultCaps(), defaultSettings())

	if decision.Method != playback.PlayRemux {
		t.Errorf("method = %q, want remux", decision.Method)
	}
}

func TestResolver_AudioPassthroughSkipsAudioTranscode(t *testing.T) {
	// Source is h264 + eac3 in mp4. Client can decode h264 but not eac3; its
	// sink advertises eac3 passthrough (e.g. HDMI AVR). Should direct-play
	// without audio transcode instead of promoting to remux.
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "eac3", Container: "mp4",
		Resolution: "1080p", HDR: false,
	}
	caps := defaultCaps()
	caps.AudioPassthroughCodecs = []string{"eac3", "ac3"}

	decision := playback.Resolve(file, caps, defaultSettings())

	if decision.Method != playback.PlayDirect {
		t.Errorf("method = %q, want direct (passthrough-supported audio)", decision.Method)
	}
	if decision.TranscodeAudio {
		t.Error("TranscodeAudio = true, want false (sink can passthrough)")
	}
}

func TestResolver_AudioPassthroughAllowsContainerRemux(t *testing.T) {
	// Source is h264 + eac3 in mkv. Client passthrough covers eac3 but container
	// is unsupported → remux without audio transcode.
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "eac3", Container: "mkv",
		Resolution: "1080p", HDR: false,
	}
	caps := defaultCaps()
	caps.AudioPassthroughCodecs = []string{"eac3"}

	decision := playback.Resolve(file, caps, defaultSettings())

	if decision.Method != playback.PlayRemux {
		t.Errorf("method = %q, want remux", decision.Method)
	}
	if decision.TranscodeAudio {
		t.Error("TranscodeAudio = true, want false (sink can passthrough)")
	}
}

func TestResolver_Transcode_UnsupportedVideoCodec(t *testing.T) {
	// hevc is not in client's supported codecs.
	file := &models.MediaFile{
		CodecVideo: "hevc", CodecAudio: "aac", Container: "mp4",
		Resolution: "1080p", HDR: false,
	}
	decision := playback.Resolve(file, defaultCaps(), defaultSettings())

	if decision.Method != playback.PlayTranscode {
		t.Errorf("method = %q, want transcode", decision.Method)
	}
}

func TestResolver_DownloadSoftwareDecodeIsFeatureGatedAndBounded(t *testing.T) {
	file := &models.MediaFile{
		CodecVideo: "av1", CodecAudio: "aac", Container: "mp4",
		Resolution: "1080p", Bitrate: 9_000,
		VideoTracks: []models.VideoTrack{{
			Codec: "av1", Profile: "Main", Width: 1920, Height: 1080, FrameRate: "24/1",
			Bitrate: 9_000, BitDepth: 10,
		}},
	}
	caps := playback.ClientCapabilities{
		ClientFeatures: []string{playback.FeatureSoftwareVideoDecodeV3},
		VideoEvidence:  playback.EvidencePlatformAttestedV3,
		CodecsVideo:    []string{"av1"},
		CodecsAudio:    []string{"aac"},
		Containers:     []string{"mp4"},
		MaxResolution:  "2160p",
		VideoDecode: []playback.VideoDecodeCapabilityV3{{
			Codec: "av1", Profiles: []string{"main"}, BitDepths: []int{10}, MaxWidth: 1920,
			MaxHeight: 1080, MaxFrameRate: 60, MaxBitrateKbps: 40_000,
			Hardware: false,
		}},
	}

	if decision := playback.Resolve(file, caps, defaultSettings()); decision.Method != playback.PlayDirect {
		t.Fatalf("opted-in bounded software source = %q, want direct", decision.Method)
	}

	withoutFeature := caps
	withoutFeature.ClientFeatures = nil
	if decision := playback.Resolve(file, withoutFeature, defaultSettings()); decision.Method != playback.PlayTranscode {
		t.Fatalf("software source without opt-in = %q, want transcode", decision.Method)
	}

	file.Resolution = "2160p"
	file.VideoTracks[0].Width = 3840
	file.VideoTracks[0].Height = 2160
	if decision := playback.Resolve(file, caps, defaultSettings()); decision.Method != playback.PlayTranscode {
		t.Fatalf("software source beyond decoder bounds = %q, want transcode", decision.Method)
	}
}

func TestResolver_DetailedHardwareEvidenceOverridesLegacyDownloadCeiling(t *testing.T) {
	file := &models.MediaFile{
		CodecVideo: "hevc", CodecAudio: "aac", Container: "mp4",
		Resolution: "2160p", Bitrate: 60_000,
		VideoTracks: []models.VideoTrack{{
			Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160,
			FrameRate: "60/1", Bitrate: 60_000, BitDepth: 10,
		}},
	}
	caps := playback.ClientCapabilities{
		VideoEvidence: playback.EvidencePlatformAttestedV3,
		CodecsVideo:   []string{"hevc"},
		CodecsAudio:   []string{"aac"},
		Containers:    []string{"mp4"},
		// Older servers conservatively stop here. The detailed-aware server
		// validates the per-decoder 4K hardware bound instead.
		MaxResolution: "1080p",
		VideoDecode: []playback.VideoDecodeCapabilityV3{{
			Codec: "hevc", BitDepths: []int{8, 10}, MaxWidth: 3840,
			MaxHeight: 2160, MaxFrameRate: 60, MaxBitrateKbps: 120_000,
			Hardware: true,
		}},
	}

	if decision := playback.Resolve(file, caps, defaultSettings()); decision.Method != playback.PlayDirect {
		t.Fatalf("detailed 4K hardware source = %q, want direct", decision.Method)
	}
}

func TestResolver_DetailedDownloadEvidenceFailsClosedWhenProbeFactsAreIncomplete(t *testing.T) {
	file := &models.MediaFile{
		CodecVideo: "av1", CodecAudio: "aac", Container: "mp4",
		Resolution: "1080p",
	}
	caps := playback.ClientCapabilities{
		ClientFeatures: []string{playback.FeatureSoftwareVideoDecodeV3},
		VideoEvidence:  playback.EvidencePlatformAttestedV3,
		CodecsVideo:    []string{"av1"},
		CodecsAudio:    []string{"aac"},
		Containers:     []string{"mp4"},
		MaxResolution:  "2160p",
		VideoDecode: []playback.VideoDecodeCapabilityV3{{
			Codec: "av1", BitDepths: []int{8, 10}, MaxWidth: 1920,
			MaxHeight: 1080, MaxFrameRate: 60, MaxBitrateKbps: 40_000,
		}},
	}

	if decision := playback.Resolve(file, caps, defaultSettings()); decision.Method != playback.PlayTranscode {
		t.Fatalf("incomplete strict evidence = %q, want transcode", decision.Method)
	}
}

func TestResolver_Transcode_ResolutionExceeds(t *testing.T) {
	file := &models.MediaFile{
		CodecVideo: "h264", CodecAudio: "aac", Container: "mp4",
		Resolution: "2160p", HDR: false,
	}
	caps := defaultCaps()
	caps.MaxResolution = "1080p"

	decision := playback.Resolve(file, caps, defaultSettings())

	if decision.Method != playback.PlayTranscode {
		t.Errorf("method = %q, want transcode for resolution downscale", decision.Method)
	}
}

func TestResolver_HDR_PassthroughToRemux(t *testing.T) {
	file := &models.MediaFile{
		CodecVideo: "hevc", CodecAudio: "aac", Container: "mkv",
		Resolution: "1080p", HDR: true,
	}
	caps := defaultCaps()
	caps.CodecsVideo = []string{"h264", "hevc"}
	caps.HDR = false

	decision := playback.Resolve(file, caps, defaultSettings())

	if decision.Method != playback.PlayRemux {
		t.Errorf("method = %q, want remux — HDR should pass through without tone mapping", decision.Method)
	}
}

func TestResolver_TranscodeDisabled_FallsToDirect(t *testing.T) {
	file := &models.MediaFile{
		CodecVideo: "hevc", CodecAudio: "aac", Container: "mkv",
		Resolution: "1080p", HDR: false,
	}
	settings := defaultSettings()
	settings.TranscodeEnabled = false

	decision := playback.Resolve(file, defaultCaps(), settings)

	if decision.Method != playback.PlayDirect {
		t.Errorf("method = %q, want direct (transcode disabled)", decision.Method)
	}
}

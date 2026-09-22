package jellycompat

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

func TestEncodedOutputConditionsDoNotUseSourceCodecFacts(t *testing.T) {
	version := catalog.FileVersion{
		FileID: 42, Container: "mkv", CodecVideo: "hevc", CodecAudio: "aac",
		Bitrate: 10000, Resolution: "1080p", HDR: true,
		VideoTracks: []models.VideoTrack{{Codec: "hevc", Profile: "Main 10", Level: 153,
			Width: 1920, Height: 1080, BitDepth: 10, VideoRangeType: "HDR10", ReferenceFrames: 8}},
		AudioTracks: []models.AudioTrack{{Codec: "aac", Channels: 2}},
	}
	for _, tc := range []struct {
		name      string
		condition ProfileCondition
		bitrate   int64
		want      bool
	}{
		{"source codec level", ProfileCondition{Condition: "LessThanEqual", Property: "VideoLevel", Value: "51"}, 0, true},
		{"source reference frames", ProfileCondition{Condition: "LessThanEqual", Property: "RefFrames", Value: "4"}, 0, true},
		{"tone mapped range", ProfileCondition{Condition: "Equals", Property: "VideoRangeType", Value: "SDR", IsRequired: true}, 0, true},
		{"scaled width", ProfileCondition{Condition: "LessThanEqual", Property: "Width", Value: "1280", IsRequired: true}, 4000000, true},
		{"scaled height", ProfileCondition{Condition: "LessThanEqual", Property: "Height", Value: "720", IsRequired: true}, 4000000, true},
		{"known width exceeds constraint", ProfileCondition{Condition: "LessThanEqual", Property: "Width", Value: "1280", IsRequired: true}, 0, false},
		{"required encoder level unknown", ProfileCondition{Condition: "LessThanEqual", Property: "VideoLevel", Value: "51", IsRequired: true}, 0, false},
		{"required encoder profile unknown", ProfileCondition{Condition: "EqualsAny", Property: "VideoProfile", Value: "high|main|baseline", IsRequired: true}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := DeviceProfile{
				TranscodingProfiles: []TranscodingProfile{{Type: "Video", Protocol: "hls", Container: "ts", VideoCodec: "h264", AudioCodec: "aac"}},
				CodecProfiles:       []CodecProfile{{Type: "Video", Codec: "h264", Conditions: []ProfileCondition{tc.condition}}},
			}
			h := &PlaybackHandler{codec: NewResourceIDCodec()}
			source := h.buildPlaybackSource("item", "play", version, profile, playbackInfoRequest{
				EnableDirectPlay: new(false), EnableDirectStream: new(false), MaxStreamingBitrate: tc.bitrate,
			}, true)
			if source.SupportsTranscoding != tc.want || source.CanBurnSubtitle != tc.want {
				t.Fatalf("transcoding=%v burn=%v, want %v for %+v", source.SupportsTranscoding, source.CanBurnSubtitle, tc.want, tc.condition)
			}
		})
	}
}

func TestPlaybackBitrateResolutionDoesNotUpscale(t *testing.T) {
	for _, tc := range []struct {
		name       string
		height     int
		resolution string
	}{
		{"smaller source", 480, ""},
		{"matching source", 720, ""},
		{"larger source", 1080, "720p"},
		{"unknown source", 0, "720p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := testCompatVersion()
			version.VideoTracks[0].Height = tc.height
			h := &PlaybackHandler{codec: NewResourceIDCodec()}
			source := h.buildPlaybackSource("item", "play", version, DefaultDeviceProfile(), playbackInfoRequest{MaxStreamingBitrate: 4000000}, true)
			if !source.SupportsTranscoding || source.TargetResolution != tc.resolution {
				t.Fatalf("height=%d: transcoding=%v resolution=%q, want %q", tc.height, source.SupportsTranscoding, source.TargetResolution, tc.resolution)
			}
		})
	}
}

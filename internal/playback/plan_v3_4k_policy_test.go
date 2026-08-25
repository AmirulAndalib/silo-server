package playback

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestIs4KMediaFileV3(t *testing.T) {
	tests := []struct {
		name string
		file *models.MediaFile
		want bool
	}{
		{name: "2160p", file: &models.MediaFile{Resolution: "2160p"}, want: true},
		{name: "uppercase 4K", file: &models.MediaFile{Resolution: "4K"}, want: true},
		{name: "padded uhd", file: &models.MediaFile{Resolution: " uhd "}, want: true},
		{name: "mixed case UHD", file: &models.MediaFile{Resolution: "Uhd"}, want: true},
		{name: "1080p", file: &models.MediaFile{Resolution: "1080p"}, want: false},
		{name: "720p", file: &models.MediaFile{Resolution: "720p"}, want: false},
		{name: "empty label", file: &models.MediaFile{}, want: false},
		{name: "nil file", file: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Is4KMediaFileV3(tt.file); got != tt.want {
				t.Fatalf("Is4KMediaFileV3(%+v) = %t, want %t", tt.file, got, tt.want)
			}
		})
	}
}

func TestIs4KSourceV3UsesLabelAndProbedDimensions(t *testing.T) {
	tests := []struct {
		name   string
		file   *models.MediaFile
		source SourceDescriptorV3
		want   bool
	}{
		{name: "label only", file: &models.MediaFile{Resolution: "4k"}, source: SourceDescriptorV3{Width: 1920, Height: 1080}, want: true},
		{name: "width only", file: &models.MediaFile{Resolution: "1080p"}, source: SourceDescriptorV3{Width: 3840, Height: 1626}, want: true},
		{name: "height only", file: &models.MediaFile{Resolution: ""}, source: SourceDescriptorV3{Width: 2880, Height: 2160}, want: true},
		{name: "neither", file: &models.MediaFile{Resolution: "1080p"}, source: SourceDescriptorV3{Width: 1920, Height: 1080}, want: false},
		{name: "nothing known", file: nil, source: SourceDescriptorV3{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := is4KSourceV3(tt.file, tt.source); got != tt.want {
				t.Fatalf("is4KSourceV3 = %t, want %t", got, tt.want)
			}
		})
	}
}

// A measured 1080p source must still plan a transcode with the 4K policy off.
// still plans a transcode with the 4K policy off.
func TestPlanPlaybackV3PlansKnownNon4KSourceWhen4KDisabled(t *testing.T) {
	file := detailedFixtureFileV3()
	file.Resolution = "1080p"
	file.Bitrate = 8_000
	file.VideoTracks[0] = models.VideoTrack{Codec: "hevc", Profile: "Main 10", Level: 120, Width: 1920, Height: 1080, FrameRate: "24000/1001", Bitrate: 8_000, BitDepth: 10, VideoRange: "SDR", VideoRangeType: "SDR", ColorRange: "tv", ColorTransfer: "bt709"}

	req := validStartRequestV3()
	req.Capabilities.CodecsVideo = []string{"h264"}
	req.Capabilities.MaxResolution = "1080p"
	for _, delivery := range []string{DeliveryClassProgressiveV3, DeliveryClassHLSV3} {
		packaged := req.ClientPlaybackContext.Deliveries[delivery]
		packaged.VideoCodecs = []string{"h264"}
		packaged.AudioDecodeCodecs = []string{"aac"}
		req.ClientPlaybackContext.Deliveries[delivery] = packaged
	}

	result := PlanPlaybackV3(PlannerInputV3{
		Request: req, RequestedFile: file, EffectiveFile: file, AudioTrackIndex: 0,
		Settings: PlannerSettingsV3{TranscodeEnabled: true, Allow4KTranscode: false}, Registry: testTransformationRegistryV3(),
	})
	if result.Plan == nil || result.Plan.Delivery != DeliveryTranscodeHLSV3 {
		t.Fatalf("a measured 1080p source lost its transcode route: %s", ExplainPlannerResultV3(result))
	}
}

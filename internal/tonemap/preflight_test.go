package tonemap

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sync/singleflight"
)

func TestValidateSourceCachesPositiveAndNegativeVerdicts(t *testing.T) {
	tests := []struct {
		name            string
		conversionErr   error
		wantErr         bool
		wantConversions int
	}{
		{name: "positive", wantConversions: 3},
		{name: "negative", conversionErr: errors.New("decode failed"), wantErr: true, wantConversions: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetSourcePreflightCache(t)
			request := sourcePreflightTestRequest(t)
			conversions := 0
			runner := sourcePreflightTestRunner(&conversions, func() string { return "ffmpeg version 1" }, tt.conversionErr)
			for attempt := 0; attempt < 2; attempt++ {
				err := ValidateSourceWithRunner(context.Background(), request, runner)
				if (err != nil) != tt.wantErr {
					t.Fatalf("ValidateSourceWithRunner() error = %v, want error %t", err, tt.wantErr)
				}
			}
			if conversions != tt.wantConversions {
				t.Fatalf("conversion calls = %d, want %d before cached verdict", conversions, tt.wantConversions)
			}
		})
	}
}

func TestValidateSourceCacheInvalidatesExecutorAndSourceFacts(t *testing.T) {
	resetSourcePreflightCache(t)
	request := sourcePreflightTestRequest(t)
	version := "ffmpeg version 1"
	conversions := 0
	runner := sourcePreflightTestRunner(&conversions, func() string { return version }, nil)
	validate := func(label string, request SourcePreflightRequest) {
		t.Helper()
		before := conversions
		if err := ValidateSourceWithRunner(context.Background(), request, runner); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if conversions != before+3 {
			t.Fatalf("%s conversion calls = %d, want %d", label, conversions, before+3)
		}
	}

	validate("initial", request)
	request.RecipeVersion = "2"
	validate("recipe", request)
	request.FFmpegPath = "/opt/ffmpeg"
	validate("binary", request)
	version = "ffmpeg version 2"
	validate("version", request)
	request.HardwareDevice = "/dev/dri/renderD129"
	validate("device", request)
	request.DriverFingerprint = "driver-2"
	validate("driver", request)
	request.SourceRevision.FileHash = "replacement-hash"
	validate("file revision", request)
	request.SourceRevision.ProbeUpdatedUnixNano++
	validate("rescan", request)
	request.SourceRevision.StreamSignature = "replacement-stream"
	validate("stream signature", request)
}

func TestValidateSourceDoesNotCacheWithoutStableRevision(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceRevision)
	}{
		{name: "missing modification time", mutate: func(revision *SourceRevision) { revision.FileModifiedUnixNano = 0 }},
		{name: "missing file hash", mutate: func(revision *SourceRevision) { revision.FileHash = "" }},
		{name: "missing probe revision", mutate: func(revision *SourceRevision) { revision.ProbeUpdatedUnixNano = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetSourcePreflightCache(t)
			request := sourcePreflightTestRequest(t)
			tt.mutate(&request.SourceRevision)
			conversions := 0
			runner := sourcePreflightTestRunner(&conversions, func() string { return "ffmpeg version 1" }, nil)
			for attempt := 0; attempt < 2; attempt++ {
				if err := ValidateSourceWithRunner(context.Background(), request, runner); err != nil {
					t.Fatal(err)
				}
			}
			if conversions != 6 {
				t.Fatalf("conversion calls = %d, want six uncached sample conversions", conversions)
			}
		})
	}
}

func TestValidateSourceChecksEverySampleOutput(t *testing.T) {
	resetSourcePreflightCache(t)
	request := sourcePreflightTestRequest(t)
	conversions := 0
	outputInspections := 0
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if len(args) == 1 && args[0] == "-version" {
			return []byte("ffmpeg version 1"), nil
		}
		if strings.Contains(name, "ffprobe") {
			if !strings.Contains(joined, "stream=codec_name") {
				return []byte(`{"frames":[{"color_range":"tv","color_space":"bt2020nc","color_transfer":"smpte2084","color_primaries":"bt2020"}]}`), nil
			}
			outputInspections++
			sideData := "[]"
			if outputInspections == 2 {
				sideData = `[{"side_data_type":"Mastering display metadata"}]`
			}
			return []byte(`{"streams":[{"codec_name":"h264","pix_fmt":"yuv420p","color_range":"tv","color_space":"bt709","color_transfer":"bt709","color_primaries":"bt709","side_data_list":[]}],"frames":[{"color_range":"tv","color_space":"bt709","color_transfer":"bt709","color_primaries":"bt709","side_data_list":` + sideData + `}]}`), nil
		}
		conversions++
		return nil, nil
	}
	if err := ValidateSourceWithRunner(context.Background(), request, runner); err == nil {
		t.Fatal("preflight accepted HDR metadata in a later sample")
	}
	if conversions != 2 || outputInspections != 2 {
		t.Fatalf("conversions=%d output inspections=%d, want two of each", conversions, outputInspections)
	}
}

func TestSourcePreflightPositionsCoverBeginningMiddleAndEnd(t *testing.T) {
	want := []float64{0, 50, 90}
	got := sourcePreflightPositions(100)
	if !slices.Equal(got, want) {
		t.Fatalf("sourcePreflightPositions() = %v, want %v", got, want)
	}
}

func TestSourceConversionPreflightFilterMapsQSVFramesOnce(t *testing.T) {
	filter := sourceConversionPreflightFilter(SourcePreflightRequest{
		Mode: ModeHardware, Backend: BackendQSV, Kind: SourcePQ,
	})
	if got := strings.Count(filter, "hwmap=derive_device=qsv"); got != 1 {
		t.Fatalf("QSV preflight map count = %d, want 1: %s", got, filter)
	}
	if scale, mapping, metadata := strings.Index(filter, "scale_vaapi"), strings.Index(filter, "hwmap=derive_device=qsv"), strings.Index(filter, "sidedata=mode=delete"); scale < 0 || mapping <= scale || metadata <= mapping {
		t.Fatalf("QSV preflight stages are out of order: %s", filter)
	}
}

func TestSourceConversionPreflightUsesSiloQSVDriverSelection(t *testing.T) {
	args := sourceConversionPreflightArgs(SourcePreflightRequest{
		Mode: ModeHardware, Backend: BackendQSV, Kind: SourcePQ, HardwareDevice: "/dev/dri/renderD129",
	}, 0, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "vaapi=va:/dev/dri/renderD129,driver=iHD,kernel_driver=i915,vendor_id=0x8086") {
		t.Fatalf("QSV preflight did not mirror runtime driver selection: %s", joined)
	}
}

func sourcePreflightTestRequest(t *testing.T) SourcePreflightRequest {
	t.Helper()
	path := t.TempDir() + "/source.mkv"
	if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return SourcePreflightRequest{
		FFmpegPath: "/usr/bin/ffmpeg", FFprobePath: "/usr/bin/ffprobe", InputPath: path,
		DurationSeconds: 100, SourceBitDepth: 10, Mode: ModeSoftware, Backend: BackendSoftware,
		Filter: SoftwareFilterHable, Kind: SourcePQ, RecipeVersion: "1", DriverFingerprint: "driver-1",
		SourceRevision: SourceRevision{
			MediaFileID: 1, FileSize: info.Size(), FileModifiedUnixNano: normalizeRevisionTime(info.ModTime()).UnixNano(),
			FileHash: "hash", ProbeUpdatedUnixNano: 1, StreamSignature: "stream",
		},
	}
}

func sourcePreflightTestRunner(conversions *int, version func() string, conversionErr error) CommandRunner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if len(args) == 1 && args[0] == "-version" {
			return []byte(version()), nil
		}
		if strings.Contains(name, "ffprobe") {
			if !strings.Contains(joined, "stream=codec_name") {
				return []byte(`{"frames":[{"color_range":"tv","color_space":"bt2020nc","color_transfer":"smpte2084","color_primaries":"bt2020"}]}`), nil
			}
			return []byte(`{"streams":[{"codec_name":"h264","pix_fmt":"yuv420p","color_range":"tv","color_space":"bt709","color_transfer":"bt709","color_primaries":"bt709","side_data_list":[]}],"frames":[{"color_range":"tv","color_space":"bt709","color_transfer":"bt709","color_primaries":"bt709","side_data_list":[]}]}`), nil
		}
		*conversions++
		return nil, conversionErr
	}
}

func resetSourcePreflightCache(t *testing.T) {
	t.Helper()
	sourcePreflightCache.Lock()
	sourcePreflightCache.entries = make(map[string]sourcePreflightCacheEntry)
	sourcePreflightCache.group = singleflight.Group{}
	sourcePreflightCache.Unlock()
}

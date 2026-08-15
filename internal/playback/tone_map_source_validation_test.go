package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

func TestStartTranscodeRejectsLivePrimaryVideoMismatchBeforeFFmpeg(t *testing.T) {
	opts, ffmpegMarker := mismatchedToneMapExecutionFixture(t)
	opts.OutputDir = filepath.Join(t.TempDir(), "hls")
	opts.SessionID = "metadata-mismatch"
	opts.SegmentDuration = 2

	session, err := StartTranscode(context.Background(), opts)
	if session != nil {
		_ = session.Close()
	}
	if !errors.Is(err, tonemap.ErrSourceRevisionChanged) {
		t.Fatalf("StartTranscode() error = %v, want ErrSourceRevisionChanged", err)
	}
	if _, statErr := os.Stat(ffmpegMarker); !os.IsNotExist(statErr) {
		t.Fatalf("FFmpeg ran before live source rejection: %v", statErr)
	}
}

func TestPrepareFileRejectsLivePrimaryVideoMismatchBeforeFFmpegOrPublication(t *testing.T) {
	opts, ffmpegMarker := mismatchedToneMapExecutionFixture(t)
	outputPath := filepath.Join(t.TempDir(), "artifact.mp4")

	err := PrepareFile(context.Background(), opts, outputPath)
	if !errors.Is(err, tonemap.ErrSourceRevisionChanged) {
		t.Fatalf("PrepareFile() error = %v, want ErrSourceRevisionChanged", err)
	}
	if _, statErr := os.Stat(ffmpegMarker); !os.IsNotExist(statErr) {
		t.Fatalf("FFmpeg ran before live source rejection: %v", statErr)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("prepared output was published for stale metadata: %v", statErr)
	}
	if _, statErr := os.Stat(outputPath + ".part"); !os.IsNotExist(statErr) {
		t.Fatalf("partial output was created for stale metadata: %v", statErr)
	}
}

func TestReconstructTranscodeRejectsLivePrimaryVideoMismatchBeforeFFmpeg(t *testing.T) {
	opts, ffmpegMarker := mismatchedToneMapExecutionFixture(t)
	opts.SessionID = "metadata-reconstruct"
	opts.SegmentDuration = 2
	manager := NewTranscodeManager()
	manager.Config = func() TranscodeRuntimeConfig {
		return TranscodeRuntimeConfig{TranscodeDir: t.TempDir(), FFmpegPath: opts.FFmpegPath, HWAccel: HWAccelNone}
	}
	manager.resolveToneMapExecutor = func(_ context.Context, reconstructed TranscodeOpts) (TranscodeOpts, error) {
		reconstructed.ToneMapFilter = tonemap.SoftwareFilterBT2390
		reconstructed.HWAccel = HWAccelNone
		return reconstructed, nil
	}
	card := NewRecipeCard(7, "profile-1", opts.ToneMapSourceRevision.MediaFileID, "", opts)

	session, reconstructErr := manager.ReconstructTranscodeWithError(context.Background(), opts.SessionID, -1, card)
	if session != nil {
		_ = session.Close()
		t.Fatal("ReconstructTranscode() started a stale tone-map recipe")
	}
	if !errors.Is(reconstructErr, tonemap.ErrSourceRevisionChanged) {
		t.Fatalf("ReconstructTranscodeWithError() error = %v, want ErrSourceRevisionChanged", reconstructErr)
	}
	if _, statErr := os.Stat(ffmpegMarker); !os.IsNotExist(statErr) {
		t.Fatalf("reconstructed FFmpeg ran before live source rejection: %v", statErr)
	}
}

func TestStartTranscodeClassifiesLiveProbeTimeoutAsTransientBeforeFFmpeg(t *testing.T) {
	opts, ffmpegMarker := mismatchedToneMapExecutionFixture(t)
	opts.OutputDir = filepath.Join(t.TempDir(), "hls")
	opts.SessionID = "metadata-timeout"
	opts.SegmentDuration = 2
	ffprobePath := filepath.Join(filepath.Dir(opts.FFmpegPath), "ffprobe")
	if err := os.WriteFile(ffprobePath, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	session, err := StartTranscode(ctx, opts)
	if session != nil {
		_ = session.Close()
	}
	if !errors.Is(err, ErrToneMapSourceValidationUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartTranscode() error = %v, want transient source-validation deadline", err)
	}
	if _, statErr := os.Stat(ffmpegMarker); !os.IsNotExist(statErr) {
		t.Fatalf("FFmpeg ran after live probe timeout: %v", statErr)
	}
}

func TestToneMapPathHashFailureIsTransient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short-source.mkv")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateToneMapSource(context.Background(), TranscodeOpts{
		InputPath: path, ToneMapMode: tonemap.ModeSoftware,
		ToneMapSourceRevision: tonemap.SourceRevision{MediaFileID: 1, FileSize: 5, FileHash: "0123456789abcdef"},
	})
	if !errors.Is(err, ErrToneMapSourceValidationUnavailable) || errors.Is(err, tonemap.ErrSourceRevisionChanged) {
		t.Fatalf("validateToneMapSource() error = %v, want transient validation unavailable", err)
	}
}

func TestResolveToneMapExecutorClassifiesCanceledProbeAsUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ResolveToneMapExecutor(ctx, TranscodeOpts{
		ToneMapPolicy:        tonemap.PolicySoftwareOnly,
		ToneMapMode:          tonemap.ModeSoftware,
		ToneMapSourceKind:    tonemap.SourcePQ,
		ToneMapRecipeVersion: TransformationHDRToSDRToneMapRecipeVersionV3,
		ToneMapSourceRevision: tonemap.SourceRevision{
			MediaFileID: 1,
			FileSize:    5,
		},
	})
	if !errors.Is(err, ErrToneMapExecutorUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveToneMapExecutor() error = %v, want executor unavailable + canceled", err)
	}
}

func TestClassifyToneMapPreflightErrorPreservesTransientIdentity(t *testing.T) {
	transient := fmt.Errorf("%w: %w", tonemap.ErrSourcePreflightUnavailable, context.DeadlineExceeded)
	err := classifyToneMapPreflightError(transient)
	if !errors.Is(err, ErrToneMapSourceValidationUnavailable) ||
		!errors.Is(err, tonemap.ErrSourcePreflightUnavailable) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transient preflight = %v, want playback, preflight, and deadline identities", err)
	}

	rejected := fmt.Errorf("%w: mismatched decoded frame", tonemap.ErrSourcePreflightRejected)
	err = classifyToneMapPreflightError(rejected)
	if !errors.Is(err, tonemap.ErrSourcePreflightRejected) || errors.Is(err, ErrToneMapSourceValidationUnavailable) {
		t.Fatalf("deterministic preflight = %v, want rejection only", err)
	}
}

func TestNonToneMapStartDoesNotProbeLiveMetadata(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(inputPath, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	probeMarker := filepath.Join(dir, "ffprobe-ran")
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("#!/bin/sh\ntouch '"+probeMarker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	session, err := StartTranscode(context.Background(), TranscodeOpts{
		SessionID: "ordinary", InputPath: inputPath, OutputDir: filepath.Join(dir, "hls"),
		TargetCodecVideo: "h264", TargetCodecAudio: "aac", SegmentDuration: 2, FFmpegPath: ffmpegPath,
	})
	if err != nil {
		t.Fatalf("StartTranscode() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if _, statErr := os.Stat(probeMarker); !os.IsNotExist(statErr) {
		t.Fatalf("non-tone-map start invoked FFprobe: %v", statErr)
	}
}

func mismatchedToneMapExecutionFixture(t *testing.T) (TranscodeOpts, string) {
	t.Helper()
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(inputPath, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	frozenTrack := toneMapValidationTrack("Main 10")
	revision := tonemap.RevisionForFile(&models.MediaFile{
		ID: 42, FileSize: info.Size(), VideoTracks: []models.VideoTrack{frozenTrack},
	})
	ffprobePath := filepath.Join(dir, "ffprobe")
	liveJSON := `{"streams":[{"index":0,"codec_name":"hevc","codec_type":"video","profile":"Main","level":153,"width":3840,"height":2160,"avg_frame_rate":"24000/1001","pix_fmt":"yuv420p10le","color_range":"tv","color_primaries":"bt2020","color_transfer":"smpte2084","color_space":"bt2020nc"}]}`
	if err := os.WriteFile(ffprobePath, []byte("#!/bin/sh\nprintf '%s' '"+liveJSON+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegMarker := filepath.Join(dir, "ffmpeg-ran")
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	ffmpegScript := "#!/bin/sh\ntouch '" + ffmpegMarker + "'\nfor last; do :; done\nprintf output > \"$last\"\n"
	if err := os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return TranscodeOpts{
		InputPath: inputPath, TargetCodecVideo: "h264", TargetCodecAudio: "aac", FFmpegPath: ffmpegPath,
		ToneMapPolicy: tonemap.PolicySoftwareOnly, ToneMapMode: tonemap.ModeSoftware,
		ToneMapSourceKind: tonemap.SourcePQ, ToneMapFilter: tonemap.SoftwareFilterBT2390,
		ToneMapRecipeVersion:  TransformationHDRToSDRToneMapRecipeVersionV3,
		ToneMapSourceRevision: revision,
	}, ffmpegMarker
}

func toneMapValidationTrack(profile string) models.VideoTrack {
	return models.VideoTrack{
		Codec: "hevc", Profile: profile, Level: 153, Width: 3840, Height: 2160, FrameRate: "23.976",
		ColorRange: "tv", ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
		BitDepth: 10, PixelFormat: "yuv420p10le",
	}
}

package tonemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const sourcePreflightTimeout = 30 * time.Second

type SourcePreflightRequest struct {
	FFmpegPath        string
	FFprobePath       string
	InputPath         string
	DurationSeconds   float64
	SourceBitDepth    int
	Mode              Mode
	Backend           string
	Filter            string
	Kind              SourceKind
	RecipeVersion     string
	HardwareDevice    string
	DriverFingerprint string
	SourceRevision    SourceRevision
}

type sourcePreflightCacheEntry struct {
	errorMessage string
}

var sourcePreflightCache = struct {
	sync.Mutex
	entries map[string]sourcePreflightCacheEntry
	group   singleflight.Group
}{entries: make(map[string]sourcePreflightCacheEntry)}

func ValidateSource(ctx context.Context, request SourcePreflightRequest) error {
	return ValidateSourceWithRunner(ctx, request, runCommand)
}

func ValidateSourceWithRunner(ctx context.Context, request SourcePreflightRequest, run CommandRunner) error {
	if request.Kind == "" || request.Mode == "" || strings.TrimSpace(request.InputPath) == "" {
		return errors.New("incomplete tone-map source preflight")
	}
	if err := request.SourceRevision.ValidatePath(request.InputPath); err != nil {
		return err
	}
	if strings.TrimSpace(request.FFmpegPath) == "" {
		request.FFmpegPath = "ffmpeg"
	}
	if strings.TrimSpace(request.FFprobePath) == "" {
		request.FFprobePath = ffprobeForFFmpeg(request.FFmpegPath)
	}

	preflightCtx, cancel := context.WithTimeout(ctx, sourcePreflightTimeout)
	defer cancel()
	key, cacheable := sourcePreflightKey(preflightCtx, request, run)
	if cacheable {
		sourcePreflightCache.Lock()
		entry, ok := sourcePreflightCache.entries[key]
		sourcePreflightCache.Unlock()
		if ok {
			return cachedPreflightError(entry)
		}
		value, _, _ := sourcePreflightCache.group.Do(key, func() (any, error) {
			sourcePreflightCache.Lock()
			entry, ok := sourcePreflightCache.entries[key]
			sourcePreflightCache.Unlock()
			if ok {
				return entry, nil
			}
			entry = sourcePreflightCacheEntry{}
			if err := runSourcePreflight(preflightCtx, request, run); err != nil {
				entry.errorMessage = err.Error()
			}
			if preflightCtx.Err() == nil {
				sourcePreflightCache.Lock()
				sourcePreflightCache.entries[key] = entry
				sourcePreflightCache.Unlock()
			}
			return entry, nil
		})
		entry, ok = value.(sourcePreflightCacheEntry)
		if !ok {
			return errors.New("tone-map source preflight failed")
		}
		return cachedPreflightError(entry)
	}
	return runSourcePreflight(preflightCtx, request, run)
}

func cachedPreflightError(entry sourcePreflightCacheEntry) error {
	if entry.errorMessage == "" {
		return nil
	}
	return errors.New(entry.errorMessage)
}

func sourcePreflightKey(ctx context.Context, request SourcePreflightRequest, run CommandRunner) (string, bool) {
	if !request.SourceRevision.Stable() {
		return "", false
	}
	version, err := runBounded(ctx, run, request.FFmpegPath, "-version")
	if err != nil || len(version) == 0 {
		return "", false
	}
	driver := strings.TrimSpace(request.DriverFingerprint)
	if driver == "" {
		driver = driverFingerprint(request.Backend, request.HardwareDevice)
	}
	executor := strings.Join([]string{
		strings.TrimSpace(request.FFmpegPath),
		hashRevisionValue(string(version)),
		strings.ToLower(strings.TrimSpace(request.Backend)),
		strings.TrimSpace(request.HardwareDevice),
		driver,
		string(request.Mode), string(request.Kind), request.Filter, request.RecipeVersion,
	}, "\x00")
	return request.SourceRevision.Fingerprint() + "\x00" + hashRevisionValue(executor), true
}

func driverFingerprint(backend, device string) string {
	values := []string{strings.ToLower(strings.TrimSpace(backend)), strings.TrimSpace(device)}
	device = firstDevice(device)
	if strings.HasPrefix(device, "/dev/dri/") {
		name := filepath.Base(device)
		for _, path := range []string{
			filepath.Join("/sys/class/drm", name, "device", "uevent"),
			filepath.Join("/sys/class/drm", name, "device", "driver", "module", "version"),
		} {
			if data, err := os.ReadFile(path); err == nil {
				values = append(values, string(data))
			}
		}
	}
	if strings.EqualFold(backend, BackendNVENC) {
		if data, err := os.ReadFile("/proc/driver/nvidia/version"); err == nil {
			values = append(values, string(data))
		}
	}
	return hashRevisionValue(strings.Join(values, "\x00"))
}

func runSourcePreflight(ctx context.Context, request SourcePreflightRequest, run CommandRunner) error {
	positions := sourcePreflightPositions(request.DurationSeconds)
	for _, position := range positions {
		frame, err := inspectSourceFrame(ctx, request, position, run)
		if err != nil {
			return err
		}
		if !frameMatchesSourceKind(frame, request.Kind) {
			return fmt.Errorf("decoded frame metadata does not match %s fallback", request.Kind)
		}
		file, err := os.CreateTemp("", "silo-tonemap-preflight-*.mkv")
		if err != nil {
			return fmt.Errorf("create tone-map preflight output: %w", err)
		}
		outputPath := file.Name()
		if err := file.Close(); err != nil {
			_ = os.Remove(outputPath)
			return fmt.Errorf("close tone-map preflight output: %w", err)
		}
		args := sourceConversionPreflightArgs(request, position, outputPath)
		if output, err := runBounded(ctx, run, request.FFmpegPath, args...); err != nil {
			_ = os.Remove(outputPath)
			return fmt.Errorf("tone-map source conversion failed: %s", boundedCommandFailure(err, output))
		}
		if err := inspectPreflightOutput(ctx, request.FFprobePath, outputPath, run); err != nil {
			_ = os.Remove(outputPath)
			return err
		}
		_ = os.Remove(outputPath)
	}
	return nil
}

func sourcePreflightPositions(duration float64) []float64 {
	positions := []float64{0}
	for _, position := range []float64{duration * 0.5, duration * 0.9} {
		if duration <= 2 || position <= 0 {
			continue
		}
		duplicate := false
		for _, existing := range positions {
			if position-existing < 1 && existing-position < 1 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			positions = append(positions, position)
		}
	}
	return positions
}

type preflightFrame struct {
	ColorRange     string `json:"color_range"`
	ColorSpace     string `json:"color_space"`
	ColorTransfer  string `json:"color_transfer"`
	ColorPrimaries string `json:"color_primaries"`
}

func inspectSourceFrame(ctx context.Context, request SourcePreflightRequest, position float64, run CommandRunner) (preflightFrame, error) {
	interval := strconv.FormatFloat(position, 'f', 3, 64) + "%+#1"
	args := []string{
		"-v", ffmpegErrorLogLevel, "-read_intervals", interval, "-select_streams", "v:0",
		"-show_frames",
		"-show_entries", "frame=color_range,color_space,color_transfer,color_primaries",
		"-of", "json", request.InputPath,
	}
	output, err := runBounded(ctx, run, request.FFprobePath, args...)
	if err != nil {
		return preflightFrame{}, fmt.Errorf("inspect tone-map source frame: %s", boundedCommandFailure(err, output))
	}
	var payload struct {
		Frames []preflightFrame `json:"frames"`
	}
	if err := decodeCommandJSON(output, &payload); err != nil || len(payload.Frames) == 0 {
		return preflightFrame{}, errors.New("tone-map source frame metadata unavailable")
	}
	return payload.Frames[0], nil
}

func frameMatchesSourceKind(frame preflightFrame, kind SourceKind) bool {
	complete, compatible := sourceMetadataCompatibility(kind, SourceMetadata{
		ColorRange: frame.ColorRange, ColorPrimaries: frame.ColorPrimaries,
		ColorTransfer: frame.ColorTransfer, ColorSpace: frame.ColorSpace,
	})
	return complete && compatible
}

func sourceConversionPreflightArgs(request SourcePreflightRequest, position float64, outputPath string) []string {
	args := []string{ffmpegHideBannerArg, ffmpegLogLevelArg, ffmpegErrorLogLevel}
	device := firstDevice(request.HardwareDevice)
	if request.Mode == ModeHardware {
		switch request.Backend {
		case BackendQSV:
			if device == "" {
				device = defaultDRIRenderDevice
			}
			args = append(args, "-init_hw_device", qsvVAAPIInitDevice(device), "-init_hw_device", "qsv=qs@va", "-filter_hw_device", "va", "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi")
		case BackendVAAPI:
			if device == "" {
				device = defaultDRIRenderDevice
			}
			args = append(args, "-init_hw_device", "vaapi=va:"+device, "-filter_hw_device", "va", "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi")
		case BackendNVENC:
			if device == "" {
				device = "0"
			}
			args = append(args, "-init_hw_device", "cuda=cu:"+device, "-filter_hw_device", "cu", "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
		}
	}
	args = append(args, "-ss", strconv.FormatFloat(position, 'f', 3, 64), "-i", request.InputPath, "-map", "0:v:0", "-frames:v", "1", "-an", "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1")
	filter := sourceConversionPreflightFilter(request)
	args = append(args, "-vf", filter, "-c:v", sourcePreflightEncoder(request), "-color_range", "tv", "-color_primaries", colorBT709, "-color_trc", colorBT709, "-colorspace", colorBT709)
	if outputPath == "" {
		return append(args, "-f", "null", "-")
	}
	return append(args, "-f", "matroska", "-y", outputPath)
}

func sourceConversionPreflightFilter(request SourcePreflightRequest) string {
	if request.Mode == ModeSoftware {
		return SoftwareFilter(request.Kind, request.Filter)
	}
	if request.Backend == BackendNVENC {
		if IsSDRSource(request.Kind) {
			format := "nv12"
			if request.SourceBitDepth > 8 {
				format = "p010le"
			}
			return "hwdownload,format=" + format + "," + SoftwareFilter(request.Kind, "") + ",format=nv12,hwupload_cuda"
		}
		return SourceParameters(request.Kind) + "," + CUDAFilter() + "," + HDRMetadataRemovalFilter()
	}
	filter := VAAPIFilter(request.Kind)
	if request.Backend == BackendQSV {
		filter += "," + QSVInteropFilter()
	}
	return filter + "," + HDRMetadataRemovalFilter()
}

func sourcePreflightEncoder(request SourcePreflightRequest) string {
	if request.Mode == ModeSoftware {
		return "libx264"
	}
	return hardwareEncoder(request.Backend)
}

func inspectPreflightOutput(ctx context.Context, ffprobePath, outputPath string, run CommandRunner) error {
	args := []string{
		"-v", ffmpegErrorLogLevel, "-select_streams", "v:0",
		"-show_frames",
		"-show_entries", "stream=codec_name,pix_fmt,color_range,color_space,color_transfer,color_primaries:stream_side_data=side_data_type:frame=color_range,color_space,color_transfer,color_primaries:frame_side_data=side_data_type",
		"-of", "json", outputPath,
	}
	output, err := runBounded(ctx, run, ffprobePath, args...)
	if err != nil {
		return fmt.Errorf("inspect tone-map preflight output: %s", boundedCommandFailure(err, output))
	}
	type sideDataRecord struct {
		Type string `json:"side_data_type"`
	}
	var payload struct {
		Streams []struct {
			CodecName      string           `json:"codec_name"`
			PixelFormat    string           `json:"pix_fmt"`
			ColorRange     string           `json:"color_range"`
			ColorSpace     string           `json:"color_space"`
			ColorTransfer  string           `json:"color_transfer"`
			ColorPrimaries string           `json:"color_primaries"`
			SideData       []sideDataRecord `json:"side_data_list"`
		} `json:"streams"`
		Frames []struct {
			ColorRange     string           `json:"color_range"`
			ColorSpace     string           `json:"color_space"`
			ColorTransfer  string           `json:"color_transfer"`
			ColorPrimaries string           `json:"color_primaries"`
			SideData       []sideDataRecord `json:"side_data_list"`
		} `json:"frames"`
	}
	if err := decodeCommandJSON(output, &payload); err != nil || len(payload.Streams) != 1 {
		return errors.New("tone-map preflight output metadata unavailable")
	}
	stream := payload.Streams[0]
	if stream.CodecName != "h264" || stream.PixelFormat != "yuv420p" || !rangeIsLimited(normalizeColorValue(stream.ColorRange)) ||
		!colorIsBT709(normalizeColorValue(stream.ColorSpace)) || !colorIsBT709(normalizeColorValue(stream.ColorTransfer)) || !colorIsBT709(normalizeColorValue(stream.ColorPrimaries)) {
		return errors.New("tone-map preflight output is not limited-range BT.709 H.264")
	}
	if len(payload.Frames) == 0 {
		return errors.New("tone-map preflight output frame metadata unavailable")
	}
	allSideData := append([]sideDataRecord(nil), stream.SideData...)
	for _, frame := range payload.Frames {
		if complete, compatible := sourceMetadataCompatibility(SourceSDRBT709, SourceMetadata{
			ColorRange: frame.ColorRange, ColorPrimaries: frame.ColorPrimaries,
			ColorTransfer: frame.ColorTransfer, ColorSpace: frame.ColorSpace,
		}); !complete || !compatible {
			return errors.New("tone-map preflight output frame is not limited-range BT.709")
		}
		allSideData = append(allSideData, frame.SideData...)
	}
	for _, sideData := range allSideData {
		if isHDRSideData(sideData.Type) {
			return errors.New("tone-map preflight output retains HDR metadata")
		}
	}
	return nil
}

func isHDRSideData(value string) bool {
	normalized := strings.ToLower(value)
	return strings.Contains(normalized, "dovi") || strings.Contains(normalized, "dolby") ||
		strings.Contains(normalized, "mastering") || strings.Contains(normalized, "content light") ||
		strings.Contains(normalized, "hdr")
}

func ffprobeForFFmpeg(ffmpegPath string) string {
	dir, base := filepath.Split(ffmpegPath)
	if index := strings.Index(strings.ToLower(base), "ffmpeg"); index >= 0 {
		base = base[:index] + "ffprobe" + base[index+len("ffmpeg"):]
		return filepath.Join(dir, base)
	}
	return "ffprobe"
}

func decodeCommandJSON(output []byte, target any) error {
	start := strings.IndexByte(string(output), '{')
	end := strings.LastIndexByte(string(output), '}')
	if start < 0 || end < start {
		return errors.New("JSON payload unavailable")
	}
	return json.Unmarshal(output[start:end+1], target)
}

func boundedCommandFailure(err error, output []byte) string {
	message := err.Error()
	if detail := strings.TrimSpace(string(output)); detail != "" {
		if len(detail) > 512 {
			detail = detail[len(detail)-512:]
		}
		message += ": " + detail
	}
	return message
}

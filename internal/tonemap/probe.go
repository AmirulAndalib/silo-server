package tonemap

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const probeCommandTimeout = 5 * time.Second
const probeTotalTimeout = 30 * time.Second

// One deterministic 64x64 Main 10 HEVC frame. Keeping the compressed fixture
// in the binary lets production probes exercise the real decoder without
// depending on a media mount or generating source files with an encoder whose
// availability is itself under test.
const decodeProbeFixtureBase64 = "AAAAAUABDAH//wIgAAADAJAAAAMAAAMAHpWUCQAAAAFCAQECIAAAAwCQAAADAAADAB6gIIEE2WVlSkwvAWgIAAADAAgAAAMACEAAAAABRAHAc8CJAAABKAGsTtcff/U+nK/q+A=="

type CommandRunner func(context.Context, string, ...string) ([]byte, error)

var probeCache = struct {
	sync.Mutex
	entries map[string]Capabilities
	group   singleflight.Group
}{entries: make(map[string]Capabilities)}

func Probe(ctx context.Context, ffmpegPath, hardwareBackend, hardwareDevice string) Capabilities {
	key := strings.Join([]string{strings.TrimSpace(ffmpegPath), strings.ToLower(strings.TrimSpace(hardwareBackend)), strings.TrimSpace(hardwareDevice)}, "\x00")
	probeCache.Lock()
	if cached, ok := probeCache.entries[key]; ok {
		result := append(Capabilities(nil), cached...)
		probeCache.Unlock()
		return result
	}
	probeCache.Unlock()

	value, _, _ := probeCache.group.Do(key, func() (any, error) {
		probeCache.Lock()
		cached, ok := probeCache.entries[key]
		probeCache.Unlock()
		if ok {
			return append(Capabilities(nil), cached...), nil
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTotalTimeout)
		defer cancel()
		result := ProbeWithRunner(probeCtx, ffmpegPath, hardwareBackend, hardwareDevice, runCommand)
		if probeCtx.Err() != nil {
			return result, nil
		}
		probeCache.Lock()
		probeCache.entries[key] = append(Capabilities(nil), result...)
		probeCache.Unlock()
		return result, nil
	})
	capabilities, ok := value.(Capabilities)
	if !ok {
		return nil
	}
	return append(Capabilities(nil), capabilities...)
}

func ProbeWithRunner(
	ctx context.Context,
	ffmpegPath, hardwareBackend, hardwareDevice string,
	run CommandRunner,
) Capabilities {
	if strings.TrimSpace(ffmpegPath) == "" {
		ffmpegPath = "ffmpeg"
	}
	filters, filterErr := runBounded(ctx, run, ffmpegPath, ffmpegHideBannerArg, "-filters")
	encoders, encoderErr := runBounded(ctx, run, ffmpegPath, ffmpegHideBannerArg, "-encoders")
	if filterErr != nil || encoderErr != nil {
		return Capabilities{}
	}
	fixturePath, cleanupFixture, fixtureErr := writeDecodeProbeFixture()
	if fixtureErr != nil {
		return Capabilities{}
	}
	defer cleanupFixture()

	capabilities := make(Capabilities, 0, 2)
	softwareFilter := ""
	if selected, _ := SelectSoftwareFilter(filters); hasToken(filters, "sidedata") {
		softwareFilter = selected
	}
	if softwareFilter != "" && hasToken(encoders, "libx264") {
		kinds := smokeSourceKinds(ctx, run, ffmpegPath, func(kind SourceKind) []string {
			return softwareSmokeArgs(fixturePath, kind, softwareFilter)
		})
		if len(kinds) > 0 {
			capabilities = append(capabilities, Capability{Mode: ModeSoftware, Backend: BackendSoftware, Filter: softwareFilter, SourceKinds: kinds})
		}
	}

	backend := strings.ToLower(strings.TrimSpace(hardwareBackend))
	if hardwareProbeAvailable(backend, filters, encoders) {
		kinds := hardwareSmokeSourceKinds(ctx, run, ffmpegPath, fixturePath, backend, hardwareDevice)
		if len(kinds) > 0 {
			capabilities = append(capabilities, Capability{Mode: ModeHardware, Backend: backend, Filter: hardwareFilter(backend), SourceKinds: kinds})
		}
	}
	return capabilities
}

func hardwareSmokeSourceKinds(ctx context.Context, run CommandRunner, ffmpegPath, fixturePath, backend, hardwareDevice string) []SourceKind {
	devices := probeDevices(hardwareDevice, backend)
	validated := AllSourceKinds()
	for _, device := range devices {
		supported := smokeSourceKinds(ctx, run, ffmpegPath, func(kind SourceKind) []string {
			return hardwareSmokeArgs(fixturePath, backend, device, kind)
		})
		validated = intersectSourceKinds(validated, supported)
		if len(validated) == 0 {
			break
		}
	}
	return validated
}

func probeDevices(value, backend string) []string {
	parts := strings.Split(value, ",")
	devices := make([]string, 0, len(parts))
	for _, part := range parts {
		if device := strings.TrimSpace(part); device != "" {
			devices = append(devices, device)
		}
	}
	if len(devices) == 0 {
		if backend == BackendNVENC {
			return []string{"0"}
		}
		return []string{defaultDRIRenderDevice}
	}
	return devices
}

func intersectSourceKinds(left, right []SourceKind) []SourceKind {
	result := make([]SourceKind, 0, len(left))
	for _, kind := range left {
		for _, candidate := range right {
			if candidate == kind {
				result = append(result, kind)
				break
			}
		}
	}
	return result
}

func hardwareFilter(backend string) string {
	if backend == BackendNVENC {
		return HardwareFilterCUDA
	}
	return HardwareFilterVAAPI
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func runBounded(ctx context.Context, run CommandRunner, name string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, probeCommandTimeout)
	defer cancel()
	return run(commandCtx, name, args...)
}

func hasToken(output []byte, token string) bool {
	return bytes.Contains(bytes.ToLower(output), []byte(strings.ToLower(token)))
}

func hardwareProbeAvailable(backend string, filters, encoders []byte) bool {
	switch backend {
	case BackendQSV:
		return hasToken(filters, HardwareFilterVAAPI) && hasToken(filters, "scale_vaapi") && hasToken(encoders, "h264_qsv")
	case BackendVAAPI:
		return hasToken(filters, HardwareFilterVAAPI) && hasToken(filters, "scale_vaapi") && hasToken(encoders, "h264_vaapi")
	case BackendNVENC:
		return hasToken(filters, HardwareFilterCUDA) && hasToken(filters, "scale_cuda") && hasToken(encoders, "h264_nvenc")
	default:
		return false
	}
}

func smokeSourceKinds(
	ctx context.Context,
	run CommandRunner,
	ffmpegPath string,
	argsFor func(SourceKind) []string,
) []SourceKind {
	kinds := make([]SourceKind, 0, len(AllSourceKinds()))
	for _, kind := range AllSourceKinds() {
		if _, err := runBounded(ctx, run, ffmpegPath, argsFor(kind)...); err == nil {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func softwareSmokeArgs(fixturePath string, kind SourceKind, filterName string) []string {
	return []string{
		ffmpegHideBannerArg, ffmpegLogLevelArg, ffmpegErrorLogLevel,
		"-f", codecHEVC, "-i", fixturePath,
		"-vf", SoftwareFilter(kind, filterName),
		"-frames:v", "1", "-c:v", "libx264", "-f", "null", "-",
	}
}

func hardwareSmokeArgs(fixturePath, backend, hardwareDevice string, kind SourceKind) []string {
	device := firstDevice(hardwareDevice)
	if device == "" && backend != BackendNVENC {
		device = defaultDRIRenderDevice
	}
	base := []string{ffmpegHideBannerArg, ffmpegLogLevelArg, ffmpegErrorLogLevel}
	switch backend {
	case BackendQSV:
		base = append(base,
			"-init_hw_device", qsvVAAPIInitDevice(device),
			"-init_hw_device", "qsv=qs@va",
			"-filter_hw_device", "va",
			"-hwaccel", BackendVAAPI, "-hwaccel_output_format", BackendVAAPI,
		)
	case BackendVAAPI:
		base = append(base, "-init_hw_device", "vaapi=va:"+device, "-filter_hw_device", "va", "-hwaccel", BackendVAAPI, "-hwaccel_output_format", BackendVAAPI)
	case BackendNVENC:
		cudaDevice := device
		if cudaDevice == "" {
			cudaDevice = "0"
		}
		base = append(base, "-init_hw_device", "cuda=cu:"+cudaDevice, "-filter_hw_device", "cu", "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
	}
	base = append(base,
		"-f", codecHEVC, "-i", fixturePath,
		"-vf", hardwareSmokeFilter(backend, kind),
		"-frames:v", "1", "-c:v", hardwareEncoder(backend), "-f", "null", "-",
	)
	return base
}

func hardwareSmokeFilter(backend string, kind SourceKind) string {
	if backend == BackendNVENC {
		if IsSDRSource(kind) {
			return "hwdownload,format=p010le," + SoftwareFilter(kind, "") + ",format=nv12,hwupload_cuda"
		}
		return SourceParameters(kind) + "," + CUDAFilter() + "," + HDRMetadataRemovalFilter()
	}
	filter := VAAPIFilter(kind)
	if backend == BackendQSV {
		filter += "," + QSVInteropFilter()
	}
	return filter + "," + HDRMetadataRemovalFilter()
}

func writeDecodeProbeFixture() (string, func(), error) {
	data, err := base64.StdEncoding.DecodeString(decodeProbeFixtureBase64)
	if err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp("", "silo-tonemap-probe-*.hevc")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func hardwareEncoder(backend string) string {
	switch backend {
	case BackendQSV:
		return "h264_qsv"
	case BackendVAAPI:
		return "h264_vaapi"
	default:
		return "h264_nvenc"
	}
}

func firstDevice(value string) string {
	if i := strings.IndexByte(value, ','); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}

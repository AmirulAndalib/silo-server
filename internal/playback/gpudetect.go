package playback

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/tonemap"
	"golang.org/x/sync/singleflight"
)

// GOOS names this package and its tests compare runtime.GOOS against.
const (
	directPlayDarwinGOOS  = "darwin"
	directPlayLinuxGOOS   = "linux"
	directPlayWindowsGOOS = "windows"
)

var (
	defaultDRIDir              = "/dev/dri"
	defaultNVIDIAControlDevice = "/dev/nvidiactl"
	defaultNVIDIADeviceGlob    = "/dev/nvidia[0-9]*"
	sysClassDRMDir             = "/sys/class/drm"
	procBootIDPath             = "/proc/sys/kernel/random/boot_id"
	currentGOOS                = runtime.GOOS
	hwProbeCommandTimeout      = 3 * time.Second
	hwProbeNegativeTTL         = 15 * time.Second
	// hwProbeNow is the cache clock; tests advance it instead of sleeping.
	hwProbeNow = time.Now
)

// hwProbeResult records whether one backend was verified end to end on this
// host. reason is populated only for a failure and is operator-facing.
type hwProbeResult struct {
	available bool
	reason    string
}

type hwProbeCacheEntry struct {
	result    hwProbeResult
	expiresAt time.Time
}

var hwProbeCache = struct {
	sync.Mutex
	entries map[string]hwProbeCacheEntry
	group   singleflight.Group
}{
	entries: make(map[string]hwProbeCacheEntry),
}

// DetectedBackend reports one hardware backend that has candidate devices on
// this host, together with the outcome of its FFmpeg verification probe.
type DetectedBackend struct {
	Backend string `json:"backend"`
	// Verified reports whether at least one candidate device passed its probe.
	Verified bool `json:"verified"`
	// Devices lists every candidate considered for this backend, in probe order.
	Devices []string `json:"devices,omitempty"`
	// Device is the candidate whose probe passed. NVENC addresses its GPU
	// through the CUDA runtime, so it stays empty there even when verified.
	Device string `json:"device,omitempty"`
	// Reason explains a failure, attributed per device when several were tried.
	Reason string `json:"reason,omitempty"`
	// Skipped reports that no probe was attempted because none of the
	// backend's candidate devices is accessible to this process — a proxy
	// node reading a cluster-wide hw_device meant for the transcode nodes,
	// not a driver failure. Reason still says which devices were skipped.
	Skipped bool `json:"skipped,omitempty"`
}

// HWAccelInfo describes the detected hardware acceleration capability.
type HWAccelInfo struct {
	Resolved            string               `json:"resolved"`
	RenderDevices       []string             `json:"render_devices"`
	RenderDeviceDetails []RenderDeviceInfo   `json:"render_device_details"`
	IntelDetected       bool                 `json:"intel_detected"`
	DetectedBackends    []DetectedBackend    `json:"detected_backends,omitempty"`
	Source              string               `json:"source"`
	NodeURL             string               `json:"node_url,omitempty"`
	Transformations     []TransformationV3   `json:"transformations,omitempty"`
	ToneMapCapabilities tonemap.Capabilities `json:"tone_map_capabilities,omitempty"`
	// BootID is this host's kernel boot identity (Linux only). Paired with a
	// render device's PCI address it distinguishes "same GPU, same boot" from
	// "same device path on a host that rebooted or was replaced".
	BootID string `json:"boot_id,omitempty"`
	// CapabilityHash summarizes every hardware-identity and capability field
	// below, so a reader can detect change without diffing the whole report.
	// Set by the node that serves the report; see ComputeCapabilityHash.
	CapabilityHash string `json:"capability_hash,omitempty"`
	// ProbeRequestTimeoutMillis is the caller-side budget for this node's
	// effective tone-map probe matrix, including endpoint and transport slack.
	ProbeRequestTimeoutMillis int64 `json:"probe_request_timeout_ms,omitempty"`
}

const (
	probeRequestMinTimeout = 5 * time.Second
	probeRequestMaxTimeout = 5 * time.Minute
)

// NormalizeProbeRequestTimeout bounds a node-advertised probe budget while
// preserving the caller's established fallback for a missing advertisement.
func NormalizeProbeRequestTimeout(millis int64, fallback time.Duration) time.Duration {
	if millis <= 0 {
		return fallback
	}
	if millis < probeRequestMinTimeout.Milliseconds() {
		return probeRequestMinTimeout
	}
	if millis > probeRequestMaxTimeout.Milliseconds() {
		return probeRequestMaxTimeout
	}
	return time.Duration(millis) * time.Millisecond
}

// DetectHWAccel probes this host's GPU hardware and returns structured info.
func DetectHWAccel() HWAccelInfo {
	return DetectHWAccelWithFFmpeg(hwAccelAuto, "", "")
}

// DetectHWAccelWithFFmpeg probes this host's GPU hardware and configured FFmpeg.
func DetectHWAccelWithFFmpeg(hwAccel, ffmpegPath, hwDevice string) HWAccelInfo {
	return DetectHWAccelWithFFmpegContext(context.Background(), hwAccel, ffmpegPath, hwDevice)
}

// DetectHWAccelWithFFmpegContext probes this host without outliving ctx. Unlike
// resolution it verifies every backend with candidate hardware, so an operator
// sees why a present GPU was not selected. Resolved still honors the
// pass-through contract: an explicitly configured backend wins even when its
// probe failed, and the report carries the failure reason.
func DetectHWAccelWithFFmpegContext(ctx context.Context, hwAccel, ffmpegPath, hwDevice string) HWAccelInfo {
	candidates := collectHWCandidates(hwDevice)
	resolved := HWAccelNone
	var detected []DetectedBackend
	if currentGOOS == directPlayLinuxGOOS {
		resolved, detected = walkHWAccelBackends(ctx, ffmpegPath, candidates, false)
	}
	if configured := strings.TrimSpace(hwAccel); configured != "" && configured != hwAccelAuto {
		resolved = configured
	}
	return HWAccelInfo{
		Resolved:            resolved,
		RenderDevices:       candidates.renderDevices,
		RenderDeviceDetails: renderDeviceDetails(candidates.renderDevices),
		IntelDetected:       candidates.intelPresent,
		DetectedBackends:    detected,
		BootID:              detectBootID(),
		Source:              "local",
	}
}

// PickRenderDevice returns the GPU render device path to use.
// If explicit is non-empty, it is returned as-is — multi-device lists are
// resolved to one device by AcquireHWDevice before args are built, so this
// never sees a list on a live path.
// Otherwise, it attempts to discover a render device under /dev/dri/.
// Returns empty string if no device is found (caller should fall back to CPU).
func PickRenderDevice(explicit string) string {
	if explicit != "" {
		return explicit
	}
	dev := detectRenderDevice(defaultDRIDir)
	if dev != "" {
		slog.Info("auto-detected GPU render device", "device", dev)
	}
	return dev
}

// ResolveHWAccelWithFFmpeg resolves "auto" into a concrete acceleration method
// by probing the system and the configured FFmpeg binary.
// Preference order: nvenc > qsv > vaapi > none.
// Non-"auto" values are returned unchanged.
// hwDevice is the configured playback.hw_device value; probes run against it so
// verification covers the device a transcode will actually open.
func ResolveHWAccelWithFFmpeg(hwAccel, ffmpegPath, hwDevice string) string {
	return ResolveHWAccelWithFFmpegContext(context.Background(), hwAccel, ffmpegPath, hwDevice)
}

// ResolveHWAccelWithFFmpegContext resolves auto hardware without allowing any
// FFmpeg capability probe to outlive ctx.
func ResolveHWAccelWithFFmpegContext(ctx context.Context, hwAccel, ffmpegPath, hwDevice string) string {
	if hwAccel != hwAccelAuto {
		return hwAccel
	}
	if currentGOOS != "linux" {
		return HWAccelNone
	}
	resolved, _ := walkHWAccelBackends(ctx, ffmpegPath, collectHWCandidates(hwDevice), true)
	return resolved
}

// hwAccelAuto is the configured hw_accel value that asks this package to pick
// a backend by probing the host, rather than the operator naming one outright.
const hwAccelAuto = "auto"

// hwAccelPreferenceOrder is the auto-resolution order; the first backend whose
// probe passes wins.
var hwAccelPreferenceOrder = []string{transcodeHWNVENC, transcodeHWQSV, transcodeHWVAAPI}

// hwAccelWalkTimeout bounds one full backend walk regardless of how many
// candidate devices a host exposes, so a wedged driver cannot stretch detection
// without limit. tonemap.probeEndpointSlack budgets a capability request for it.
const hwAccelWalkTimeout = 30 * time.Second

// hwCandidates groups the candidate render devices by the backend each one can
// plausibly drive, before any FFmpeg verification.
type hwCandidates struct {
	// renderDevices is this host's full inventory, reported to operators even
	// when probes are pinned to a configured subset.
	renderDevices []string
	nvidia        []string
	intel         []string
	vaapi         []string
	// accessible records, for a configured probe set only, which devices this
	// process can actually open. nil means the set came from discovery, which
	// already filtered on openability. NVENC's empty device string never
	// consults it — CUDA selects its GPU without a render-node path.
	accessible    map[string]bool
	nvidiaPresent bool
	// intelPresent describes the inventory rather than the probe set, so a
	// pinned non-Intel device does not hide an Intel GPU from operators.
	intelPresent bool
}

// collectHWCandidates enumerates render devices once and classifies them by
// sysfs vendor id. Probes run against the configured playback.hw_device set
// when there is one, because that — not whatever sorts first under /dev/dri —
// is what a transcode opens. NVIDIA hardware also counts when only the control
// device is exposed, which is how NVENC-only containers appear.
func collectHWCandidates(configuredDevice string) hwCandidates {
	candidates := hwCandidates{renderDevices: listRenderDevices(defaultDRIDir)}
	probeDevices := ParseHWDeviceSet(configuredDevice).List()
	if len(probeDevices) == 0 {
		probeDevices = candidates.renderDevices
	} else {
		// A configured device this process cannot open can never pass a smoke
		// encode, so it is classified for reporting but never probed. This is
		// the normal state of a proxy node reading the cluster-wide hw_device
		// meant for the transcode nodes.
		candidates.accessible = make(map[string]bool, len(probeDevices))
		for _, device := range probeDevices {
			candidates.accessible[device] = deviceOpenable(device)
		}
	}
	for _, device := range probeDevices {
		switch {
		case isNVIDIADevice(device):
			// NVIDIA render nodes carry no libva driver, so listing one as a
			// VAAPI candidate would only fail a probe another GPU can pass.
			candidates.nvidia = append(candidates.nvidia, device)
		case isIntelDevice(device):
			candidates.intel = append(candidates.intel, device)
			candidates.vaapi = append(candidates.vaapi, device)
		default:
			candidates.vaapi = append(candidates.vaapi, device)
		}
	}
	candidates.nvidiaPresent = len(candidates.nvidia) > 0 || hasNVIDIADevice()
	candidates.intelPresent = len(candidates.intel) > 0
	for _, device := range candidates.renderDevices {
		if candidates.intelPresent {
			break
		}
		candidates.intelPresent = isIntelDevice(device)
	}
	return candidates
}

// devicesFor returns the devices a backend may drive. VAAPI is the generic
// fallback, so every non-NVIDIA candidate belongs to it.
func (c hwCandidates) devicesFor(backend string) []string {
	switch backend {
	case transcodeHWNVENC:
		return c.nvidia
	case transcodeHWQSV:
		return c.intel
	case transcodeHWVAAPI:
		return c.vaapi
	default:
		return nil
	}
}

// presentFor reports whether a backend has hardware worth probing.
func (c hwCandidates) presentFor(backend string) bool {
	if backend == transcodeHWNVENC {
		return c.nvidiaPresent
	}
	return len(c.devicesFor(backend)) > 0
}

// probeDevicesFor returns the devices a backend's smoke encode is tried
// against, in order. NVENC addresses its GPU through the CUDA runtime rather
// than a render node, so it probes once with no device path.
func (c hwCandidates) probeDevicesFor(backend string) []string {
	if backend == transcodeHWNVENC {
		return []string{""}
	}
	return c.devicesFor(backend)
}

// walkHWAccelBackends verifies each backend with candidate hardware in
// preference order and reports the first one whose probe passes. Resolution
// stops there; detection continues so every candidate backend is reported.
func walkHWAccelBackends(ctx context.Context, ffmpegPath string, candidates hwCandidates, stopAtFirstVerified bool) (string, []DetectedBackend) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, hwAccelWalkTimeout)
	defer cancel()
	resolved := ""
	var detected []DetectedBackend
	for _, backend := range hwAccelPreferenceOrder {
		if !candidates.presentFor(backend) {
			continue
		}
		// A caller that has already given up must not start further probes.
		if ctx.Err() != nil {
			break
		}
		entry := verifyHWAccelBackend(ctx, backend, ffmpegPath, candidates)
		if !entry.Verified {
			slog.WarnContext(ctx, "hw_accel=auto: candidate hardware failed its FFmpeg probe",
				"backend", backend, "devices", entry.Devices,
				"ffmpeg", normalizeFFmpegPath(ffmpegPath), "reason", entry.Reason)
		} else if resolved == "" {
			resolved = backend
			slog.InfoContext(ctx, "hw_accel=auto: verified hardware backend", "backend", backend, "device", entry.Device)
		}
		detected = append(detected, entry)
		if resolved != "" && stopAtFirstVerified {
			return resolved, detected
		}
	}
	if resolved == "" {
		slog.InfoContext(ctx, "hw_accel=auto: no verified hardware backend, using software encoding")
		return HWAccelNone, detected
	}
	return resolved, detected
}

// verifyHWAccelBackend probes a backend's candidate devices in order and stops
// at the first one that passes, so a broken GPU sorting ahead of a working one
// does not disable the backend for the whole host.
func verifyHWAccelBackend(ctx context.Context, backend, ffmpegPath string, candidates hwCandidates) DetectedBackend {
	devices := candidates.probeDevicesFor(backend)
	entry := DetectedBackend{Backend: backend, Devices: candidates.devicesFor(backend)}
	reasons := make([]string, 0, len(devices))
	probed := false
	for _, device := range devices {
		if ctx.Err() != nil {
			break
		}
		if !candidates.deviceProbeable(device) {
			reasons = append(reasons, hwProbeFailureReason(len(devices), device, "device not accessible on this node"))
			continue
		}
		probed = true
		available, reason := ffmpegSupportsBackendContext(ctx, backend, ffmpegPath, device)
		if available {
			entry.Verified = true
			entry.Device = device
			return entry
		}
		reasons = append(reasons, hwProbeFailureReason(len(devices), device, reason))
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "hardware detection budget exhausted before probing "+backend)
	}
	entry.Skipped = !probed && len(devices) > 0 && ctx.Err() == nil
	entry.Reason = strings.Join(reasons, "; ")
	return entry
}

// deviceProbeable reports whether a candidate device may be smoke-encoded on.
// The empty device is NVENC's — CUDA needs no render-node path — and a nil map
// means the candidate set came from discovery, which is openable by
// construction.
func (c hwCandidates) deviceProbeable(device string) bool {
	if device == "" || c.accessible == nil {
		return true
	}
	return c.accessible[device]
}

// deviceOpenable mirrors the accessibility filter listRenderDevices applies to
// discovered devices: a device this process cannot open cannot host a probe or
// a transcode.
func deviceOpenable(device string) bool {
	f, err := os.Open(device)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// hwProbeFailureReason attributes a failure to its device only when several
// candidates were tried; a single candidate reads better bare.
func hwProbeFailureReason(candidateCount int, device, reason string) string {
	if candidateCount < 2 || device == "" {
		return reason
	}
	return device + ": " + reason
}

// hwBackendProbe verifies one backend against an FFmpeg binary and candidate
// device. commandCount is the number of bounded commands the probe may run and
// budgets the shared deadline.
type hwBackendProbe struct {
	commandCount int
	run          func(ctx context.Context, ffmpegPath, device string, commandTimeout time.Duration) hwProbeResult
}

func hwBackendProbeFor(backend string) (hwBackendProbe, bool) {
	switch backend {
	case transcodeHWNVENC:
		return hwBackendProbe{commandCount: 4, run: probeFFmpegNVENCContext}, true
	case transcodeHWQSV:
		return hwBackendProbe{commandCount: 3, run: probeFFmpegQSVContext}, true
	case transcodeHWVAAPI:
		return hwBackendProbe{commandCount: 2, run: probeFFmpegVAAPIContext}, true
	default:
		return hwBackendProbe{}, false
	}
}

func ffmpegSupportsBackend(backend, ffmpegPath, device string) (bool, string) {
	return ffmpegSupportsBackendContext(context.Background(), backend, ffmpegPath, device)
}

// ffmpegSupportsBackendContext verifies one backend, coalescing concurrent cold
// probes and reusing a positive result for the process lifetime. A failure is
// retried once its short negative TTL expires, so a driver or binary repaired
// underneath a running server is picked up without a restart.
func ffmpegSupportsBackendContext(ctx context.Context, backend, ffmpegPath, device string) (bool, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	probe, ok := hwBackendProbeFor(backend)
	if !ok {
		return false, "unsupported hardware backend " + backend
	}
	ffmpegPath = normalizeFFmpegPath(ffmpegPath)
	cacheKey := hwProbeCacheKey(ffmpegPath, backend, device)
	// The flight below outlives an abandoned caller, so every test-mutable seam
	// it touches is snapshotted here rather than dereferenced inside it.
	commandTimeout := hwProbeCommandTimeout
	negativeTTL := hwProbeNegativeTTL
	now := hwProbeNow
	hwProbeCache.Lock()
	if entry, ok := hwProbeCache.entries[cacheKey]; ok && hwProbeCacheEntryCurrent(entry, now()) {
		hwProbeCache.Unlock()
		return entry.result.available, entry.result.reason
	}
	hwProbeCache.Unlock()

	resultCh := hwProbeCache.group.DoChan(cacheKey, func() (any, error) {
		hwProbeCache.Lock()
		cached, ok := hwProbeCache.entries[cacheKey]
		hwProbeCache.Unlock()
		if ok && hwProbeCacheEntryCurrent(cached, now()) {
			return cached.result, nil
		}
		probeCtx, cancel := context.WithTimeout(context.Background(), time.Duration(probe.commandCount)*commandTimeout+time.Second)
		defer cancel()
		result := probe.run(probeCtx, ffmpegPath, device, commandTimeout)
		entry := hwProbeCacheEntry{result: result}
		if !result.available {
			entry.expiresAt = now().Add(negativeTTL)
		}
		hwProbeCache.Lock()
		hwProbeCache.entries[cacheKey] = entry
		hwProbeCache.Unlock()
		return result, nil
	})
	select {
	case <-ctx.Done():
		return false, ctx.Err().Error()
	case shared := <-resultCh:
		if shared.Err != nil {
			return false, shared.Err.Error()
		}
		result, ok := shared.Val.(hwProbeResult)
		if !ok {
			return false, "invalid shared hardware probe result"
		}
		return result.available, result.reason
	}
}

func hwProbeCacheEntryCurrent(entry hwProbeCacheEntry, now time.Time) bool {
	return entry.result.available || now.Before(entry.expiresAt)
}

// hwProbeCacheKey separates results per backend and per candidate device on top
// of the FFmpeg binary's identity.
func hwProbeCacheKey(ffmpegPath, backend, device string) string {
	return strings.Join([]string{ffmpegIdentityKey(ffmpegPath), backend, device}, "\x00")
}

// ffmpegIdentityKey invalidates cached capability results when an FFmpeg
// executable is replaced at the same configured path.
func ffmpegIdentityKey(ffmpegPath string) string {
	identityPath := ffmpegPath
	if !strings.ContainsRune(identityPath, os.PathSeparator) {
		if resolved, err := exec.LookPath(identityPath); err == nil {
			identityPath = resolved
		}
	}
	if absolute, err := filepath.Abs(identityPath); err == nil {
		identityPath = absolute
	}
	info, err := os.Stat(identityPath)
	if err != nil {
		return ffmpegPath
	}
	return fmt.Sprintf("%s\x00%d\x00%d", identityPath, info.Size(), info.ModTime().UnixNano())
}

func normalizeFFmpegPath(ffmpegPath string) string {
	ffmpegPath = strings.TrimSpace(ffmpegPath)
	if ffmpegPath == "" {
		ffmpegPath = ffmpegBinary()
	}
	if strings.ContainsRune(ffmpegPath, os.PathSeparator) {
		return filepath.Clean(ffmpegPath)
	}
	return ffmpegPath
}

// probeFFmpegNVENCContext verifies the CUDA decode, scaling, and encode path a
// NVENC transcode depends on. NVENC selects its GPU through CUDA, so the
// candidate device path is not part of the command line.
func probeFFmpegNVENCContext(ctx context.Context, ffmpegPath, device string, commandTimeout time.Duration) hwProbeResult {
	if output, err := runFFmpegProbe(ctx, commandTimeout, ffmpegPath, "-hide_banner", "-hwaccels"); err != nil {
		return hwProbeResult{reason: "hwaccels probe failed: " + FormatFFmpegProbeFailure(err, output)}
	} else if !ffmpegOutputHasToken(output, "cuda") {
		return hwProbeResult{reason: "cuda hwaccel unavailable"}
	}

	if output, err := runFFmpegProbe(ctx, commandTimeout, ffmpegPath, "-hide_banner", "-encoders"); err != nil {
		return hwProbeResult{reason: "encoders probe failed: " + FormatFFmpegProbeFailure(err, output)}
	} else if !ffmpegOutputHasToken(output, encoderH264NVENC) {
		return hwProbeResult{reason: encoderUnavailableReason(encoderH264NVENC)}
	} else if !ffmpegOutputHasToken(output, "hevc_nvenc") {
		return hwProbeResult{reason: "hevc_nvenc encoder unavailable"}
	}

	if output, err := runFFmpegProbe(ctx, commandTimeout, ffmpegPath, "-hide_banner", "-filters"); err != nil {
		return hwProbeResult{reason: "filters probe failed: " + FormatFFmpegProbeFailure(err, output)}
	} else if !ffmpegOutputHasToken(output, "scale_cuda") {
		return hwProbeResult{reason: "scale_cuda filter unavailable"}
	} else if !ffmpegOutputHasToken(output, "hwupload_cuda") {
		return hwProbeResult{reason: "hwupload_cuda filter unavailable"}
	}

	return smokeEncodeResult(ctx, ffmpegPath, transcodeHWNVENC, device, commandTimeout)
}

// probeFFmpegQSVContext verifies the VAAPI-derived QSV chain against a
// candidate Intel render device. Either hwaccel listing is enough: the chain
// initializes a VAAPI display and derives the QSV device from it.
func probeFFmpegQSVContext(ctx context.Context, ffmpegPath, device string, commandTimeout time.Duration) hwProbeResult {
	if output, err := runFFmpegProbe(ctx, commandTimeout, ffmpegPath, "-hide_banner", "-hwaccels"); err != nil {
		return hwProbeResult{reason: "hwaccels probe failed: " + FormatFFmpegProbeFailure(err, output)}
	} else if !ffmpegOutputHasToken(output, transcodeHWQSV) && !ffmpegOutputHasToken(output, transcodeHWVAAPI) {
		return hwProbeResult{reason: "qsv and vaapi hwaccels unavailable"}
	}

	if output, err := runFFmpegProbe(ctx, commandTimeout, ffmpegPath, "-hide_banner", "-encoders"); err != nil {
		return hwProbeResult{reason: "encoders probe failed: " + FormatFFmpegProbeFailure(err, output)}
	} else if !ffmpegOutputHasToken(output, encoderH264QSV) {
		return hwProbeResult{reason: encoderUnavailableReason(encoderH264QSV)}
	} else if !ffmpegOutputHasToken(output, "hevc_qsv") {
		return hwProbeResult{reason: "hevc_qsv encoder unavailable"}
	}

	return smokeEncodeResult(ctx, ffmpegPath, transcodeHWQSV, device, commandTimeout)
}

// probeFFmpegVAAPIContext verifies the generic VAAPI encode path. VAAPI is the
// last fallback and only needs H.264 encoding to be useful, so the listing gate
// stays narrower than QSV's.
func probeFFmpegVAAPIContext(ctx context.Context, ffmpegPath, device string, commandTimeout time.Duration) hwProbeResult {
	if output, err := runFFmpegProbe(ctx, commandTimeout, ffmpegPath, "-hide_banner", "-encoders"); err != nil {
		return hwProbeResult{reason: "encoders probe failed: " + FormatFFmpegProbeFailure(err, output)}
	} else if !ffmpegOutputHasToken(output, encoderH264VAAPI) {
		return hwProbeResult{reason: encoderUnavailableReason(encoderH264VAAPI)}
	}

	return smokeEncodeResult(ctx, ffmpegPath, transcodeHWVAAPI, device, commandTimeout)
}

// smokeEncodeResult runs the backend's bounded single-frame encode, which is
// the only step that exercises the driver rather than FFmpeg's build flags.
func smokeEncodeResult(ctx context.Context, ffmpegPath, backend, device string, commandTimeout time.Duration) hwProbeResult {
	output, err := runFFmpegProbe(ctx, commandTimeout, ffmpegPath, hardwareSmokeEncodeArgs(backend, device)...)
	if err != nil {
		return hwProbeResult{reason: hardwareEncoder(backend) + " smoke encode failed: " + FormatFFmpegProbeFailure(err, output)}
	}
	return hwProbeResult{available: true}
}

// H.264 encoder names FFmpeg reports for each hardware backend.
const (
	encoderH264QSV   = "h264_qsv"
	encoderH264VAAPI = "h264_vaapi"
	encoderH264NVENC = "h264_nvenc"
)

// encoderUnavailableReason reports that a probe's -encoders listing did not
// include the given encoder.
func encoderUnavailableReason(encoder string) string {
	return encoder + " encoder unavailable"
}

// hardwareEncoder returns the H.264 encoder paired with a backend.
func hardwareEncoder(backend string) string {
	switch backend {
	case transcodeHWQSV:
		return encoderH264QSV
	case transcodeHWVAAPI:
		return encoderH264VAAPI
	default:
		return encoderH264NVENC
	}
}

func runFFmpegProbe(ctx context.Context, timeout time.Duration, ffmpegPath string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput()
}

func ffmpegOutputHasToken(output []byte, token string) bool {
	for _, field := range strings.Fields(string(output)) {
		if strings.EqualFold(field, token) {
			return true
		}
	}
	return false
}

// FormatFFmpegProbeFailure combines a probe error with bounded command output.
func FormatFFmpegProbeFailure(err error, output []byte) string {
	message := strings.TrimSpace(err.Error())
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		if len(trimmed) > 240 {
			trimmed = trimmed[:240] + "..."
		}
		message += ": " + trimmed
	}
	return message
}

// listRenderDevices returns all accessible /dev/dri/renderD* paths, sorted.
func listRenderDevices(driDir string) []string {
	pattern := filepath.Join(driDir, "renderD*")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)

	var accessible []string
	for _, dev := range matches {
		if f, err := os.Open(dev); err == nil {
			f.Close()
			accessible = append(accessible, dev)
		}
	}
	return accessible
}

// isIntelDevice checks whether a render device belongs to an Intel GPU by
// reading the PCI vendor ID from sysfs. Intel vendor ID is 0x8086.
func isIntelDevice(renderDevPath string) bool {
	// /dev/dri/renderD128 → card name "renderD128"
	name := filepath.Base(renderDevPath)
	vendorPath := filepath.Join(sysClassDRMDir, name, "device", "vendor")
	data, err := os.ReadFile(vendorPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "0x8086"
}

// isNVIDIADevice checks whether a render device belongs to an NVIDIA GPU by
// reading the PCI vendor ID from sysfs. NVIDIA vendor ID is 0x10de.
func isNVIDIADevice(renderDevPath string) bool {
	name := filepath.Base(renderDevPath)
	vendorPath := filepath.Join(sysClassDRMDir, name, "device", "vendor")
	data, err := os.ReadFile(vendorPath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "0x10de"
}

func hasNVIDIADevice() bool {
	if file, err := os.Open(defaultNVIDIAControlDevice); err == nil {
		file.Close()
		return true
	}
	matches, err := filepath.Glob(defaultNVIDIADeviceGlob)
	if err != nil || len(matches) == 0 {
		return false
	}
	for _, dev := range matches {
		if file, err := os.Open(dev); err == nil {
			file.Close()
			return true
		}
	}
	return false
}

// detectRenderDevice enumerates /dev/dri/renderD* and returns the first
// available device, or empty string if none found.
func detectRenderDevice(driDir string) string {
	devices := listRenderDevices(driDir)
	if len(devices) > 0 {
		return devices[0]
	}
	return ""
}

// RenderDeviceInfo describes one render device for operator-facing surfaces.
type RenderDeviceInfo struct {
	Path string `json:"path"`
	// PCIAddress is the device's sysfs PCI slot (e.g. 0000:03:00.0). It is
	// stable across reboots for a card that stays in its slot, which /dev/dri
	// paths are not, so it — not Path — identifies the hardware.
	PCIAddress string `json:"pci_address,omitempty"`
	// GPUUUID is NVIDIA's own permanent GPU identity, reported only when
	// nvidia-smi is installed. It survives a card moving between slots and
	// hosts, so it outranks PCIAddress wherever both are present.
	GPUUUID     string `json:"gpu_uuid,omitempty"`
	Description string `json:"description"`
}

// describeRenderDevice builds a short human label for a render device from
// its sysfs PCI vendor/device ids; best-effort, never fails.
func describeRenderDevice(renderDevPath string) string {
	name := filepath.Base(renderDevPath)
	vendor := readSysfsID(filepath.Join(sysClassDRMDir, name, "device", "vendor"))
	label := ""
	switch vendor {
	case "0x8086":
		label = "Intel GPU"
	case "0x10de":
		label = "NVIDIA GPU"
	case "0x1002":
		label = "AMD GPU"
	case "":
		return "GPU"
	default:
		label = "GPU (vendor " + vendor + ")"
	}
	if device := readSysfsID(filepath.Join(sysClassDRMDir, name, "device", "device")); device != "" && vendor != "0x1002" {
		label += " (" + device + ")"
	}
	return label
}

func readSysfsID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// RenderDeviceIdentity is a render device's hardware identity, without the
// probing that a full capability report performs.
type RenderDeviceIdentity struct {
	// Path is the render node, e.g. /dev/dri/renderD128.
	Path string
	// PCIAddress is the sysfs PCI slot, e.g. 0000:03:00.0.
	PCIAddress string
	// Vendor is "intel", "nvidia", "amd", or empty when sysfs names one we do
	// not recognize.
	Vendor string
}

// RenderDeviceIdentities enumerates this host's render devices with the sysfs
// identity of each.
//
// It exists for callers that need to correlate a device across surfaces —
// notably resource sampling, which learns about GPUs by PCI address from DRM
// fdinfo and has to name them the way the rest of the server does. It runs no
// ffmpeg probe and takes no lock: it is a sysfs read, cheap enough to call on a
// sampling interval, unlike DetectHWAccelWithFFmpeg.
func RenderDeviceIdentities() []RenderDeviceIdentity {
	if currentGOOS != "linux" {
		return nil
	}
	devices := listRenderDevices(defaultDRIDir)
	identities := make([]RenderDeviceIdentity, 0, len(devices))
	for _, device := range devices {
		identities = append(identities, RenderDeviceIdentity{
			Path:       device,
			PCIAddress: renderDevicePCIAddress(device),
			Vendor:     renderDeviceVendor(device),
		})
	}
	return identities
}

// renderDeviceVendor maps a device's sysfs PCI vendor id to a short label.
func renderDeviceVendor(renderDevPath string) string {
	name := filepath.Base(renderDevPath)
	switch readSysfsID(filepath.Join(sysClassDRMDir, name, "device", "vendor")) {
	case "0x8086":
		return "intel"
	case "0x10de":
		return "nvidia"
	case "0x1002":
		return "amd"
	default:
		return ""
	}
}

// renderDeviceDetails describes every listed device.
func renderDeviceDetails(devices []string) []RenderDeviceInfo {
	details := make([]RenderDeviceInfo, 0, len(devices))
	for _, device := range devices {
		pciAddress := renderDevicePCIAddress(device)
		details = append(details, RenderDeviceInfo{
			Path:        device,
			PCIAddress:  pciAddress,
			GPUUUID:     renderDeviceGPUUUID(device, pciAddress),
			Description: describeRenderDevice(device),
		})
	}
	return details
}

// renderDevicePCIAddress resolves the sysfs device symlink behind a render node
// and returns its PCI slot. Best effort: an unresolvable link (a virtual or
// non-PCI device, a restricted sysfs) yields an empty address rather than an
// error, because a missing identity only weakens inventory, never breaks it.
func renderDevicePCIAddress(renderDevPath string) string {
	name := filepath.Base(renderDevPath)
	devicePath := filepath.Join(sysClassDRMDir, name, "device")
	// sysfs exposes this as a symlink into the PCI tree. Anything else is a
	// device with no PCI identity, and its own directory name would be "device".
	info, err := os.Lstat(devicePath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		return ""
	}
	return filepath.Base(resolved)
}

// renderDeviceGPUUUID returns NVIDIA's permanent GPU identity for a render
// device. Only NVIDIA-vendor devices are looked up: no other vendor publishes
// such an id, so querying for them would only cost a subprocess.
func renderDeviceGPUUUID(renderDevPath, pciAddress string) string {
	if pciAddress == "" || !isNVIDIADevice(renderDevPath) {
		return ""
	}
	return nvidiaGPUUUIDsByPCIAddress()[normalizePCIAddress(pciAddress)]
}

// nvidiaSMIQueryTimeout bounds the single nvidia-smi invocation. A wedged
// driver makes nvidia-smi hang, and hardware inventory must not inherit that.
var nvidiaSMIQueryTimeout = 3 * time.Second

// nvidiaSMIQuery is the execution seam for the GPU uuid listing; tests replace
// it rather than installing a fake binary on PATH.
var nvidiaSMIQuery = runNVIDIASMIQuery

// nvidiaGPUUUIDs caches the one nvidia-smi listing this process makes. GPU
// identities cannot change without a reboot, so a second query could only cost
// a subprocess to learn the same answer.
var nvidiaGPUUUIDs struct {
	once  sync.Once
	byPCI map[string]string
}

func runNVIDIASMIQuery(ctx context.Context) ([]byte, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, path, "--query-gpu=uuid,pci.bus_id", "--format=csv,noheader").Output()
}

func nvidiaGPUUUIDsByPCIAddress() map[string]string {
	nvidiaGPUUUIDs.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), nvidiaSMIQueryTimeout)
		defer cancel()
		output, err := nvidiaSMIQuery(ctx)
		if err != nil {
			// Expected on every host without the NVIDIA toolkit installed, so
			// this stays at debug: the report is complete without it.
			slog.Debug("nvidia-smi gpu identity query unavailable", "component", "playback", "error", err)
			return
		}
		nvidiaGPUUUIDs.byPCI = parseNVIDIAGPUUUIDs(output)
	})
	return nvidiaGPUUUIDs.byPCI
}

// parseNVIDIAGPUUUIDs reads "csv,noheader" rows of "<uuid>, <pci bus id>" and
// keys them by normalized PCI address. Malformed rows are skipped.
func parseNVIDIAGPUUUIDs(output []byte) map[string]string {
	byPCI := make(map[string]string)
	for line := range strings.Lines(string(output)) {
		uuid, address, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		uuid = strings.TrimSpace(uuid)
		address = normalizePCIAddress(address)
		if uuid == "" || address == "" {
			continue
		}
		byPCI[address] = uuid
	}
	return byPCI
}

// normalizePCIAddress makes sysfs and nvidia-smi addresses comparable:
// sysfs prints a 16-bit domain (0000:03:00.0) and nvidia-smi a 32-bit one
// (00000000:03:00.0), and neither guarantees a case.
func normalizePCIAddress(address string) string {
	address = strings.ToLower(strings.TrimSpace(address))
	domain, rest, ok := strings.Cut(address, ":")
	if !ok {
		return address
	}
	value, err := strconv.ParseUint(domain, 16, 64)
	if err != nil {
		return address
	}
	return fmt.Sprintf("%04x:%s", value, rest)
}

// detectBootID reads the kernel's per-boot identity. It is Linux-only and
// best effort: an empty value simply means device identities cannot be scoped
// to a boot on this host.
func detectBootID() string {
	if currentGOOS != "linux" {
		return ""
	}
	data, err := os.ReadFile(procBootIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

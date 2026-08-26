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
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/tonemap"
	"golang.org/x/sync/singleflight"
)

var (
	defaultDRIDir              = "/dev/dri"
	defaultNVIDIAControlDevice = "/dev/nvidiactl"
	defaultNVIDIADeviceGlob    = "/dev/nvidia[0-9]*"
	sysClassDRMDir             = "/sys/class/drm"
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
	return DetectHWAccelWithFFmpeg("auto", "", "")
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
	if currentGOOS == "linux" {
		resolved, detected = walkHWAccelBackends(ctx, ffmpegPath, candidates, false)
	}
	if configured := strings.TrimSpace(hwAccel); configured != "" && configured != "auto" {
		resolved = configured
	}
	return HWAccelInfo{
		Resolved:            resolved,
		RenderDevices:       candidates.renderDevices,
		RenderDeviceDetails: renderDeviceDetails(candidates.renderDevices),
		IntelDetected:       candidates.intelPresent,
		DetectedBackends:    detected,
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
	if hwAccel != "auto" {
		return hwAccel
	}
	if currentGOOS != "linux" {
		return HWAccelNone
	}
	resolved, _ := walkHWAccelBackends(ctx, ffmpegPath, collectHWCandidates(hwDevice), true)
	return resolved
}

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
			slog.Warn("hw_accel=auto: candidate hardware failed its FFmpeg probe",
				"backend", backend, "devices", entry.Devices,
				"ffmpeg", normalizeFFmpegPath(ffmpegPath), "reason", entry.Reason)
		} else if resolved == "" {
			resolved = backend
			slog.Info("hw_accel=auto: verified hardware backend", "backend", backend, "device", entry.Device)
		}
		detected = append(detected, entry)
		if resolved != "" && stopAtFirstVerified {
			return resolved, detected
		}
	}
	if resolved == "" {
		slog.Info("hw_accel=auto: no verified hardware backend, using software encoding")
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
	for _, device := range devices {
		if ctx.Err() != nil {
			break
		}
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
	entry.Reason = strings.Join(reasons, "; ")
	return entry
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
	} else if !ffmpegOutputHasToken(output, "h264_nvenc") {
		return hwProbeResult{reason: "h264_nvenc encoder unavailable"}
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
	} else if !ffmpegOutputHasToken(output, "h264_qsv") {
		return hwProbeResult{reason: "h264_qsv encoder unavailable"}
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
	} else if !ffmpegOutputHasToken(output, "h264_vaapi") {
		return hwProbeResult{reason: "h264_vaapi encoder unavailable"}
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

// hardwareEncoder returns the H.264 encoder paired with a backend.
func hardwareEncoder(backend string) string {
	switch backend {
	case transcodeHWQSV:
		return "h264_qsv"
	case transcodeHWVAAPI:
		return "h264_vaapi"
	default:
		return "h264_nvenc"
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
	Path        string `json:"path"`
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

// renderDeviceDetails describes every listed device.
func renderDeviceDetails(devices []string) []RenderDeviceInfo {
	details := make([]RenderDeviceInfo, 0, len(devices))
	for _, device := range devices {
		details = append(details, RenderDeviceInfo{
			Path:        device,
			Description: describeRenderDevice(device),
		})
	}
	return details
}

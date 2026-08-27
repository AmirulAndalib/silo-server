package playback

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type hwAccelTestEnv struct {
	devDir string
	driDir string
	sysDir string
}

type fakeFFmpegProbe struct {
	cuda         bool
	qsvHWAccel   bool
	vaapiHWAccel bool
	h264NVENC    bool
	hevcNVENC    bool
	h264QSV      bool
	hevcQSV      bool
	h264VAAPI    bool
	scaleCUDA    bool
	uploadCUDA   bool
	smokeOK      bool
	// smokeFailures names encoders whose smoke encode fails even when smokeOK
	// is set, modeling a listed encoder with no working driver behind it.
	smokeFailures []string
	// smokeDeviceFailures names render devices (by basename) whose smoke encode
	// fails, modeling one broken GPU on a host that has another working one.
	smokeDeviceFailures []string
	hang                bool
	delay               time.Duration
}

type fakeFFmpegBinary struct {
	path    string
	logPath string
}

func TestResolveHWAccelWithFFmpegAutoPrefersNVENCOverIntel(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x10de")
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "nvenc" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want nvenc", got)
	}
}

func TestResolveHWAccelWithFFmpegFallsBackToIntelWhenNVENCProbeFails(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x10de")
	ffmpeg := writeFakeFFmpeg(t, successfulQSVProbe())

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "qsv" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want qsv", got)
	}
}

func TestResolveHWAccelWithFFmpegFallsBackToVAAPIWhenNVENCProbeFails(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x10de")
	env.addRenderDevice(t, "renderD129", "0x1002")
	ffmpeg := writeFakeFFmpeg(t, successfulVAAPIProbe())

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "vaapi" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want vaapi", got)
	}
}

func TestResolveHWAccelWithFFmpegFallsBackToVAAPIWhenQSVListingFails(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	probe := successfulVAAPIProbe()
	probe.qsvHWAccel = true
	probe.h264QSV = true
	// hevc_qsv is missing, so the QSV listing gate rejects an Intel GPU that
	// VAAPI can still drive.
	ffmpeg := writeFakeFFmpeg(t, probe)

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "vaapi" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want vaapi", got)
	}
}

func TestResolveHWAccelWithFFmpegTriesEveryCandidateDevice(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x1002")
	env.addRenderDevice(t, "renderD129", "0x1002")
	probe := successfulVAAPIProbe()
	// The GPU that sorts first has no working driver; the second one does.
	probe.smokeDeviceFailures = []string{"renderD128"}
	ffmpeg := writeFakeFFmpeg(t, probe)

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "vaapi" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want vaapi from the working device", got)
	}
	info := DetectHWAccelWithFFmpeg("auto", ffmpeg.path, "")
	vaapi := info.DetectedBackends[len(info.DetectedBackends)-1]
	if vaapi.Backend != "vaapi" || !vaapi.Verified {
		t.Fatalf("vaapi entry = %+v, want verified", vaapi)
	}
	if device := filepath.Base(vaapi.Device); device != "renderD129" {
		t.Fatalf("verified device = %q, want renderD129", device)
	}
}

func TestResolveHWAccelWithFFmpegSkipsNVIDIANodesAsVAAPIDevices(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x10de")
	env.addRenderDevice(t, "renderD129", "0x1002")
	probe := successfulVAAPIProbe()
	// An NVIDIA render node has no libva driver; probing it would reject a
	// backend the AMD card can drive.
	probe.smokeDeviceFailures = []string{"renderD128"}
	ffmpeg := writeFakeFFmpeg(t, probe)

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "vaapi" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want vaapi from the AMD device", got)
	}
	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "vaapi=hw:"+filepath.Join(env.driDir, "renderD128")) {
		t.Fatalf("VAAPI probe used the NVIDIA render node; log:\n%s", logData)
	}
}

func TestResolveHWAccelWithFFmpegProbesTheConfiguredHWDevice(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x8086")
	probe := successfulQSVProbe()
	probe.smokeDeviceFailures = []string{"renderD128"}
	ffmpeg := writeFakeFFmpeg(t, probe)

	// The operator pinned the working GPU, which is the device a transcode
	// opens; auto resolution has to verify that one rather than renderD128.
	pinned := filepath.Join(env.driDir, "renderD129")
	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, pinned); got != "qsv" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want qsv on the pinned device", got)
	}
	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), filepath.Join(env.driDir, "renderD128")) {
		t.Fatalf("probe touched an unconfigured device; log:\n%s", logData)
	}
}

func TestDetectHWAccelReportsHostInventoryBehindAPinnedDevice(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x1002")
	ffmpeg := writeFakeFFmpeg(t, successfulVAAPIProbe())

	info := DetectHWAccelWithFFmpeg("auto", ffmpeg.path, filepath.Join(env.driDir, "renderD129"))
	if info.Resolved != "vaapi" {
		t.Fatalf("Resolved = %q, want vaapi from the pinned AMD device", info.Resolved)
	}
	// Pinning a device narrows what is probed, never what is reported.
	if !info.IntelDetected {
		t.Fatal("IntelDetected = false, want the host's Intel GPU still reported")
	}
	if len(info.RenderDevices) != 2 {
		t.Fatalf("RenderDevices = %v, want the full host inventory", info.RenderDevices)
	}
}

// A proxy node reads the cluster-wide hw_device meant for the transcode nodes:
// the paths and their sysfs vendor entries are visible, but the devices cannot
// be opened. Detection must skip the probes entirely — no ffmpeg spawn, no
// alarming driver error — and say why.
func TestDetectHWAccelSkipsConfiguredDevicesItCannotOpen(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x8086")
	configured := filepath.Join(env.driDir, "renderD128") + "," + filepath.Join(env.driDir, "renderD129")
	for _, name := range []string{"renderD128", "renderD129"} {
		if err := os.Remove(filepath.Join(env.driDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	info := DetectHWAccelWithFFmpeg("auto", ffmpeg.path, configured)
	if info.Resolved != HWAccelNone {
		t.Fatalf("Resolved = %q, want none", info.Resolved)
	}
	if len(info.DetectedBackends) == 0 {
		t.Fatal("DetectedBackends is empty, want skipped qsv/vaapi entries")
	}
	for _, backend := range info.DetectedBackends {
		if !backend.Skipped {
			t.Fatalf("backend %q Skipped = false, want true", backend.Backend)
		}
		if backend.Verified {
			t.Fatalf("backend %q Verified = true, want false", backend.Backend)
		}
		if !strings.Contains(backend.Reason, "not accessible") {
			t.Fatalf("backend %q Reason = %q, want an accessibility reason", backend.Backend, backend.Reason)
		}
	}
	if logData, err := os.ReadFile(ffmpeg.logPath); err == nil && len(strings.TrimSpace(string(logData))) > 0 {
		t.Fatalf("ffmpeg was spawned for inaccessible devices; log:\n%s", logData)
	}
}

// One configured device is gone, the other works: the accessible one must
// still be probed and win, and the missing one must not be smoke-encoded.
func TestDetectHWAccelProbesOnlyTheAccessibleConfiguredDevices(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x8086")
	if err := os.Remove(filepath.Join(env.driDir, "renderD128")); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(env.driDir, "renderD128") + "," + filepath.Join(env.driDir, "renderD129")
	ffmpeg := writeFakeFFmpeg(t, successfulQSVProbe())

	info := DetectHWAccelWithFFmpeg("auto", ffmpeg.path, configured)
	if info.Resolved != "qsv" {
		t.Fatalf("Resolved = %q, want qsv from the accessible device", info.Resolved)
	}
	var qsv *DetectedBackend
	for i := range info.DetectedBackends {
		if info.DetectedBackends[i].Backend == "qsv" {
			qsv = &info.DetectedBackends[i]
		}
	}
	if qsv == nil || qsv.Skipped || !qsv.Verified {
		t.Fatalf("qsv entry = %+v, want verified and not skipped", qsv)
	}
	if qsv.Device != filepath.Join(env.driDir, "renderD129") {
		t.Fatalf("qsv Device = %q, want the accessible renderD129", qsv.Device)
	}
	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), filepath.Join(env.driDir, "renderD128")) {
		t.Fatalf("probe touched the inaccessible device; log:\n%s", logData)
	}
}

func TestResolveHWAccelWithFFmpegReturnsNoneWhenVAAPISmokeEncodeFails(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x1002")
	probe := successfulVAAPIProbe()
	probe.smokeFailures = []string{"h264_vaapi"}
	ffmpeg := writeFakeFFmpeg(t, probe)

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != HWAccelNone {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want none", got)
	}
}

func TestResolveHWAccelWithFFmpegReturnsNoneWhenNVENCProbeFailsWithoutFallback(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x10de")
	ffmpeg := writeFakeFFmpeg(t, fakeFFmpegProbe{})

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "none" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want none", got)
	}
}

func TestResolveHWAccelWithFFmpegUsesNVIDIADeviceNodesWithoutDRM(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addNVIDIADevice(t, "nvidia0")
	ffmpeg := writeFakeFFmpeg(t, successfulNVENCProbe())

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "nvenc" {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want nvenc", got)
	}
}

func TestResolveHWAccelPassesThroughConfiguredBackends(t *testing.T) {
	setupHWAccelTest(t)

	for _, configured := range []string{"nvenc", "qsv", "vaapi", "none", "custom"} {
		t.Run(configured, func(t *testing.T) {
			if got := ResolveHWAccelWithFFmpeg(configured, "/does/not/exist/ffmpeg", ""); got != configured {
				t.Fatalf("ResolveHWAccelWithFFmpeg(%q) = %q, want unchanged", configured, got)
			}
		})
	}
}

func TestResolveHWAccelAutoIsNoneOffLinux(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	currentGOOS = "darwin"
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != HWAccelNone {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want none", got)
	}
	info := DetectHWAccelWithFFmpeg("auto", ffmpeg.path, "")
	if info.Resolved != HWAccelNone {
		t.Fatalf("DetectHWAccelWithFFmpeg().Resolved = %q, want none", info.Resolved)
	}
	if len(info.DetectedBackends) != 0 {
		t.Fatalf("DetectHWAccelWithFFmpeg().DetectedBackends = %+v, want empty off Linux", info.DetectedBackends)
	}
	if _, err := os.Stat(ffmpeg.logPath); !os.IsNotExist(err) {
		t.Fatalf("off-Linux detection ran FFmpeg probes (stat err = %v)", err)
	}
}

func TestDetectHWAccelReportsEveryCandidateBackend(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x10de")
	probe := fullyCapableProbe()
	probe.h264NVENC = false
	probe.smokeFailures = []string{"h264_vaapi"}
	ffmpeg := writeFakeFFmpeg(t, probe)

	info := DetectHWAccelWithFFmpeg("auto", ffmpeg.path, "")
	if info.Resolved != "qsv" {
		t.Fatalf("Resolved = %q, want qsv", info.Resolved)
	}
	if len(info.DetectedBackends) != 3 {
		t.Fatalf("DetectedBackends = %+v, want one entry per candidate backend", info.DetectedBackends)
	}
	// The NVIDIA render node carries no libva driver, so it is not a VAAPI
	// candidate even though it is a render device.
	want := []DetectedBackend{
		{Backend: "nvenc", Verified: false, Devices: []string{"/dev/dri/renderD129"}},
		{Backend: "qsv", Verified: true, Devices: []string{"/dev/dri/renderD128"}},
		{Backend: "vaapi", Verified: false, Devices: []string{"/dev/dri/renderD128"}},
	}
	for i, expected := range want {
		got := info.DetectedBackends[i]
		if got.Backend != expected.Backend || got.Verified != expected.Verified {
			t.Fatalf("DetectedBackends[%d] = %+v, want backend %q verified=%v", i, got, expected.Backend, expected.Verified)
		}
		if !slices.Equal(stripDevicePrefix(got.Devices), stripDevicePrefix(expected.Devices)) {
			t.Fatalf("DetectedBackends[%d].Devices = %v, want %v", i, got.Devices, expected.Devices)
		}
		if expected.Verified && got.Reason != "" {
			t.Fatalf("DetectedBackends[%d].Reason = %q, want empty for a verified backend", i, got.Reason)
		}
		if !expected.Verified && got.Reason == "" {
			t.Fatalf("DetectedBackends[%d].Reason is empty, want a failure explanation", i)
		}
	}
	if device := filepath.Base(info.DetectedBackends[1].Device); device != "renderD128" {
		t.Fatalf("qsv verified device = %q, want the Intel render node", device)
	}
	if reason := info.DetectedBackends[0].Reason; reason != "h264_nvenc encoder unavailable" {
		t.Fatalf("nvenc reason = %q, want the missing encoder", reason)
	}
	if reason := info.DetectedBackends[2].Reason; !strings.HasPrefix(reason, "h264_vaapi smoke encode failed") {
		t.Fatalf("vaapi reason = %q, want the failed smoke encode", reason)
	}
}

func TestDetectHWAccelOmitsBackendsWithoutCandidateHardware(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	info := DetectHWAccelWithFFmpeg("auto", ffmpeg.path, "")
	backends := make([]string, 0, len(info.DetectedBackends))
	for _, entry := range info.DetectedBackends {
		backends = append(backends, entry.Backend)
	}
	if !slices.Equal(backends, []string{"qsv", "vaapi"}) {
		t.Fatalf("detected backends = %v, want qsv and vaapi only", backends)
	}
	if !info.IntelDetected {
		t.Fatal("IntelDetected = false, want true")
	}
}

func TestDetectedBackendJSONShape(t *testing.T) {
	encoded, err := json.Marshal(HWAccelInfo{
		Resolved: "qsv",
		DetectedBackends: []DetectedBackend{
			{Backend: "qsv", Verified: true, Devices: []string{"/dev/dri/renderD128"}},
			{Backend: "nvenc", Verified: false, Reason: "h264_nvenc encoder unavailable"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"detected_backends":[`,
		`{"backend":"qsv","verified":true,"devices":["/dev/dri/renderD128"]}`,
		`{"backend":"nvenc","verified":false,"reason":"h264_nvenc encoder unavailable"}`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("HWAccelInfo JSON = %s, missing %s", encoded, want)
		}
	}

	empty, err := json.Marshal(HWAccelInfo{Resolved: HWAccelNone})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "detected_backends") {
		t.Fatalf("HWAccelInfo JSON = %s, want detected_backends omitted when empty", empty)
	}
}

func TestResolveHWAccelWithFFmpegContextHonorsCallerDeadline(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x10de")
	ffmpeg := writeFakeFFmpeg(t, fakeFFmpegProbe{hang: true})

	// 60ms, not the caller deadline's natural handful of milliseconds: the
	// budget has to cover the fake sysfs walk in listRenderDevices before the
	// probe is reached, which is cold when this test runs after the rest of the
	// package. Too tight and exec never starts the process, so nothing is
	// logged. It stays far below the 200ms per-command timeout the assertion
	// below distinguishes it from.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	started := time.Now()
	got := ResolveHWAccelWithFFmpegContext(ctx, "auto", ffmpeg.path, "")
	cancel()
	if got != HWAccelNone {
		t.Fatalf("ResolveHWAccelWithFFmpegContext() = %q, want none", got)
	}
	if elapsed := time.Since(started); elapsed >= 150*time.Millisecond {
		t.Fatalf("caller deadline took %s, want less than per-command timeout", elapsed)
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
	}

	retryCtx, retryCancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	_ = ResolveHWAccelWithFFmpegContext(retryCtx, "auto", ffmpeg.path, "")
	retryCancel()
	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if calls := strings.Count(string(logData), "\n"); calls < 1 || calls > 4 {
		t.Fatalf("canceled probe command count = %d, want one shared attempt of at most four commands", calls)
	}
}

func TestNormalizeProbeRequestTimeout(t *testing.T) {
	for _, test := range []struct {
		name     string
		millis   int64
		fallback time.Duration
		want     time.Duration
	}{
		{name: "missing uses caller fallback", fallback: 2 * time.Minute, want: 2 * time.Minute},
		{name: "negative uses caller fallback", millis: -1, fallback: 2 * time.Minute, want: 2 * time.Minute},
		{name: "too small", millis: time.Second.Milliseconds(), fallback: 2 * time.Minute, want: 5 * time.Second},
		{name: "advertised", millis: (137 * time.Second).Milliseconds(), fallback: 2 * time.Minute, want: 137 * time.Second},
		{name: "too large", millis: (10 * time.Minute).Milliseconds(), fallback: 2 * time.Minute, want: 5 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeProbeRequestTimeout(test.millis, test.fallback); got != test.want {
				t.Fatalf("NormalizeProbeRequestTimeout() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestResolveFFmpegPathTrimsWithoutCleaningRelativeExecutable(t *testing.T) {
	if got := ResolveFFmpegPath(" ./ffmpeg "); got != "./ffmpeg" {
		t.Fatalf("ResolveFFmpegPath() = %q, want ./ffmpeg", got)
	}
}

func TestFFmpegSupportsNVENCRequiresCUDAEncodersFiltersAndSmoke(t *testing.T) {
	setupHWAccelTest(t)
	tests := []struct {
		name  string
		probe fakeFFmpegProbe
	}{
		{
			name: "missing cuda hwaccel",
			probe: fakeFFmpegProbe{
				h264NVENC: true, hevcNVENC: true, scaleCUDA: true, uploadCUDA: true, smokeOK: true,
			},
		},
		{
			name: "missing h264 nvenc encoder",
			probe: fakeFFmpegProbe{
				cuda: true, hevcNVENC: true, scaleCUDA: true, uploadCUDA: true, smokeOK: true,
			},
		},
		{
			name: "missing hevc nvenc encoder",
			probe: fakeFFmpegProbe{
				cuda: true, h264NVENC: true, scaleCUDA: true, uploadCUDA: true, smokeOK: true,
			},
		},
		{
			name: "missing scale cuda filter",
			probe: fakeFFmpegProbe{
				cuda: true, h264NVENC: true, hevcNVENC: true, uploadCUDA: true, smokeOK: true,
			},
		},
		{
			name: "missing hwupload cuda filter",
			probe: fakeFFmpegProbe{
				cuda: true, h264NVENC: true, hevcNVENC: true, scaleCUDA: true, smokeOK: true,
			},
		},
		{
			name: "smoke encode failure",
			probe: fakeFFmpegProbe{
				cuda: true, h264NVENC: true, hevcNVENC: true, scaleCUDA: true, uploadCUDA: true,
			},
		},
		{
			name:  "probe timeout",
			probe: fakeFFmpegProbe{hang: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetHWProbeCacheForTest()
			ffmpeg := writeFakeFFmpeg(t, tt.probe)
			if ok, reason := ffmpegSupportsBackend(transcodeHWNVENC, ffmpeg.path, ""); ok {
				t.Fatalf("ffmpegSupportsBackend(nvenc) = true, want false")
			} else if reason == "" {
				t.Fatalf("ffmpegSupportsBackend(nvenc) reason is empty")
			}
		})
	}
}

func TestFFmpegSupportsQSVRequiresListingsAndSmoke(t *testing.T) {
	setupHWAccelTest(t)
	tests := []struct {
		name  string
		probe fakeFFmpegProbe
		want  string
	}{
		{
			name:  "missing qsv and vaapi hwaccels",
			probe: fakeFFmpegProbe{h264QSV: true, hevcQSV: true, smokeOK: true},
			want:  "qsv and vaapi hwaccels unavailable",
		},
		{
			name:  "missing h264 qsv encoder",
			probe: fakeFFmpegProbe{qsvHWAccel: true, hevcQSV: true, smokeOK: true},
			want:  "h264_qsv encoder unavailable",
		},
		{
			name:  "missing hevc qsv encoder",
			probe: fakeFFmpegProbe{qsvHWAccel: true, h264QSV: true, smokeOK: true},
			want:  "hevc_qsv encoder unavailable",
		},
		{
			name:  "smoke encode failure",
			probe: fakeFFmpegProbe{vaapiHWAccel: true, h264QSV: true, hevcQSV: true},
			want:  "h264_qsv smoke encode failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetHWProbeCacheForTest()
			ffmpeg := writeFakeFFmpeg(t, tt.probe)
			ok, reason := ffmpegSupportsBackend(transcodeHWQSV, ffmpeg.path, "/dev/dri/renderD128")
			if ok {
				t.Fatal("ffmpegSupportsBackend(qsv) = true, want false")
			}
			if !strings.HasPrefix(reason, tt.want) {
				t.Fatalf("reason = %q, want prefix %q", reason, tt.want)
			}
		})
	}

	resetHWProbeCacheForTest()
	ffmpeg := writeFakeFFmpeg(t, successfulQSVProbe())
	if ok, reason := ffmpegSupportsBackend(transcodeHWQSV, ffmpeg.path, "/dev/dri/renderD128"); !ok {
		t.Fatalf("ffmpegSupportsBackend(qsv) = false, want true (reason=%q)", reason)
	}
	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	for _, want := range []string{
		"vaapi=va:/dev/dri/renderD128,driver=iHD,kernel_driver=i915,vendor_id=0x8086",
		"qsv=qs@va",
		"testsrc2=size=640x360:rate=1",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("QSV smoke command missing %q; log:\n%s", want, logText)
		}
	}
}

func TestFFmpegSupportsVAAPIRequiresEncoderAndSmoke(t *testing.T) {
	setupHWAccelTest(t)

	resetHWProbeCacheForTest()
	missing := writeFakeFFmpeg(t, fakeFFmpegProbe{vaapiHWAccel: true, smokeOK: true})
	if ok, reason := ffmpegSupportsBackend(transcodeHWVAAPI, missing.path, "/dev/dri/renderD128"); ok {
		t.Fatal("ffmpegSupportsBackend(vaapi) = true, want false")
	} else if reason != "h264_vaapi encoder unavailable" {
		t.Fatalf("reason = %q, want the missing encoder", reason)
	}

	resetHWProbeCacheForTest()
	ffmpeg := writeFakeFFmpeg(t, successfulVAAPIProbe())
	if ok, reason := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpeg.path, "/dev/dri/renderD128"); !ok {
		t.Fatalf("ffmpegSupportsBackend(vaapi) = false, want true (reason=%q)", reason)
	}
	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "vaapi=hw:/dev/dri/renderD128") {
		t.Fatalf("VAAPI smoke command missing its init chain; log:\n%s", logText)
	}
	if strings.Count(logText, "\n") != 2 {
		t.Fatalf("VAAPI probe ran %d commands, want an encoders listing and one smoke encode; log:\n%s",
			strings.Count(logText, "\n"), logText)
	}
}

func TestFFmpegSupportsNVENCCachesByFFmpegPath(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x10de")
	ffmpeg := writeFakeFFmpeg(t, successfulNVENCProbe())

	for i := 0; i < 2; i++ {
		if got := ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, ""); got != "nvenc" {
			t.Fatalf("ResolveHWAccelWithFFmpeg() call %d = %q, want nvenc", i+1, got)
		}
	}

	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatalf("read ffmpeg probe log: %v", err)
	}
	if got := strings.Count(string(logData), "\n"); got != 4 {
		t.Fatalf("probe command count = %d, want 4; log:\n%s", got, logData)
	}
}

func TestHWProbeCacheSeparatesBackendsAndDevices(t *testing.T) {
	setupHWAccelTest(t)
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	keys := map[string]string{
		"nvenc":       hwProbeCacheKey(0, ffmpeg.path, transcodeHWNVENC, ""),
		"qsv-128":     hwProbeCacheKey(0, ffmpeg.path, transcodeHWQSV, "/dev/dri/renderD128"),
		"qsv-129":     hwProbeCacheKey(0, ffmpeg.path, transcodeHWQSV, "/dev/dri/renderD129"),
		"vaapi-128":   hwProbeCacheKey(0, ffmpeg.path, transcodeHWVAAPI, "/dev/dri/renderD128"),
		"identity-eq": hwProbeCacheKey(0, ffmpeg.path, transcodeHWNVENC, ""),
	}
	if keys["nvenc"] != keys["identity-eq"] {
		t.Fatal("identical backend and device produced different cache keys")
	}
	seen := map[string]string{}
	for name, key := range keys {
		if name == "identity-eq" {
			continue
		}
		if other, ok := seen[key]; ok {
			t.Fatalf("cache keys for %s and %s collided", name, other)
		}
		seen[key] = name
	}

	// Each distinct key runs its own probe command set: 3 for QSV on two
	// devices, 2 for VAAPI.
	for _, device := range []string{"/dev/dri/renderD128", "/dev/dri/renderD129"} {
		if ok, reason := ffmpegSupportsBackend(transcodeHWQSV, ffmpeg.path, device); !ok {
			t.Fatalf("QSV probe on %s failed: %s", device, reason)
		}
	}
	if ok, reason := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpeg.path, "/dev/dri/renderD128"); !ok {
		t.Fatalf("VAAPI probe failed: %s", reason)
	}
	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(logData), "\n"); got != 8 {
		t.Fatalf("probe command count = %d, want 8 across three distinct cache keys; log:\n%s", got, logData)
	}
}

func TestFFmpegSupportsNVENCCoalescesConcurrentColdProbes(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x10de")
	probe := successfulNVENCProbe()
	probe.delay = 30 * time.Millisecond
	ffmpeg := writeFakeFFmpeg(t, probe)

	start := make(chan struct{})
	results := make(chan string, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- ResolveHWAccelWithFFmpeg("auto", ffmpeg.path, "")
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if result != "nvenc" {
			t.Fatalf("concurrent resolution = %q, want nvenc", result)
		}
	}

	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatalf("read ffmpeg probe log: %v", err)
	}
	if got := strings.Count(string(logData), "\n"); got != 4 {
		t.Fatalf("probe command count = %d, want one four-command shared probe; log:\n%s", got, logData)
	}
}

func TestFFmpegSupportsNVENCInvalidatesWhenBinaryChangesInPlace(t *testing.T) {
	setupHWAccelTest(t)
	ffmpeg := writeFakeFFmpeg(t, successfulNVENCProbe())
	if ok, reason := ffmpegSupportsBackend(transcodeHWNVENC, ffmpeg.path, ""); !ok {
		t.Fatalf("initial NVENC probe failed: %s", reason)
	}

	replacement := "#!/bin/sh\necho 'replacement without NVENC support'\nexit 0\n"
	if err := os.WriteFile(ffmpeg.path, []byte(replacement), 0o755); err != nil {
		t.Fatalf("replace fake FFmpeg: %v", err)
	}
	changedAt := time.Now().Add(time.Second)
	if err := os.Chtimes(ffmpeg.path, changedAt, changedAt); err != nil {
		t.Fatalf("advance replacement timestamp: %v", err)
	}
	if ok, _ := ffmpegSupportsBackend(transcodeHWNVENC, ffmpeg.path, ""); ok {
		t.Fatal("replaced FFmpeg binary reused a stale positive NVENC result")
	}
}

func TestFFmpegIdentityKeyIncludesResolvedPATHIdentity(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	stamp := time.Unix(100, 0)
	for _, dir := range []string{firstDir, secondDir} {
		path := filepath.Join(dir, "ffmpeg")
		if err := os.WriteFile(path, []byte("same-binary-shape"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PATH", firstDir)
	firstKey := ffmpegIdentityKey("ffmpeg")
	t.Setenv("PATH", secondDir)
	secondKey := ffmpegIdentityKey("ffmpeg")

	if firstKey == secondKey {
		t.Fatalf("PATH-resolved FFmpeg identities collided: %q", firstKey)
	}
}

func TestHWProbeNegativeResultExpires(t *testing.T) {
	setupHWAccelTest(t)
	clock := time.Now()
	hwProbeNow = func() time.Time { return clock }

	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	markerPath := filepath.Join(dir, "vaapi-ready")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
  *-encoders*) echo 'h264_vaapi'; exit 0 ;;
  *) test -e %q ;;
esac
`, markerPath)
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write marker-controlled FFmpeg: %v", err)
	}

	if ok, _ := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpegPath, "/dev/dri/renderD128"); ok {
		t.Fatal("initial VAAPI probe unexpectedly succeeded")
	}
	if err := os.WriteFile(markerPath, []byte("ready"), 0o600); err != nil {
		t.Fatalf("enable VAAPI smoke probe: %v", err)
	}
	clock = clock.Add(hwProbeNegativeTTL - time.Millisecond)
	if ok, _ := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpegPath, "/dev/dri/renderD128"); ok {
		t.Fatal("negative result was not retained during its TTL")
	}
	clock = clock.Add(2 * time.Millisecond)
	if ok, reason := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpegPath, "/dev/dri/renderD128"); !ok {
		t.Fatalf("expired negative result was not retried: %s", reason)
	}
	// A positive result is kept for the process lifetime, so removing the
	// marker after success must not change the answer.
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	if ok, _ := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpegPath, "/dev/dri/renderD128"); !ok {
		t.Fatal("positive probe result expired, want process-lifetime caching")
	}
}

func TestFFmpegSupportsNVENCSmokeProbeUsesSafeFrameDimensions(t *testing.T) {
	setupHWAccelTest(t)
	ffmpeg := writeFakeFFmpeg(t, successfulNVENCProbe())

	if ok, reason := ffmpegSupportsBackend(transcodeHWNVENC, ffmpeg.path, ""); !ok {
		t.Fatalf("ffmpegSupportsBackend(nvenc) = false, want true (reason=%q)", reason)
	}

	logData, err := os.ReadFile(ffmpeg.logPath)
	if err != nil {
		t.Fatalf("read ffmpeg probe log: %v", err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "testsrc2=size=640x360:rate=1") {
		t.Fatalf("smoke probe should use 640x360 input; log:\n%s", logText)
	}
}

func successfulNVENCProbe() fakeFFmpegProbe {
	return fakeFFmpegProbe{
		cuda:       true,
		h264NVENC:  true,
		hevcNVENC:  true,
		scaleCUDA:  true,
		uploadCUDA: true,
		smokeOK:    true,
	}
}

func successfulQSVProbe() fakeFFmpegProbe {
	return fakeFFmpegProbe{
		qsvHWAccel: true,
		h264QSV:    true,
		hevcQSV:    true,
		smokeOK:    true,
	}
}

func successfulVAAPIProbe() fakeFFmpegProbe {
	return fakeFFmpegProbe{
		vaapiHWAccel: true,
		h264VAAPI:    true,
		smokeOK:      true,
	}
}

func fullyCapableProbe() fakeFFmpegProbe {
	return fakeFFmpegProbe{
		cuda:         true,
		qsvHWAccel:   true,
		vaapiHWAccel: true,
		h264NVENC:    true,
		hevcNVENC:    true,
		h264QSV:      true,
		hevcQSV:      true,
		h264VAAPI:    true,
		scaleCUDA:    true,
		uploadCUDA:   true,
		smokeOK:      true,
	}
}

// stripDevicePrefix compares device lists by basename so expectations stay
// readable against the test's temporary /dev/dri stand-in.
func stripDevicePrefix(devices []string) []string {
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		names = append(names, filepath.Base(device))
	}
	return names
}

func setupHWAccelTest(t *testing.T) *hwAccelTestEnv {
	t.Helper()

	oldDRIDir := defaultDRIDir
	oldNVIDIAControlDevice := defaultNVIDIAControlDevice
	oldNVIDIADeviceGlob := defaultNVIDIADeviceGlob
	oldSysClassDRMDir := sysClassDRMDir
	oldGOOS := currentGOOS
	oldProbeTimeout := hwProbeCommandTimeout
	oldProbeNow := hwProbeNow
	resetHWProbeCacheForTest()

	tmp := t.TempDir()
	env := &hwAccelTestEnv{
		devDir: filepath.Join(tmp, "dev"),
		driDir: filepath.Join(tmp, "dev", "dri"),
		sysDir: filepath.Join(tmp, "sys", "class", "drm"),
	}
	defaultDRIDir = env.driDir
	defaultNVIDIAControlDevice = filepath.Join(env.devDir, "nvidiactl")
	defaultNVIDIADeviceGlob = filepath.Join(env.devDir, "nvidia[0-9]*")
	sysClassDRMDir = env.sysDir
	currentGOOS = "linux"
	hwProbeCommandTimeout = 200 * time.Millisecond

	if err := os.MkdirAll(env.driDir, 0o755); err != nil {
		t.Fatalf("create test dri dir: %v", err)
	}
	if err := os.MkdirAll(env.devDir, 0o755); err != nil {
		t.Fatalf("create test dev dir: %v", err)
	}

	t.Cleanup(func() {
		defaultDRIDir = oldDRIDir
		defaultNVIDIAControlDevice = oldNVIDIAControlDevice
		defaultNVIDIADeviceGlob = oldNVIDIADeviceGlob
		sysClassDRMDir = oldSysClassDRMDir
		currentGOOS = oldGOOS
		hwProbeCommandTimeout = oldProbeTimeout
		hwProbeNow = oldProbeNow
		resetHWProbeCacheForTest()
	})

	return env
}

func (e *hwAccelTestEnv) addRenderDevice(t *testing.T, name string, vendor string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.driDir, name), []byte{}, 0o600); err != nil {
		t.Fatalf("create render device: %v", err)
	}
	vendorPath := filepath.Join(e.sysDir, name, "device", "vendor")
	if err := os.MkdirAll(filepath.Dir(vendorPath), 0o755); err != nil {
		t.Fatalf("create vendor dir: %v", err)
	}
	if err := os.WriteFile(vendorPath, []byte(vendor+"\n"), 0o644); err != nil {
		t.Fatalf("write vendor file: %v", err)
	}
}

func (e *hwAccelTestEnv) addNVIDIADevice(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.devDir, name), []byte{}, 0o600); err != nil {
		t.Fatalf("create nvidia device: %v", err)
	}
}

func writeFakeFFmpeg(t *testing.T, probe fakeFFmpegProbe) fakeFFmpegBinary {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	logPath := filepath.Join(dir, "probe.log")

	script := "#!/bin/sh\n"
	script += fmt.Sprintf("printf '%%s\\n' \"$*\" >> %q\n", logPath)
	if probe.delay > 0 {
		script += fmt.Sprintf("sleep %.3f\n", probe.delay.Seconds())
	}
	if probe.hang {
		script += "exec sleep 2147483647\n"
	}
	script += "case \"$*\" in\n"
	script += "  *-hwaccels*)\n"
	script += "    echo 'Hardware acceleration methods:'\n"
	if probe.cuda {
		script += "    echo 'cuda'\n"
	}
	if probe.qsvHWAccel {
		script += "    echo 'qsv'\n"
	}
	if probe.vaapiHWAccel {
		script += "    echo 'vaapi'\n"
	}
	script += "    exit 0 ;;\n"
	script += "  *-encoders*)\n"
	if probe.h264NVENC {
		script += "    echo ' V..... h264_nvenc NVIDIA NVENC H.264 encoder'\n"
	}
	if probe.hevcNVENC {
		script += "    echo ' V..... hevc_nvenc NVIDIA NVENC hevc encoder'\n"
	}
	if probe.h264QSV {
		script += "    echo ' V..... h264_qsv H.264 QSV encoder'\n"
	}
	if probe.hevcQSV {
		script += "    echo ' V..... hevc_qsv HEVC QSV encoder'\n"
	}
	if probe.h264VAAPI {
		script += "    echo ' V..... h264_vaapi H.264 VAAPI encoder'\n"
	}
	script += "    exit 0 ;;\n"
	script += "  *-filters*)\n"
	if probe.scaleCUDA {
		script += "    echo ' ... scale_cuda V->V GPU video scaling'\n"
	}
	if probe.uploadCUDA {
		script += "    echo ' ... hwupload_cuda V->V upload CUDA frames'\n"
	}
	script += "    exit 0 ;;\n"
	for _, encoder := range []string{"h264_nvenc", "h264_qsv", "h264_vaapi"} {
		script += fmt.Sprintf("  *%s*)\n", encoder)
		if probe.smokeOK && !slices.Contains(probe.smokeFailures, encoder) {
			// The init chain carries the device path, so a broken GPU is modeled
			// by matching the command rather than by ignoring the argument.
			if len(probe.smokeDeviceFailures) > 0 {
				patterns := make([]string, 0, len(probe.smokeDeviceFailures))
				for _, device := range probe.smokeDeviceFailures {
					patterns = append(patterns, "*"+device+"*")
				}
				script += "    case \"$*\" in\n"
				script += fmt.Sprintf("      %s)\n", strings.Join(patterns, "|"))
				script += fmt.Sprintf("        echo 'no capable devices found for %s' >&2\n", encoder)
				script += "        exit 1 ;;\n"
				script += "    esac\n"
			}
			script += "    exit 0 ;;\n"
		} else {
			script += fmt.Sprintf("    echo 'no capable devices found for %s' >&2\n", encoder)
			script += "    exit 1 ;;\n"
		}
	}
	script += "  *)\n"
	script += "    echo 'unexpected probe command' >&2\n"
	script += "    exit 1 ;;\n"
	script += "esac\n"

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return fakeFFmpegBinary{path: path, logPath: logPath}
}

// resetHWProbeCacheForTest clears the probe cache between cases. It delegates
// to the exported invalidation so tests exercise the same seam the re-probe
// action uses rather than a second, drifting implementation.
func resetHWProbeCacheForTest() {
	InvalidateHWProbeCache()
}

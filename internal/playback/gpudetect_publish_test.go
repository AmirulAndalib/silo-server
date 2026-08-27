package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// A detection walk that ran out of budget marks backends it never reached
// Verified=false, which is byte-identical to a real hardware failure. A node
// that hashed and published that report would tell the API its GPU regressed,
// and the API would persist a capability_drift note, latch it until a clean
// report arrives, and route the node to software in the meantime — all for
// hardware that is fine. So the incompleteness has to reach the publisher.
func TestDetectHWAccelReportsAnIncompleteWalk(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	info, err := DetectHWAccelWithFFmpegContextResult(ctx, hwAccelAuto, ffmpeg.path, "")
	if !errors.Is(err, ErrHardwareDetectionIncomplete) {
		t.Fatalf("error = %v, want ErrHardwareDetectionIncomplete", err)
	}
	// The report still comes back for an operator-facing surface to show; it is
	// only publishing it as this host's capabilities that is refused.
	if info.Resolved != HWAccelNone {
		t.Fatalf("Resolved = %q, want an abandoned walk to resolve to software", info.Resolved)
	}
}

// The complement: a walk that reached every candidate backend publishes
// normally, or nothing would ever be inventoried.
func TestDetectHWAccelReportsACompleteWalk(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	info, err := DetectHWAccelWithFFmpegContextResult(context.Background(), hwAccelAuto, ffmpeg.path, "")
	if err != nil {
		t.Fatalf("DetectHWAccelWithFFmpegContextResult: %v", err)
	}
	if info.Resolved != transcodeHWQSV {
		t.Fatalf("Resolved = %q, want qsv", info.Resolved)
	}
}

// Detection walks a backend's candidates in order and stops at the first that
// passes a smoke encode; execution with no configured playback.hw_device used to
// fall back to PickRenderDevice, which returns whatever sorts first under
// /dev/dri. On a mixed-vendor host those are different GPUs, so a report saying
// "qsv verified" was paired with a transcode initializing a card the probe had
// never touched.
func TestAcquireHWDeviceUsesTheVerifiedRenderDevice(t *testing.T) {
	env := setupHWAccelTest(t)
	// renderD128 sorts first and is AMD, so it is not a QSV candidate at all;
	// only renderD129 can pass the probe.
	env.addRenderDevice(t, "renderD128", "0x1002")
	env.addRenderDevice(t, "renderD129", "0x8086")
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())

	if got := ResolveHWAccelWithFFmpeg(hwAccelAuto, ffmpeg.path, ""); got != transcodeHWQSV {
		t.Fatalf("ResolveHWAccelWithFFmpeg() = %q, want qsv", got)
	}
	verified := VerifiedHWDevice(transcodeHWQSV)
	if want := env.devicePath("renderD129"); verified != want {
		t.Fatalf("VerifiedHWDevice(qsv) = %q, want %q", verified, want)
	}
	// The unverified device is the one auto-detection would otherwise pick.
	if fallback := PickRenderDevice(""); fallback == verified {
		t.Skip("test setup no longer distinguishes the verified device from the first render node")
	}

	device, release := AcquireHWDevice("", transcodeHWQSV)
	defer release()
	if device != verified {
		t.Fatalf("AcquireHWDevice() = %q, want the verified device %q", device, verified)
	}
	// Counting it is the other half: a default-configured node reported zero
	// sessions beside a busy engine because an unnamed device was never counted.
	if got := hwDeviceActiveCount(verified); got != 1 {
		t.Fatalf("active workloads on %s = %d, want 1", verified, got)
	}
	release()
	if got := hwDeviceActiveCount(verified); got != 0 {
		t.Fatalf("active workloads after release = %d, want 0", got)
	}

	// An operator re-probe discards the verdicts, so the device they blessed
	// goes with them: answering from the old generation would let execution
	// keep using a device the re-probe was asked to re-verify.
	InvalidateHWProbeCache()
	if got := VerifiedHWDevice(transcodeHWQSV); got != "" {
		t.Fatalf("VerifiedHWDevice(qsv) after invalidation = %q, want empty", got)
	}
}

// With nothing verified — a backend named explicitly and never walked — the
// device stays unresolved and ffmpeg picks one downstream, exactly as before.
func TestAcquireHWDeviceLeavesTheDeviceUnsetWithoutAVerifiedProbe(t *testing.T) {
	setupHWAccelTest(t)

	device, workload, release := acquireHWDevice("", transcodeHWQSV, "")
	defer release()
	if device != "" || workload != "" {
		t.Fatalf("acquireHWDevice() = (%q, %q), want both empty with no verified device", device, workload)
	}
}

// Invalidation has to supersede a probe already in flight, not merely clear the
// map in front of it. The operator-facing re-probe exists to force a cold
// re-verification; if a probe that started before the invalidation could hand
// its pre-invalidation verdict to the caller that invalidated, the action would
// republish exactly what it was asked to discard and report "nothing changed".
func TestInvalidateHWProbeCacheSupersedesAnInFlightProbe(t *testing.T) {
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	ffmpeg := writeFakeFFmpeg(t, successfulVAAPIProbe())
	device := env.devicePath("renderD128")

	// The race is decided by channel receipts rather than a sleep: the first
	// flight parks inside the probe until the invalidation has landed, so this
	// cannot pass or fail on how loaded the machine is.
	started := make(chan struct{})
	blocked := make(chan struct{})
	var flights atomic.Int32
	hwProbeFlightStarted = func() {
		if flights.Add(1) == 1 {
			close(started)
			<-blocked
		}
	}
	t.Cleanup(func() { hwProbeFlightStarted = nil })

	var wg sync.WaitGroup
	wg.Go(func() {
		if ok, reason := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpeg.path, device); !ok {
			t.Errorf("in-flight probe failed: %s", reason)
		}
	})

	<-started
	InvalidateHWProbeCache()
	close(blocked)

	if ok, reason := ffmpegSupportsBackend(transcodeHWVAAPI, ffmpeg.path, device); !ok {
		t.Fatalf("post-invalidation probe failed: %s", reason)
	}
	wg.Wait()

	// Two independent smoke encodes ran: the second call started its own probe
	// rather than joining the flight the invalidation superseded, which is the
	// whole difference between clearing the map and moving the key.
	if got := smokeEncodeCount(t, ffmpeg.logPath); got < 2 {
		t.Fatalf("smoke encodes = %d, want the post-invalidation probe to run its own", got)
	}
}

// smokeEncodeCount counts the synthetic single-frame encodes in a fake ffmpeg's
// command log. Every hardware probe ends in exactly one.
func smokeEncodeCount(t *testing.T, logPath string) int {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read ffmpeg probe log: %v", err)
	}
	return strings.Count(string(data), "testsrc2")
}

// devicePath is the full path of a render device added to this test's /dev/dri
// stand-in.
func (e *hwAccelTestEnv) devicePath(name string) string {
	return filepath.Join(e.driDir, name)
}

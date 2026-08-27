package transcodenode

import "testing"

// The bug this gate exists for: a node idle at a point-in-time check accepts a
// transcode milliseconds later, and the re-probe's smoke encode — minutes long
// — then races a live encoder session and publishes working hardware as failed.
// Admitted work has to keep the re-probe out even before it is visible as an
// active job.
func TestGPUGateRefusesReprobeWhileWorkIsAdmitted(t *testing.T) {
	var gate gpuGate

	if !gate.beginWork() {
		t.Fatal("beginWork on an idle gate was refused")
	}
	// activeJobs is still 0 here: the counter only moves once ffmpeg is running,
	// which is exactly the window a point-in-time check missed.
	if busy, ok := gate.beginReprobe(0); ok {
		t.Fatal("re-probe admitted while a transcode was starting")
	} else if busy != 1 {
		t.Fatalf("busy = %d, want the admitted work counted", busy)
	}

	gate.endWork()
	if _, ok := gate.beginReprobe(0); !ok {
		t.Fatal("re-probe refused after the work finished")
	}
}

// The node's own running-session count is read under the same lock, so "no
// admitted work" and "no active jobs" cannot be true at two different instants.
func TestGPUGateRefusesReprobeWhileJobsAreActive(t *testing.T) {
	var gate gpuGate

	busy, ok := gate.beginReprobe(2)
	if ok {
		t.Fatal("re-probe admitted on a node running transcodes")
	}
	if busy != 2 {
		t.Fatalf("busy = %d, want 2", busy)
	}
}

// A re-probe holds the encoder for the whole rebuild, so work arriving mid-probe
// is refused rather than queued: a viewer pressing play must not wait minutes
// for a probe, and the API retries elsewhere.
func TestGPUGateRefusesWorkWhileReprobing(t *testing.T) {
	var gate gpuGate

	if _, ok := gate.beginReprobe(0); !ok {
		t.Fatal("re-probe refused on an idle gate")
	}
	if gate.beginWork() {
		t.Fatal("GPU work admitted while a re-probe held the encoder")
	}
	if _, ok := gate.beginReprobe(0); ok {
		t.Fatal("a second concurrent re-probe was admitted")
	}

	gate.endReprobe()
	if !gate.beginWork() {
		t.Fatal("GPU work refused after the re-probe released the encoder")
	}
	gate.endWork()
}

// endWork must not drive the counter negative, or one unbalanced release would
// let a re-probe run beside real transcodes forever.
func TestGPUGateEndWorkDoesNotUnderflow(t *testing.T) {
	var gate gpuGate

	gate.endWork()
	gate.endWork()
	if !gate.beginWork() {
		t.Fatal("beginWork refused after unbalanced releases")
	}
	if _, ok := gate.beginReprobe(0); ok {
		t.Fatal("re-probe admitted while one unit of work was outstanding")
	}
}

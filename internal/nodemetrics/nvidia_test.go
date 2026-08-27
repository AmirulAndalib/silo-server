package nodemetrics

import (
	"context"
	"errors"
	"testing"
)

const nvidiaSMIOutput = "0, GPU-1234abcd, 00000000:03:00.0, 71, 63, 12, 812, 8192\n" +
	"1, GPU-5678efgh, 00000000:04:00.0, 5, 0, 0, 100, 8192\n"

func TestParseNVIDIASMI(t *testing.T) {
	gpus := parseNVIDIASMI([]byte(nvidiaSMIOutput))
	if len(gpus) != 2 {
		t.Fatalf("gpus = %+v, want 2", gpus)
	}
	first := gpus[0]
	if first.Index != 0 || first.UUID != "GPU-1234abcd" {
		t.Fatalf("gpus[0] identity = %+v", first)
	}
	// The wide domain nvidia-smi prints has to normalize to the sysfs form or it
	// will never join with a DRM device.
	if first.PCIAddress != "0000:03:00.0" {
		t.Fatalf("PCIAddress = %q, want the normalized sysfs form", first.PCIAddress)
	}
	if first.GPUUtil != 71 || first.EncoderUtil != 63 || first.DecoderUtil != 12 {
		t.Fatalf("gpus[0] utilization = %+v", first)
	}
	if first.MemUsedMB != 812 || first.MemTotalMB != 8192 {
		t.Fatalf("gpus[0] memory = %+v", first)
	}
}

// Drivers print "[N/A]" for a column a card does not support. One unsupported
// column must not discard the whole row.
func TestParseNVIDIASMIToleratesPlaceholders(t *testing.T) {
	gpus := parseNVIDIASMI([]byte("0, GPU-x, 00000000:03:00.0, [N/A], [Not Supported], 4, 100, 8192\n"))
	if len(gpus) != 1 {
		t.Fatalf("gpus = %+v, want the row kept", gpus)
	}
	if gpus[0].GPUUtil != 0 || gpus[0].EncoderUtil != 0 || gpus[0].DecoderUtil != 4 {
		t.Fatalf("gpus[0] = %+v, want placeholders read as zero and 4 preserved", gpus[0])
	}
}

func TestSampleGPUEnrichesWithNVIDIASMI(t *testing.T) {
	tree := newProcTree(t)
	clock := newFakeClock()
	tree.write("stat", "cpu  0 0 0 0 0 0 0 0\n")
	tree.write("loadavg", "0 0 0 0/0 0\n")
	tree.write("meminfo", "MemTotal: 1024 kB\n")
	tree.write("net/dev", "")

	s := newTestSampler(t, tree, clock, Options{
		DeviceSessions: func() map[string]int { return map[string]int{"cuda:0": 3} },
	})
	s.runNVIDIASMI = func(context.Context) ([]byte, error) { return []byte(nvidiaSMIOutput), nil }
	s.sample(context.Background())

	gpu := s.Snapshot().GPU
	if len(gpu) != 2 {
		t.Fatalf("GPU = %+v, want both cards", gpu)
	}
	first := gpu[0]
	// The proprietary driver exposes no DRM node this process can read, so the
	// device is named the way playback addresses it.
	if first.Device != "cuda:0" {
		t.Fatalf("Device = %q, want cuda:0", first.Device)
	}
	if first.Vendor != "nvidia" || first.Source != SourceNVIDIASMI {
		t.Fatalf("GPU[0] = %+v, want nvidia via nvidia-smi", first)
	}
	if first.Sessions != 3 {
		t.Fatalf("Sessions = %d, want the balancer's count for cuda:0", first.Sessions)
	}
	if first.TotalBusyPct == nil || *first.TotalBusyPct != 71 {
		t.Fatalf("TotalBusyPct = %v, want 71", first.TotalBusyPct)
	}
	if first.VideoBusyPct != 63 {
		t.Fatalf("VideoBusyPct = %d, want the busier of encoder/decoder", first.VideoBusyPct)
	}
	if first.VRAMUsedMB == nil || *first.VRAMUsedMB != 812 {
		t.Fatalf("VRAMUsedMB = %v, want 812", first.VRAMUsedMB)
	}
}

// A GPU that has both DRM counters and an nvidia-smi row must be one entry
// crediting both sources, not two entries.
func TestSampleGPUMergesFdinfoAndNVIDIASMIOnOneDevice(t *testing.T) {
	tree := newProcTree(t)
	clock := newFakeClock()
	tree.write("stat", "cpu  0 0 0 0 0 0 0 0\n")
	tree.write("loadavg", "0 0 0 0/0 0\n")
	tree.write("meminfo", "MemTotal: 1024 kB\n")
	tree.write("net/dev", "")
	tree.write("4242/fdinfo/3", `drm-driver:	nvidia-drm
drm-pdev:	0000:03:00.0
drm-client-id:	1
drm-engine-video:	1000000000 ns
`)

	s := newTestSampler(t, tree, clock, Options{
		FFmpegChildren: func() []int { return []int{4242} },
		DeviceIdentities: func() []DeviceIdentity {
			return []DeviceIdentity{{Path: "/dev/dri/renderD128", PCIAddress: "0000:03:00.0", Vendor: "nvidia"}}
		},
	})
	s.runNVIDIASMI = func(context.Context) ([]byte, error) {
		return []byte("0, GPU-1234abcd, 00000000:03:00.0, 71, 63, 12, 812, 8192\n"), nil
	}
	s.sample(context.Background())

	gpu := s.Snapshot().GPU
	if len(gpu) != 1 {
		t.Fatalf("GPU = %+v, want the two views merged onto one device", gpu)
	}
	if gpu[0].Device != "/dev/dri/renderD128" {
		t.Fatalf("Device = %q, want the render node path kept", gpu[0].Device)
	}
	if gpu[0].Source != SourceFdinfoNVIDIASMI {
		t.Fatalf("Source = %q, want %q", gpu[0].Source, SourceFdinfoNVIDIASMI)
	}
	if gpu[0].TotalBusyPct == nil || *gpu[0].TotalBusyPct != 71 {
		t.Fatalf("TotalBusyPct = %v, want the whole-GPU reading", gpu[0].TotalBusyPct)
	}
}

// NVENC workloads are counted under a CUDA name or a GPU UUID, but a card whose
// DRM node this process can read is displayed by its render path. Looking
// sessions up by display name alone reports an idle GPU on an NVIDIA node that
// is transcoding.
func TestSampleGPUJoinsNVENCSessionsThroughDeviceAliases(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sessions map[string]int
		want     int
	}{
		{name: "counted by cuda index", sessions: map[string]int{"cuda:0": 3}, want: 3},
		{name: "counted by gpu uuid", sessions: map[string]int{"GPU-1234abcd": 2}, want: 2},
		{name: "counted by render path", sessions: map[string]int{"/dev/dri/renderD128": 1}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := newProcTree(t)
			clock := newFakeClock()
			tree.write("stat", "cpu  0 0 0 0 0 0 0 0\n")
			tree.write("loadavg", "0 0 0 0/0 0\n")
			tree.write("meminfo", "MemTotal: 1024 kB\n")
			tree.write("net/dev", "")

			s := newTestSampler(t, tree, clock, Options{
				DeviceSessions: func() map[string]int { return tc.sessions },
				DeviceIdentities: func() []DeviceIdentity {
					// The proprietary driver with modeset does expose a render
					// node, so this is the ordinary bare-metal NVIDIA shape.
					return []DeviceIdentity{{Path: "/dev/dri/renderD128", PCIAddress: "0000:03:00.0", Vendor: "nvidia"}}
				},
			})
			s.runNVIDIASMI = func(context.Context) ([]byte, error) {
				return []byte("0, GPU-1234abcd, 00000000:03:00.0, 71, 63, 12, 812, 8192\n"), nil
			}
			s.sample(context.Background())

			gpu := s.Snapshot().GPU
			if len(gpu) != 1 {
				t.Fatalf("GPU = %+v, want one device", gpu)
			}
			if gpu[0].Device != "/dev/dri/renderD128" {
				t.Fatalf("Device = %q, want the render node path", gpu[0].Device)
			}
			if gpu[0].Sessions != tc.want {
				t.Fatalf("Sessions = %d, want %d for %v", gpu[0].Sessions, tc.want, tc.sessions)
			}
		})
	}
}

// Two cards must not both claim a count keyed by a name only one of them
// answers to.
func TestSampleGPUDoesNotDoubleCountSessionsAcrossDevices(t *testing.T) {
	tree := newProcTree(t)
	clock := newFakeClock()
	tree.write("stat", "cpu  0 0 0 0 0 0 0 0\n")
	tree.write("loadavg", "0 0 0 0/0 0\n")
	tree.write("meminfo", "MemTotal: 1024 kB\n")
	tree.write("net/dev", "")

	s := newTestSampler(t, tree, clock, Options{
		DeviceSessions: func() map[string]int { return map[string]int{"cuda:1": 2} },
	})
	s.runNVIDIASMI = func(context.Context) ([]byte, error) { return []byte(nvidiaSMIOutput), nil }
	s.sample(context.Background())

	total := 0
	for _, gpu := range s.Snapshot().GPU {
		total += gpu.Sessions
		if gpu.Device == "cuda:0" && gpu.Sessions != 0 {
			t.Fatalf("cuda:0 claimed %d sessions belonging to cuda:1", gpu.Sessions)
		}
	}
	if total != 2 {
		t.Fatalf("sessions across devices = %d, want the 2 counted exactly once", total)
	}
}

// A host without the NVIDIA toolkit fails this query every 5 seconds forever.
// The breaker stops us from spawning a doomed subprocess for the life of the
// process.
func TestNVIDIACircuitBreakerRetiresSourceAfterRepeatedFailure(t *testing.T) {
	tree := newProcTree(t)
	clock := newFakeClock()
	s := newTestSampler(t, tree, clock, Options{})
	calls := 0
	s.runNVIDIASMI = func(context.Context) ([]byte, error) {
		calls++
		return nil, errors.New("nvidia-smi: command not found")
	}

	for range sourceFailureLimit + 5 {
		s.queryNVIDIA(context.Background())
	}
	if calls != sourceFailureLimit {
		t.Fatalf("nvidia-smi invoked %d times, want it retired after %d failures", calls, sourceFailureLimit)
	}
}

// A successful command that parses to nothing is as useless as a failure, and
// is how an unsupported query syntax presents.
func TestNVIDIACircuitBreakerCountsEmptyOutputAsFailure(t *testing.T) {
	tree := newProcTree(t)
	clock := newFakeClock()
	s := newTestSampler(t, tree, clock, Options{})
	calls := 0
	s.runNVIDIASMI = func(context.Context) ([]byte, error) {
		calls++
		return []byte("\n"), nil
	}
	for range sourceFailureLimit + 3 {
		s.queryNVIDIA(context.Background())
	}
	if calls != sourceFailureLimit {
		t.Fatalf("nvidia-smi invoked %d times, want it retired after %d empty answers", calls, sourceFailureLimit)
	}
}

// A transient failure must not retire the source: a driver busy for one sample
// is normal, and losing the only NVIDIA signal over it would be a regression an
// operator cannot recover from without a restart.
func TestNVIDIACircuitBreakerResetsOnSuccess(t *testing.T) {
	tree := newProcTree(t)
	clock := newFakeClock()
	s := newTestSampler(t, tree, clock, Options{})
	fail := true
	calls := 0
	s.runNVIDIASMI = func(context.Context) ([]byte, error) {
		calls++
		if fail {
			return nil, errors.New("busy")
		}
		return []byte(nvidiaSMIOutput), nil
	}

	for range sourceFailureLimit - 1 {
		s.queryNVIDIA(context.Background())
	}
	fail = false
	if gpus := s.queryNVIDIA(context.Background()); len(gpus) != 2 {
		t.Fatalf("recovered query returned %d gpus, want 2", len(gpus))
	}
	fail = true
	for range sourceFailureLimit - 1 {
		s.queryNVIDIA(context.Background())
	}
	if calls != 2*(sourceFailureLimit-1)+1 {
		t.Fatalf("nvidia-smi invoked %d times, want the failure count reset by the success", calls)
	}
}

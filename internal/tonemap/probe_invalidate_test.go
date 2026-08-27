package tonemap

import (
	"context"
	"testing"
	"time"
)

// A non-empty inventory never expires, which is the blind spot
// InvalidateProbeCache closes: a driver replaced underneath a running node
// changes the answer without changing the binary's identity key. The observable
// contract is that the probe commands run again.
func TestInvalidateProbeCacheForcesAnotherProbe(t *testing.T) {
	resetProbeCache(t)
	now := time.Unix(100, 0)
	calls := 0
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		if len(args) > 0 && args[len(args)-1] == "-filters" {
			return []byte(" .S. zscale V->V\n .S. tonemapx V->V\n .S. sidedata V->V\n"), nil
		}
		if len(args) > 0 && args[len(args)-1] == "-encoders" {
			return []byte("libx264"), nil
		}
		return nil, nil
	}
	clock := func() time.Time { return now }

	probe := func(stage string) int {
		capabilities, err := probeCached(context.Background(), "/ffmpeg-invalidate", BackendSoftware, "", runner, clock)
		if err != nil {
			t.Fatalf("%s probe error = %v", stage, err)
		}
		if len(capabilities) != 1 {
			t.Fatalf("%s probe capabilities = %#v, want one software entry", stage, capabilities)
		}
		return calls
	}

	first := probe("first")
	if first == 0 {
		t.Fatal("first probe ran no commands")
	}
	if cached := probe("cached"); cached != first {
		t.Fatalf("cached probe ran %d commands, want the cached inventory reused", cached-first)
	}

	InvalidateProbeCache()

	if reprobed := probe("re-probed"); reprobed != first*2 {
		t.Fatalf("probe after invalidation ran %d commands total, want %d", reprobed, first*2)
	}
}

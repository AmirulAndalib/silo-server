package tonemap

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
)

func TestProbeTotalTimeoutCoversBoundedCommandMatrix(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		device  string
		count   int
	}{
		{name: "software", backend: BackendSoftware, count: 7},
		{name: "one hardware device", backend: BackendQSV, device: "/dev/dri/renderD128", count: 12},
		{name: "two hardware devices", backend: BackendVAAPI, device: "/dev/dri/renderD128,/dev/dri/renderD129", count: 17},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := time.Duration(tt.count)*probeCommandTimeout + probeTimeoutSlack
			if got := probeTotalTimeout(tt.backend, tt.device); got != want {
				t.Fatalf("probeTotalTimeout() = %s, want %s", got, want)
			}
		})
	}
}

func TestProbeEmptyCapabilitiesExpire(t *testing.T) {
	resetProbeCache(t)
	now := time.Unix(100, 0)
	calls := 0
	runner := func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("temporarily unavailable")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if got := probeCached(context.Background(), "/ffmpeg-empty", BackendSoftware, "", runner, func() time.Time { return now }); len(got) != 0 {
			t.Fatalf("empty probe = %#v", got)
		}
	}
	if calls != 2 {
		t.Fatalf("listing calls = %d, want two from one cached empty probe", calls)
	}
	now = now.Add(probeNegativeTTL + time.Second)
	_ = probeCached(context.Background(), "/ffmpeg-empty", BackendSoftware, "", runner, func() time.Time { return now })
	if calls != 4 {
		t.Fatalf("listing calls = %d, want a fresh probe after expiry", calls)
	}
}

func TestProbeSuccessfulCapabilitiesDoNotExpire(t *testing.T) {
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
	if got := probeCached(context.Background(), "/ffmpeg-success", BackendSoftware, "", runner, func() time.Time { return now }); len(got) != 1 {
		t.Fatalf("successful probe = %#v", got)
	}
	firstCalls := calls
	now = now.Add(24 * time.Hour)
	if got := probeCached(context.Background(), "/ffmpeg-success", BackendSoftware, "", runner, func() time.Time { return now }); len(got) != 1 {
		t.Fatalf("cached successful probe = %#v", got)
	}
	if calls != firstCalls {
		t.Fatalf("successful probe reran: calls = %d, want %d", calls, firstCalls)
	}
}

func TestProbeCallerCancellationDoesNotCancelSharedProbe(t *testing.T) {
	resetProbeCache(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var starts atomic.Int32
	var sharedCancelled atomic.Bool
	runner := func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		if starts.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return nil, errors.New("unavailable")
		case <-ctx.Done():
			sharedCancelled.Store(true)
			return nil, ctx.Err()
		}
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first := make(chan Capabilities, 1)
	go func() {
		first <- probeCached(firstCtx, "/ffmpeg-shared", BackendSoftware, "", runner, time.Now)
	}()
	<-started
	second := make(chan Capabilities, 1)
	go func() {
		second <- probeCached(context.Background(), "/ffmpeg-shared", BackendSoftware, "", runner, time.Now)
	}()
	cancelFirst()
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("canceled caller did not stop waiting")
	}
	close(release)
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("remaining caller did not receive the shared probe result")
	}
	if sharedCancelled.Load() {
		t.Fatal("caller cancellation propagated into the shared probe context")
	}
}

func resetProbeCache(t *testing.T) {
	t.Helper()
	probeCache.Lock()
	probeCache.entries = make(map[string]probeCacheEntry)
	probeCache.group = singleflight.Group{}
	probeCache.Unlock()
}

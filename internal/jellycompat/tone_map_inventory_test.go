package jellycompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

type compatToneMapInventoryPlanner struct {
	urls []string
}

func (p compatToneMapInventoryPlanner) PlanSession(string, string, bool, int) nodepool.Plan {
	return nodepool.Plan{}
}

func (p compatToneMapInventoryPlanner) TranscodeNodeURLs() []string {
	return p.urls
}

func TestCompatToneMapCapabilityInventoryFetchesNodesConcurrently(t *testing.T) {
	var active atomic.Int32
	var startedOnce sync.Once
	bothStarted := make(chan struct{})
	release := make(chan struct{})
	serve := func(w http.ResponseWriter, _ *http.Request) {
		if active.Add(1) == 2 {
			startedOnce.Do(func() { close(bothStarted) })
		}
		<-release
		_ = json.NewEncoder(w).Encode(playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{{
			Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
			SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
		}}})
	}
	first := httptest.NewServer(http.HandlerFunc(serve))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(serve))
	defer second.Close()

	handler := &PlaybackHandler{
		NodePlanner: compatToneMapInventoryPlanner{urls: []string{first.URL, second.URL}},
		SettingsRepo: stubSettingsReader{values: map[string]string{
			config.PlaybackLocalTranscodeFallbackSettingKey: "false",
		}},
	}
	result := make(chan tonemap.Capabilities, 1)
	go func() {
		capabilities, _ := handler.compatToneMapCapabilityInventory(context.Background())
		result <- capabilities
	}()
	select {
	case <-bothStarted:
		close(release)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("node capability probes did not overlap")
	}
	if got := <-result; len(got) != 2 {
		t.Fatalf("aggregated capabilities = %#v, want both nodes", got)
	}
}

func TestCompatToneMapCapabilityInventoryHonorsSharedDeadline(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(playback.HWAccelInfo{ToneMapCapabilities: tonemap.Capabilities{{
			Mode: tonemap.ModeSoftware, Backend: tonemap.BackendSoftware, Filter: tonemap.SoftwareFilterBT2390,
			SourceKinds: []tonemap.SourceKind{tonemap.SourcePQ},
		}}})
	}))
	defer fast.Close()

	handler := &PlaybackHandler{
		NodePlanner: compatToneMapInventoryPlanner{urls: []string{slow.URL, fast.URL}},
		SettingsRepo: stubSettingsReader{values: map[string]string{
			config.PlaybackLocalTranscodeFallbackSettingKey: "false",
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	capabilities, byNode := handler.compatToneMapCapabilityInventory(ctx)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("capability aggregation took %s, want shared caller deadline", elapsed)
	}
	if len(capabilities) != 1 || !capabilities.Supports(tonemap.ModeSoftware, tonemap.SourcePQ) {
		t.Fatalf("aggregated capabilities = %#v, want successful node retained", capabilities)
	}
	if _, ok := byNode[fast.URL]; !ok {
		t.Fatalf("per-node capabilities = %#v, want successful node retained", byNode)
	}
	if _, ok := byNode[slow.URL]; ok {
		t.Fatalf("per-node capabilities = %#v, failed node should be ignored", byNode)
	}
}

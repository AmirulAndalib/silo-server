package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func decodeProxyHealth(t *testing.T, server *Server) healthResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var health healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	return health
}

// A proxy executes remux recipes, so the API tracks its capabilities the same
// way it tracks a transcode node's: by the hash health advertises. Until the
// first snapshot the field must be absent rather than a hash of nothing, which
// the sweep would treat as a real inventory.
func TestProxyHealthPublishesCapabilityHashOnlyAfterSnapshot(t *testing.T) {
	server := newDownloadProxyServer(t, "capability-secret")

	if got := decodeProxyHealth(t, server).CapabilitiesHash; got != "" {
		t.Fatalf("capabilities_hash = %q before any snapshot, want empty", got)
	}

	server.refreshCapabilitySnapshot(context.Background())

	hash := decodeProxyHealth(t, server).CapabilitiesHash
	if hash == "" {
		t.Fatal("capabilities_hash is still empty after a snapshot")
	}
	// A second snapshot of unchanged hardware must not move the hash, or the
	// sweep would refetch this proxy's inventory forever.
	server.refreshCapabilitySnapshot(context.Background())
	if got := decodeProxyHealth(t, server).CapabilitiesHash; got != hash {
		t.Fatalf("capabilities_hash changed without hardware changing: %q then %q", hash, got)
	}
}

// The endpoint and the background snapshot share one assembly, so a served
// report carries the same hash health publishes.
func TestProxyCapabilitiesPublishCapabilityHash(t *testing.T) {
	const secret = "capability-secret"
	server := newDownloadProxyServer(t, secret)

	request := httptest.NewRequest(http.MethodGet, "/hw-capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var info playback.HWAccelInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if info.CapabilityHash == "" {
		t.Fatal("served capability report carries no capability_hash")
	}
	served := info
	served.CapabilityHash = ""
	if want := playback.ComputeCapabilityHash(served); want != info.CapabilityHash {
		t.Fatalf("capability_hash = %s, want %s for the served payload", info.CapabilityHash, want)
	}
	if got := decodeProxyHealth(t, server).CapabilitiesHash; got != info.CapabilityHash {
		t.Fatalf("health capabilities_hash = %q, want the just-served %q", got, info.CapabilityHash)
	}
}

// A probe that did not finish hashes differently from the same hardware probed
// successfully, so publishing it would announce a hardware change that never
// happened — and cost the API a full capability refetch plus a planning-cache
// drop. A caller that gives up must leave the published hash alone.
func TestProxyCapabilitiesRejectsIncompleteProbeWithoutPublishing(t *testing.T) {
	const secret = "capability-secret"
	server := newDownloadProxyServer(t, secret)
	server.refreshCapabilitySnapshot(context.Background())
	published := decodeProxyHealth(t, server).CapabilitiesHash
	if published == "" {
		t.Fatal("no capability hash was published before the canceled request")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/hw-capabilities", nil).WithContext(canceled)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if got := decodeProxyHealth(t, server).CapabilitiesHash; got != published {
		t.Fatalf("health capabilities_hash = %q after an unfinished probe, want the previous %q", got, published)
	}
}

// The background snapshot has the same duty: a probe it could not finish is not
// evidence the proxy lost hardware.
func TestProxySnapshotKeepsPreviousHashWhenProbeCannotFinish(t *testing.T) {
	server := newDownloadProxyServer(t, "capability-secret")
	server.refreshCapabilitySnapshot(context.Background())
	published := decodeProxyHealth(t, server).CapabilitiesHash
	if published == "" {
		t.Fatal("no capability hash was published by the first snapshot")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	server.refreshCapabilitySnapshot(canceled)

	if got := decodeProxyHealth(t, server).CapabilitiesHash; got != published {
		t.Fatalf("health capabilities_hash = %q after an unfinished snapshot, want the previous %q", got, published)
	}
}

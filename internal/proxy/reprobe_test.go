package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A proxy runs ffmpeg for remux recipes, so it gets the same escape hatch a
// transcode node has: re-probe now, publish the result, and let health advertise
// it immediately instead of at the next 15-minute tick.
func TestProxyReprobeCapabilitiesRecomputesAndStoresHash(t *testing.T) {
	const secret = "capability-secret"
	server := newCapabilityProxyServer(t, secret)
	server.storeCapabilityHash("sha256:stale")

	request := httptest.NewRequest(http.MethodPost, "/admin/reprobe-capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var result reprobeCapabilitiesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode re-probe result: %v", err)
	}
	if result.CapabilityHash == "" || result.CapabilityHash == "sha256:stale" {
		t.Fatalf("capability_hash = %q, want a recomputed hash", result.CapabilityHash)
	}
	if got := decodeProxyHealth(t, server).CapabilitiesHash; got != result.CapabilityHash {
		t.Fatalf("health capabilities_hash = %q, want the re-probed %q", got, result.CapabilityHash)
	}
}

// A re-probe that cannot finish must answer 503 and keep the published hash: an
// unfinished probe is not evidence the proxy lost hardware.
func TestProxyReprobeCapabilitiesKeepsHashOnIncompleteProbe(t *testing.T) {
	const secret = "capability-secret"
	server := newCapabilityProxyServer(t, secret)
	server.refreshCapabilitySnapshot(context.Background())
	published := decodeProxyHealth(t, server).CapabilitiesHash
	if published == "" {
		t.Fatal("no capability hash was published before the canceled re-probe")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/admin/reprobe-capabilities", nil).WithContext(canceled)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decodeProxyHealth(t, server).CapabilitiesHash; got != published {
		t.Fatalf("health capabilities_hash = %q after an unfinished re-probe, want %q", got, published)
	}
}

// The route executes ffmpeg, so it stays inside the bearer-authed admin group.
func TestProxyReprobeCapabilitiesRequiresBearer(t *testing.T) {
	server := newCapabilityProxyServer(t, "capability-secret")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/reprobe-capabilities", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a bearer token", recorder.Code)
	}
}

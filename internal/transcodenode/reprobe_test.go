package transcodenode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func postReprobe(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/admin/reprobe-capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+testSecret)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

// A re-probe must recompute and publish, so health starts advertising the new
// hash immediately — the whole point of the action is that the API stops seeing
// a stale answer without waiting for the 15-minute snapshot tick.
func TestReprobeCapabilitiesRecomputesAndStoresHash(t *testing.T) {
	server := newTestServer(t)
	server.storeCapabilityHash("sha256:stale")

	recorder := postReprobe(t, server)
	if recorder.Code == http.StatusServiceUnavailable {
		t.Skip("this host's ffmpeg cannot answer a capability probe")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var result reprobeCapabilitiesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode re-probe result: %v", err)
	}
	if result.CapabilityHash == "" {
		t.Fatal("re-probe reported no capability_hash")
	}
	if result.CapabilityHash == "sha256:stale" {
		t.Fatal("re-probe echoed the stale hash instead of a recomputed one")
	}
	if result.Resolved == "" {
		t.Fatal("re-probe reported no resolved backend")
	}
	if got := server.storedCapabilityHash(); got != result.CapabilityHash {
		t.Fatalf("stored hash = %q, want the re-probed %q", got, result.CapabilityHash)
	}
	if got := decodeHealth(t, server).CapabilitiesHash; got != result.CapabilityHash {
		t.Fatalf("health capabilities_hash = %q, want the re-probed %q", got, result.CapabilityHash)
	}

	// The reported hash must describe the report the capability endpoint would
	// now serve, or the API would refetch and store something else.
	capabilityRequest := httptest.NewRequest(http.MethodGet, "/hw-capabilities", nil)
	capabilityRequest.Header.Set("Authorization", "Bearer "+testSecret)
	capabilityRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(capabilityRecorder, capabilityRequest)
	if capabilityRecorder.Code != http.StatusOK {
		t.Fatalf("capability status = %d after a re-probe", capabilityRecorder.Code)
	}
	var info playback.HWAccelInfo
	if err := json.Unmarshal(capabilityRecorder.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if info.CapabilityHash != result.CapabilityHash {
		t.Fatalf("served capability_hash = %q, want the re-probed %q", info.CapabilityHash, result.CapabilityHash)
	}
}

// An incomplete probe is not evidence the hardware changed, so a degraded
// re-probe must answer 503 and leave the previously published hash alone —
// publishing a partial report would announce a hardware change that did not
// happen and make the API store it.
func TestReprobeCapabilitiesKeepsHashOnProbeFailure(t *testing.T) {
	server, _ := newCapabilityTestServer(t)
	server.storeCapabilityHash("sha256:previous")

	if got := postReprobe(t, server).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for a probe that cannot complete", got)
	}
	if got := server.storedCapabilityHash(); got != "sha256:previous" {
		t.Fatalf("stored hash = %q, want the previous hash kept", got)
	}
}

// Every hardware probe ends in a real smoke encode that opens an encoder
// session. On a card at its concurrent session cap that encode fails with an
// error indistinguishable from a missing device, and the verdict would be
// published as verified:false — a hardware regression the server then persists
// and warns on, for a GPU that is fine and is at that moment encoding. So a busy
// node refuses, and keeps the report it has.
func TestReprobeCapabilitiesRefusesWhileTranscoding(t *testing.T) {
	server := newTestServer(t)
	server.storeCapabilityHash("sha256:previous")
	server.activeJobs.Store(2)
	t.Cleanup(func() { server.activeJobs.Store(0) })

	recorder := postReprobe(t, server)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while the node is transcoding", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "idle") {
		t.Fatalf("body = %q, want it to tell the operator when to retry", body)
	}
	if got := server.storedCapabilityHash(); got != "sha256:previous" {
		t.Fatalf("stored hash = %q, want the previous report untouched", got)
	}
}

// The route is bearer-authed like the rest of the admin group: it executes
// ffmpeg, so an unauthenticated caller could otherwise make a node do work.
func TestReprobeCapabilitiesRequiresBearer(t *testing.T) {
	server := newTestServer(t)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/reprobe-capabilities", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a bearer token", recorder.Code)
	}
}

// The active-job count only moves once ffmpeg is already running, so checking it
// alone leaves a window: a node idle at the check accepts a transcode while the
// probe still has minutes to go, and the smoke encode races the live encoder
// after all. Work that has been admitted but is not yet an active job has to
// refuse the re-probe too.
func TestReprobeCapabilitiesRefusesWhileWorkIsStarting(t *testing.T) {
	server := newTestServer(t)
	server.storeCapabilityHash("sha256:previous")
	if !server.gpu.beginWork() {
		t.Fatal("beginWork on an idle node was refused")
	}
	t.Cleanup(server.gpu.endWork)

	// activeJobs is deliberately zero: this is exactly the state the old
	// point-in-time check read as idle.
	if got := server.activeJobs.Load(); got != 0 {
		t.Fatalf("active jobs = %d, want the pre-registration window", got)
	}
	recorder := postReprobe(t, server)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while a transcode is starting", recorder.Code)
	}
	if got := server.storedCapabilityHash(); got != "sha256:previous" {
		t.Fatalf("stored hash = %q, want the previous report untouched", got)
	}
}

// The other direction: while a re-probe holds the encoder, new GPU work is
// refused rather than allowed to collide with the smoke encode. It is refused,
// not queued — a viewer pressing play must not wait out a multi-minute probe,
// and the API retries on another node.
func TestTranscodeStartRefusedWhileReprobing(t *testing.T) {
	server := newTestServer(t)
	if _, ok := server.gpu.beginReprobe(0); !ok {
		t.Fatal("re-probe refused on an idle node")
	}
	t.Cleanup(server.gpu.endReprobe)

	if server.gpu.beginWork() {
		t.Fatal("GPU work admitted while a re-probe held the encoder")
	}
}

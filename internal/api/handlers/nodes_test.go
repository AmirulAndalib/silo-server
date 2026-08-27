package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/go-chi/chi/v5"
)

type stubNodeRepository struct {
	nodes []*nodepool.Node
	// updateResult is what Update returns once validation passes; nil keeps the
	// default "unknown node" answer.
	updateResult *nodepool.Node
	updated      *nodepool.UpdateNodeInput
	// node is what GetByID returns; nil keeps the default "unknown node" answer.
	node *nodepool.Node
}

func (s *stubNodeRepository) List(context.Context) ([]*nodepool.Node, error) { return s.nodes, nil }

func (s *stubNodeRepository) GetByID(context.Context, int) (*nodepool.Node, error) {
	if s.node == nil {
		return nil, nodepool.ErrNodeNotFound
	}
	return s.node, nil
}

func (s *stubNodeRepository) Create(context.Context, nodepool.CreateNodeInput) (*nodepool.Node, error) {
	return nil, nodepool.ErrNodeNotFound
}

// Update mirrors Repository.Update's order of operations — validate, then
// write — so handler tests see the same errors production returns.
func (s *stubNodeRepository) Update(_ context.Context, _ int, input nodepool.UpdateNodeInput) (*nodepool.Node, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	s.updated = &input
	if s.updateResult == nil {
		return nil, nodepool.ErrNodeNotFound
	}
	return s.updateResult, nil
}

func (s *stubNodeRepository) Delete(context.Context, int) error { return nil }

func (s *stubNodeRepository) UpdateHealth(context.Context, int, string, bool, int, int, []byte) error {
	return nil
}

// The node list is the admin's inventory view, so it must carry the stored
// capability report, its age, and the derived GPU identities beside the
// existing node fields.
func TestHandleListNodesIncludesCapabilities(t *testing.T) {
	hash := "sha256:abc"
	refreshedAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repo := &stubNodeRepository{nodes: []*nodepool.Node{
		{
			ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: "http://gpu-1", Enabled: true, Healthy: true,
			Capabilities: json.RawMessage(`{"boot_id":"boot-1","resolved":"nvenc","render_device_details":[` +
				`{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0","gpu_uuid":"GPU-aaa"}]}`),
			CapabilitiesHash:        &hash,
			CapabilitiesRefreshedAt: &refreshedAt,
			// Production derives this in the node store's row scanner (covered
			// by TestScanNodeDerivesPhysicalGPUKeys); the stub stands in for it
			// so this test can assert the handler passes the field through.
			PhysicalGPUKeys: []string{"GPU-aaa"},
		},
		{ID: 2, Name: "old-node", Type: nodepool.NodeTypeProxy, URL: "http://old", Enabled: true},
	}}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	recorder := httptest.NewRecorder()
	handler.HandleListNodes(recorder, httptest.NewRequest(http.MethodGet, "/admin/nodes", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("returned %d nodes, want 2", len(items))
	}
	// Existing fields must still be present: this response is embedded, not
	// rebuilt, so a client reading it today keeps working.
	if items[0]["name"] != "gpu-1" || items[0]["healthy"] != true {
		t.Fatalf("node fields were lost: %v", items[0])
	}
	if items[0]["capabilities_hash"] != "sha256:abc" {
		t.Fatalf("capabilities_hash = %v", items[0]["capabilities_hash"])
	}
	if items[0]["capabilities_refreshed_at"] == nil {
		t.Fatalf("capabilities_refreshed_at missing: %v", items[0])
	}
	if items[0]["capabilities"] == nil {
		t.Fatalf("capabilities missing: %v", items[0])
	}
	keys, ok := items[0]["physical_gpu_keys"].([]any)
	if !ok || len(keys) != 1 || keys[0] != "GPU-aaa" {
		t.Fatalf("physical_gpu_keys = %v", items[0]["physical_gpu_keys"])
	}
	// A node that never reported capabilities carries none of the new fields
	// rather than empty ones a client would have to special-case.
	for _, field := range []string{"capabilities", "capabilities_hash", "capabilities_refreshed_at", "physical_gpu_keys"} {
		if _, present := items[1][field]; present {
			t.Fatalf("node without capabilities carries %q: %v", field, items[1])
		}
	}
}

func updateNodeRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/admin/nodes/1", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "1")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

// A per-node override may only name a backend the cluster-wide setting could
// also name. Rejecting it here is what turns a CHECK-constraint violation into
// an answer the admin UI can show.
func TestHandleUpdateNodeRejectsUnknownHWAccelOverride(t *testing.T) {
	repo := &stubNodeRepository{updateResult: &nodepool.Node{ID: 1, Name: "gpu-1"}}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	recorder := httptest.NewRecorder()
	handler.HandleUpdateNode(recorder, updateNodeRequest(t, `{"hw_accel_override":"videotoolbox"}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
	if repo.updated != nil {
		t.Fatalf("rejected input still reached the store: %+v", repo.updated)
	}
	if !strings.Contains(recorder.Body.String(), "hw_accel_override") {
		t.Fatalf("error body does not name the field: %s", recorder.Body.String())
	}
}

// Setting an override and clearing it again are both ordinary updates; the
// clear has to survive JSON decoding as a clear rather than as "unchanged".
func TestHandleUpdateNodeAcceptsAndClearsHWOverrides(t *testing.T) {
	accel, device := "vaapi", "/dev/dri/renderD129"
	tests := []struct {
		name       string
		body       string
		wantAccel  *string
		wantDevice *string
	}{
		{
			name:       "sets both",
			body:       `{"hw_accel_override":"vaapi","hw_device_override":"/dev/dri/renderD129"}`,
			wantAccel:  &accel,
			wantDevice: &device,
		},
		{
			name:       "explicit null clears both",
			body:       `{"hw_accel_override":null,"hw_device_override":null}`,
			wantAccel:  new(string),
			wantDevice: new(string),
		},
		{
			name: "omitted leaves both alone",
			body: `{"name":"gpu-1"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stored := "qsv"
			repo := &stubNodeRepository{updateResult: &nodepool.Node{ID: 1, Name: "gpu-1", HWAccelOverride: &stored}}
			handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

			recorder := httptest.NewRecorder()
			handler.HandleUpdateNode(recorder, updateNodeRequest(t, test.body))

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
			}
			if repo.updated == nil {
				t.Fatal("update never reached the store")
			}
			if !equalStringPointer(repo.updated.HWAccelOverride, test.wantAccel) {
				t.Fatalf("HWAccelOverride = %v, want %v", repo.updated.HWAccelOverride, test.wantAccel)
			}
			if !equalStringPointer(repo.updated.HWDeviceOverride, test.wantDevice) {
				t.Fatalf("HWDeviceOverride = %v, want %v", repo.updated.HWDeviceOverride, test.wantDevice)
			}
			// The response is the stored row, so the admin UI sees the effective
			// policy without a second read.
			var response map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response["hw_accel_override"] != "qsv" {
				t.Fatalf("response hw_accel_override = %v, want the stored value", response["hw_accel_override"])
			}
		})
	}
}

func equalStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// A node re-reads its own row on a 60s config poll, but this server starts
// dispatching the new backend the moment its pool reloads. An operator moving a
// node from QSV on a render node to NVENC on a CUDA index would otherwise get
// up to a minute of start requests pairing the new backend with the old device,
// so the node is nudged to reload before the updated policy is published.
//
// The nudge targets /admin/reload-config, never /admin/force-reload: the latter
// tears down every live playback session on a transcode node.
func TestHandleUpdateNodeReloadsTheNodeAfterAnOverrideChange(t *testing.T) {
	reloaded := make(chan string, 4)
	var destructive atomic.Bool
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/reload-config":
			reloaded <- r.Header.Get("Authorization")
		case "/admin/force-reload":
			destructive.Store(true)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(node.Close)

	qsv := "qsv"
	before := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL, HWAccelOverride: &qsv}
	nvenc := "nvenc"
	after := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL, HWAccelOverride: &nvenc}
	repo := &stubNodeRepository{updateResult: after, node: before}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	recorder := httptest.NewRecorder()
	handler.HandleUpdateNode(recorder, updateNodeRequest(t, `{"hw_accel_override":"nvenc"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case authorization := <-reloaded:
		if authorization != "Bearer secret" {
			t.Fatalf("node saw authorization %q, want the bearer secret", authorization)
		}
	default:
		t.Fatal("the node was not asked to reload after its overrides changed")
	}
	if destructive.Load() {
		t.Fatal("a policy edit hit the destructive force-reload route")
	}
	// The route answers 204, so a client that accepted only 200 would warn on
	// every successful reload — a standing false alarm on the ordinary path.
	if logged := recorder.Body.String(); strings.Contains(logged, "refused") {
		t.Fatalf("body mentions a refusal: %s", logged)
	}
}

// The node reload route answers 204. Treating anything outside 2xx as a refusal
// is what keeps a successful reload from logging a failure an operator would
// then go looking for.
func TestReloadNodeConfigAcceptsNoContent(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			called := make(chan struct{}, 1)
			node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called <- struct{}{}
				w.WriteHeader(status)
			}))
			t.Cleanup(node.Close)

			handler := NewNodeHandler(&stubNodeRepository{}, nil, nil, nil, nil, nil, "secret")
			handler.reloadNodeConfig(context.Background(), &nodepool.Node{ID: 1, Name: "gpu-1", URL: node.URL})

			select {
			case <-called:
			default:
				t.Fatal("the node was never called")
			}
		})
	}
}

// This server's cached view of what a node can do — the v3 planning inventory —
// is keyed by node URL and holds the tone-map executors and transformations the
// *previous* backend advertised. Changing the policy without dropping it plans
// the next minute's sessions against filters the worker has already moved off,
// and the worker then rejects the start.
func TestHandleUpdateNodeInvalidatesCapabilityCacheAfterAnOverrideChange(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(node.Close)

	qsv, nvenc := "qsv", "nvenc"
	before := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL, HWAccelOverride: &qsv}
	after := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL, HWAccelOverride: &nvenc}
	repo := &stubNodeRepository{updateResult: after, node: before}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	invalidated := make(chan string, 4)
	handler.SetCapabilityInvalidator(func(url string) { invalidated <- url })

	handler.HandleUpdateNode(httptest.NewRecorder(), updateNodeRequest(t, `{"hw_accel_override":"nvenc"}`))

	select {
	case url := <-invalidated:
		if url != node.URL {
			t.Fatalf("invalidated %q, want the node's URL %q", url, node.URL)
		}
	default:
		t.Fatal("the capability cache was not dropped after the policy changed")
	}
}

// An edit that moves neither override leaves the cache alone: re-probing every
// node on every rename would put ffmpeg execs behind an unrelated form save.
func TestHandleUpdateNodeKeepsCapabilityCacheWithoutAnOverrideChange(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(node.Close)

	stored := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL}
	repo := &stubNodeRepository{updateResult: stored, node: stored}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	invalidated := make(chan string, 4)
	handler.SetCapabilityInvalidator(func(url string) { invalidated <- url })

	handler.HandleUpdateNode(httptest.NewRecorder(), updateNodeRequest(t, `{"name":"gpu-one"}`))

	select {
	case url := <-invalidated:
		t.Fatalf("a rename dropped the capability cache for %q", url)
	default:
	}
}

// The admin form posts both override fields on every transcode-node save, so
// their presence says nothing about them moving. Nudging on presence alone made
// an unrelated edit — a rename, a capacity change, or a plain resubmit — ask the
// node to re-read its config for nothing.
func TestHandleUpdateNodeDoesNotReloadWhenOverridesAreUnchanged(t *testing.T) {
	reloaded := make(chan struct{}, 4)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin/reload") || strings.HasPrefix(r.URL.Path, "/admin/force") {
			reloaded <- struct{}{}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(node.Close)

	qsv := "qsv"
	device := "/dev/dri/renderD128"
	unchanged := func() *nodepool.Node {
		accel, path := qsv, device
		return &nodepool.Node{
			ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL,
			HWAccelOverride: &accel, HWDeviceOverride: &path,
		}
	}
	repo := &stubNodeRepository{updateResult: unchanged(), node: unchanged()}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	body := `{"name":"gpu-1","hw_accel_override":"qsv","hw_device_override":"/dev/dri/renderD128"}`
	handler.HandleUpdateNode(httptest.NewRecorder(), updateNodeRequest(t, body))

	select {
	case <-reloaded:
		t.Fatal("an edit that moved neither override still asked the node to reload")
	default:
	}
}

// An edit that touches no acceleration field must not cost a round trip to the
// node: renaming a node has nothing to do with what it probes.
func TestHandleUpdateNodeDoesNotReloadWithoutAnOverrideChange(t *testing.T) {
	reloaded := make(chan struct{}, 4)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			reloaded <- struct{}{}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(node.Close)

	stored := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL}
	repo := &stubNodeRepository{updateResult: stored, node: stored}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	handler.HandleUpdateNode(httptest.NewRecorder(), updateNodeRequest(t, `{"name":"gpu-one"}`))

	select {
	case <-reloaded:
		t.Fatal("a rename asked the node to reload its configuration")
	default:
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func (s *stubNodeRepository) UpdateHealth(context.Context, int, bool, int, int, []byte) error {
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
func TestHandleUpdateNodeReloadsTheNodeAfterAnOverrideChange(t *testing.T) {
	reloaded := make(chan string, 4)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/force-reload" && r.Method == http.MethodPost {
			reloaded <- r.Header.Get("Authorization")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(node.Close)

	stored := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL}
	repo := &stubNodeRepository{updateResult: stored, node: stored}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	body := `{"hw_accel_override":"nvenc","hw_device_override":"0"}`
	request := httptest.NewRequest(http.MethodPut, "/admin/nodes/1", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "1")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx))
	recorder := httptest.NewRecorder()
	handler.HandleUpdateNode(recorder, request)

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
}

// An edit that touches no acceleration field must not cost a round trip to the
// node: renaming a node has nothing to do with what it probes.
func TestHandleUpdateNodeDoesNotReloadWithoutAnOverrideChange(t *testing.T) {
	reloaded := make(chan struct{}, 4)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/force-reload" {
			reloaded <- struct{}{}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(node.Close)

	stored := &nodepool.Node{ID: 1, Name: "gpu-1", Type: nodepool.NodeTypeTranscode, URL: node.URL}
	repo := &stubNodeRepository{updateResult: stored, node: stored}
	handler := NewNodeHandler(repo, nil, nil, nil, nil, nil, "secret")

	request := httptest.NewRequest(http.MethodPut, "/admin/nodes/1", strings.NewReader(`{"name":"gpu-one"}`))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "1")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx))
	handler.HandleUpdateNode(httptest.NewRecorder(), request)

	select {
	case <-reloaded:
		t.Fatal("a rename asked the node to reload its configuration")
	default:
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodepool"
)

// An NVIDIA uuid identifies a card wherever it is plugged in; a PCI address
// only identifies a slot, and only within one boot of one kernel. Deriving the
// key this way is what lets an admin see that two nodes are sharing one GPU.
func TestPhysicalGPUKeys(t *testing.T) {
	tests := []struct {
		name         string
		capabilities string
		want         []string
	}{
		{
			name: "prefers gpu uuid over slot identity",
			capabilities: `{"boot_id":"boot-1","render_device_details":[
				{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0","gpu_uuid":"GPU-aaa"}]}`,
			want: []string{"GPU-aaa"},
		},
		{
			name: "falls back to boot-scoped pci address",
			capabilities: `{"boot_id":"boot-1","render_device_details":[
				{"path":"/dev/dri/renderD129","pci_address":"0000:04:00.0"}]}`,
			want: []string{"boot-1|0000:04:00.0"},
		},
		{
			name: "mixed devices are deduped and sorted",
			capabilities: `{"boot_id":"boot-1","render_device_details":[
				{"path":"/dev/dri/renderD130","pci_address":"0000:05:00.0"},
				{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0","gpu_uuid":"GPU-bbb"},
				{"path":"/dev/dri/renderD129","pci_address":"0000:03:00.0","gpu_uuid":"GPU-bbb"}]}`,
			want: []string{"GPU-bbb", "boot-1|0000:05:00.0"},
		},
		{
			name: "device with no identity contributes no key",
			capabilities: `{"boot_id":"boot-1","render_device_details":[
				{"path":"/dev/dri/renderD128"},{"path":"/dev/dri/renderD129","gpu_uuid":"GPU-ccc"}]}`,
			want: []string{"GPU-ccc"},
		},
		{name: "no capabilities stored", capabilities: "", want: nil},
		{name: "unparseable payload", capabilities: `not json`, want: nil},
		{name: "no render devices", capabilities: `{"boot_id":"boot-1","render_device_details":[]}`, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := physicalGPUKeys([]byte(tt.capabilities))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("physicalGPUKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

type stubNodeRepository struct {
	nodes []*nodepool.Node
}

func (s *stubNodeRepository) List(context.Context) ([]*nodepool.Node, error) { return s.nodes, nil }

func (s *stubNodeRepository) GetByID(context.Context, int) (*nodepool.Node, error) {
	return nil, nodepool.ErrNodeNotFound
}

func (s *stubNodeRepository) Create(context.Context, nodepool.CreateNodeInput) (*nodepool.Node, error) {
	return nil, nodepool.ErrNodeNotFound
}

func (s *stubNodeRepository) Update(context.Context, int, nodepool.UpdateNodeInput) (*nodepool.Node, error) {
	return nil, nodepool.ErrNodeNotFound
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

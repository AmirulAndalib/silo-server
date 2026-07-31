package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transfers"
)

const (
	testTransferID = "transfer-1"
	testProfileID  = "profile-1"
	testLocalNode  = "local"
)

func TestHandleListSessionsAddsSiblingTransfersWithoutChangingSessionShape(t *testing.T) {
	sm := playback.NewSessionManager(0, 0)
	ctx := playback.WithClientInfo(context.Background(), playback.ClientInfo{Name: "native client"})
	session, err := sm.StartSessionWithContext(ctx, 42, testProfileID, 9, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("StartSessionWithContext: %v", err)
	}
	registry := transfers.New()
	started := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	if err := registry.Begin(transfers.Transfer{
		ID:          testTransferID,
		UserID:      42,
		ProfileID:   testProfileID,
		MediaFileID: 9,
		Route:       "native_direct",
		StartedAt:   started,
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	h := NewNodeHandler(nil, nil, nil, nil, nil, nil, "")
	h.SetLocalSessionSource(sm, testLocalNode)
	h.SetTransferSource(registry)
	rec := httptest.NewRecorder()
	h.HandleListSessions(rec, httptest.NewRequest("GET", "/admin/nodes/sessions", nil))

	var body struct {
		Sessions  []json.RawMessage    `json:"sessions"`
		Transfers []transfers.Transfer `json:"transfers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("sessions = %s", body.Sessions)
	}
	// This session has never served a byte, so last_served_at is omitted
	// rather than reporting the client's last progress report as a serve time
	// (decision A5). The field is still emitted once real bytes are served —
	// asserted below.
	expectedSession, err := json.Marshal(nodesessions.SessionInfo{
		SessionID:   session.ID,
		NodeName:    testLocalNode,
		AuthUserID:  session.UserID,
		ProfileID:   session.ProfileID,
		Type:        string(session.PlayMethod),
		Route:       session.Origin(),
		MediaFileID: session.MediaFileID,
		ClientName:  session.ClientName,
		StartedAt:   session.StartedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal expected session: %v", err)
	}
	if !bytes.Equal(body.Sessions[0], expectedSession) {
		t.Fatalf("session JSON changed:\n got %s\nwant %s", body.Sessions[0], expectedSession)
	}
	if len(body.Transfers) != 1 || body.Transfers[0].ID != testTransferID {
		t.Fatalf("transfers = %+v", body.Transfers)
	}

	// Once the server actually serves bytes, last_served_at reappears — the
	// field is omitted for want of a server-observed serve, not removed.
	if err := sm.BeginTransport(session.ID); err != nil {
		t.Fatalf("BeginTransport: %v", err)
	}
	rec = httptest.NewRecorder()
	h.HandleListSessions(rec, httptest.NewRequest("GET", "/admin/nodes/sessions", nil))
	var served struct {
		Sessions []nodesessions.SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &served); err != nil {
		t.Fatalf("decode served response: %v", err)
	}
	if len(served.Sessions) != 1 || served.Sessions[0].LastServedAt == "" {
		t.Fatalf("last_served_at missing after a served transport: %+v", served.Sessions)
	}
}

func TestHandleListSessionsNodeFilterExcludesLocalTransfers(t *testing.T) {
	registry := transfers.New()
	if err := registry.Begin(transfers.Transfer{ID: testTransferID}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	h := NewNodeHandler(nil, nil, nil, nil, nil, nil, "")
	h.SetTransferSource(registry)
	rec := httptest.NewRecorder()
	h.HandleListSessions(rec, httptest.NewRequest("GET", "/admin/nodes/sessions?node_id=12", nil))

	var body struct {
		Transfers []transfers.Transfer `json:"transfers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Transfers == nil || len(body.Transfers) != 0 {
		t.Fatalf("filtered transfers = %+v, want non-nil empty array", body.Transfers)
	}
}

// Schema support and runtime availability are separate claims: the transfers key
// is always in the payload once this endpoint exists, but the registry behind it
// is optional wiring. Advertising them as one value would promise download
// monitoring an edge deployment is not actually running.
func TestNodeSessionsCapabilitiesSeparatesSchemaFromRuntimeWiring(t *testing.T) {
	t.Run("registry wired", func(t *testing.T) {
		h := NewNodeHandler(nil, nil, nil, nil, nil, nil, "")
		h.SetTransferSource(transfers.New())
		rec := httptest.NewRecorder()
		h.HandleGetNodeSessionsCapabilities(rec, httptest.NewRequest("GET", "/admin/node-sessions/capabilities", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body nodeSessionsCapabilitiesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !body.LogicalSessionID || !body.Transfers || !body.TransfersActive {
			t.Fatalf("capabilities = %+v, want schema flags and transfers_active true", body)
		}
	})

	t.Run("no registry wired", func(t *testing.T) {
		h := NewNodeHandler(nil, nil, nil, nil, nil, nil, "")
		rec := httptest.NewRecorder()
		h.HandleGetNodeSessionsCapabilities(rec, httptest.NewRequest("GET", "/admin/node-sessions/capabilities", nil))

		var body nodeSessionsCapabilitiesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !body.Transfers {
			t.Error("transfers = false; the response shape carries the key regardless of wiring")
		}
		if !body.LogicalSessionID {
			t.Error("logical_session_id = false; SessionInfo schema supports the field regardless of wiring")
		}
		if body.TransfersActive {
			t.Error("transfers_active = true with no registry wired; that advertises monitoring that is not running")
		}
	})
}

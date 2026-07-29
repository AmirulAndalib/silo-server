package handlers

import (
	"bytes"
	"context"
	"encoding/json"
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
	expectedSession, err := json.Marshal(nodesessions.SessionInfo{
		SessionID:    session.ID,
		NodeName:     testLocalNode,
		AuthUserID:   session.UserID,
		ProfileID:    session.ProfileID,
		Type:         string(session.PlayMethod),
		Route:        session.Origin(),
		MediaFileID:  session.MediaFileID,
		ClientName:   session.ClientName,
		StartedAt:    session.StartedAt.UTC().Format(time.RFC3339),
		LastServedAt: session.LastActivityAt.UTC().Format(time.RFC3339),
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

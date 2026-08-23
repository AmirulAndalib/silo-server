package handlers

import (
	"encoding/json"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func realtimeResultMessage(t *testing.T, sessionID, commandID string, status playback.RealtimeResultStatus) []byte {
	t.Helper()
	data, err := json.Marshal(playback.ResultEnvelope{
		Type:      playback.RealtimeMessageTypeResult,
		CommandID: commandID,
		SessionID: sessionID,
		Status:    status,
	})
	if err != nil {
		t.Fatalf("marshal result envelope: %v", err)
	}
	return data
}

// A client that refuses a plan invalidation is left running a route the server
// has withdrawn, and its rejection already canceled the command deadline —
// so the rejection itself has to stop the session.
func TestRealtimeRejectedPlanInvalidationStopsSession(t *testing.T) {
	sessionMgr := playback.NewSessionManager(0, 0)
	handler := NewPlaybackHandler(sessionMgr)
	handler.CommandTracker = playback.NewCommandTracker()
	defer handler.CommandTracker.Close()

	session, err := sessionMgr.StartSession(1, "profile-1", 100, playback.PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	handler.rememberRealtimeCommand("cmd-1", session.ID, playback.CommandPlanInvalidated)

	if err := handler.handleRealtimeClientMessage(session.ID,
		realtimeResultMessage(t, session.ID, "cmd-1", playback.RealtimeResultStatusRejected)); err != nil {
		t.Fatalf("handleRealtimeClientMessage: %v", err)
	}

	if _, err := sessionMgr.GetSession(session.ID); err == nil {
		t.Fatal("session survived a rejected plan invalidation, want it stopped")
	}
}

// A completed invalidation means the client replanned itself: the session must
// stay alive on its replacement plan.
func TestRealtimeCompletedPlanInvalidationKeepsSession(t *testing.T) {
	sessionMgr := playback.NewSessionManager(0, 0)
	handler := NewPlaybackHandler(sessionMgr)
	handler.CommandTracker = playback.NewCommandTracker()
	defer handler.CommandTracker.Close()

	session, err := sessionMgr.StartSession(1, "profile-1", 100, playback.PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	handler.rememberRealtimeCommand("cmd-1", session.ID, playback.CommandPlanInvalidated)

	if err := handler.handleRealtimeClientMessage(session.ID,
		realtimeResultMessage(t, session.ID, "cmd-1", playback.RealtimeResultStatusCompleted)); err != nil {
		t.Fatalf("handleRealtimeClientMessage: %v", err)
	}

	if _, err := sessionMgr.GetSession(session.ID); err != nil {
		t.Fatalf("GetSession after a completed replan: %v, want the session kept", err)
	}
}

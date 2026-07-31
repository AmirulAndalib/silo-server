package playback

import (
	"testing"
	"time"
)

func TestNeverServedProgressPingDoesNotPreventReaping(t *testing.T) {
	m := NewSessionManager(0, 0)
	m.SetLivenessGracePeriods(time.Minute, time.Hour)
	m.SetUnservedSessionGrace(2 * time.Minute)

	session, err := m.StartSession(1, "profile", 100, PlayDirect, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	m.mu.Lock()
	m.sessions[session.ID].StartedAt = time.Now().Add(-90 * time.Second)
	m.mu.Unlock()
	if err := m.UpdateProgress(session.ID, 42, false); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	if expired := m.CleanStale(); len(expired) != 0 {
		t.Fatalf("expired within configured unserved grace = %+v, want none", expired)
	}

	m.mu.Lock()
	m.sessions[session.ID].StartedAt = time.Now().Add(-3 * time.Minute)
	m.mu.Unlock()
	if err := m.UpdateProgress(session.ID, 43, false); err != nil {
		t.Fatalf("UpdateProgress after grace: %v", err)
	}
	expired := m.CleanStale()
	if len(expired) != 1 || expired[0].ID != session.ID {
		t.Fatalf("expired = %+v, want progress-pinged never-served session %q", expired, session.ID)
	}
}

func TestActiveTransportPreventsServerObservedReaping(t *testing.T) {
	m := NewSessionManager(0, 0)
	m.SetUnservedSessionGrace(0)

	session, err := m.StartSession(1, "profile", 100, PlayDirect, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := m.BeginTransport(session.ID); err != nil {
		t.Fatalf("BeginTransport: %v", err)
	}

	if expired := m.CleanInactive(0, 0); len(expired) != 0 {
		t.Fatalf("expired active transport = %+v, want none", expired)
	}
}

func TestFreshLastServedSurvivesStaleClientActivity(t *testing.T) {
	m := NewSessionManager(0, 0)
	m.SetUnservedSessionGrace(0)

	session, err := m.StartSession(1, "profile", 100, PlayDirect, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	m.mu.Lock()
	s := m.sessions[session.ID]
	s.LastServedAt = time.Now()
	s.LastActivityAt = time.Now().Add(-time.Hour)
	s.UpdatedAt = s.LastActivityAt
	m.mu.Unlock()

	if expired := m.CleanInactive(time.Minute, time.Hour); len(expired) != 0 {
		t.Fatalf("expired freshly-served session = %+v, want none", expired)
	}
}

func TestPausedWebSocketConnectionPreventsServerObservedReaping(t *testing.T) {
	m := NewSessionManager(0, 0)
	m.SetUnservedSessionGrace(0)

	session, err := m.StartSession(1, "profile", 100, PlayTranscode, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := m.UpdateProgress(session.ID, 42, true); err != nil {
		t.Fatalf("UpdateProgress(paused): %v", err)
	}
	if err := m.SetWebSocket(session.ID, true); err != nil {
		t.Fatalf("SetWebSocket: %v", err)
	}
	m.mu.Lock()
	m.sessions[session.ID].LastServedAt = time.Now().Add(-time.Hour)
	m.mu.Unlock()

	if expired := m.CleanInactive(time.Minute, 30*time.Minute); len(expired) != 0 {
		t.Fatalf("expired WebSocket-connected paused session = %+v, want none", expired)
	}
}

func TestPausedWithoutRealtimeConnectionReapsFromLastServed(t *testing.T) {
	m := NewSessionManager(0, 0)
	m.SetUnservedSessionGrace(0)

	session, err := m.StartSession(1, "profile", 100, PlayTranscode, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	m.mu.Lock()
	m.sessions[session.ID].LastServedAt = time.Now().Add(-time.Hour)
	m.mu.Unlock()
	if err := m.UpdateProgress(session.ID, 42, true); err != nil {
		t.Fatalf("UpdateProgress(paused): %v", err)
	}

	expired := m.CleanInactive(time.Minute, 30*time.Minute)
	if len(expired) != 1 || expired[0].ID != session.ID {
		t.Fatalf("expired = %+v, want paused disconnected session %q", expired, session.ID)
	}
}

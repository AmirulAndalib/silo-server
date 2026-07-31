package playback

import (
	"errors"
	"testing"
	"time"
)

func TestAddServedBytesUnknownDoesNotCreateSession(t *testing.T) {
	m := NewSessionManager(0, 0)
	if err := m.AddServedBytes("missing", 100); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("AddServedBytes error=%v, want ErrSessionNotFound", err)
	}
	if len(m.AllSessions()) != 0 {
		t.Fatal("AddServedBytes recreated an unknown session")
	}
}

func TestLastServedAtOnlyAdvancesForServerObservedEvents(t *testing.T) {
	m := NewSessionManager(0, 0)
	s, err := m.StartSession(1, "p", 1, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateProgress(s.ID, 10, false); err != nil {
		t.Fatal(err)
	}
	afterProgress, _ := m.GetSession(s.ID)
	if !afterProgress.LastServedAt.IsZero() {
		t.Fatalf("UpdateProgress advanced LastServedAt: %v", afterProgress.LastServedAt)
	}
	if err := m.AddServedBytes(s.ID, 7); err != nil {
		t.Fatal(err)
	}
	afterBytes, _ := m.GetSession(s.ID)
	if afterBytes.LastServedAt.IsZero() || afterBytes.BytesServed != 7 {
		t.Fatalf("AddServedBytes did not update served state: %+v", afterBytes)
	}
	first := afterBytes.LastServedAt
	time.Sleep(time.Millisecond)
	if err := m.BeginTransport(s.ID); err != nil {
		t.Fatal(err)
	}
	afterBegin, _ := m.GetSession(s.ID)
	if !afterBegin.LastServedAt.After(first) {
		t.Fatal("BeginTransport did not advance LastServedAt")
	}
}

func TestTranscodeLivenessGraceAndPausedPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		method     PlayMethod
		paused     bool
		idle       time.Duration
		wantReaped bool
	}{
		{name: "transcode five minutes retained", method: PlayTranscode, idle: 5 * time.Minute},
		{name: "transcode fifteen minutes reaped", method: PlayTranscode, idle: 15 * time.Minute, wantReaped: true},
		{name: "paused transcode keeps thirty minutes", method: PlayTranscode, paused: true, idle: 15 * time.Minute},
		{name: "direct keeps active grace", method: PlayDirect, idle: time.Minute, wantReaped: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSessionManager(0, 0)
			s, err := m.StartSession(1, "p", 1, tt.method, false)
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			m.sessions[s.ID].IsPaused = tt.paused
			m.sessions[s.ID].LastServedAt = time.Now().Add(-tt.idle)
			m.mu.Unlock()
			reaped := m.CleanStale()
			if (len(reaped) > 0) != tt.wantReaped {
				t.Fatalf("reaped=%d, wantReaped=%v", len(reaped), tt.wantReaped)
			}
		})
	}
}

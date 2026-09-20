package watchtogether

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestReconnectPreservesPlayingSession(t *testing.T) {
	now := time.Now().UTC()
	repo := &stubRepo{room: baseRoom(now)}
	s := newServiceForTest(now, repo, &stubSessions{session: &playback.Session{UserID: 7, ProfileID: "host", MediaFileID: 1}}, &stubFiles{file: &models.MediaFile{ContentID: "movie-1"}}, nil)
	t.Cleanup(s.Close)
	old := new(recordingConn)
	live := s.rooms[repo.room.ID]
	live.members[buildMemberKey(7, "host")] = &memberState{userID: 7, profileID: "host", sessionID: "host-session", connection: old, isReady: true}
	live.members[buildMemberKey(8, "guest")] = &memberState{userID: 8, profileID: "guest", sessionID: "guest-session", connection: new(recordingConn), isReady: true}
	s.Disconnect(registrationFor(repo.room.ID, 7, "host", old), false)
	reg, _, err := s.Connect(t.Context(), repo.room.ID, 7, "host", new(recordingConn))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.AttachSessionForConnection(t.Context(), reg, 7, "host", "host-session")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PlaybackState != RoomPlaybackStatePlaying || repo.room.Generation != 1 {
		t.Fatalf("socket renewal interrupted playback: %+v", snapshot)
	}
}

func TestAttachDuringSeekRequiresSeekPosition(t *testing.T) {
	now := time.Now().UTC()
	repo := &stubRepo{room: baseRoom(now)}
	s := newServiceForTest(now, repo, &stubSessions{session: &playback.Session{UserID: 7, ProfileID: "host", MediaFileID: 1}}, &stubFiles{file: &models.MediaFile{ContentID: "movie-1"}}, nil)
	t.Cleanup(s.Close)
	conn := new(recordingConn)
	live := s.rooms[repo.room.ID]
	live.members[buildMemberKey(7, "host")] = &memberState{userID: 7, profileID: "host", sessionID: "session", connection: conn}
	reg := registrationFor(repo.room.ID, 7, "host", conn)
	if _, err := s.HandleTransportRequestForConnection(t.Context(), reg, 7, "host", TransportRequest{Action: TransportActionSeek, PositionSeconds: new(1500.0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachSessionForConnection(t.Context(), reg, 7, "host", "replacement-session"); err != nil {
		t.Fatal(err)
	}
	command := conn.payloads[len(conn.payloads)-1]["command"].(TransportCommand)
	snapshot, err := s.HandleReadyForConnection(t.Context(), reg, 7, "host", StateReport{SessionID: "replacement-session", CommandID: command.CommandID, PositionSeconds: 20})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PlaybackState != RoomPlaybackStateWaiting {
		t.Fatalf("old stream satisfied seek barrier: %+v", snapshot)
	}
}

type attemptLookup struct {
	record *playback.AttemptRecordV3
	err    error
}

func (a attemptLookup) GetAttempt(context.Context, string) (*playback.AttemptRecordV3, error) {
	return a.record, a.err
}

func TestAttachUsesDurablePlaybackOwnership(t *testing.T) {
	now := time.Now().UTC()
	s := newServiceForTest(now, &stubRepo{room: baseRoom(now)}, &stubSessions{}, &stubFiles{file: &models.MediaFile{ContentID: "movie-1"}}, nil)
	t.Cleanup(s.Close)
	reg, _, err := s.Connect(t.Context(), "room-1", 7, "host", new(recordingConn))
	if err != nil {
		t.Fatal(err)
	}
	record := &playback.AttemptRecordV3{SessionID: "remote-session", UserID: 7, ProfileID: "host", EffectiveMediaFileID: 1}
	s.SetPlaybackAttemptStore(attemptLookup{record: record})
	if _, err = s.AttachSessionForConnection(t.Context(), reg, 7, "host", record.SessionID); err != nil {
		t.Fatalf("durable session on another node: %v", err)
	}
	record.ProfileID = "someone-else"
	if _, err = s.AttachSessionForConnection(t.Context(), reg, 7, "host", record.SessionID); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("ownership bypass: %v", err)
	}
	record.ProfileID = "host"
	record.StoppedAt = &now
	if _, err = s.AttachSessionForConnection(t.Context(), reg, 7, "host", record.SessionID); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("stopped session accepted: %v", err)
	}
}

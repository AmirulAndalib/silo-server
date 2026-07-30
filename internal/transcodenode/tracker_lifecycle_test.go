package transcodenode

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type transcodeRedisHook struct {
	mu   sync.Mutex
	keys map[string]bool
}

func (h *transcodeRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *transcodeRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		switch cmd.Name() {
		case "set":
			h.keys[cmd.Args()[1].(string)] = true
			if status, ok := cmd.(*redis.StatusCmd); ok {
				status.SetVal("OK")
			}
			return nil
		case "del":
			for _, arg := range cmd.Args()[1:] {
				delete(h.keys, arg.(string))
			}
			if count, ok := cmd.(*redis.IntCmd); ok {
				count.SetVal(1)
			}
			return nil
		}
		return next(ctx, cmd)
	}
}
func (h *transcodeRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func newTranscodeLifecycleTracker(t *testing.T) (*nodesessions.Tracker, *transcodeRedisHook) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Dialer: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("unexpected Redis dial")
		return nil, nil
	}})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &transcodeRedisHook{keys: make(map[string]bool)}
	rdb.AddHook(hook)
	return nodesessions.NewTracker(rdb, "http://node", "node", "transcode"), hook
}

func (h *transcodeRedisHook) has(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.keys[key]
}

func TestDelayedTrackSkipsStoppedSession(t *testing.T) {
	tracker, redisState := newTranscodeLifecycleTracker(t)
	session := &playback.TranscodeSession{}
	var queued []func()
	s := &Server{
		tracker:  tracker,
		sessions: map[string]*playback.TranscodeSession{"transport": session},
		runTracker: func(fn func()) {
			queued = append(queued, fn)
		},
	}
	s.trackIfCurrent(context.Background(), "transport", session, nodesessions.SessionInfo{
		SessionID: "transport", LogicalSessionID: "logical",
	})

	unlock := s.lockSessionLifecycle("transport")
	s.mu.Lock()
	delete(s.sessions, "transport")
	s.mu.Unlock()
	tracker.Remove(context.Background(), "transport")
	unlock()
	queued[0]()

	if got := tracker.Snapshot(); len(got) != 0 {
		t.Fatalf("delayed track recreated stopped record: %+v", got)
	}
	key := nodesessions.KeyPrefix + tracker.NodeHash() + ":transport"
	if redisState.has(key) {
		t.Fatal("delayed track recreated stopped Redis key")
	}
}

func TestDelayedTrackSkipsReplacedSession(t *testing.T) {
	tracker, _ := newTranscodeLifecycleTracker(t)
	oldSession := &playback.TranscodeSession{}
	newSession := &playback.TranscodeSession{}
	var queued []func()
	s := &Server{
		tracker:  tracker,
		sessions: map[string]*playback.TranscodeSession{"transport": oldSession},
		runTracker: func(fn func()) {
			queued = append(queued, fn)
		},
	}
	s.trackIfCurrent(context.Background(), "transport", oldSession, nodesessions.SessionInfo{
		SessionID: "transport", LogicalSessionID: "old",
	})

	unlock := s.lockSessionLifecycle("transport")
	s.mu.Lock()
	s.sessions["transport"] = newSession
	s.mu.Unlock()
	tracker.Remove(context.Background(), "transport")
	tracker.Track(context.Background(), nodesessions.SessionInfo{
		SessionID: "transport", LogicalSessionID: "new",
	})
	unlock()
	queued[0]()

	got := tracker.Snapshot()
	if len(got) != 1 || got[0].LogicalSessionID != "new" {
		t.Fatalf("old delayed track replaced new record: %+v", got)
	}
}

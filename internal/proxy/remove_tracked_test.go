package proxy

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/redis/go-redis/v9"
)

type deleteCaptureHook struct {
	mu         sync.Mutex
	called     bool
	contextErr error
	values     map[string]string
}

func (h *deleteCaptureHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *deleteCaptureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		switch cmd.Name() {
		case "set":
			args := cmd.Args()
			h.values[args[1].(string)] = "set"
			if status, ok := cmd.(*redis.StatusCmd); ok {
				status.SetVal("OK")
			}
			return nil
		case "del":
			h.called = true
			h.contextErr = ctx.Err()
			for _, arg := range cmd.Args()[1:] {
				delete(h.values, arg.(string))
			}
			if count, ok := cmd.(*redis.IntCmd); ok {
				count.SetVal(1)
			}
			return nil
		}
		return next(ctx, cmd)
	}
}

func (h *deleteCaptureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func newProxyTestTracker(t *testing.T) (*nodesessions.Tracker, *deleteCaptureHook) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("Redis command should be intercepted before dialing")
			return nil, nil
		},
	})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &deleteCaptureHook{values: make(map[string]string)}
	rdb.AddHook(hook)
	return nodesessions.NewTracker(rdb, "http://node", "node", "proxy"), hook
}

func (h *deleteCaptureHook) has(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.values[key]
	return ok
}

func TestReleaseTrackedUsesLiveContextAfterRequestCancellation(t *testing.T) {
	tracker, hook := newProxyTestTracker(t)
	server := &Server{tracker: tracker}
	lease := tracker.Track(context.Background(), nodesessions.SessionInfo{SessionID: "session-1"})

	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if requestCtx.Err() == nil {
		t.Fatal("request context was not canceled")
	}
	func() {
		defer server.releaseTracked(lease)
	}()

	hook.mu.Lock()
	defer hook.mu.Unlock()
	if !hook.called {
		t.Fatal("Redis DEL was not attempted")
	}
	if hook.contextErr != nil {
		t.Fatalf("DEL context error = %v, want live bounded cleanup context", hook.contextErr)
	}
}

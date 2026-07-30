package proxy

import (
	"context"
	"net"
	"testing"

	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/redis/go-redis/v9"
)

type deleteCaptureHook struct {
	called     bool
	contextErr error
}

func (h *deleteCaptureHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *deleteCaptureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "del" {
			h.called = true
			h.contextErr = ctx.Err()
			return nil
		}
		return next(ctx, cmd)
	}
}

func (h *deleteCaptureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestRemoveTrackedUsesLiveContextAfterRequestCancellation(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("DEL should be intercepted before dialing")
			return nil, nil
		},
	})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &deleteCaptureHook{}
	rdb.AddHook(hook)
	server := &Server{tracker: nodesessions.NewTracker(rdb, "http://node", "node", "proxy")}

	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if requestCtx.Err() == nil {
		t.Fatal("request context was not canceled")
	}
	func() {
		defer server.removeTracked("session-1")
	}()

	if !hook.called {
		t.Fatal("Redis DEL was not attempted")
	}
	if hook.contextErr != nil {
		t.Fatalf("DEL context error = %v, want live bounded cleanup context", hook.contextErr)
	}
}

package nodesessions

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

type memoryRedisHook struct {
	mu     sync.Mutex
	values map[string]string
}

func (h *memoryRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *memoryRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		switch c := cmd.(type) {
		case *redis.StatusCmd:
			if c.Name() == "set" {
				args := c.Args()
				var value string
				switch v := args[2].(type) {
				case string:
					value = v
				case []byte:
					value = string(v)
				}
				h.values[args[1].(string)] = value
				c.SetVal("OK")
				return nil
			}
		case *redis.IntCmd:
			if c.Name() == "del" {
				for _, arg := range c.Args()[1:] {
					delete(h.values, arg.(string))
				}
				c.SetVal(1)
				return nil
			}
		}
		return next(ctx, cmd)
	}
}

func (h *memoryRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if err := h.ProcessHook(func(context.Context, redis.Cmder) error { return nil })(ctx, cmd); err != nil {
				return err
			}
		}
		return nil
	}
}

func newMemoryTracker(t *testing.T) (*Tracker, *memoryRedisHook) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("unexpected Redis dial")
			return nil, nil
		},
	})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &memoryRedisHook{values: make(map[string]string)}
	rdb.AddHook(hook)
	return NewTracker(rdb, "http://node", "node", "proxy"), hook
}

func (h *memoryRedisHook) has(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.values[key]
	return ok
}

func TestStaleLeaseCannotReleaseReplacementAfterRemove(t *testing.T) {
	tr, _ := newMemoryTracker(t)
	ctx := context.Background()
	old := tr.Track(ctx, SessionInfo{SessionID: "s"})
	tr.Remove(ctx, "s")
	current := tr.Track(ctx, SessionInfo{SessionID: "s"})

	tr.Release(ctx, old)
	if got := tr.Snapshot(); len(got) != 1 || got[0].SessionID != "s" {
		t.Fatalf("stale release removed replacement: %+v", got)
	}
	tr.Release(ctx, current)
}

func TestStaleLeaseCannotReleaseReplacementAfterCleanup(t *testing.T) {
	tr, _ := newMemoryTracker(t)
	ctx := context.Background()
	old := tr.Track(ctx, SessionInfo{SessionID: "s"})
	tr.Cleanup(ctx)
	current := tr.Track(ctx, SessionInfo{SessionID: "s"})

	tr.Release(ctx, old)
	if got := tr.Snapshot(); len(got) != 1 || got[0].SessionID != "s" {
		t.Fatalf("stale release after cleanup removed replacement: %+v", got)
	}
	tr.Release(ctx, current)
}

func TestDoubleReleaseDoesNotConsumeAnotherLease(t *testing.T) {
	tr, _ := newMemoryTracker(t)
	ctx := context.Background()
	first := tr.Track(ctx, SessionInfo{SessionID: "s"})
	second := tr.Track(ctx, SessionInfo{SessionID: "s"})

	tr.Release(ctx, first)
	tr.Release(ctx, first)
	if got := tr.Snapshot(); len(got) != 1 {
		t.Fatalf("double release consumed live lease: %+v", got)
	}
	tr.Release(ctx, second)
}

func TestOverlappingLeasesKeepRecordKeyAndBytesAlive(t *testing.T) {
	tr, redisState := newMemoryTracker(t)
	ctx := context.Background()
	first := tr.Track(ctx, SessionInfo{SessionID: "s"})
	second := tr.Track(ctx, SessionInfo{SessionID: "s"})

	tr.Release(ctx, first)
	tr.AddBytes("s", 123)
	got := tr.Snapshot()
	if len(got) != 1 || got[0].BytesServed != 123 {
		t.Fatalf("live overlap record = %+v, want 123 bytes", got)
	}
	if !redisState.has(tr.redisKey("s")) {
		t.Fatal("first release deleted Redis key while second lease was live")
	}

	tr.Release(ctx, second)
	if redisState.has(tr.redisKey("s")) {
		t.Fatal("final release left Redis key behind")
	}
}

func TestEnsureEphemeralSeparatesVisibilityFromServedLiveness(t *testing.T) {
	tr, redisState := newMemoryTracker(t)
	ctx := context.Background()
	tr.EnsureEphemeral(ctx, SessionInfo{SessionID: "s", Type: "transcode"})
	got := tr.Snapshot()
	if len(got) != 1 || got[0].LastServedAt != "" || got[0].BytesServed != 0 {
		t.Fatalf("ensured record claims served liveness: %+v", got)
	}
	if !redisState.has(tr.redisKey("s")) {
		t.Fatal("ensured record was not projected to Redis")
	}

	tr.AddBytes("s", 7)
	got = tr.Snapshot()
	if len(got) != 1 || got[0].LastServedAt == "" || got[0].BytesServed != 7 {
		t.Fatalf("served record = %+v, want liveness and bytes", got)
	}

	redisState.mu.Lock()
	var projected SessionInfo
	err := json.Unmarshal([]byte(redisState.values[tr.redisKey("s")]), &projected)
	redisState.mu.Unlock()
	if err != nil {
		t.Fatalf("decode projected record: %v", err)
	}
	if projected.LastServedAt != "" {
		t.Fatalf("first projection LastServedAt = %q, want empty", projected.LastServedAt)
	}
}

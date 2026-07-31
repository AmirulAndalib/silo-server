package nodesessions

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type expiringRedisHook struct {
	mu      sync.Mutex
	expires map[string]time.Time
}

func (h *expiringRedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *expiringRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return h.process(ctx, cmd)
	}
}

func (h *expiringRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if err := h.process(ctx, cmd); err != nil {
				return err
			}
		}
		return nil
	}
}

func (h *expiringRedisHook) process(_ context.Context, cmd redis.Cmder) error {
	args := cmd.Args()
	if len(args) < 2 {
		return nil
	}
	key, _ := args[1].(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	switch strings.ToLower(cmd.Name()) {
	case "set":
		expiry := time.Time{}
		for i := 3; i+1 < len(args); i++ {
			if strings.EqualFold(strings.TrimSpace(toString(args[i])), "px") {
				ms, _ := strconv.ParseInt(toString(args[i+1]), 10, 64)
				expiry = time.Now().Add(time.Duration(ms) * time.Millisecond)
				break
			}
		}
		h.expires[key] = expiry
	case "del":
		delete(h.expires, key)
	}
	return nil
}

func toString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case time.Duration:
		return strconv.FormatInt(value.Milliseconds(), 10)
	default:
		return ""
	}
}

func (h *expiringRedisHook) has(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	expiry, ok := h.expires[key]
	if ok && !expiry.IsZero() && !time.Now().Before(expiry) {
		delete(h.expires, key)
		return false
	}
	return ok
}

func testPublisherRedis(t *testing.T) (*redis.Client, *expiringRedisHook) {
	t.Helper()
	hook := &expiringRedisHook{expires: make(map[string]time.Time)}
	rdb := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	rdb.AddHook(hook)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, hook
}

func TestPublishersWithSharedNodeNameDoNotClobberEachOther(t *testing.T) {
	ctx := context.Background()
	rdb, redisState := testPublisherRedis(t)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	first := NewPublisher(rdb, "first:"+suffix, "shared-name")
	second := NewPublisher(rdb, "second:"+suffix, "shared-name")
	t.Cleanup(func() {
		first.Cleanup(ctx)
		second.Cleanup(ctx)
	})

	info := SessionInfo{SessionID: "same-session"}
	first.Publish(ctx, []SessionInfo{info})
	second.Publish(ctx, []SessionInfo{info})
	first.Publish(ctx, nil)

	firstKey := sessionRedisKey(first.nodeHash, info.SessionID)
	secondKey := sessionRedisKey(second.nodeHash, info.SessionID)
	if redisState.has(firstKey) {
		t.Fatal("departed first key is still present")
	}
	if !redisState.has(secondKey) {
		t.Fatal("live second key is absent")
	}
}

func TestPublisherRefreshesStableRecordPastTTL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rdb, redisState := testPublisherRedis(t)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	publisher := NewPublisher(rdb, "refresh:"+suffix, "node")
	publisher.ttl = 80 * time.Millisecond
	t.Cleanup(func() { publisher.Cleanup(context.Background()) })

	publisher.Start(ctx, 20*time.Millisecond, func() []SessionInfo {
		return []SessionInfo{{SessionID: "stable"}}
	})
	time.Sleep(220 * time.Millisecond)

	key := sessionRedisKey(publisher.nodeHash, "stable")
	if !redisState.has(key) {
		t.Fatal("stable key is absent after > TTL")
	}
}

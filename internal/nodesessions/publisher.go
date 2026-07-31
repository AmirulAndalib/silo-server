package nodesessions

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Publisher mirrors the integrated API process's live sessions into the
// shared node-session keyspace.
type Publisher struct {
	rdb      *redis.Client
	nodeName string
	nodeHash string
	ttl      time.Duration

	mu  sync.Mutex
	ids map[string]struct{}
}

// NewPublisher creates a publisher whose key namespace is derived from the
// process-unique instanceID. nodeName remains display metadata only.
func NewPublisher(rdb *redis.Client, instanceID, nodeName string) *Publisher {
	return &Publisher{
		rdb:      rdb,
		nodeName: nodeName,
		nodeHash: namespaceHash(instanceID),
		ttl:      sessionTTL,
		ids:      make(map[string]struct{}),
	}
}

// Publish refreshes every currently-live record and immediately removes only
// records which disappeared since the preceding successful snapshot.
func (p *Publisher) Publish(ctx context.Context, infos []SessionInfo) {
	if p == nil || p.rdb == nil {
		return
	}

	type record struct {
		id   string
		data []byte
	}
	records := make([]record, 0, len(infos))
	current := make(map[string]struct{}, len(infos))
	for _, info := range infos {
		if info.SessionID == "" {
			continue
		}
		info.NodeName = p.nodeName
		data, err := json.Marshal(info)
		if err != nil {
			slog.DebugContext(ctx, "integrated session marshal failed",
				"component", "nodesessions", "session", info.SessionID, "error", err)
			continue
		}
		records = append(records, record{id: info.SessionID, data: data})
		current[info.SessionID] = struct{}{}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	pipe := p.rdb.Pipeline()
	for _, rec := range records {
		pipe.Set(ctx, sessionRedisKey(p.nodeHash, rec.id), rec.data, p.ttl)
	}
	for id := range p.ids {
		if _, live := current[id]; !live {
			pipe.Del(ctx, sessionRedisKey(p.nodeHash, id))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.DebugContext(ctx, "integrated session publish failed",
			"component", "nodesessions", "error", err)
		return
	}
	p.ids = current
}

// Start publishes snapshots at interval until ctx is canceled.
func (p *Publisher) Start(ctx context.Context, interval time.Duration, snapshot func() []SessionInfo) {
	if p == nil || p.rdb == nil || interval <= 0 || snapshot == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.Publish(ctx, snapshot())
			}
		}
	}()
}

// Cleanup removes all records owned by this process.
func (p *Publisher) Cleanup(ctx context.Context) {
	if p == nil || p.rdb == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.ids) == 0 {
		return
	}
	pipe := p.rdb.Pipeline()
	for id := range p.ids {
		pipe.Del(ctx, sessionRedisKey(p.nodeHash, id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.DebugContext(ctx, "integrated session cleanup failed",
			"component", "nodesessions", "error", err)
		return
	}
	p.ids = make(map[string]struct{})
}

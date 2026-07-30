package nodesessions

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestTranscodeIdleTTLAgreesAcrossCountSnapshotAndRefresh(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	tr := NewTracker(rdb, "http://node", "node", "proxy")
	idle := time.Now().Add(-90 * time.Second)
	tr.ephemeral[sessionTypeTranscode] = idle
	tr.records[sessionTypeTranscode] = SessionInfo{SessionID: sessionTypeTranscode, Type: sessionTypeTranscode}
	tr.ephemeral["direct"] = idle
	tr.records["direct"] = SessionInfo{SessionID: "direct", Type: "direct_play"}

	if got := tr.ActiveCount(); got != 1 {
		t.Fatalf("ActiveCount=%d, want only transcode", got)
	}
	snapshot := tr.Snapshot()
	if len(snapshot) != 1 || snapshot[0].SessionID != sessionTypeTranscode {
		t.Fatalf("Snapshot=%+v, want only transcode", snapshot)
	}

	tr.refreshAll(context.Background())
	tr.mu.Lock()
	_, transcodeRetained := tr.records[sessionTypeTranscode]
	_, directRetained := tr.records["direct"]
	tr.mu.Unlock()
	if !transcodeRetained || directRetained {
		t.Fatalf("refresh records: transcode=%v direct=%v, want true/false", transcodeRetained, directRetained)
	}
}

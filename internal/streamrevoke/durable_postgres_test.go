package streamrevoke

import (
	"strings"
	"testing"
)

func TestPostgresUpsertMergesCutoffAndExpiryIndependently(t *testing.T) {
	required := []string{
		"reason = CASE WHEN EXCLUDED.revoked_at > stream_revocations.revoked_at",
		"revoked_at = GREATEST(stream_revocations.revoked_at, EXCLUDED.revoked_at)",
		"GREATEST(stream_revocations.expires_at, EXCLUDED.expires_at)",
		"stream_revocations.tombstone_expires_at <= now()",
		"EXCLUDED.revoked_at > stream_revocations.unrevoked_at",
	}
	for _, clause := range required {
		if !strings.Contains(durableUpsertSQL, clause) {
			t.Fatalf("durable upsert is missing independent merge clause %q", clause)
		}
	}
	if strings.Contains(durableUpsertSQL, "EXCLUDED.expires_at >= stream_revocations.expires_at") {
		t.Fatal("durable upsert still selects cutoff/reason according to expiry")
	}
}

func TestPostgresTombstoneUpsertRejectsStaleState(t *testing.T) {
	required := []string{
		"EXCLUDED.unrevoked_at >= stream_revocations.revoked_at",
		"EXCLUDED.unrevoked_at >= stream_revocations.unrevoked_at",
	}
	for _, clause := range required {
		if !strings.Contains(durableTombstoneUpsertSQL, clause) {
			t.Fatalf("durable tombstone upsert is missing ordering clause %q", clause)
		}
	}
}

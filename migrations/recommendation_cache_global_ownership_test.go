package migrations

import (
	"strings"
	"testing"
)

// The user-integrity foreign key (20260912231023) deletes and then rejects the
// sentinel-owned global recommendation cache rows (issue #1261). This migration
// moves global ownership to user_id NULL while keeping personalized rows under
// the account foreign key. Assert its contract without a database.
func TestRecommendationCacheGlobalOwnershipMigrationContract(t *testing.T) {
	raw, err := FS.ReadFile("sql/20260922130000_recommendation_cache_global_ownership.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	migration := string(raw)

	up, down, ok := strings.Cut(migration, "-- +goose Down")
	if !ok {
		t.Fatal("migration missing a -- +goose Down section")
	}

	for _, want := range []string{
		"ALTER TABLE public.recommendation_cache DROP CONSTRAINT recommendation_cache_pkey;",
		"ALTER TABLE public.recommendation_cache ALTER COLUMN user_id DROP NOT NULL;",
		"UPDATE public.recommendation_cache SET user_id = NULL WHERE user_id = 0;",
		"NULLS NOT DISTINCT",
		"CREATE UNIQUE INDEX recommendation_cache_identity_key",
	} {
		if !strings.Contains(up, want) {
			t.Fatalf("Up section missing %q", want)
		}
	}

	// The account foreign key must survive so personalized rows keep their
	// integrity and ON DELETE CASCADE cleanup; only global ownership changes.
	for _, ddl := range []string{
		"DROP CONSTRAINT recommendation_cache_user_id_fkey",
		"ADD CONSTRAINT recommendation_cache_user_id_fkey",
	} {
		if strings.Contains(migration, ddl) {
			t.Fatalf("migration must not touch the account foreign key: %q", ddl)
		}
	}

	for _, want := range []string{
		"DROP INDEX IF EXISTS recommendation_cache_identity_key;",
		"ALTER TABLE public.recommendation_cache ALTER COLUMN user_id SET NOT NULL;",
		"ADD CONSTRAINT recommendation_cache_pkey PRIMARY KEY (user_id, profile_id, rec_type, source_item_id);",
	} {
		if !strings.Contains(down, want) {
			t.Fatalf("Down section missing %q", want)
		}
	}
}

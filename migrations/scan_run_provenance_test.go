package migrations

import (
	"strings"
	"testing"
)

func TestScanRunProvenanceIndexRetryUsesConcurrentCleanup(t *testing.T) {
	migrationBytes, err := FS.ReadFile("sql/20260807094340_add_scan_run_provenance_to_media_availability.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up := strings.SplitN(string(migrationBytes), "-- +goose Down", 2)[0]

	for _, indexName := range []string{
		"idx_media_files_first_seen_scan_run_id",
		"idx_episode_libraries_first_seen_scan_run_id",
	} {
		drop := "DROP INDEX CONCURRENTLY IF EXISTS public." + indexName + ";"
		create := "CREATE INDEX CONCURRENTLY " + indexName
		dropAt, createAt := strings.Index(up, drop), strings.Index(up, create)
		if dropAt < 0 {
			t.Fatalf("migration missing concurrent retry cleanup %q", drop)
		}
		if createAt < 0 {
			t.Fatalf("migration missing concurrent create %q", create)
		}
		if dropAt > createAt {
			t.Fatalf("retry cleanup for %s must precede its create", indexName)
		}
		if strings.Contains(up, "DROP INDEX public."+indexName) {
			t.Fatalf("migration contains blocking ordinary drop for %s", indexName)
		}
	}
}

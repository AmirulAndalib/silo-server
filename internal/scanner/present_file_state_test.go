package scanner

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSyncPresentFileStateScopesMembershipRepairToFile(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := time.Now().UnixNano()
	seriesID := fmt.Sprintf("present-file-series-%d", suffix)
	episodeID := fmt.Sprintf("present-file-episode-%d", suffix)
	unrelatedID := fmt.Sprintf("present-file-unrelated-%d", suffix)
	targetPath := fmt.Sprintf("/tmp/present-file-%d/episode.mkv", suffix)
	unrelatedPath := fmt.Sprintf("/tmp/present-file-%d/unrelated.mkv", suffix)

	var folderID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_folders (type, name, enabled)
		VALUES ('series', 'Present File State Test', true)
		RETURNING id
	`).Scan(&folderID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM media_folders WHERE id = $1`, folderID)
		_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = ANY($1)`, []string{seriesID, unrelatedID})
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO media_items (content_id, type, title, status, genres)
		VALUES
			($1, 'series', 'Target Series', 'matched', '{}'::text[]),
			($2, 'movie', 'Unrelated Movie', 'matched', '{}'::text[])
	`, seriesID, unrelatedID); err != nil {
		t.Fatalf("seed media items: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO episodes (content_id, series_id, season_number, episode_number, title)
		VALUES ($1, $2, 1, 1, 'Episode')
	`, episodeID, seriesID); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	firstSeen := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_files (
			content_id, episode_id, media_folder_id, file_path, file_size,
			season_number, episode_number, created_at
		)
		VALUES
			($1, $2, $3, $4, 1024, 1, 1, $5),
			($6, NULL, $3, $7, 1024, NULL, NULL, NOW())
	`, seriesID, episodeID, folderID, targetPath, firstSeen, unrelatedID, unrelatedPath); err != nil {
		t.Fatalf("seed media files: %v", err)
	}

	scanner := &Scanner{fileRepo: NewFileRepository(pool)}
	if err := scanner.syncPresentFileState(ctx, folderID, targetPath); err != nil {
		t.Fatalf("syncPresentFileState: %v", err)
	}

	var targetMembership bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM media_item_libraries
			WHERE content_id = $1 AND media_folder_id = $2
		)
	`, seriesID, folderID).Scan(&targetMembership); err != nil {
		t.Fatalf("read target membership: %v", err)
	}
	if !targetMembership {
		t.Fatal("target media item membership was not restored")
	}

	var unrelatedMembership bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM media_item_libraries
			WHERE content_id = $1 AND media_folder_id = $2
		)
	`, unrelatedID, folderID).Scan(&unrelatedMembership); err != nil {
		t.Fatalf("read unrelated membership: %v", err)
	}
	if unrelatedMembership {
		t.Fatal("unrelated media item membership was repaired by a single-file sync")
	}

	var episodeFirstSeen time.Time
	if err := pool.QueryRow(ctx, `
		SELECT first_seen_at
		FROM episode_libraries
		WHERE episode_id = $1 AND media_folder_id = $2
	`, episodeID, folderID).Scan(&episodeFirstSeen); err != nil {
		t.Fatalf("read episode membership: %v", err)
	}
	if !episodeFirstSeen.Equal(firstSeen) {
		t.Fatalf("episode first_seen_at = %v, want %v", episodeFirstSeen, firstSeen)
	}

	var latestEpisodeAdded *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT latest_episode_added_at FROM media_items WHERE content_id = $1
	`, seriesID).Scan(&latestEpisodeAdded); err != nil {
		t.Fatalf("read latest episode timestamp: %v", err)
	}
	if latestEpisodeAdded == nil || !latestEpisodeAdded.Equal(firstSeen) {
		t.Fatalf("latest_episode_added_at = %v, want %v", latestEpisodeAdded, firstSeen)
	}
}

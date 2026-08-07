package catalog

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlayableTargetResolverProfileStateAvailabilityAndAccess(t *testing.T) {
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
	id := func(name string) string { return fmt.Sprintf("play-target-%s-%d", name, suffix) }
	profileA, profileB := id("profile-a"), id("profile-b")
	movie, missingMovie := id("movie"), id("missing-movie")
	series, completedSeries, deniedSeries := id("series"), id("completed"), id("denied")
	season := id("season-1")
	special, episode1, episode2 := id("special"), id("episode-1"), id("episode-2")
	completed1, completed2 := id("completed-1"), id("completed-2")
	deniedEpisode := id("denied-episode")

	var userID, allowedFolderID, deniedFolderID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, username, role, enabled)
		VALUES ($1, $1, 'user', TRUE)
		RETURNING id
	`, id("user")+"@example.invalid").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name) VALUES ($1, $3, 'A'), ($2, $3, 'B')
	`, profileA, profileB, userID); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, enabled) VALUES ('mixed', $1, TRUE) RETURNING id`, id("allowed-folder")).Scan(&allowedFolderID); err != nil {
		t.Fatalf("seed allowed folder: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, enabled) VALUES ('series', $1, TRUE) RETURNING id`, id("denied-folder")).Scan(&deniedFolderID); err != nil {
		t.Fatalf("seed denied folder: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = ANY($1)`, []string{movie, missingMovie, series, completedSeries, deniedSeries})
		_, _ = pool.Exec(ctx, `DELETE FROM media_folders WHERE id = ANY($1)`, []int{allowedFolderID, deniedFolderID})
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO media_items (content_id, type, title, status, genres)
		VALUES ($1, 'movie', 'Movie', 'matched', '{}'),
		       ($2, 'movie', 'Missing Movie', 'matched', '{}'),
		       ($3, 'series', 'Series', 'matched', '{}'),
		       ($4, 'series', 'Completed Series', 'matched', '{}'),
		       ($5, 'series', 'Denied Series', 'matched', '{}')
	`, movie, missingMovie, series, completedSeries, deniedSeries); err != nil {
		t.Fatalf("seed media items: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO seasons (content_id, series_id, season_number, title)
		VALUES ($1, $2, 1, 'Season 1')
	`, season, series); err != nil {
		t.Fatalf("seed season: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO episodes (content_id, series_id, season_number, episode_number, title)
		VALUES ($1, $8, 0, 1, 'Special'),
		       ($2, $8, 1, 1, 'One'),
		       ($3, $8, 1, 2, 'Two'),
		       ($4, $9, 1, 1, 'Completed One'),
		       ($5, $9, 1, 2, 'Completed Two'),
		       ($6, $10, 1, 1, 'Denied'),
		       ($7, $8, 1, 3, 'Unavailable')
	`, special, episode1, episode2, completed1, completed2, deniedEpisode, id("unavailable-episode"), series, completedSeries, deniedSeries); err != nil {
		t.Fatalf("seed episodes: %v", err)
	}
	seedFile := func(contentID, episodeID string, folderID int, missing bool) {
		var missingSince *time.Time
		if missing {
			value := time.Now().UTC()
			missingSince = &value
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_files (content_id, episode_id, media_folder_id, file_path, missing_since)
			VALUES ($1, $2, $3, $4, $5)
		`, nilIfBlank(contentID), nilIfBlank(episodeID), folderID, id("file-"+contentID+episodeID)+".mkv", missingSince); err != nil {
			t.Fatalf("seed file for %s%s: %v", contentID, episodeID, err)
		}
	}
	seedFile(movie, "", allowedFolderID, false)
	seedFile(missingMovie, "", allowedFolderID, true)
	for _, episodeID := range []string{special, episode1, episode2, completed1, completed2} {
		seedFile("", episodeID, allowedFolderID, false)
	}
	seedFile("", deniedEpisode, deniedFolderID, false)

	base := time.Now().UTC().Add(-time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds, duration_seconds, completed, updated_at)
		VALUES ($1, $2, $3, 0, 1200, TRUE, $8),
		       ($1, $2, $4, 300, 1200, FALSE, $9),
		       ($1, $2, $5, 0, 1200, TRUE, $8),
		       ($1, $2, $6, 0, 1200, TRUE, $8),
		       ($1, $7, $3, 200, 1200, FALSE, $10)
	`, userID, profileA, episode1, episode2, completed1, completed2, profileB, base, base.Add(time.Minute), base.Add(2*time.Minute)); err != nil {
		t.Fatalf("seed progress: %v", err)
	}

	inputs := []PlayableTargetInput{
		{ContentID: movie, Type: "movie"},
		{ContentID: missingMovie, Type: "movie"},
		{ContentID: episode1, Type: "episode"},
		{ContentID: series, Type: "series"},
		{ContentID: completedSeries, Type: "series"},
		{ContentID: season, Type: "season"},
		{ContentID: deniedSeries, Type: "series"},
	}
	resolver := NewPlayableTargetResolver(pool)
	targetsA, err := resolver.Resolve(ctx, PlayableTargetQuery{
		UserID: userID, ProfileID: profileA, Items: inputs,
		Access: AccessFilter{AllowedLibraryIDs: []int{allowedFolderID}},
	})
	if err != nil {
		t.Fatalf("resolve profile A: %v", err)
	}
	wantA := map[string]string{
		movie:           movie,
		episode1:        episode1,
		series:          episode2,
		completedSeries: completed1,
		season:          episode2,
	}
	if !reflect.DeepEqual(targetsA, wantA) {
		t.Fatalf("profile A targets = %#v, want %#v", targetsA, wantA)
	}

	targetsB, err := resolver.Resolve(ctx, PlayableTargetQuery{
		UserID: userID, ProfileID: profileB,
		Items:  []PlayableTargetInput{{ContentID: series, Type: "series"}},
		Access: AccessFilter{AllowedLibraryIDs: []int{allowedFolderID}},
	})
	if err != nil || targetsB[series] != episode1 {
		t.Fatalf("profile B target = %#v, err %v; want resumable %s", targetsB, err, episode1)
	}

	untouched, err := resolver.Resolve(ctx, PlayableTargetQuery{
		UserID: userID, ProfileID: id("no-progress"),
		Items:  []PlayableTargetInput{{ContentID: series, Type: "series"}},
		Access: AccessFilter{AllowedLibraryIDs: []int{allowedFolderID}},
	})
	if err != nil || untouched[series] != episode1 {
		t.Fatalf("untouched target = %#v, err %v; want regular season before special %s", untouched, err, episode1)
	}

	for name, input := range map[string]PlayableTargetInput{
		"explicit season identity": {ContentID: season, Type: "season", SeriesID: series, SeasonNumber: intPtr(1)},
		"stale explicit hints":     {ContentID: season, Type: "season", SeriesID: "wrong-series", SeasonNumber: intPtr(99)},
	} {
		t.Run(name, func(t *testing.T) {
			targets, err := resolver.Resolve(ctx, PlayableTargetQuery{
				UserID: userID, ProfileID: profileA, Items: []PlayableTargetInput{input},
				Access: AccessFilter{AllowedLibraryIDs: []int{allowedFolderID}},
			})
			if err != nil || targets[season] != episode2 {
				t.Fatalf("season target = %#v, err %v; want %s", targets, err, episode2)
			}
		})
	}
}

func nilIfBlank(value string) any {
	if value == "" {
		return nil
	}
	return value
}

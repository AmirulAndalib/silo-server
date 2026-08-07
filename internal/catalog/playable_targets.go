package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	playableTypeMovie  = "movie"
	playableTypeSeries = "series"
	playableTypeSeason = "season"
	playableFileExists = "mf.missing_since IS NULL"
)

// PlayableTargetInput identifies a displayed card whose direct-play target
// should be resolved for the acting profile.
type PlayableTargetInput struct {
	ContentID    string
	Type         string
	SeriesID     string
	SeasonNumber *int
}

// PlayableTargetQuery scopes direct-play target resolution to the acting
// profile and, when supplied, the libraries represented by the surface.
type PlayableTargetQuery struct {
	UserID     int
	ProfileID  string
	LibraryIDs []int
	Access     AccessFilter
	Items      []PlayableTargetInput
}

// PlayableTargetResolver resolves card-level playback targets in one query.
// It deliberately returns a map instead of mutating MediaItem models because
// section/catalog models may have come from a process-global shared cache.
type PlayableTargetResolver struct {
	pool *pgxpool.Pool
}

func NewPlayableTargetResolver(pool *pgxpool.Pool) *PlayableTargetResolver {
	return &PlayableTargetResolver{pool: pool}
}

// NewPlayableTargetResolverForItems builds a resolver from the repository
// already owned by ItemsHandler without exposing the repository's pool.
func NewPlayableTargetResolverForItems(repo *ItemRepository) *PlayableTargetResolver {
	if repo == nil {
		return &PlayableTargetResolver{}
	}
	return NewPlayableTargetResolver(repo.pool)
}

// Resolve returns one accessible, currently available target for each
// playable movie/TV card. Series and seasons prefer the newest in-progress
// episode, then the first unwatched episode, then the first available episode.
// MaxContentRating and AllowedContentIDs are intentionally not reapplied to
// candidate episodes: the displayed item has already passed those content
// access filters, and episodes inherit the parent series rating. File-library
// access is still enforced here before any target is returned.
func (r *PlayableTargetResolver) Resolve(ctx context.Context, q PlayableTargetQuery) (map[string]string, error) {
	result := make(map[string]string)
	if r == nil || r.pool == nil || q.UserID <= 0 || strings.TrimSpace(q.ProfileID) == "" {
		return result, nil
	}
	if q.Access.AllowedLibraryIDs != nil && len(q.Access.AllowedLibraryIDs) == 0 {
		return result, nil
	}

	ids := make([]string, 0, len(q.Items))
	types := make([]string, 0, len(q.Items))
	seriesIDs := make([]string, 0, len(q.Items))
	seasonNumbers := make([]int, 0, len(q.Items))
	seen := make(map[string]struct{}, len(q.Items))
	for _, item := range q.Items {
		contentID := strings.TrimSpace(item.ContentID)
		mediaType := strings.ToLower(strings.TrimSpace(item.Type))
		if contentID == "" || (mediaType != playableTypeMovie && mediaType != recentTVTypeEpisode && mediaType != playableTypeSeries && mediaType != playableTypeSeason) {
			continue
		}
		key := mediaType + "\x00" + contentID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ids = append(ids, contentID)
		types = append(types, mediaType)
		seriesIDs = append(seriesIDs, strings.TrimSpace(item.SeriesID))
		seasonNumber := -1
		if item.SeasonNumber != nil {
			seasonNumber = *item.SeasonNumber
		}
		seasonNumbers = append(seasonNumbers, seasonNumber)
	}
	if len(ids) == 0 {
		return result, nil
	}

	args := []any{ids, types, seriesIDs, seasonNumbers, q.UserID, q.ProfileID}
	argIdx := 7
	fileConditions := []string{
		playableFileExists,
		"EXISTS (SELECT 1 FROM media_folders pf WHERE pf.id = mf.media_folder_id AND pf.enabled = TRUE)",
	}
	effectiveLibraries := uniquePositiveInts(q.LibraryIDs)
	if len(effectiveLibraries) > 0 {
		if q.Access.AllowedLibraryIDs != nil {
			effectiveLibraries = intersectOptionalInts(effectiveLibraries, q.Access.AllowedLibraryIDs)
		}
		effectiveLibraries = subtractInts(effectiveLibraries, q.Access.DisabledLibraryIDs)
		if len(effectiveLibraries) == 0 {
			return result, nil
		}
		fileConditions = append(fileConditions, fmt.Sprintf("mf.media_folder_id = ANY($%d)", argIdx))
		args = append(args, effectiveLibraries)
	} else {
		if q.Access.AllowedLibraryIDs != nil {
			fileConditions = append(fileConditions, fmt.Sprintf("mf.media_folder_id = ANY($%d)", argIdx))
			args = append(args, q.Access.AllowedLibraryIDs)
			argIdx++
		}
		if len(q.Access.DisabledLibraryIDs) > 0 {
			fileConditions = append(fileConditions, fmt.Sprintf("NOT (mf.media_folder_id = ANY($%d))", argIdx))
			args = append(args, q.Access.DisabledLibraryIDs)
		}
	}

	query := fmt.Sprintf(`
		WITH requested AS (
			SELECT content_id, media_type, series_id, season_number, ord
			FROM unnest($1::text[], $2::text[], $3::text[], $4::integer[]) WITH ORDINALITY
			  AS requested(content_id, media_type, series_id, season_number, ord)
		),
		leaf_targets AS (
			SELECT requested.ord, requested.content_id AS play_content_id
			FROM requested
			WHERE requested.media_type IN ('movie', 'episode')
			  AND EXISTS (
				SELECT 1
				FROM media_files mf
				WHERE CASE
					WHEN requested.media_type = 'movie' THEN mf.content_id = requested.content_id
					ELSE mf.episode_id = requested.content_id
				END
				  AND %s
			  )
		),
		candidate_episodes AS (
			SELECT requested.ord, episode.content_id, episode.season_number, episode.episode_number
			FROM requested
			JOIN episodes episode
			  ON requested.media_type = 'series'
			 AND episode.series_id = requested.content_id
			UNION ALL
			SELECT requested.ord, episode.content_id, episode.season_number, episode.episode_number
			FROM requested
			JOIN episodes episode
			  ON requested.media_type = 'season'
			 AND requested.series_id <> ''
			 AND requested.season_number >= 0
			 AND episode.series_id = requested.series_id
			 AND episode.season_number = requested.season_number
			UNION ALL
			SELECT requested.ord, episode.content_id, episode.season_number, episode.episode_number
			FROM requested
			JOIN seasons season
			  ON requested.media_type = 'season'
			 AND season.content_id = requested.content_id
			 AND NOT (
				requested.series_id <> ''
				AND requested.season_number >= 0
				AND requested.series_id = season.series_id
				AND requested.season_number = season.season_number
			 )
			JOIN episodes episode
			  ON episode.series_id = season.series_id
			 AND episode.season_number = season.season_number
		),
		episode_candidates AS (
			SELECT requested.ord,
			       candidate.content_id AS play_content_id,
			       ROW_NUMBER() OVER (
				   PARTITION BY requested.ord
				   ORDER BY
				     CASE
				       WHEN progress.position_seconds > 0 AND NOT progress.completed THEN 0
				       WHEN progress.media_item_id IS NULL OR NOT progress.completed THEN 1
				       ELSE 2
				     END ASC,
				     CASE WHEN progress.position_seconds > 0 AND NOT progress.completed THEN progress.updated_at END DESC NULLS LAST,
				     CASE WHEN candidate.season_number = 0 THEN 1 ELSE 0 END ASC,
				     candidate.season_number ASC,
				     candidate.episode_number ASC,
				     candidate.content_id ASC
			       ) AS candidate_rank
			FROM requested
			JOIN candidate_episodes candidate ON candidate.ord = requested.ord
			LEFT JOIN user_watch_progress progress
			  ON progress.user_id = $5
			 AND progress.profile_id = $6
			 AND progress.media_item_id = candidate.content_id
			WHERE EXISTS (
				SELECT 1
				FROM media_files mf
				WHERE mf.episode_id = candidate.content_id
				  AND %s
			  )
		),
		resolved AS (
			SELECT ord, play_content_id FROM leaf_targets
			UNION ALL
			SELECT ord, play_content_id FROM episode_candidates WHERE candidate_rank = 1
		)
		SELECT requested.content_id, resolved.play_content_id
		FROM requested
		JOIN resolved USING (ord)
		ORDER BY requested.ord
	`, strings.Join(fileConditions, " AND "), strings.Join(fileConditions, " AND "))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("resolving playable poster targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var contentID, playContentID string
		if err := rows.Scan(&contentID, &playContentID); err != nil {
			return nil, fmt.Errorf("scanning playable poster target: %w", err)
		}
		result[contentID] = playContentID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating playable poster targets: %w", err)
	}
	return result, nil
}

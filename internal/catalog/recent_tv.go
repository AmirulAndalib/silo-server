package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	recentTVTypeEpisode = "episode"
	recentTVTypeSeries  = "series"
)

// RecentTVTarget is one card-producing TV availability event. Separate scan
// runs remain separate even when two multi-episode runs target the same show.
type RecentTVTarget struct {
	ContentID     string
	Type          string
	AddedAt       time.Time
	PlayContentID string
}

// RecentTVQuery describes one page of Plex-style recently-added TV events.
type RecentTVQuery struct {
	LibraryIDs []int
	Access     AccessFilter
	NamePrefix string
	SnapshotAt *time.Time
	Limit      int
	Offset     int
}

// RecentTVRepository resolves scan-batched episode availability into episode
// or series card targets.
type RecentTVRepository struct {
	pool *pgxpool.Pool
}

func NewRecentTVRepository(pool *pgxpool.Pool) *RecentTVRepository {
	return &RecentTVRepository{pool: pool}
}

// ResolveRecentTVLibraryIDs decides whether a recently-added section is
// exclusively TV-targeted and returns the effective visible TV libraries.
// Explicit series scopes may include mixed libraries; implicit scopes require
// every requested library to be a dedicated series library.
func ResolveRecentTVLibraryIDs(
	ctx context.Context,
	pool *pgxpool.Pool,
	requested []int,
	explicitSeries bool,
	access AccessFilter,
) ([]int, bool, error) {
	if pool == nil {
		return nil, false, nil
	}
	requested = uniquePositiveInts(requested)
	if len(requested) == 0 && !explicitSeries {
		return nil, false, nil
	}

	conditions := []string{"enabled = true"}
	args := []any{}
	if len(requested) > 0 {
		conditions = append(conditions, "id = ANY($1)")
		args = append(args, requested)
	}
	rows, err := pool.Query(ctx, `
		SELECT id, type
		FROM media_folders
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY id ASC
	`, args...)
	if err != nil {
		return nil, false, fmt.Errorf("listing TV recent libraries: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]string)
	for rows.Next() {
		var id int
		var libraryType string
		if err := rows.Scan(&id, &libraryType); err != nil {
			return nil, false, fmt.Errorf("scanning TV recent library: %w", err)
		}
		byID[id] = strings.ToLower(strings.TrimSpace(libraryType))
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterating TV recent libraries: %w", err)
	}

	if !explicitSeries {
		if len(byID) != len(requested) {
			return nil, false, nil
		}
		for _, id := range requested {
			if byID[id] != recentTVTypeSeries {
				return nil, false, nil
			}
		}
	}

	ids := make([]int, 0, len(byID))
	for id, libraryType := range byID {
		if libraryType == recentTVTypeSeries || (explicitSeries && libraryType == "mixed") {
			ids = append(ids, id)
		}
	}
	ids = intersectOptionalInts(ids, access.AllowedLibraryIDs)
	ids = subtractInts(ids, access.DisabledLibraryIDs)
	if len(ids) == 0 {
		return []int{}, true, nil
	}
	return sortedUniqueInts(ids), true, nil
}

// List returns one page after event grouping and per-event target de-duplication.
func (r *RecentTVRepository) List(ctx context.Context, q RecentTVQuery) ([]RecentTVTarget, int, bool, error) {
	if r == nil || r.pool == nil || len(q.LibraryIDs) == 0 {
		return []RecentTVTarget{}, 0, false, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	args := []any{sortedUniqueInts(q.LibraryIDs)}
	argIdx := 2
	availabilityConditions := []string{
		"el.media_folder_id = ANY($1)",
		`EXISTS (
			SELECT 1 FROM media_files mf_event
			WHERE mf_event.episode_id = el.episode_id
			  AND mf_event.media_folder_id = el.media_folder_id
			  AND mf_event.missing_since IS NULL
		)`,
	}
	seriesConditions := []string{"mil.media_folder_id = ANY($1)", "mi.type = 'series'"}
	seriesWithoutEpisodeSnapshotCondition := ""

	if q.SnapshotAt != nil {
		availabilityConditions = append(availabilityConditions, fmt.Sprintf("el.first_seen_at <= $%d", argIdx))
		seriesConditions = append(seriesConditions, fmt.Sprintf("mil.first_seen_at <= $%d", argIdx))
		seriesWithoutEpisodeSnapshotCondition = fmt.Sprintf("AND el_none.first_seen_at <= $%d", argIdx)
		args = append(args, *q.SnapshotAt)
		argIdx++
	}

	access := q.Access
	access.NamePrefix = ""
	appendLibraryAccessConditions("si.content_id", access, &availabilityConditions, &args, &argIdx)
	applyAccessFilter("si", AccessFilter{
		MaxContentRating:   access.MaxContentRating,
		ExcludedMediaTypes: access.ExcludedMediaTypes,
	}, &availabilityConditions, &args, &argIdx)
	appendAllowedContentCondition("si.content_id", access.AllowedContentIDs, &availabilityConditions, &args, &argIdx)

	appendLibraryAccessConditions("mi.content_id", access, &seriesConditions, &args, &argIdx)
	applyAccessFilter("mi", AccessFilter{
		MaxContentRating:   access.MaxContentRating,
		ExcludedMediaTypes: access.ExcludedMediaTypes,
	}, &seriesConditions, &args, &argIdx)
	appendAllowedContentCondition("mi.content_id", access.AllowedContentIDs, &seriesConditions, &args, &argIdx)

	if prefix := strings.TrimSpace(q.NamePrefix); prefix != "" {
		pattern := likePrefixPattern(strings.ToLower(prefix))
		availabilityConditions = append(availabilityConditions, fmt.Sprintf(
			"(LOWER(COALESCE(NULLIF(BTRIM(si.sort_title), ''), si.title)) LIKE $%d ESCAPE '\\' OR LOWER(e.title) LIKE $%d ESCAPE '\\')",
			argIdx, argIdx,
		))
		seriesConditions = append(seriesConditions, fmt.Sprintf(
			"LOWER(COALESCE(NULLIF(BTRIM(mi.sort_title), ''), mi.title)) LIKE $%d ESCAPE '\\'",
			argIdx,
		))
		args = append(args, pattern)
		argIdx++
	}

	limitIdx := argIdx
	offsetIdx := argIdx + 1
	args = append(args, limit, q.Offset)

	sql := fmt.Sprintf(`
		WITH episode_events AS (
			SELECT e.series_id,
			       el.first_seen_scan_run_id AS scan_run_id,
			       MAX(el.first_seen_at) AS added_at,
			       COUNT(DISTINCT el.episode_id) AS episode_count,
			       (array_agg(el.episode_id ORDER BY el.first_seen_at DESC, e.season_number DESC, e.episode_number DESC, el.episode_id ASC))[1] AS episode_id,
			       (array_agg(e.season_number ORDER BY el.first_seen_at DESC, e.season_number DESC, e.episode_number DESC, el.episode_id ASC))[1] AS anchor_season_number
			FROM episode_libraries el
			JOIN episodes e ON e.content_id = el.episode_id
			JOIN media_items si ON si.content_id = e.series_id
			WHERE %s
			GROUP BY e.series_id, el.first_seen_scan_run_id
		),
		series_without_episode_events AS (
			SELECT mi.content_id AS series_id,
			       NULL::text AS scan_run_id,
			       MAX(mil.first_seen_at) AS added_at,
			       0::bigint AS episode_count,
			       NULL::text AS episode_id,
			       NULL::integer AS anchor_season_number
			FROM media_item_libraries mil
			JOIN media_items mi ON mi.content_id = mil.content_id
			WHERE %s
			  AND NOT EXISTS (
				SELECT 1
				FROM episodes e_none
				JOIN episode_libraries el_none ON el_none.episode_id = e_none.content_id
				WHERE e_none.series_id = mi.content_id
				  AND el_none.media_folder_id = ANY($1)
				  %s
				  AND EXISTS (
					  SELECT 1 FROM media_files mf_none
					  WHERE mf_none.episode_id = el_none.episode_id
					    AND mf_none.media_folder_id = el_none.media_folder_id
					    AND mf_none.missing_since IS NULL
				  )
			  )
			GROUP BY mi.content_id
		),
		all_events AS (
			SELECT CASE WHEN scan_run_id IS NOT NULL AND episode_count = 1 THEN episode_id ELSE series_id END AS target_id,
			       CASE WHEN scan_run_id IS NOT NULL AND episode_count = 1 THEN 'episode'::text ELSE 'series'::text END AS target_type,
			       added_at,
			       COALESCE(scan_run_id, '') AS event_id,
			       series_id,
			       anchor_season_number,
			       CASE WHEN scan_run_id IS NOT NULL AND episode_count = 1 THEN episode_id END AS single_episode_id
			FROM episode_events
			UNION ALL
			SELECT series_id, 'series'::text, added_at, ''::text, series_id, NULL::integer, NULL::text
			FROM series_without_episode_events
		),
		deduplicated AS (
			SELECT target_id, target_type, added_at, event_id, series_id, anchor_season_number, single_episode_id,
			       ROW_NUMBER() OVER (PARTITION BY target_id, target_type, event_id ORDER BY added_at DESC) AS target_rank
			FROM all_events
		),
		filtered AS (
			SELECT target_id, target_type, added_at, event_id, series_id, anchor_season_number, single_episode_id
			FROM deduplicated
			WHERE target_rank = 1
		),
		totals AS (
			SELECT COUNT(*)::int AS total_count FROM filtered
		),
		page AS (
			SELECT target_id, target_type, added_at, event_id, series_id, anchor_season_number, single_episode_id
			FROM filtered
			ORDER BY added_at DESC, target_type ASC, target_id ASC, event_id ASC
			LIMIT $%d OFFSET $%d
		)
		SELECT page.target_id, page.target_type, page.added_at,
		       COALESCE(page.single_episode_id, play_target.content_id) AS play_content_id,
		       totals.total_count
		FROM totals
		LEFT JOIN page ON true
		LEFT JOIN LATERAL (
			SELECT e_play.content_id
			FROM episodes e_play
			WHERE page.target_type = 'series'
			  AND e_play.series_id = page.series_id
			  AND e_play.season_number = page.anchor_season_number
			  AND EXISTS (
				SELECT 1
				FROM episode_libraries el_play
				WHERE el_play.episode_id = e_play.content_id
				  AND el_play.media_folder_id = ANY($1)
				  AND EXISTS (
					SELECT 1 FROM media_files mf_play
					WHERE mf_play.episode_id = el_play.episode_id
					  AND mf_play.media_folder_id = el_play.media_folder_id
					  AND mf_play.missing_since IS NULL
				  )
			  )
			ORDER BY e_play.episode_number ASC, e_play.content_id ASC
			LIMIT 1
		) play_target ON true
		ORDER BY page.added_at DESC, page.target_type ASC, page.target_id ASC, page.event_id ASC
	`, strings.Join(availabilityConditions, " AND "), strings.Join(seriesConditions, " AND "), seriesWithoutEpisodeSnapshotCondition, limitIdx, offsetIdx)

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, false, fmt.Errorf("listing recently-added TV events: %w", err)
	}
	defer rows.Close()

	targets := make([]RecentTVTarget, 0, limit)
	total := 0
	for rows.Next() {
		var contentID, targetType, playContentID *string
		var addedAt *time.Time
		if err := rows.Scan(&contentID, &targetType, &addedAt, &playContentID, &total); err != nil {
			return nil, 0, false, fmt.Errorf("scanning recently-added TV event: %w", err)
		}
		if contentID != nil && targetType != nil && addedAt != nil {
			target := RecentTVTarget{ContentID: *contentID, Type: *targetType, AddedAt: *addedAt}
			if playContentID != nil {
				target.PlayContentID = *playContentID
			}
			targets = append(targets, target)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("iterating recently-added TV events: %w", err)
	}
	return targets, total, q.Offset+len(targets) < total, nil
}

func appendAllowedContentCondition(column string, allowed []string, conditions *[]string, args *[]any, argIdx *int) {
	if allowed == nil {
		return
	}
	if len(allowed) == 0 {
		*conditions = append(*conditions, "1 = 0")
		return
	}
	*conditions = append(*conditions, fmt.Sprintf("%s = ANY($%d)", column, *argIdx))
	*args = append(*args, allowed)
	*argIdx++
}

func uniquePositiveInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func sortedUniqueInts(values []int) []int {
	values = uniquePositiveInts(values)
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values
}

func intersectOptionalInts(values, allowed []int) []int {
	if allowed == nil {
		return values
	}
	allowedSet := make(map[int]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := allowedSet[value]; ok {
			result = append(result, value)
		}
	}
	return result
}

func subtractInts(values, denied []int) []int {
	if len(denied) == 0 {
		return values
	}
	deniedSet := make(map[int]struct{}, len(denied))
	for _, value := range denied {
		deniedSet[value] = struct{}{}
	}
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, denied := deniedSet[value]; !denied {
			result = append(result, value)
		}
	}
	return result
}

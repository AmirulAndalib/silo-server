package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newUnmatchedTestPool connects to the test database used for DB-backed handler
// tests. It skips when SILO_TEST_DATABASE_URL is unset or the manga_chapters
// migration has not been applied, mirroring the literaryworks repository tests.
func newUnmatchedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	var tableName *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.manga_chapters')::text`).Scan(&tableName); err != nil {
		t.Fatalf("check manga_chapters table: %v", err)
	}
	if tableName == nil || *tableName == "" {
		t.Skip("test database has not applied manga_chapters migration")
	}
	return pool
}

// TestHandleListUnmatchedItems_ExcludesMangaChapters verifies that manga chapter
// rows (type='ebook' linked into a manga series via manga_chapters) are hidden
// from the admin Unmatched queue, while genuinely unmatched standalone items are
// still returned. This is the regression guard for issue #204: matched manga
// items were leaking into the queue because the chapter rows kept a non-matched
// status and the queries lacked the MangaChapterExclusionWhere guard.
func TestHandleListUnmatchedItems_ExcludesMangaChapters(t *testing.T) {
	ctx := context.Background()
	pool := newUnmatchedTestPool(t)

	suffix := time.Now().UnixNano()
	seriesID := fmt.Sprintf("manga-series-%d", suffix)
	chapterID := fmt.Sprintf("manga-chapter-%d", suffix)
	ebookID := fmt.Sprintf("standalone-ebook-%d", suffix)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM manga_chapters WHERE chapter_content_id = $1`, chapterID)
		_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = ANY($1)`, []string{seriesID, chapterID, ebookID})
	})

	seed := func(contentID, mediaType, title, status string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_items (content_id, type, title, status, genres)
			VALUES ($1, $2, $3, $4, '{}'::text[])
		`, contentID, mediaType, title, status); err != nil {
			t.Fatalf("seed media item %s: %v", contentID, err)
		}
	}

	// Titles embed the unique suffix so the search filter below scopes the
	// query to exactly these seeded rows in the shared test database.
	tag := fmt.Sprintf("issue204-%d", suffix)
	// The matched manga series is correctly out of the queue already.
	seed(seriesID, "manga", "Attack on Titan "+tag, "matched")
	// The chapter row is a type='ebook' sub-unit that stays in a non-matched
	// status; before the fix it leaked into the queue.
	seed(chapterID, "ebook", "Attack on Titan Ch. 1 "+tag, "pending")
	// A genuinely unmatched standalone ebook must still appear in the queue.
	seed(ebookID, "ebook", "Some Loose Ebook "+tag, "unmatched")

	if _, err := pool.Exec(ctx, `
		INSERT INTO manga_chapters (chapter_content_id, series_content_id, chapter_index)
		VALUES ($1, $2, 1)
	`, chapterID, seriesID); err != nil {
		t.Fatalf("link manga chapter: %v", err)
	}

	h := &LibraryHandler{pool: pool}
	r := chi.NewRouter()
	r.Get("/libraries/unmatched-items", h.HandleListUnmatchedItems)

	// Scope the query to our seeded rows via the search filter so the assertion
	// is robust against other data in the shared test database.
	req := httptest.NewRequest(http.MethodGet, "/libraries/unmatched-items?q="+tag, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp unmatchedItemsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	for _, item := range resp.Items {
		if item.ContentID == chapterID {
			t.Errorf("manga chapter %s leaked into the Unmatched queue", chapterID)
		}
	}

	foundStandalone := false
	for _, item := range resp.Items {
		if item.ContentID == ebookID {
			foundStandalone = true
		}
	}
	if !foundStandalone {
		t.Errorf("standalone unmatched ebook %s missing from the queue", ebookID)
	}

	// Total must also exclude the chapter: only the standalone ebook matches the
	// scoped search, so the count is exactly 1.
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1 (chapter must be excluded from the count too)", resp.Total)
	}
}

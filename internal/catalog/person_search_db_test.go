package catalog

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPersonSearchScopeAndRankingPostgres(t *testing.T) {
	pool := collectionSortTestPool(t)
	ctx := t.Context()
	prefix := "person-search-" + uuid.NewString()
	name := "Nathan " + prefix
	names := []string{name, "Alice, " + name, "Bob, " + name, name + " Jr", "Zoe, " + name}
	baseID := time.Now().UnixNano()
	ids := make([]int64, len(names))
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanup, `DELETE FROM item_people WHERE person_id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM people WHERE id = ANY($1)`, ids)
		_, _ = pool.Exec(cleanup, `DELETE FROM media_items WHERE content_id LIKE $1`, prefix+"%")
	})
	for i, personName := range names {
		ids[i] = baseID + int64(i)
		exec(`INSERT INTO people(id, name) VALUES ($1, $2)`, ids[i], personName)
	}
	for i, credit := range []struct {
		person   int
		typeName string
		kind     int
	}{
		{0, "movie", 1}, {0, "series", 1}, {0, "audiobook", 8},
		{1, "audiobook", 7}, {2, "movie", 2}, {3, "series", 1},
	} {
		contentID := fmt.Sprintf("%s-%d", prefix, i)
		exec(`INSERT INTO media_items(content_id, type, title) VALUES ($1, $2, 'Synthetic title')`, contentID, credit.typeName)
		exec(`INSERT INTO item_people(id, content_id, person_id, kind) VALUES ($1, $2, $3, $4)`, baseID+int64(i), contentID, ids[credit.person], credit.kind)
	}
	repo := NewPersonRepository(pool)
	for _, tc := range []struct {
		name, scope string
		limit       int
		want        []int64
	}{
		{"all exact before limit", "", 1, ids[:1]},
		{"all retains other matches", "", 20, ids},
		{"media excludes audiobook-only credits", "video", 20, []int64{ids[0], ids[2], ids[3]}},
		{"media exact before limit", "video", 1, ids[:1]},
		{"audiobooks include narrators and authors", "audiobook", 20, ids[:2]},
		{"movies include directors", "movie", 20, []int64{ids[0], ids[2]}},
		{"series", "series", 20, []int64{ids[0], ids[3]}},
		{"empty scope results", "ebook", 20, []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			people, err := repo.SearchScoped(t.Context(), "  "+strings.ToLower(name)+"  ", tc.limit, tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]int64, len(people))
			for i, person := range people {
				got[i] = person.ID
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	legacy, err := repo.Search(ctx, name, 1)
	if err != nil || len(legacy) != 1 || legacy[0].ID != ids[1] {
		t.Fatalf("legacy alphabetical search changed: %+v, %v", legacy, err)
	}
}

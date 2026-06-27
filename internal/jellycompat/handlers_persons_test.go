package jellycompat

import (
	"context"
	"net/url"
	"slices"
	"testing"
)

// recordingPersonsContentService is a ContentService stub that records all
// SearchItems calls made by PersonsHandler. It is only used in
// handlers_persons_test.go and lives here to keep it close to the tests that
// rely on it.
type recordingPersonsContentService struct {
	searchOptions []SearchItemsOptions
	searchResult  *upstreamBrowseResponse
}

func (s *recordingPersonsContentService) SearchItems(_ context.Context, _ *Session, opts SearchItemsOptions) (*upstreamBrowseResponse, error) {
	s.searchOptions = append(s.searchOptions, opts)
	if s.searchResult != nil {
		return s.searchResult, nil
	}
	return &upstreamBrowseResponse{Items: []upstreamListItem{}}, nil
}

func (s *recordingPersonsContentService) ListUserLibraries(context.Context, *Session) ([]upstreamUserLibrary, error) {
	panic("unused")
}
func (s *recordingPersonsContentService) BrowseItems(context.Context, *Session, url.Values) (*upstreamBrowseResponse, error) {
	panic("unused")
}
func (s *recordingPersonsContentService) GetItemDetail(context.Context, *Session, string, *int) (*upstreamItemDetail, error) {
	panic("unused")
}
func (s *recordingPersonsContentService) ListSeasons(context.Context, *Session, string, *int) ([]upstreamSeason, error) {
	panic("unused")
}
func (s *recordingPersonsContentService) GetSeason(context.Context, *Session, string, int, *int) (*upstreamSeason, error) {
	panic("unused")
}
func (s *recordingPersonsContentService) ListEpisodes(context.Context, *Session, string, int, *int) ([]upstreamEpisode, error) {
	panic("unused")
}
func (s *recordingPersonsContentService) ListEpisodesBySeasonID(context.Context, *Session, string, *int) ([]upstreamEpisode, error) {
	panic("unused")
}
func (s *recordingPersonsContentService) ListItemFilters(context.Context, *Session, url.Values) (*upstreamItemFiltersResponse, error) {
	panic("unused")
}

// TestShouldSuppressSearchPeople_PassesSkipTotalTrue verifies that
// shouldSuppressSearchPeople always sets SkipTotal:true in the SearchItems call
// it issues to probe whether the query matches media titles. Fetching a total
// count on the hot people-search path is unnecessary overhead.
func TestShouldSuppressSearchPeople_PassesSkipTotalTrue(t *testing.T) {
	// "Avatar 2" fails looksLikePersonName (has a digit) so the probe fires.
	contentSvc := &recordingPersonsContentService{
		searchResult: &upstreamBrowseResponse{
			Items: []upstreamListItem{
				{ContentID: "movie-1", Title: "Avatar 2"},
			},
		},
	}
	h := &PersonsHandler{content: contentSvc}

	_ = h.shouldSuppressSearchPeople(context.Background(), &Session{StreamAppUserID: 1, ProfileID: "p1"}, "Avatar 2")

	if len(contentSvc.searchOptions) != 1 {
		t.Fatalf("SearchItems call count = %d, want 1", len(contentSvc.searchOptions))
	}
	opts := contentSvc.searchOptions[0]
	if !opts.SkipTotal {
		t.Errorf("SkipTotal = false, want true (total is unnecessary on the probe path)")
	}
	if want := []string{"movie", "series"}; !slices.Equal(opts.ItemTypes, want) {
		t.Errorf("ItemTypes = %#v, want %#v", opts.ItemTypes, want)
	}
	if opts.Limit != 5 {
		t.Errorf("Limit = %d, want 5", opts.Limit)
	}
}

// TestShouldSuppressSearchPeople_SkipsProbeForPersonNameShape verifies that
// when the query looks like a person name, SearchItems is never called —
// the handler short-circuits before reaching the media probe.
func TestShouldSuppressSearchPeople_SkipsProbeForPersonNameShape(t *testing.T) {
	contentSvc := &recordingPersonsContentService{}
	h := &PersonsHandler{content: contentSvc}

	// "Tom Hanks" looks like a person name → no probe.
	suppressed := h.shouldSuppressSearchPeople(context.Background(), &Session{StreamAppUserID: 1, ProfileID: "p1"}, "Tom Hanks")

	if len(contentSvc.searchOptions) != 0 {
		t.Errorf("SearchItems called %d times, want 0 for person-name queries", len(contentSvc.searchOptions))
	}
	if suppressed {
		t.Error("suppressed = true for a person-name query, want false")
	}
}

// TestShouldSuppressSearchPeople_ReturnsFalseWhenNoMediaMatch verifies that
// the handler does NOT suppress when the media search returns no results.
func TestShouldSuppressSearchPeople_ReturnsFalseWhenNoMediaMatch(t *testing.T) {
	// Empty result → should not suppress.
	contentSvc := &recordingPersonsContentService{
		searchResult: &upstreamBrowseResponse{Items: []upstreamListItem{}},
	}
	h := &PersonsHandler{content: contentSvc}

	suppressed := h.shouldSuppressSearchPeople(context.Background(), &Session{}, "Avatar 2")

	if suppressed {
		t.Error("suppressed = true when no media matched, want false")
	}
}

// TestShouldSuppressSearchPeople_ReturnsTrueOnExactTitleMatch verifies the
// core suppression logic: an exact title match causes the person search to
// be suppressed so clients receive an empty person list.
func TestShouldSuppressSearchPeople_ReturnsTrueOnExactTitleMatch(t *testing.T) {
	contentSvc := &recordingPersonsContentService{
		searchResult: &upstreamBrowseResponse{
			Items: []upstreamListItem{
				{ContentID: "movie-1", Title: "Avatar 2"},
			},
		},
	}
	h := &PersonsHandler{content: contentSvc}

	suppressed := h.shouldSuppressSearchPeople(context.Background(), &Session{}, "Avatar 2")

	if !suppressed {
		t.Error("suppressed = false for exact title match, want true")
	}
}

func TestLooksLikePersonName_AcceptsRealNames(t *testing.T) {
	cases := []string{"Christopher Nolan", "Tom Hanks", "Émilie Dequenne", "O'Brien"}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			if !looksLikePersonName(name) {
				t.Errorf("expected %q to look like a person name", name)
			}
		})
	}
}

func TestLooksLikePersonName_RejectsMediaTitles(t *testing.T) {
	cases := []string{"Avatar 2", "Star Wars: A New Hope", "1917"}
	for _, q := range cases {
		q := q
		t.Run(q, func(t *testing.T) {
			if looksLikePersonName(q) {
				t.Errorf("expected %q to NOT look like a person name", q)
			}
		})
	}
}

func TestLooksLikePersonName_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		term string
		want bool
	}{
		// Punctuation in names — accept
		{"hyphenated", "Jean-Luc Picard", true},
		{"apostrophe", "O'Brien", true},
		{"period after initial", "Robert J. Oppenheimer", true},

		// Unicode — accept
		{"accents", "Émilie Dequenne", true},
		{"diacritic", "Pelé", true},
		{"cjk", "李娜", true},

		// Length guard
		{"single char", "A", false},
		{"empty", "", false},
		{"whitespace only", "   ", false},

		// Trim leading/trailing whitespace
		{"leading whitespace", "   Tom Hanks", true},
		{"trailing whitespace", "Tom Hanks   ", true},

		// Forms that should fall through to media probe
		{"ampersand", "Day & Night", false},
		{"slash", "Either/Or", false},
		{"digit anywhere", "Apollo 13", false},
		{"colon", "Star Wars: A New Hope", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := looksLikePersonName(c.term); got != c.want {
				t.Errorf("looksLikePersonName(%q) = %v; want %v", c.term, got, c.want)
			}
		})
	}
}

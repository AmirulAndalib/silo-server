package jellycompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/subtitles"
)

type recordingSubtitleProvider struct {
	name      string
	results   []subtitles.SubtitleResult
	data      []byte
	format    subtitles.SubtitleFormat
	lastQuery subtitles.SearchRequest
}

func (p *recordingSubtitleProvider) Name() string {
	return p.name
}

func (p *recordingSubtitleProvider) Search(_ context.Context, req subtitles.SearchRequest) ([]subtitles.SubtitleResult, error) {
	p.lastQuery = req
	return p.results, nil
}

func (p *recordingSubtitleProvider) Download(_ context.Context, _ string) ([]byte, subtitles.SubtitleFormat, error) {
	return p.data, p.format, nil
}

type fakeSubtitleS3Client struct {
	objects map[string][]byte
}

func (s *fakeSubtitleS3Client) PutObject(_ context.Context, _, key string, data []byte) error {
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

func (s *fakeSubtitleS3Client) GetObject(_ context.Context, _, key string) ([]byte, error) {
	return append([]byte(nil), s.objects[key]...), nil
}

func (s *fakeSubtitleS3Client) DeleteObject(_ context.Context, _, key string) error {
	delete(s.objects, key)
	return nil
}

func TestHandleSearchRemoteSubtitlesReturnsJellyfinResults(t *testing.T) {
	codec := NewResourceIDCodec()
	contentID := "episode-1"
	routeID := codec.EncodeStringID(EncodedIDItem, contentID)
	provider := &recordingSubtitleProvider{
		name: "subdl",
		results: []subtitles.SubtitleResult{
			{
				ID:              "/download/subtitle.srt",
				Provider:        "subdl",
				Language:        "eng",
				ReleaseName:     "Show.S01E02.1080p.WEB",
				Format:          subtitles.FormatSRT,
				Downloads:       42,
				HearingImpaired: true,
			},
		},
	}
	repo := fakeSubtitleRepository{downloaded: map[int][]subtitles.DownloadedSubtitle{}, byKey: map[string]*subtitles.DownloadedSubtitle{}}
	manager := subtitles.NewManager(repo, &fakeSubtitleS3Client{objects: map[string][]byte{}}, "bucket")
	manager.RegisterProvider(provider)
	handler := NewSubtitleHandler(&stubContentService{detail: &upstreamItemDetail{
		ContentID:     contentID,
		Title:         "Episode title",
		SeriesTitle:   "Show",
		Year:          2026,
		ImdbID:        "tt1234567",
		SeasonNumber:  intPtr(1),
		EpisodeNumber: intPtr(2),
		Versions: []catalog.FileVersion{
			{
				FileID:     42,
				FileName:   "Show.S01E02.1080p.WEB.mkv",
				FilePath:   "/media/Show.S01E02.1080p.WEB.mkv",
				Resolution: "1080p",
				CodecVideo: "h264",
				CodecAudio: "aac",
			},
		},
	}}, codec, manager)

	req := newRemoteSubtitleRequest(http.MethodGet, "/Items/"+routeID+"/RemoteSearch/Subtitles/eng", routeID, "eng")
	rec := httptest.NewRecorder()
	handler.HandleSearchRemoteSubtitles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var results []remoteSubtitleInfoDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1: %s", len(results), rec.Body.String())
	}
	if results[0].ThreeLetterISOLanguageName != "eng" {
		t.Fatalf("ThreeLetterISOLanguageName = %q, want eng", results[0].ThreeLetterISOLanguageName)
	}
	if results[0].ID == "" || !strings.Contains(results[0].Name, "SubDL") {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	if got := provider.lastQuery.Languages; len(got) != 1 || got[0] != "en" {
		t.Fatalf("search languages = %#v, want [en]", got)
	}
	if provider.lastQuery.Title != "Show" || provider.lastQuery.Season != 1 || provider.lastQuery.Episode != 2 {
		t.Fatalf("search query = %+v", provider.lastQuery)
	}
}

func TestHandleDownloadRemoteSubtitleStoresSelectedResult(t *testing.T) {
	codec := NewResourceIDCodec()
	contentID := "movie-1"
	routeID := codec.EncodeStringID(EncodedIDItem, contentID)
	repo := fakeSubtitleRepository{downloaded: map[int][]subtitles.DownloadedSubtitle{}, byKey: map[string]*subtitles.DownloadedSubtitle{}}
	s3 := &fakeSubtitleS3Client{objects: map[string][]byte{}}
	manager := subtitles.NewManager(repo, s3, "bucket")
	manager.RegisterProvider(&recordingSubtitleProvider{
		name:   "subdl",
		data:   []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"),
		format: subtitles.FormatSRT,
	})
	handler := NewSubtitleHandler(&stubContentService{detail: &upstreamItemDetail{
		ContentID: contentID,
		Title:     "Movie",
		Year:      2026,
		Versions: []catalog.FileVersion{
			{FileID: 99, FileName: "Movie.2026.mkv", FilePath: "/media/Movie.2026.mkv"},
		},
	}}, codec, manager)
	subtitleID, err := encodeRemoteSubtitleID(remoteSubtitleID{
		Provider:        "subdl",
		ID:              "/download/subtitle.srt",
		Language:        "eng",
		ReleaseName:     "Movie.2026",
		Format:          "srt",
		Score:           10,
		HearingImpaired: true,
	})
	if err != nil {
		t.Fatalf("encodeRemoteSubtitleID: %v", err)
	}

	req := newRemoteSubtitleRequest(http.MethodPost, "/Items/"+routeID+"/RemoteSearch/Subtitles/"+subtitleID, routeID, subtitleID)
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, &Session{
		StreamAppUserID: 7,
		ProfileID:       "profile-1",
	}))
	rec := httptest.NewRecorder()
	handler.HandleDownloadRemoteSubtitle(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	stored := repo.downloaded[99]
	if len(stored) != 1 {
		t.Fatalf("stored subtitles = %d, want 1", len(stored))
	}
	if stored[0].Language != "en" || stored[0].Provider != "subdl" || !stored[0].HearingImpaired {
		t.Fatalf("stored subtitle = %+v", stored[0])
	}
	if stored[0].DownloadedBy == nil || *stored[0].DownloadedBy != 7 {
		t.Fatalf("DownloadedBy = %v, want 7", stored[0].DownloadedBy)
	}
	if _, ok := s3.objects[stored[0].S3Key]; !ok {
		t.Fatalf("s3 object %q was not written", stored[0].S3Key)
	}
}

func newRemoteSubtitleRequest(method, path, itemID, subtitleParam string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("itemId", itemID)
	if method == http.MethodGet {
		routeCtx.URLParams.Add("language", subtitleParam)
	} else {
		routeCtx.URLParams.Add("subtitleId", subtitleParam)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, &Session{StreamAppUserID: 7, ProfileID: "profile-1"}))
	return req
}

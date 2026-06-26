package jellycompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/subtitles"
)

type fakeSubtitleRepository struct {
	downloaded map[int][]subtitles.DownloadedSubtitle
	byKey      map[string]*subtitles.DownloadedSubtitle
}

func (r fakeSubtitleRepository) InsertDownloadedSubtitle(_ context.Context, sub *subtitles.DownloadedSubtitle) error {
	if sub == nil {
		return nil
	}
	if sub.ID == 0 && r.byKey != nil {
		sub.ID = len(r.byKey) + 1
	}
	if r.byKey != nil {
		copy := *sub
		r.byKey[sub.S3Key] = &copy
	}
	if r.downloaded != nil {
		r.downloaded[sub.MediaFileID] = append(r.downloaded[sub.MediaFileID], *sub)
	}
	return nil
}

func (r fakeSubtitleRepository) GetDownloadedSubtitle(context.Context, int) (*subtitles.DownloadedSubtitle, error) {
	panic("unused")
}

func (r fakeSubtitleRepository) ListDownloadedSubtitles(_ context.Context, mediaFileID int) ([]subtitles.DownloadedSubtitle, error) {
	return r.downloaded[mediaFileID], nil
}

func (r fakeSubtitleRepository) UpdateDownloadedSubtitle(context.Context, int, subtitles.SubtitleMetadataUpdate) (*subtitles.DownloadedSubtitle, error) {
	panic("unused")
}

func (r fakeSubtitleRepository) DeleteDownloadedSubtitle(context.Context, int) (*subtitles.DownloadedSubtitle, error) {
	panic("unused")
}

func (r fakeSubtitleRepository) GetDownloadedSubtitleByS3Key(_ context.Context, s3Key string) (*subtitles.DownloadedSubtitle, error) {
	if r.byKey == nil {
		return nil, nil
	}
	return r.byKey[s3Key], nil
}

func (r fakeSubtitleRepository) ListProviderConfigs(context.Context) ([]subtitles.ProviderConfig, error) {
	panic("unused")
}

func (r fakeSubtitleRepository) GetProviderConfig(context.Context, string) (*subtitles.ProviderConfig, error) {
	panic("unused")
}

func (r fakeSubtitleRepository) UpsertProviderConfig(context.Context, *subtitles.ProviderConfig) error {
	panic("unused")
}

func TestHandlePlaybackInfo_AuthenticatesSubtitleDeliveryURLs(t *testing.T) {
	codec := NewResourceIDCodec()
	contentID := "movie-1"
	routeID := codec.EncodeStringID(EncodedIDItem, contentID)
	version := catalog.FileVersion{
		FileID:    42,
		Duration:  3600,
		Container: "mkv",
		Bitrate:   8000,
		VideoTracks: []models.VideoTrack{
			{Codec: "h264", Width: 1920, Height: 1080},
		},
		AudioTracks: []models.AudioTrack{
			{Codec: "aac", Default: true, Title: "Main"},
		},
		SubtitleTracks: []catalog.VersionSubtitleTrack{
			{Index: 2, Codec: "subrip", Language: "eng", Title: "English"},
			{Codec: "srt", Language: "spa", Title: "Spanish", External: true},
		},
	}

	handler := &PlaybackHandler{
		content: &stubContentService{detail: &upstreamItemDetail{
			ContentID: contentID,
			Versions:  []catalog.FileVersion{version},
		}},
		codec:          codec,
		deviceProfiles: NewDeviceProfileStore(time.Hour, nil),
		playbackStore:  NewPlaybackSessionStore(time.Hour, nil),
		SubtitleRepo: fakeSubtitleRepository{downloaded: map[int][]subtitles.DownloadedSubtitle{
			42: {
				{MediaFileID: 42, Language: "fre", Format: subtitles.FormatSRT, Provider: "opensubtitles"},
			},
		}},
	}

	req := httptest.NewRequest(http.MethodPost, "/Items/"+routeID+"/PlaybackInfo", strings.NewReader(`{}`))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", routeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, &Session{Token: "token-1"}))

	rr := httptest.NewRecorder()
	handler.HandlePlaybackInfo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var resp playbackInfoResponseDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.MediaSources) != 1 {
		t.Fatalf("media sources = %d, want 1", len(resp.MediaSources))
	}

	subtitleURLs := make([]string, 0, 3)
	for _, stream := range resp.MediaSources[0].MediaStreams {
		if stream.Type == "Subtitle" {
			subtitleURLs = append(subtitleURLs, stream.DeliveryURL)
		}
	}
	if len(subtitleURLs) != 3 {
		t.Fatalf("subtitle URLs = %d, want 3: %#v", len(subtitleURLs), subtitleURLs)
	}

	for _, rawURL := range subtitleURLs {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parse subtitle URL %q: %v", rawURL, err)
		}
		query := parsed.Query()
		if got := query.Get("api_key"); got != "token-1" {
			t.Fatalf("api_key for %q = %q, want token-1", rawURL, got)
		}
		if got := query.Get("PlaySessionId"); got != resp.PlaySessionID {
			t.Fatalf("PlaySessionId for %q = %q, want %q", rawURL, got, resp.PlaySessionID)
		}
	}
}

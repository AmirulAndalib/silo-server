package jellycompat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/text/language"

	"github.com/Silo-Server/silo-server/internal/catalog"
	silolang "github.com/Silo-Server/silo-server/internal/lang"
	"github.com/Silo-Server/silo-server/internal/subtitles"
)

// SubtitleHandler serves Jellyfin-compatible remote subtitle search/download
// routes backed by Silo's configured subtitle providers.
type SubtitleHandler struct {
	content ContentService
	codec   *ResourceIDCodec
	manager *subtitles.Manager
}

func NewSubtitleHandler(content ContentService, codec *ResourceIDCodec, manager *subtitles.Manager) *SubtitleHandler {
	return &SubtitleHandler{content: content, codec: codec, manager: manager}
}

type remoteSubtitleInfoDTO struct {
	ThreeLetterISOLanguageName string     `json:"ThreeLetterISOLanguageName,omitempty"`
	ID                         string     `json:"Id,omitempty"`
	ProviderName               string     `json:"ProviderName,omitempty"`
	Name                       string     `json:"Name,omitempty"`
	Format                     string     `json:"Format,omitempty"`
	DateCreated                *time.Time `json:"DateCreated,omitempty"`
	CommunityRating            *float64   `json:"CommunityRating,omitempty"`
	DownloadCount              *int       `json:"DownloadCount,omitempty"`
	IsHashMatch                *bool      `json:"IsHashMatch,omitempty"`
	HearingImpaired            *bool      `json:"HearingImpaired,omitempty"`
}

type remoteSubtitleID struct {
	Provider        string  `json:"p"`
	ID              string  `json:"id"`
	Language        string  `json:"l"`
	ReleaseName     string  `json:"r,omitempty"`
	Format          string  `json:"f,omitempty"`
	Score           float64 `json:"s,omitempty"`
	HearingImpaired bool    `json:"hi,omitempty"`
}

// HandleSearchRemoteSubtitles serves GET /Items/{id}/RemoteSearch/Subtitles/{language}.
func (h *SubtitleHandler) HandleSearchRemoteSubtitles(w http.ResponseWriter, r *http.Request) {
	session := SessionFromContext(r.Context())
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "Missing authentication token")
		return
	}
	if h == nil || h.manager == nil {
		writeJSON(w, http.StatusOK, []remoteSubtitleInfoDTO{})
		return
	}
	detail, version, ok := h.resolveSubtitleItem(w, r, session)
	if !ok {
		return
	}

	languageCode := silolang.Canonical(chi.URLParam(r, "language"))
	resp, err := h.manager.Search(r.Context(), subtitleSearchRequestForDetail(*detail, version, languageCode))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ServerError", "Subtitle search failed")
		return
	}

	results := make([]remoteSubtitleInfoDTO, 0, len(resp.Results))
	for _, result := range resp.Results {
		id, err := encodeRemoteSubtitleID(remoteSubtitleID{
			Provider:        result.Provider,
			ID:              result.ID,
			Language:        result.Language,
			ReleaseName:     result.ReleaseName,
			Format:          string(result.Format),
			Score:           result.Score,
			HearingImpaired: result.HearingImpaired,
		})
		if err != nil {
			continue
		}
		results = append(results, remoteSubtitleInfoDTO{
			ThreeLetterISOLanguageName: compatThreeLetterLanguageName(result.Language),
			ID:                         id,
			ProviderName:               subtitleProviderLabel(result.Provider),
			Name:                       remoteSubtitleName(result),
			Format:                     string(result.Format),
			DateCreated:                optionalTime(result.UploadDate),
			CommunityRating:            optionalFloat(result.Score),
			DownloadCount:              optionalInt(result.Downloads),
			HearingImpaired:            boolPtr(result.HearingImpaired),
		})
	}
	writeJSON(w, http.StatusOK, results)
}

// HandleDownloadRemoteSubtitle serves POST /Items/{id}/RemoteSearch/Subtitles/{subtitleId}.
func (h *SubtitleHandler) HandleDownloadRemoteSubtitle(w http.ResponseWriter, r *http.Request) {
	session := SessionFromContext(r.Context())
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "Missing authentication token")
		return
	}
	if h == nil || h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "Unavailable", "Subtitle providers are unavailable")
		return
	}
	_, version, ok := h.resolveSubtitleItem(w, r, session)
	if !ok {
		return
	}
	decoded, err := decodeRemoteSubtitleID(chi.URLParam(r, "subtitleId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "Invalid subtitle id")
		return
	}
	languageCode := silolang.Canonical(decoded.Language)
	if languageCode == "" {
		languageCode = decoded.Language
	}

	_, err = h.manager.Download(r.Context(), subtitles.DownloadRequest{
		ProviderName:    decoded.Provider,
		SubtitleID:      decoded.ID,
		MediaFileID:     version.FileID,
		UserID:          &session.StreamAppUserID,
		Language:        languageCode,
		ReleaseName:     decoded.ReleaseName,
		Score:           decoded.Score,
		HearingImpaired: decoded.HearingImpaired,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ServerError", "Failed to download subtitle")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SubtitleHandler) resolveSubtitleItem(
	w http.ResponseWriter,
	r *http.Request,
	session *Session,
) (*upstreamItemDetail, catalog.FileVersion, bool) {
	contentID, err := decodeItemID(h.codec, chi.URLParam(r, "itemId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "NotFound", "Item not found")
		return nil, catalog.FileVersion{}, false
	}
	if h.content == nil {
		writeError(w, http.StatusServiceUnavailable, "Unavailable", "Content service unavailable")
		return nil, catalog.FileVersion{}, false
	}
	detail, err := h.content.GetItemDetail(r.Context(), session, contentID, nil)
	if err != nil {
		writeCompatUpstreamError(w, err)
		return nil, catalog.FileVersion{}, false
	}
	if detail == nil || len(detail.Versions) == 0 {
		writeError(w, http.StatusNotFound, "NotFound", "Item not found")
		return nil, catalog.FileVersion{}, false
	}
	return detail, detail.Versions[0], true
}

func subtitleSearchRequestForDetail(detail upstreamItemDetail, version catalog.FileVersion, languageCode string) subtitles.SearchRequest {
	season, episode := 0, 0
	if detail.SeasonNumber != nil {
		season = *detail.SeasonNumber
	}
	if detail.EpisodeNumber != nil {
		episode = *detail.EpisodeNumber
	}
	releaseInfo := subtitles.ParseReleaseInfo(version.FilePath)
	req := subtitles.SearchRequest{
		IMDbID:   detail.ImdbID,
		TMDbID:   detail.TmdbID,
		Title:    firstNonEmpty(detail.SeriesTitle, detail.Title),
		Year:     detail.Year,
		Season:   season,
		Episode:  episode,
		Filename: firstNonEmpty(version.FileName, filepath.Base(version.FilePath)),
		MediaInfo: &subtitles.MediaMatchInfo{
			ReleaseGroup: releaseInfo.ReleaseGroup,
			Resolution:   firstNonEmpty(version.Resolution, releaseInfo.Resolution),
			VideoCodec:   firstNonEmpty(version.CodecVideo, releaseInfo.VideoCodec),
			AudioCodec:   firstNonEmpty(version.CodecAudio, releaseInfo.AudioCodec),
			Source:       releaseInfo.Source,
		},
	}
	if languageCode != "" {
		req.Languages = []string{languageCode}
	}
	return req
}

func encodeRemoteSubtitleID(id remoteSubtitleID) (string, error) {
	data, err := json.Marshal(id)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeRemoteSubtitleID(value string) (remoteSubtitleID, error) {
	var id remoteSubtitleID
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return id, err
	}
	if err := json.Unmarshal(data, &id); err != nil {
		return id, err
	}
	if strings.TrimSpace(id.Provider) == "" || strings.TrimSpace(id.ID) == "" {
		return id, fmt.Errorf("missing provider or id")
	}
	return id, nil
}

func remoteSubtitleName(result subtitles.SubtitleResult) string {
	name := strings.TrimSpace(result.ReleaseName)
	if name == "" {
		name = compatLanguageName(result.Language)
	}
	tags := make([]string, 0, 3)
	if result.HearingImpaired {
		tags = append(tags, "SDH")
	}
	if result.Format != "" {
		tags = append(tags, subtitleFormatLabel(string(result.Format)))
	}
	if provider := subtitleProviderLabel(result.Provider); provider != "" {
		tags = append(tags, provider)
	}
	return formatSubtitleLabel(name, tags...)
}

func compatThreeLetterLanguageName(code string) string {
	normalized := silolang.Canonical(code)
	if normalized == "" {
		return strings.ToLower(strings.TrimSpace(code))
	}
	tag, err := language.Parse(normalized)
	if err != nil {
		return normalized
	}
	base, confidence := tag.Base()
	if confidence == language.No {
		return normalized
	}
	if iso3 := base.ISO3(); iso3 != "" {
		return iso3
	}
	return base.String()
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func optionalFloat(value float64) *float64 {
	if value == 0 {
		return nil
	}
	return &value
}

func optionalInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

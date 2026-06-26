package jellycompat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestUserDTOReportsSubtitleManagementAndProfileConfiguration(t *testing.T) {
	provider := newProgressCountingStoreProvider()
	provider.store.profile = &userstore.Profile{
		ID:                  "profile-1",
		Language:            "ja",
		SubtitleLanguage:    "en",
		SubtitleMode:        "off",
		ShowForcedSubtitles: true,
	}
	handler := NewAuthHandler(func() *config.Config {
		return &config.Config{}
	}, nil, nil, provider)
	session := &Session{
		StreamAppUserID: 7,
		ProfileID:       "profile-1",
		PseudoUserID:    PseudoUserID(7, "profile-1"),
		Username:        "user",
	}

	dto := handler.userDTO(session)

	if !dto.Policy.EnableSubtitleManagement {
		t.Fatal("EnableSubtitleManagement = false, want true")
	}
	if dto.Configuration.AudioLanguagePreference != "ja" {
		t.Fatalf("AudioLanguagePreference = %q, want ja", dto.Configuration.AudioLanguagePreference)
	}
	if dto.Configuration.SubtitleLanguagePreference != "en" {
		t.Fatalf("SubtitleLanguagePreference = %q, want en", dto.Configuration.SubtitleLanguagePreference)
	}
	if dto.Configuration.SubtitleMode != "OnlyForced" {
		t.Fatalf("SubtitleMode = %q, want OnlyForced", dto.Configuration.SubtitleMode)
	}
}

func TestHandleUpdateConfigurationMapsJellyfinSubtitlePreferences(t *testing.T) {
	provider := newProgressCountingStoreProvider()
	provider.store.profile = &userstore.Profile{ID: "profile-1"}
	handler := NewAuthHandler(func() *config.Config {
		return &config.Config{}
	}, nil, nil, provider)
	session := &Session{
		StreamAppUserID: 7,
		ProfileID:       "profile-1",
		PseudoUserID:    PseudoUserID(7, "profile-1"),
	}
	body := bytes.NewBufferString(`{
		"AudioLanguagePreference": "jpn",
		"SubtitleLanguagePreference": "eng",
		"SubtitleMode": "OnlyForced"
	}`)
	req := httptest.NewRequest(
		http.MethodPost,
		"/Users/Configuration?userId="+session.PseudoUserID.String(),
		body,
	)
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, session))
	rec := httptest.NewRecorder()

	handler.HandleUpdateConfiguration(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.store.updatedProfileID != "profile-1" {
		t.Fatalf("updated profile = %q, want profile-1", provider.store.updatedProfileID)
	}
	if got := provider.store.profile.Language; got != "ja" {
		t.Fatalf("profile language = %q, want ja", got)
	}
	if got := provider.store.profile.SubtitleLanguage; got != "en" {
		t.Fatalf("subtitle language = %q, want en", got)
	}
	if got := provider.store.profile.SubtitleMode; got != "off" {
		t.Fatalf("subtitle mode = %q, want off", got)
	}
	if !provider.store.profile.ShowForcedSubtitles {
		t.Fatal("ShowForcedSubtitles = false, want true")
	}
}

func TestHandleUpdateConfigurationRejectsMismatchedUser(t *testing.T) {
	provider := newProgressCountingStoreProvider()
	handler := NewAuthHandler(func() *config.Config {
		return &config.Config{}
	}, nil, nil, provider)
	session := &Session{
		StreamAppUserID: 7,
		ProfileID:       "profile-1",
		PseudoUserID:    PseudoUserID(7, "profile-1"),
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/Users/Configuration?userId="+PseudoUserID(8, "other").String(),
		bytes.NewBufferString(`{"SubtitleMode":"Always"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, session))
	rec := httptest.NewRecorder()

	handler.HandleUpdateConfiguration(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if provider.store.updatedProfileID != "" {
		t.Fatalf("updated profile = %q, want no update", provider.store.updatedProfileID)
	}
}

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// metadata.cache_images copies provider artwork into the public bucket, so both
// write paths reject enabling it when no public bucket exists anywhere. A saved
// but not-yet-active bucket counts: the setup wizard configures the bucket and
// enables caching in one batch, and the UI badges the pending restart.

func cacheImagesHandler(settings *fakeServerSettingsStore, storage bool) *AdminHandler {
	h := &AdminHandler{SettingsRepo: settings}
	if storage {
		h.PublicStorageConfigured = func() bool { return true }
	}
	return h
}

func updateCacheImagesBatch(h *AdminHandler, values string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.HandleUpdateSettings(rec, httptest.NewRequest(
		http.MethodPut,
		"/admin/settings",
		strings.NewReader(`{"values":{`+values+`}}`),
	))
	return rec
}

func updateCacheImagesSingle(h *AdminHandler, value string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Put("/admin/settings/{key}", h.HandleUpdateSetting)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(
		http.MethodPut,
		"/admin/settings/metadata.cache_images",
		strings.NewReader(`{"value":"`+value+`"}`),
	))
	return rec
}

func assertStorageUnavailable(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "storage_unavailable" {
		t.Fatalf("error code = %q, want storage_unavailable; body=%#v", body.Error, body)
	}
	if !strings.Contains(body.Message, "S3 image caching requires a configured public storage bucket") {
		t.Fatalf("error message = %q", body.Message)
	}
}

func TestCacheImagesEnableRequiresPublicBucket(t *testing.T) {
	const cacheImagesTrue = `"metadata.cache_images":"true"`

	t.Run("batch with active storage", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{}}
		rec := updateCacheImagesBatch(cacheImagesHandler(settings, true), cacheImagesTrue)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "true" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	t.Run("batch with saved bucket but inactive store", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{"s3.public_bucket": "silo-public"}}
		rec := updateCacheImagesBatch(cacheImagesHandler(settings, false), cacheImagesTrue)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "true" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	// The setup wizard writes both in one request, before any restart.
	t.Run("batch configuring the bucket in the same request", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{}}
		rec := updateCacheImagesBatch(cacheImagesHandler(settings, false),
			cacheImagesTrue+`,"s3.public_endpoint":"https://s3.example.com","s3.public_bucket":"silo-public"`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "true" ||
			settings.values["s3.public_bucket"] != "silo-public" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	t.Run("batch with no bucket anywhere", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{}}
		rec := updateCacheImagesBatch(cacheImagesHandler(settings, false), cacheImagesTrue)
		assertStorageUnavailable(t, rec)
		if settings.setManyCalls != 0 || settings.atomicCalls != 0 {
			t.Fatalf("write attempted: setMany=%d atomic=%d", settings.setManyCalls, settings.atomicCalls)
		}
		if _, stored := settings.values["metadata.cache_images"]; stored {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	// Clearing the bucket in the same batch must not count as configuring one.
	t.Run("batch clearing the bucket while enabling", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{}}
		rec := updateCacheImagesBatch(cacheImagesHandler(settings, false),
			cacheImagesTrue+`,"s3.public_bucket":""`)
		assertStorageUnavailable(t, rec)
	})

	t.Run("batch disable with no bucket", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{"metadata.cache_images": "true"}}
		rec := updateCacheImagesBatch(cacheImagesHandler(settings, false), `"metadata.cache_images":"false"`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "false" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	t.Run("single with active storage", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{}}
		rec := updateCacheImagesSingle(cacheImagesHandler(settings, true), "true")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "true" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	t.Run("single with saved bucket but inactive store", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{"s3.public_bucket": "silo-public"}}
		rec := updateCacheImagesSingle(cacheImagesHandler(settings, false), "true")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "true" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	t.Run("single with an environment-supplied bucket", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{}}
		h := cacheImagesHandler(settings, false)
		h.BootstrapSensitiveConfigured = map[string]bool{"s3.public_bucket": true}
		h.BootstrapSensitiveValues = map[string]string{"s3.public_bucket": "silo-public"}
		rec := updateCacheImagesSingle(h, "true")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "true" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	t.Run("single with no bucket anywhere", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{}}
		rec := updateCacheImagesSingle(cacheImagesHandler(settings, false), "true")
		assertStorageUnavailable(t, rec)
		if settings.setCalls != 0 {
			t.Fatalf("Set calls = %d, want 0", settings.setCalls)
		}
		if _, stored := settings.values["metadata.cache_images"]; stored {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})

	t.Run("single disable with no bucket", func(t *testing.T) {
		settings := &fakeServerSettingsStore{values: map[string]string{"metadata.cache_images": "true"}}
		rec := updateCacheImagesSingle(cacheImagesHandler(settings, false), "false")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if settings.values["metadata.cache_images"] != "false" {
			t.Fatalf("stored values = %#v", settings.values)
		}
	})
}

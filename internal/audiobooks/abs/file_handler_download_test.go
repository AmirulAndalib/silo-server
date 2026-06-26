package abs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/models"
)

// fakeDownloadPolicy is an in-memory abs.DownloadPolicy for the file-route
// download-privilege tests.
type fakeDownloadPolicy struct {
	allowed map[string]bool
	err     error
}

func (f *fakeDownloadPolicy) DownloadAllowed(_ context.Context, userID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.allowed[userID], nil
}

// dispatchFileStream invokes handleFileStream with the chi route params and ABS
// auth context the bearerAuth middleware would normally inject.
func dispatchFileStream(h *Handler, path, contentID, ino, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("libraryItemId", contentID)
	rctx.URLParams.Add("ino", ino)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, ctxKey{}, ctxAuth{UserID: userID})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.handleFileStream(rec, req)
	return rec
}

// newFileStreamHandler builds a Handler whose media store holds one audio file
// for contentID, with the supplied download policy.
func newFileStreamHandler(t *testing.T, contentID string, policy DownloadPolicy) *Handler {
	t.Helper()
	mediaStore := &filesMediaStore{
		contentID: contentID,
		files:     []*models.MediaFile{{ID: 1, FilePath: makeTempAudio(t)}},
	}
	return New(Dependencies{MediaStore: mediaStore, DownloadPolicy: policy})
}

// TestFileStream_DeniesWhenDownloadDisabled is the regression guard for issue
// #141: a restricted user (DownloadAllowed=false) must get 403 from both the
// bare /file/{ino} route and the /download variant, and the bytes must not be
// served.
func TestFileStream_DeniesWhenDownloadDisabled(t *testing.T) {
	policy := &fakeDownloadPolicy{allowed: map[string]bool{"1": false}}
	h := newFileStreamHandler(t, "book-1", policy)

	for _, path := range []string{
		"/api/items/book-1/file/0",
		"/api/items/book-1/file/0/download",
	} {
		rec := dispatchFileStream(h, path, "book-1", "0", "1")
		if rec.Code != http.StatusForbidden {
			t.Errorf("path %s: status = %d, want 403; body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestFileStream_AllowsWhenDownloadEnabled confirms the gate does not regress
// the happy path: a permitted user still receives the file.
func TestFileStream_AllowsWhenDownloadEnabled(t *testing.T) {
	policy := &fakeDownloadPolicy{allowed: map[string]bool{"1": true}}
	h := newFileStreamHandler(t, "book-1", policy)

	rec := dispatchFileStream(h, "/api/items/book-1/file/0/download", "book-1", "0", "1")
	if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 200/206; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Error("expected audio bytes for a permitted download")
	}
}

// TestFileStream_FailsClosedOnPolicyError verifies that a resolver error denies
// the download (fail closed) rather than serving the file.
func TestFileStream_FailsClosedOnPolicyError(t *testing.T) {
	policy := &fakeDownloadPolicy{err: errors.New("user store unavailable")}
	h := newFileStreamHandler(t, "book-1", policy)

	rec := dispatchFileStream(h, "/api/items/book-1/file/0/download", "book-1", "0", "1")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 on resolver error; body=%s", rec.Code, rec.Body.String())
	}
}

// TestFileStream_NilPolicyAllows documents that when no DownloadPolicy is wired
// (non-production), the handler preserves its prior allow-everything behavior.
func TestFileStream_NilPolicyAllows(t *testing.T) {
	h := newFileStreamHandler(t, "book-1", nil)

	rec := dispatchFileStream(h, "/api/items/book-1/file/0/download", "book-1", "0", "1")
	if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 200/206 with nil policy; body=%s", rec.Code, rec.Body.String())
	}
}

// TestLoginEnvelope_DownloadPermissionReflectsPolicy asserts the login envelope
// advertises the real download privilege so compliant clients hide the
// offline-save UI for restricted users (issue #141, gap 2).
func TestLoginEnvelope_DownloadPermissionReflectsPolicy(t *testing.T) {
	cases := []struct {
		name    string
		allowed bool
	}{
		{"allowed", true},
		{"denied", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(Dependencies{
				MediaStore:     &noopMediaStore{},
				DownloadPolicy: &fakeDownloadPolicy{allowed: map[string]bool{"7": tc.allowed}},
			})
			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			env := h.loginEnvelope(req, time.Now(), "7", "Reader", "a", "r")

			user, ok := env["user"].(map[string]any)
			if !ok {
				t.Fatalf("user is not a map: %T", env["user"])
			}
			perms, ok := user["permissions"].(map[string]any)
			if !ok {
				t.Fatalf("permissions is not a map: %T", user["permissions"])
			}
			if got := perms["download"]; got != tc.allowed {
				t.Errorf("permissions.download = %v, want %v", got, tc.allowed)
			}
		})
	}
}

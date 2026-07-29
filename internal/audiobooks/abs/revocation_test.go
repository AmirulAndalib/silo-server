package abs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/streamrevoke"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

func TestBearerAuthCarriesJWTTimestampForUserCutoff(t *testing.T) {
	const accessTokenType = "access"
	secret := []byte("test-secret-32-bytes-aaaaaaaaaaaaa")
	tokens := newMemTokenStore()
	revocations := streamrevoke.New(streamrevoke.Options{})
	if err := revocations.RevokeUser(context.Background(), 7, "cutoff"); err != nil {
		t.Fatal(err)
	}
	cutoff := revocations.List()[0].RevokedAt
	h := New(Dependencies{
		MediaStore: noopMediaStore{},
		TokenStore: tokens,
		Config:     &staticConfig{secret: secret},
		Revocation: revocations,
	})

	issue := func(jti string, issuedAt time.Time) string {
		t.Helper()
		raw, err := issueJWT(secret, Claims{
			Type:   accessTokenType,
			UserID: "7",
			JTI:    jti,
			RegisteredClaims: jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(issuedAt),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := tokens.InsertToken(context.Background(), ABSToken{
			UserID: "7", Type: accessTokenType, JTI: jti, ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		return raw
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ok := absAuthFrom(r)
		if !ok {
			t.Fatal("missing ctxAuth")
		}
		if revocations.Refuse(w, "", 7, auth.IssuedAt) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	protected := h.bearerAuth(next)
	for _, tc := range []struct {
		name   string
		token  string
		status int
	}{
		{name: "before cutoff refused", token: issue("old", cutoff.Add(-time.Hour)), status: http.StatusForbidden},
		{name: "after cutoff admitted", token: issue("new", cutoff.Add(time.Hour)), status: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
}

func TestPublicTrackUsesSessionStartedAtForCutoff(t *testing.T) {
	h, _ := newPublicTrackHandler(t, "sid-cutoff", "book-1", false)
	store := streamrevoke.New(streamrevoke.Options{})
	h.deps.Revocation = store
	if err := store.RevokeUser(context.Background(), 1, "cutoff"); err != nil {
		t.Fatal(err)
	}

	rec := dispatchTrack(h, http.MethodGet, "sid-cutoff", "1")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body.String())
	}

	sessions, ok := h.deps.PlaybackSessionStore.(*fakePlaybackSessionStore)
	if !ok {
		t.Fatal("unexpected playback session store type")
	}
	session := sessions.sessions["sid-cutoff"]
	session.StartedAt = time.Now().Add(time.Hour)
	sessions.sessions["sid-cutoff"] = session
	rec = dispatchTrack(h, http.MethodGet, "sid-cutoff", "1")
	if rec.Code != http.StatusOK {
		t.Fatalf("post-cutoff session status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

type revocationFeedFileMediaStore struct {
	noopMediaStore
	file  *models.MediaFile
	onGet func()
}

func (s *revocationFeedFileMediaStore) GetMediaFileByID(_ context.Context, id int) (*models.MediaFile, error) {
	if s.onGet != nil {
		s.onGet()
	}
	if s.file != nil && s.file.ID == id {
		return s.file, nil
	}
	return nil, ErrNotFound
}

type deadlineResponseRecorder struct {
	*httptest.ResponseRecorder
	deadlines int
}

func (w *deadlineResponseRecorder) SetWriteDeadline(time.Time) error {
	w.deadlines++
	return nil
}

func TestPublicFeedFileUsesFeedCreatedAtForCutoff(t *testing.T) {
	const (
		feedSlug = "cutoff-feed"
		ino      = 9
	)
	ctx := context.Background()
	feeds := newMemRSSFeedStore()
	feed := RSSFeed{
		ID:            "feed-1",
		UserID:        "1",
		LibraryItemID: "book-1",
		Slug:          feedSlug,
		CreatedAt:     time.Now().Add(-time.Hour),
	}
	feeds.rows[feed.ID] = feed
	revocations := streamrevoke.New(streamrevoke.Options{})
	if err := revocations.RevokeUser(ctx, 1, "cutoff"); err != nil {
		t.Fatal(err)
	}
	cutoff := revocations.List()[0].RevokedAt
	mediaStore := &revocationFeedFileMediaStore{file: &models.MediaFile{
		ID: ino, ContentID: feed.LibraryItemID, FilePath: makeTempAudio(t),
	}}
	h := New(Dependencies{
		MediaStore:   mediaStore,
		RSSFeedStore: feeds,
		Revocation:   revocations,
	})

	rec := dispatchABSWithParams(http.MethodGet, "/feed/"+feedSlug+"/file/9",
		map[string]string{"slug": feedSlug, "ino": "9"}, nil, "", "", h.handlePublicFeedFile)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("pre-cutoff feed status=%d body=%s, want 403", rec.Code, rec.Body.String())
	}

	feed.CreatedAt = cutoff.Add(time.Nanosecond)
	feeds.rows[feed.ID] = feed
	for !time.Now().After(feed.CreatedAt) {
		time.Sleep(time.Nanosecond)
	}
	rec = dispatchABSWithParams(http.MethodGet, "/feed/"+feedSlug+"/file/9",
		map[string]string{"slug": feedSlug, "ino": "9"}, nil, "", "", h.handlePublicFeedFile)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-cutoff feed status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}

	// Revoke again between the handler's initial Refuse check and WatchAndCut.
	// The deadline recorder makes the watcher observable without a real socket.
	mediaStore.onGet = func() {
		mediaStore.onGet = nil
		if err := revocations.RevokeUser(ctx, 1, "cut during pour"); err != nil {
			t.Fatalf("RevokeUser during pour: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/feed/"+feedSlug+"/file/9", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", feedSlug)
	rctx.URLParams.Add("ino", "9")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	deadlineRec := &deadlineResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.handlePublicFeedFile(deadlineRec, req)
	if deadlineRec.Code != http.StatusOK {
		t.Fatalf("watched feed status=%d body=%s, want 200", deadlineRec.Code, deadlineRec.Body.String())
	}
	if deadlineRec.deadlines != 1 {
		t.Fatalf("write deadlines=%d, want 1 from WatchAndCut", deadlineRec.deadlines)
	}
}

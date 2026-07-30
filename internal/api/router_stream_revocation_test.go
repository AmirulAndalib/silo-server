package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/streamrevoke"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

func signStreamClaimsAt(
	t *testing.T,
	secret, sessionID string,
	userID int,
	issuedAt, expiresAt time.Time,
) string {
	t.Helper()
	claims := streamtoken.Claims{
		SessionID: sessionID,
		UserID:    userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	if !issuedAt.IsZero() {
		claims.IssuedAt = jwt.NewNumericDate(issuedAt)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func mountedRevocationGuard(
	store *streamrevoke.Store,
	secret string,
	claims *auth.Claims,
) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
		})
	})
	router.Get("/stream/{session_id}", guardRevocation(store, secret, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	return router
}

func TestMountedRevocationGuardUsesCredentialCutoff(t *testing.T) {
	const secret = "test-secret"
	const userID = 7
	store := streamrevoke.New(streamrevoke.Options{})
	beforeCutoff := time.Now().Add(-time.Hour)
	if err := store.RevokeUser(context.Background(), userID, "sessions_revoked"); err != nil {
		t.Fatal(err)
	}
	afterCutoff := time.Now().Add(time.Hour)
	handler := mountedRevocationGuard(store, secret, &auth.Claims{
		UserID: userID, TokenType: auth.TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(afterCutoff)},
	})

	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"credential before cutoff is refused": {
			token: signStreamClaimsAt(t, secret, "session-1", userID, beforeCutoff, time.Now().Add(time.Hour)),
			want:  http.StatusForbidden,
		},
		"credential after cutoff plays": {
			token: signStreamClaimsAt(t, secret, "session-1", userID, afterCutoff, time.Now().Add(2*time.Hour)),
			want:  http.StatusNoContent,
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/stream/session-1?st="+tc.token, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestMountedRevocationGuardStreamTokenBinding(t *testing.T) {
	const secret = "test-secret"
	now := time.Now()
	accessClaims := &auth.Claims{
		UserID: 7, TokenType: auth.TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(now.Add(-time.Hour))},
	}
	handler := mountedRevocationGuard(streamrevoke.New(streamrevoke.Options{}), secret, accessClaims)
	matching := signStreamClaimsAt(t, secret, "session-1", 7, now, now.Add(time.Hour))
	mismatched := signStreamClaimsAt(t, secret, "other-session", 7, now, now.Add(time.Hour))
	badSignature := signStreamClaimsAt(t, "wrong-secret", "session-1", 7, now, now.Add(time.Hour))
	expired := signStreamClaimsAt(t, secret, "session-1", 7, now.Add(-2*time.Hour), now.Add(-time.Hour))
	missingIssuedAt := signStreamClaimsAt(t, secret, "session-1", 7, time.Time{}, now.Add(time.Hour))

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{name: "matching", query: "?st=" + matching, wantStatus: http.StatusNoContent},
		{name: "absent", wantStatus: http.StatusNoContent},
		{name: "bad signature falls back to request auth", query: "?st=" + badSignature, wantStatus: http.StatusNoContent},
		{name: "expired falls back to request auth", query: "?st=" + expired, wantStatus: http.StatusNoContent},
		{name: "missing iat fails open", query: "?st=" + missingIssuedAt, wantStatus: http.StatusNoContent},
		{name: "mismatched session id", query: "?st=" + mismatched, wantStatus: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/stream/session-1"+tc.query, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestMountedRevocationGuardAPIKeyFailsOpenForUserCutoff(t *testing.T) {
	store := streamrevoke.New(streamrevoke.Options{})
	if err := store.RevokeUser(context.Background(), 7, "sessions_revoked"); err != nil {
		t.Fatal(err)
	}
	handler := mountedRevocationGuard(store, "test-secret", &auth.Claims{
		UserID: 7, TokenType: auth.TokenTypeAPIKey, APIKeyID: 42,
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/session-1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("API-key request status = %d, want %d (documented cutoff hole)", rec.Code, http.StatusNoContent)
	}
}

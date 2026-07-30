package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestCredentialIssuedAt(t *testing.T) {
	issuedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	access := &auth.Claims{
		UserID: 7, TokenType: auth.TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(issuedAt)},
	}
	if got := CredentialIssuedAt(SetClaims(context.Background(), access)); !got.Equal(issuedAt) {
		t.Fatalf("access credential time = %v, want %v", got, issuedAt)
	}

	for name, claims := range map[string]*auth.Claims{
		"API key":     {UserID: 7, TokenType: auth.TokenTypeAPIKey},
		"missing iat": {UserID: 7, TokenType: auth.TokenTypeAccess},
	} {
		t.Run(name, func(t *testing.T) {
			if got := CredentialIssuedAt(SetClaims(context.Background(), claims)); !got.IsZero() {
				t.Fatalf("credential time = %v, want zero", got)
			}
		})
	}
}

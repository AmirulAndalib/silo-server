package artworkurl

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

var ErrInvalid = errors.New("invalid artwork URL")

type Signer struct {
	key []byte
	TTL time.Duration
	Now func() time.Time
}

func NewSigner(jwtSecret string, ttl time.Duration) *Signer {
	if ttl <= 0 {
		ttl = 4 * time.Hour
	}
	if ttl < time.Minute {
		ttl = time.Minute
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	h := sha256.Sum256(append([]byte("silo-artwork-url-v1"), []byte(jwtSecret)...))
	return &Signer{key: h[:], TTL: ttl, Now: time.Now}
}
func (s *Signer) mac(key, exp string) []byte {
	h := hmac.New(sha256.New, s.key)
	fmt.Fprintf(h, "artwork-v1\n%s\n%s", key, exp)
	return h.Sum(nil)[:16]
}
func (s *Signer) Sign(key string) string {
	now := s.Now()
	exp := now.Add(s.TTL).Unix()
	exp = (exp / 900) * 900
	e := strconv.FormatInt(exp, 10)
	sig := base64.RawURLEncoding.EncodeToString(s.mac(key, e))
	return "/api/v2/artwork/" + strings.TrimPrefix(key, "/") + "?exp=" + e + "&sig=" + url.QueryEscape(sig)
}
func (s *Signer) Verify(key, exp, sig string, now time.Time) error {
	n, e := strconv.ParseInt(exp, 10, 64)
	if e != nil || n < now.Unix() {
		return ErrInvalid
	}
	got, e := base64.RawURLEncoding.DecodeString(sig)
	if e != nil || !hmac.Equal(got, s.mac(key, exp)) {
		return ErrInvalid
	}
	return nil
}

type Resolver interface {
	ResolveURLs(ctx context.Context, keys []string) map[string]catalog.ResolvedImageURL
}

type ServerResolver struct{ Signer *Signer }

func (r ServerResolver) ResolveURLs(ctx context.Context, keys []string) map[string]catalog.ResolvedImageURL {
	out := make(map[string]catalog.ResolvedImageURL, len(keys))
	for _, k := range keys {
		if ctx.Err() != nil {
			break
		}
		out[k] = catalog.ResolvedImageURL{URL: r.Signer.Sign(k)}
	}
	return out
}

type DirectResolver struct {
	Resolve func(context.Context, string) (catalog.ResolvedImageURL, error)
}

func (r DirectResolver) ResolveURLs(ctx context.Context, keys []string) map[string]catalog.ResolvedImageURL {
	out := make(map[string]catalog.ResolvedImageURL, len(keys))
	for _, k := range keys {
		if v, e := r.Resolve(ctx, k); e == nil {
			out[k] = v
		}
	}
	return out
}

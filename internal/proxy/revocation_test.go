package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

type latchCapturingRevocationStore struct {
	latch *httpstream.CutLatch
}

func (s *latchCapturingRevocationStore) IsRevoked(string, int, time.Time) bool {
	return false
}

func (s *latchCapturingRevocationStore) Refuse(http.ResponseWriter, string, int, time.Time) bool {
	return false
}

func (s *latchCapturingRevocationStore) WatchAndCutContext(ctx context.Context, _ http.ResponseWriter, _ string, _ int, _ time.Time) func() {
	s.latch = httpstream.CutLatchFrom(ctx)
	return func() {}
}

func TestHandleRemuxCarriesCutLatchToRevocationWatcher(t *testing.T) {
	const secret = "proxy-revocation-test-secret"
	watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = secret
	watcher.SetConfigForTest(cfg)

	server := NewServer(watcher, nodesessions.NewTracker(nil, "http://proxy", "proxy", "proxy"))
	revocation := &latchCapturingRevocationStore{}
	server.SetRevocationStore(revocation)

	token, err := streamtoken.Sign(streamtoken.Claims{
		SessionID: "remux-latch",
		MediaPath: t.TempDir() + "/missing.mkv",
	}, secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/stream/remux/"+token, nil)
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if revocation.latch == nil {
		t.Fatal("remux revocation watcher request context has no cut latch")
	}
}

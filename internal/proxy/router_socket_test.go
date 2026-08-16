package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

const socketProxyMedia = "0123456789abcdefghijklmnopqrstuvwxyz"

// newSocketProxyServer builds a proxy Server whose Handler() is mounted on a
// real listener, so the tests below exercise the assembled middleware chain
// (client IP resolution -> CORS -> egress metering) rather than a handler in
// isolation. That chain is the thing P0a changed; a handler-level test would
// bypass all of it.
func newSocketProxyServer(t *testing.T, secret string, resolver *clientip.Resolver) *Server {
	t.Helper()
	w := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = secret
	w.SetConfigForTest(cfg)
	srv := NewServer(w, nodesessions.NewTracker(nil, "http://proxy", "proxy", "proxy"))
	srv.SetClientIPResolver(resolver)
	return srv
}

func socketProxyMediaToken(t *testing.T, secret, path string) string {
	t.Helper()
	token, err := streamtoken.Sign(streamtoken.Claims{
		SessionID:   "socket-proxy-1",
		MediaPath:   path,
		PlayMethod:  "direct",
		UserID:      7,
		ProfileID:   "profile-1",
		MediaFileID: 42,
	}, secret, time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func writeSocketProxyMedia(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(path, []byte(socketProxyMedia), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// socketProxyResult is the fully-drained result of one request. The body is read
// and closed inside the helper so each case can assert on it directly.
type socketProxyResult struct {
	status int
	header http.Header
	body   string
}

func socketProxyRequest(t *testing.T, client *http.Client, method, url string, headers map[string]string) socketProxyResult {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, url, err)
	}
	return socketProxyResult{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

// TestMountedProxyRouterServesMediaOverSocket covers the HTTP surface of the
// proxy's mounted chain: GET/HEAD, ranges, conditional requests, and the fact
// that no compression middleware is mounted there (so media keeps its
// io.ReaderFrom path through the egress meter).
func TestMountedProxyRouterServesMediaOverSocket(t *testing.T) {
	const secret = "socket-proxy-secret"
	path := writeSocketProxyMedia(t)
	srv := newSocketProxyServer(t, secret, nil)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	t.Cleanup(client.CloseIdleConnections)
	mediaURL := server.URL + "/stream/direct/" + socketProxyMediaToken(t, secret, path)

	got := socketProxyRequest(t, client, http.MethodGet, mediaURL, nil)
	if got.status != http.StatusOK || got.body != socketProxyMedia {
		t.Fatalf("GET = %d %q, want 200 %q", got.status, got.body, socketProxyMedia)
	}
	etag := got.header.Get("ETag")

	if got = socketProxyRequest(t, client, http.MethodHead, mediaURL, nil); got.status != http.StatusOK {
		t.Fatalf("HEAD = %d, want 200", got.status)
	}

	got = socketProxyRequest(t, client, http.MethodGet, mediaURL, map[string]string{"Range": "bytes=2-5"})
	if got.status != http.StatusPartialContent || got.body != "2345" {
		t.Fatalf("Range = %d %q, want 206 %q", got.status, got.body, "2345")
	}

	// The proxy mounts no compressor, so an Accept-Encoding request must still
	// come back identity-encoded and byte-identical.
	got = socketProxyRequest(t, client, http.MethodGet, mediaURL, map[string]string{"Accept-Encoding": "gzip"})
	if enc := got.header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("media Content-Encoding = %q, want empty", enc)
	}
	if got.body != socketProxyMedia {
		t.Fatalf("gzip-offered body = %q, want %q", got.body, socketProxyMedia)
	}

	if etag != "" {
		got = socketProxyRequest(t, client, http.MethodGet, mediaURL, map[string]string{"If-None-Match": etag})
		if got.status != http.StatusNotModified {
			t.Fatalf("If-None-Match = %d, want 304", got.status)
		}
	}
}

// TestMountedProxyRouterResolvesViewerIPOverSocket is the trust-boundary test for
// the resolver P0a mounted on the proxy. It runs over a real socket because the
// resolver reads RemoteAddr, which only a real connection populates: the peer is
// loopback, so a forwarding header is honored only when loopback is trusted.
func TestMountedProxyRouterResolvesViewerIPOverSocket(t *testing.T) {
	const secret = "socket-proxy-ip-secret"

	trusted, err := clientip.ParseCIDRs("127.0.0.0/8,::1/128")
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	resolver := clientip.NewResolver(trusted)

	var seen string
	srv := newSocketProxyServer(t, secret, resolver)
	mounted := srv.Handler()
	// No proxy route consumes the resolved address yet — that arrives with the
	// telemetry phase — so observe it from a NotFound handler, which chi still
	// runs through the full mounted middleware chain. That keeps this a test of
	// the real chain rather than of clientip.Middleware in isolation.
	router, ok := mounted.(chi.Router)
	if !ok {
		t.Fatalf("proxy Handler() is %T, want chi.Router", mounted)
	}
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		seen = clientip.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mounted)
	t.Cleanup(server.Close)

	probe := func(t *testing.T, headers map[string]string) string {
		t.Helper()
		seen = ""
		client := &http.Client{}
		defer client.CloseIdleConnections()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/unrouted-probe", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return seen
	}

	// Loopback is trusted here, so the forwarded viewer address wins.
	if got := probe(t, map[string]string{"X-Forwarded-For": "203.0.113.9"}); got != "203.0.113.9" {
		t.Fatalf("trusted XFF resolved to %q, want 203.0.113.9", got)
	}
	if got := probe(t, map[string]string{"X-Real-IP": "203.0.113.10"}); got != "203.0.113.10" {
		t.Fatalf("trusted X-Real-IP resolved to %q, want 203.0.113.10", got)
	}

	// Narrow the trust set at runtime: loopback is no longer a trusted proxy, so
	// the same spoofed header must be ignored in favor of the real peer.
	narrowed, err := clientip.ParseCIDRs("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	resolver.UpdateTrustedCIDRs(narrowed)
	got := probe(t, map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if got == "203.0.113.9" {
		t.Fatal("spoofed X-Forwarded-For was honored from an untrusted peer")
	}
	if ip := net.ParseIP(got); ip == nil || !ip.IsLoopback() {
		t.Fatalf("untrusted peer resolved to %q, want the loopback peer address", got)
	}
}

// TestMountedProxyRouterRelaysToNode covers the proxy->node hop over real
// sockets: the proxy must stream the upstream node's bytes back to the viewer
// through the egress meter without corrupting them.
func TestMountedProxyRouterRelaysToNode(t *testing.T) {
	const secret = "socket-proxy-relay-secret"
	const segment = "segment-bytes-from-node"

	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/segment/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		_, _ = io.WriteString(w, segment)
	}))
	t.Cleanup(node.Close)

	token, err := streamtoken.Sign(streamtoken.Claims{
		SessionID:            "socket-relay-1",
		PlayMethod:           "transcode",
		TranscodeNode:        node.URL,
		TranscodeTransportID: "transport-1",
		UserID:               7,
		ProfileID:            "profile-1",
		MediaFileID:          42,
	}, secret, time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	srv := newSocketProxyServer(t, secret, nil)
	before := srv.egress.RateKbps()
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	t.Cleanup(client.CloseIdleConnections)

	got := socketProxyRequest(t, client, http.MethodGet, server.URL+"/stream/transcode/"+token+"/segment/000.ts", nil)
	if got.status != http.StatusOK || got.body != segment {
		t.Fatalf("relayed segment = %d %q, want 200 %q", got.status, got.body, segment)
	}
	if srv.egress.RateKbps() < before {
		t.Fatal("relayed bytes were not counted by the egress meter")
	}
}

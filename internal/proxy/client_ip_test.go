package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/clientip"
)

func mustCIDRs(t *testing.T, raw ...string) []*net.IPNet {
	t.Helper()
	out := make([]*net.IPNet, 0, len(raw))
	for _, entry := range raw {
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", entry, err)
		}
		out = append(out, network)
	}
	return out
}

// The edge attributes monitoring records to a viewer address. Behind an ingress
// or load balancer every viewer shares the connecting peer address, so without
// forwarded-header resolution the admin session view — and any per-viewer
// analysis built on it — collapses to one indistinguishable client.
func TestEdgeClientIPResolvesViewerBehindTrustedIngress(t *testing.T) {
	srv := &Server{}
	srv.SetClientIPResolver(clientip.NewResolver(mustCIDRs(t, "10.0.0.0/8")))

	first := httptest.NewRequest(http.MethodGet, "/stream/direct/tok", nil)
	first.RemoteAddr = "10.10.10.100:54321"
	first.Header.Set("X-Forwarded-For", "203.0.113.7")

	second := httptest.NewRequest(http.MethodGet, "/stream/direct/tok", nil)
	second.RemoteAddr = "10.10.10.100:54322"
	second.Header.Set("X-Forwarded-For", "198.51.100.42")

	firstIP := srv.edgeClientIP(first)
	secondIP := srv.edgeClientIP(second)

	if firstIP != "203.0.113.7" {
		t.Errorf("first viewer = %q, want 203.0.113.7", firstIP)
	}
	if secondIP != "198.51.100.42" {
		t.Errorf("second viewer = %q, want 198.51.100.42", secondIP)
	}
	if firstIP == secondIP {
		t.Fatal("two viewers behind one ingress collapsed to a single address")
	}
}

// A client that is not itself a trusted proxy must not be able to choose its
// own recorded address: with auto-enforcement built on these records, a
// believed forged header would be a spoofable primitive.
func TestEdgeClientIPIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	srv := &Server{}
	srv.SetClientIPResolver(clientip.NewResolver(mustCIDRs(t, "10.0.0.0/8")))

	req := httptest.NewRequest(http.MethodGet, "/stream/direct/tok", nil)
	req.RemoteAddr = "203.0.113.9:44444"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Real-IP", "198.51.100.2")

	if got := srv.edgeClientIP(req); got != "203.0.113.9" {
		t.Fatalf("edgeClientIP = %q, want the connecting peer 203.0.113.9", got)
	}
}

// With no trusted proxies configured, forwarding headers are never consulted —
// the fail-closed direction for the trust boundary.
func TestEdgeClientIPWithEmptyTrustListIgnoresForwardedHeader(t *testing.T) {
	srv := &Server{}
	srv.SetClientIPResolver(clientip.NewResolver(nil))

	req := httptest.NewRequest(http.MethodGet, "/stream/direct/tok", nil)
	req.RemoteAddr = "10.10.10.100:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got := srv.edgeClientIP(req); got != "10.10.10.100" {
		t.Fatalf("edgeClientIP = %q, want the connecting peer 10.10.10.100", got)
	}
}

// A directly-exposed edge with no resolver wired keeps the previous behavior.
func TestEdgeClientIPWithoutResolverUsesRemoteAddr(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/stream/direct/tok", nil)
	req.RemoteAddr = "203.0.113.5:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")

	if got := srv.edgeClientIP(req); got != "203.0.113.5" {
		t.Fatalf("edgeClientIP = %q, want 203.0.113.5", got)
	}
}

// IPv6 peers carry bracketed host:port in RemoteAddr; the fallback must not
// mangle them.
func TestEdgeClientIPHandlesIPv6RemoteAddr(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/stream/direct/tok", nil)
	req.RemoteAddr = "[2001:db8::1]:9999"

	if got := srv.edgeClientIP(req); got != "2001:db8::1" {
		t.Fatalf("edgeClientIP = %q, want 2001:db8::1", got)
	}
}

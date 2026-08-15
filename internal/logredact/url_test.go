package logredact

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeURLRemovesSecretBearingComponents(t *testing.T) {
	const raw = "https://operator:node-password@node.example:9443/transcode?access_token=query-secret#fragment-secret"
	if got, want := SanitizeURL(raw), "https://node.example:9443/transcode"; got != want {
		t.Fatalf("SanitizeURL() = %q, want %q", got, want)
	}
}

func TestSanitizeURLErrorRemovesRequestedURLSecretsAndPreservesCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := &url.Error{
		Op:  "Get",
		URL: "https://operator:node-password@node.example:9443/hw-capabilities?access_token=query-secret#fragment-secret",
		Err: &url.Error{
			Op:  "dial",
			URL: "tcp://operator:node-password@node.example:9443?access_token=nested-secret#nested-fragment",
			Err: cause,
		},
	}

	sanitized := SanitizeURLError(err)
	message := sanitized.Error()
	for _, secret := range []string{"operator", "node-password", "query-secret", "fragment-secret", "nested-secret", "nested-fragment"} {
		if strings.Contains(message, secret) {
			t.Fatalf("sanitized error contains %q: %q", secret, message)
		}
	}
	if !strings.Contains(message, "https://node.example:9443/hw-capabilities") ||
		!strings.Contains(message, "connection refused") {
		t.Fatalf("sanitized error lost useful diagnostics: %q", message)
	}
	if !errors.Is(sanitized, cause) {
		t.Fatalf("sanitized error does not preserve its cause: %v", sanitized)
	}
}

func TestSanitizeURLErrorFindsWrappedURLError(t *testing.T) {
	cause := errors.New("connection refused")
	err := errors.Join(errors.New("capability request failed"), &url.Error{
		Op:  "Get",
		URL: "https://operator:node-password@node.example/hw-capabilities?access_token=query-secret",
		Err: cause,
	})

	sanitized := SanitizeURLError(err)
	if message := sanitized.Error(); strings.Contains(message, "node-password") || strings.Contains(message, "query-secret") {
		t.Fatalf("sanitized wrapped error leaked credentials: %q", message)
	}
	if !errors.Is(sanitized, cause) {
		t.Fatalf("sanitized wrapped error does not preserve its cause: %v", sanitized)
	}
}

func TestSanitizeURLFailsClosedForMalformedInput(t *testing.T) {
	const secret = "node-password"
	got := SanitizeURL("https://operator:" + secret + "@node.example/\x00")
	if strings.Contains(got, secret) {
		t.Fatalf("malformed URL leaked credentials: %q", got)
	}
}

package logredact

import (
	"errors"
	"net/url"
)

const invalidURLPlaceholder = "[invalid URL]"

// SanitizeURL returns a diagnostic-safe URL with credential-bearing
// components removed. Malformed and non-hierarchical URLs fail closed rather
// than returning any part of the untrusted input.
func SanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return invalidURLPlaceholder
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

// SanitizeURLError clones an HTTP client's URL error while removing secrets
// from every nested requested URL. The underlying non-URL cause is preserved
// so errors.Is and useful transport diagnostics continue to work.
func SanitizeURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) || urlErr == nil {
		return err
	}
	clone := *urlErr
	clone.URL = SanitizeURL(urlErr.URL)
	clone.Err = SanitizeURLError(urlErr.Err)
	return &clone
}

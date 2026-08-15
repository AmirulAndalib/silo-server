package logredact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
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
// so errors.Is and useful transport diagnostics continue to work. When the
// URL error is nested inside another wrapper (fmt.Errorf or errors.Join), the
// surrounding chain and its diagnostic text are preserved as well.
func SanitizeURLError(err error) error {
	if err == nil {
		return nil
	}
	// errors.Join (and multi-%w fmt.Errorf) chains: sanitize every component
	// and rejoin, keeping each component's own context intact.
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		components := multi.Unwrap()
		if len(components) > 0 {
			sanitized := make([]error, len(components))
			for i, component := range components {
				sanitized[i] = SanitizeURLError(component)
			}
			return errors.Join(sanitized...)
		}
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) || urlErr == nil {
		return err
	}
	clone := *urlErr
	clone.URL = SanitizeURL(urlErr.URL)
	clone.Err = SanitizeURLError(urlErr.Err)
	// The identity check is deliberate: errors.As already located the nested
	// URL error, and this distinguishes a direct *url.Error (the clone is the
	// whole chain) from a wrapper that must keep its surrounding message.
	if _, ok := err.(*url.Error); ok { //nolint:errorlint
		// A direct *url.Error: the sanitized clone is the whole chain.
		return &clone
	}
	// A plain single-cause wrapper keeps its outer message with the sanitized
	// URL error re-attached as the cause.
	return fmt.Errorf("%s%w", strings.TrimSuffix(err.Error(), urlErr.Error()), &clone)
}

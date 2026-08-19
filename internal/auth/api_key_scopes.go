package auth

import (
	"fmt"
	"slices"
)

// API key scopes. A key with no scopes behaves as it always has: full access
// as the owning user. A key with scopes is an allowlist credential — the auth
// middleware only admits it to the routes its scopes name. Scopes narrow, they
// never grant: role checks (e.g. admin-only routes) still apply to the owning
// user afterwards.
const (
	// ScopeAdminUsers covers admin user lifecycle management: list, create,
	// read, update, and delete users, plus reading a user's profiles.
	ScopeAdminUsers = "admin:users"

	// ScopeAdminAccessGroupsRead covers read-only access-group discovery.
	ScopeAdminAccessGroupsRead = "admin:access-groups:read"
)

// ValidAPIKeyScopes returns every scope a key may carry.
func ValidAPIKeyScopes() []string {
	return []string{ScopeAdminUsers, ScopeAdminAccessGroupsRead}
}

// NormalizeAPIKeyScopes validates and deduplicates a requested scope list.
// nil or empty input is valid and means "unscoped" (full access).
func NormalizeAPIKeyScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{}, nil
	}
	valid := ValidAPIKeyScopes()
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if !slices.Contains(valid, s) {
			return nil, fmt.Errorf("unknown api key scope %q", s)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out, nil
}

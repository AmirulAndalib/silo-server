package access

import (
	"context"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
)

// GroupPolicyProvider loads the access-group restriction layer for a user.
type GroupPolicyProvider interface {
	GetPolicyForUser(ctx context.Context, userID int) (*GroupPolicy, error)
}

// GroupPolicy is the access group's policy layer. Every user-level policy
// field has a group counterpart here; the group value applies to each member
// whose own field is unset (inherits).
type GroupPolicy struct {
	ID                       int64
	LibraryIDs               []int // nil = unrestricted
	MaxPlaybackQuality       string
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               int // 0 = no cap
	MaxTranscodes            int
	AllowedPermissions       []string // nil = all assignable
	RequestsAllowed          bool
}

// EffectiveUserPolicy is the fully resolved policy for an account: every field
// carries a concrete value (user override when set, otherwise the group value,
// otherwise the permissive no-group default).
type EffectiveUserPolicy struct {
	LibraryIDs               []int // nil = unrestricted
	MaxPlaybackQuality       string
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               int
	MaxTranscodes            int
	Permissions              []string
	RequestsAllowed          bool
}

// PolicySource reports where each effective field came from.
type PolicySource struct {
	LibraryIDs               bool
	MaxPlaybackQuality       bool
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               bool
	MaxTranscodes            bool
	RequestsAllowed          bool
}

// NoGroupPolicy is the policy applied to an account with no access group
// (admins are ungrouped). It is fully permissive so that an unset field on
// such an account keeps today's unrestricted behavior.
func NoGroupPolicy() GroupPolicy {
	return GroupPolicy{
		LibraryIDs:               nil,
		MaxPlaybackQuality:       "",
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: true,
		TranscodeAllowed:         true,
		AudioTranscodeAllowed:    true,
		MaxStreams:               0,
		MaxTranscodes:            0,
		AllowedPermissions:       nil,
		RequestsAllowed:          true,
	}
}

// EffectivePolicyForUser loads a user's group policy and returns the resolved
// policy. Nil providers are treated as "no group".
func EffectivePolicyForUser(ctx context.Context, user *models.User, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil {
		return ApplyGroupPolicy(user, nil), nil
	}
	group, err := provider.GetPolicyForUser(ctx, user.ID)
	if err != nil {
		return EffectiveUserPolicy{}, err
	}
	return ApplyGroupPolicy(user, group), nil
}

// ApplyGroupPolicy resolves the user's account policy against the optional
// access group: each field takes the user's explicit override when set and
// the group's value otherwise. A nil group means the permissive
// NoGroupPolicy. Permissions are the one mask-style field: the group's
// allowed_permissions (when set) intersects the user's permissions.
func ApplyGroupPolicy(user *models.User, group *GroupPolicy) EffectiveUserPolicy {
	if user == nil {
		return EffectiveUserPolicy{RequestsAllowed: true}
	}
	base := NoGroupPolicy()
	if group != nil {
		base = *group
	}

	effective := EffectiveUserPolicy{
		LibraryIDs:               inheritLibraryIDs(user.LibraryIDs, base.LibraryIDs),
		MaxPlaybackQuality:       NormalizePlaybackQuality(inheritString(user.MaxPlaybackQuality, base.MaxPlaybackQuality)),
		DownloadAllowed:          inheritBool(user.DownloadAllowed, base.DownloadAllowed),
		DownloadTranscodeAllowed: inheritBool(user.DownloadTranscodeAllowed, base.DownloadTranscodeAllowed),
		TranscodeAllowed:         inheritBool(user.TranscodeAllowed, base.TranscodeAllowed),
		AudioTranscodeAllowed:    inheritBool(user.AudioTranscodeAllowed, base.AudioTranscodeAllowed),
		MaxStreams:               inheritInt(user.MaxStreams, base.MaxStreams),
		MaxTranscodes:            inheritInt(user.MaxTranscodes, base.MaxTranscodes),
		Permissions:              cloneStrings(user.Permissions),
		RequestsAllowed:          inheritBool(user.RequestsAllowed, base.RequestsAllowed),
	}
	if group != nil && group.AllowedPermissions != nil {
		effective.Permissions = intersectStrings(user.Permissions, group.AllowedPermissions)
	}
	return effective
}

// OverrideSources reports which effective fields are user overrides (true) as
// opposed to inherited from the group (false).
func OverrideSources(user *models.User) PolicySource {
	if user == nil {
		return PolicySource{}
	}
	return PolicySource{
		LibraryIDs:               user.LibraryIDs != nil,
		MaxPlaybackQuality:       user.MaxPlaybackQuality != nil,
		DownloadAllowed:          user.DownloadAllowed != nil,
		DownloadTranscodeAllowed: user.DownloadTranscodeAllowed != nil,
		TranscodeAllowed:         user.TranscodeAllowed != nil,
		AudioTranscodeAllowed:    user.AudioTranscodeAllowed != nil,
		MaxStreams:               user.MaxStreams != nil,
		MaxTranscodes:            user.MaxTranscodes != nil,
		RequestsAllowed:          user.RequestsAllowed != nil,
	}
}

func inheritLibraryIDs(userLibraryIDs, groupLibraryIDs []int) []int {
	if userLibraryIDs != nil {
		return sortedUniqueInts(userLibraryIDs)
	}
	if groupLibraryIDs != nil {
		return sortedUniqueInts(groupLibraryIDs)
	}
	return nil
}

func inheritInt(override *int, inherited int) int {
	if override != nil {
		if *override < 0 {
			return 0
		}
		return *override
	}
	return inherited
}

func inheritBool(override *bool, inherited bool) bool {
	if override != nil {
		return *override
	}
	return inherited
}

func inheritString(override *string, inherited string) string {
	if override != nil {
		return *override
	}
	return inherited
}

func intersectStrings(left, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return []string{}
	}
	allowed := make(map[string]struct{}, len(right))
	for _, raw := range right {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		allowed[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(left))
	out := make([]string, 0, len(left))
	for _, raw := range left {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

package audiobooks

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// ABSAccessResolver adapts silo's native profile/library access resolver to
// the ABS compatibility layer. ABS has already authenticated the account with
// a password or refresh token, so profile PIN verification is skipped here.
type ABSAccessResolver struct {
	resolver *access.Resolver
}

func NewABSAccessResolver(users access.UserRepository, stores userstore.UserStoreProvider) *ABSAccessResolver {
	if users == nil || stores == nil {
		return nil
	}
	return &ABSAccessResolver{resolver: access.NewResolver(users, stores, nil)}
}

func (r *ABSAccessResolver) ResolveABSAccess(ctx context.Context, userID, profileID string) (catalog.AccessFilter, error) {
	if r == nil || r.resolver == nil {
		return catalog.AccessFilter{}, nil
	}
	uid, err := strconv.Atoi(userID)
	if err != nil {
		return catalog.AccessFilter{}, fmt.Errorf("invalid ABS user id %q: %w", userID, err)
	}
	scope, err := r.resolver.Resolve(ctx, access.ResolveInput{
		UserID:              uid,
		ProfileID:           profileID,
		SkipPINVerification: true,
	})
	if err != nil {
		return catalog.AccessFilter{}, err
	}
	return catalog.AccessFilter{
		AllowedLibraryIDs:  scope.AllowedLibraryIDs,
		DisabledLibraryIDs: scope.DisabledLibraryIDs,
		MaxContentRating:   scope.MaxContentRating,
		MaxPlaybackQuality: scope.MaxPlaybackQuality,
		UserID:             scope.UserID,
		ProfileID:          scope.ProfileID,
	}, nil
}

// ABSDownloadPolicy reports whether an ABS-authenticated account may download
// (offline-save) media, backing the abs.DownloadPolicy dependency. It mirrors
// the native download service (internal/download/service.go), which loads the
// user and checks models.User.DownloadAllowed — keeping the privilege decision
// in one place rather than re-deriving it from the access filter.
type ABSDownloadPolicy struct {
	users access.UserRepository
}

// NewABSDownloadPolicy constructs the download-privilege resolver. Returns nil
// when no user repository is available; abs.Handler treats a nil policy as
// "allow", so callers must wire a real repository to enforce the gate.
func NewABSDownloadPolicy(users access.UserRepository) *ABSDownloadPolicy {
	if users == nil {
		return nil
	}
	return &ABSDownloadPolicy{users: users}
}

// DownloadAllowed loads the account by its numeric ABS user ID and reports its
// download privilege. A malformed ID or a load failure returns an error, which
// the caller fails closed on (denying the download).
func (p *ABSDownloadPolicy) DownloadAllowed(ctx context.Context, userID string) (bool, error) {
	if p == nil || p.users == nil {
		return true, nil
	}
	uid, err := strconv.Atoi(userID)
	if err != nil {
		return false, fmt.Errorf("invalid ABS user id %q: %w", userID, err)
	}
	user, err := p.users.GetByID(ctx, uid)
	if err != nil {
		return false, fmt.Errorf("loading user %d: %w", uid, err)
	}
	return user.DownloadAllowed, nil
}

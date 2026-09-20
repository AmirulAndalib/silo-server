package watchtogether

import (
	"context"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

type catalogWatchDetailLookup interface {
	GetWatchDetail(ctx context.Context, contentID string, filter catalog.AccessFilter) (*catalog.WatchDetail, error)
}

type CatalogSelectionResolver struct {
	details catalogWatchDetailLookup
}

func NewCatalogSelectionResolver(details catalogWatchDetailLookup) *CatalogSelectionResolver {
	return &CatalogSelectionResolver{details: details}
}

func (r *CatalogSelectionResolver) ResolveSelection(
	ctx context.Context,
	userID int,
	profileID string,
	input SelectItemInput,
) (*ResolvedSelection, error) {
	if r == nil || r.details == nil {
		return nil, ErrInvalidSelection
	}

	filter := catalog.AccessFilter{
		UserID:         userID,
		ProfileID:      profileID,
		SelectedFileID: 0,
	}
	if scope, ok := access.GetScope(ctx); ok {
		filter.AllowedLibraryIDs = scope.AllowedLibraryIDs
		filter.DisabledLibraryIDs = scope.DisabledLibraryIDs
		filter.MaxContentRating = scope.MaxContentRating
		filter.MaxPlaybackQuality = scope.MaxPlaybackQuality
	}
	if input.LibraryID != nil && *input.LibraryID > 0 {
		filter.PresentationLibraryID = input.LibraryID
	}
	if input.FileID != nil && *input.FileID > 0 {
		filter.SelectedFileID = *input.FileID
	}

	detail, err := r.details.GetWatchDetail(ctx, strings.TrimSpace(input.ContentID), filter)
	if err != nil {
		return nil, err
	}
	if detail == nil || (detail.Type != "movie" && detail.Type != "episode") || len(detail.Versions) == 0 {
		return nil, ErrInvalidSelection
	}

	resolved := &ResolvedSelection{
		ContentID: detail.ContentID,
		LibraryID: input.LibraryID,
	}
	if input.FileID != nil {
		for _, version := range detail.Versions {
			if version.FileID == *input.FileID {
				resolved.FileID = input.FileID
				return resolved, nil
			}
		}
		return nil, ErrInvalidSelection
	}

	resolved.FileID = new(preferredRoomVersion(detail.Versions).FileID)
	return resolved, nil
}

// A room has one source timeline. Use the catalogue's quality ordering within
// its preferred edition and presentation part, without a room-specific ceiling.
// Each viewer's playback planner applies device capabilities and server policy
// to that source, including 4K transcoding and tone mapping when available.
func preferredRoomVersion(versions []catalog.FileVersion) catalog.FileVersion {
	best := versions[0]
	for _, candidate := range versions[1:] {
		if candidate.EditionKey != best.EditionKey || candidate.PresentationKind != best.PresentationKind ||
			candidate.PresentationGroupKey != best.PresentationGroupKey || candidate.PresentationPartIndex != best.PresentationPartIndex {
			continue
		}
		quality := access.CompareQuality(candidate.Resolution, best.Resolution)
		if quality > 0 || (quality == 0 &&
			((candidate.HDR && !best.HDR) || (candidate.HDR == best.HDR && candidate.FileSize > best.FileSize))) {
			best = candidate
		}
	}
	return best
}

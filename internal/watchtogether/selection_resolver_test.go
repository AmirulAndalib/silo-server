package watchtogether

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

type roomWatchDetail struct {
	versions []catalog.FileVersion
}

func (d roomWatchDetail) GetWatchDetail(context.Context, string, catalog.AccessFilter) (*catalog.WatchDetail, error) {
	return &catalog.WatchDetail{ContentID: "movie-1", Type: "movie", Versions: d.versions}, nil
}

func TestRoomSelectionPrefersSharedSDRSource(t *testing.T) {
	versions := []catalog.FileVersion{
		{FileID: 1, Resolution: "2160p", HDR: true, EditionKey: "theatrical"},
		{FileID: 2, Resolution: "2160p", EditionKey: "theatrical"},
		{FileID: 3, Resolution: "720p", EditionKey: "theatrical"},
		{FileID: 4, Resolution: "1080p", FileSize: 100, EditionKey: "theatrical"},
		{FileID: 5, Resolution: "1080p", FileSize: 200, EditionKey: "theatrical"},
		{FileID: 6, Resolution: "1080p", FileSize: 300, EditionKey: "extended"},
	}
	resolver := NewCatalogSelectionResolver(roomWatchDetail{versions})
	selected, err := resolver.ResolveSelection(t.Context(), 7, "host", SelectItemInput{ContentID: "movie-1"})
	if err != nil || selected.FileID == nil || *selected.FileID != 5 {
		t.Fatalf("selection = %+v, error = %v; want largest 1080p SDR file in the selected edition", selected, err)
	}
	// Explicit API selections keep their existing meaning.
	selected, err = resolver.ResolveSelection(t.Context(), 7, "host", SelectItemInput{ContentID: "movie-1", FileID: new(1)})
	if err != nil || selected.FileID == nil || *selected.FileID != 1 {
		t.Fatalf("explicit selection = %+v, error = %v", selected, err)
	}
}

func TestRoomSelectionUsesAvailableSourceWhenNoStandardSDRExists(t *testing.T) {
	for _, versions := range [][]catalog.FileVersion{
		{{FileID: 1, Resolution: "2160p", HDR: true}, {FileID: 2, Resolution: "2160p"}},
		{{FileID: 2, Resolution: "2160p", HDR: true}},
	} {
		resolver := NewCatalogSelectionResolver(roomWatchDetail{versions})
		selected, err := resolver.ResolveSelection(t.Context(), 7, "host", SelectItemInput{ContentID: "movie-1"})
		if err != nil || selected.FileID == nil || *selected.FileID != 2 {
			t.Fatalf("selection = %+v, error = %v", selected, err)
		}
	}
}

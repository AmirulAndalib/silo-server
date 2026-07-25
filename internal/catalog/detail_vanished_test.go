package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

type recordingMissingMarker struct {
	marked []int
	err    error
}

func (m *recordingMissingMarker) MarkMissing(_ context.Context, id int, _ time.Time) error {
	m.marked = append(m.marked, id)
	return m.err
}

func fileAt(id int, path string) *models.MediaFile {
	return &models.MediaFile{ID: id, FilePath: path}
}

func TestDropVanishedFilesHidesRemovedVersions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	live := filepath.Join(dir, "movie.2160p.mkv")
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing live file: %v", err)
	}

	marker := &recordingMissingMarker{}
	svc := &DetailService{missingMarker: marker}

	// The upgrade case: an older release was replaced on disk but its row is
	// still live because no scan has run since.
	got := svc.dropVanishedFiles(context.Background(), []*models.MediaFile{
		fileAt(1, filepath.Join(dir, "movie.1080p.mkv")),
		fileAt(2, live),
	})

	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("kept %d files (first id %v), want only the file that exists on disk", len(got), fileIDsOf(got))
	}
	if len(marker.marked) != 1 || marker.marked[0] != 1 {
		t.Fatalf("marked = %v, want [1]", marker.marked)
	}
}

func TestDropVanishedFilesKeepsEverythingWhenNothingVanished(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.mkv"), filepath.Join(dir, "b.mkv")}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}

	marker := &recordingMissingMarker{}
	svc := &DetailService{missingMarker: marker}
	files := []*models.MediaFile{fileAt(1, paths[0]), fileAt(2, paths[1])}

	got := svc.dropVanishedFiles(context.Background(), files)
	if len(got) != 2 {
		t.Fatalf("kept %d files, want 2", len(got))
	}
	if len(marker.marked) != 0 {
		t.Fatalf("marked = %v, want none", marker.marked)
	}
}

// A failing MarkMissing must not resurrect the file in the response: the user
// still cannot play it, so listing it would reintroduce the exact dead-click
// this check exists to prevent.
func TestDropVanishedFilesHidesEvenWhenMarkingFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	marker := &recordingMissingMarker{err: errors.New("database down")}
	svc := &DetailService{missingMarker: marker}

	got := svc.dropVanishedFiles(context.Background(), []*models.MediaFile{
		fileAt(1, filepath.Join(dir, "gone.mkv")),
	})
	if len(got) != 0 {
		t.Fatalf("kept %d files, want 0", len(got))
	}
}

// The marker is optional wiring; the filter must still work without it.
func TestDropVanishedFilesWorksWithoutMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := &DetailService{}
	got := svc.dropVanishedFiles(context.Background(), []*models.MediaFile{
		fileAt(1, filepath.Join(dir, "gone.mkv")),
	})
	if len(got) != 0 {
		t.Fatalf("kept %d files, want 0", len(got))
	}
}

// Past the per-request limit files pass through unchecked rather than making
// an item with hundreds of files pay for hundreds of stats on every load.
func TestDropVanishedFilesRespectsCheckLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := make([]*models.MediaFile, 0, vanishedCheckLimit+5)
	for i := range vanishedCheckLimit + 5 {
		files = append(files, fileAt(i+1, filepath.Join(dir, "gone.mkv")))
	}

	marker := &recordingMissingMarker{}
	svc := &DetailService{missingMarker: marker}
	got := svc.dropVanishedFiles(context.Background(), files)

	if len(got) != 5 {
		t.Fatalf("kept %d files, want 5 (the ones past the check limit)", len(got))
	}
	if len(marker.marked) != vanishedCheckLimit {
		t.Fatalf("marked %d files, want %d", len(marker.marked), vanishedCheckLimit)
	}
}

func TestPreparePlaybackFilesDropsVanished(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	live := filepath.Join(dir, "live.mkv")
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing live file: %v", err)
	}

	svc := &DetailService{missingMarker: &recordingMissingMarker{}}
	got := svc.preparePlaybackFiles(context.Background(), []*models.MediaFile{
		fileAt(1, filepath.Join(dir, "gone.mkv")),
		fileAt(2, live),
	})

	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("preparePlaybackFiles returned %v, want only file 2", fileIDsOf(got))
	}
}

func fileIDsOf(files []*models.MediaFile) []int {
	ids := make([]int, 0, len(files))
	for _, f := range files {
		if f != nil {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

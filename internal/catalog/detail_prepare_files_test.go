package catalog

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// recordingProbeEnsurer records which half of the ensurer contract each
// prepare path asks for.
type recordingProbeEnsurer struct {
	fullCalls  []int
	probeCalls []int
}

func (e *recordingProbeEnsurer) Ensure(_ context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	e.fullCalls = append(e.fullCalls, file.ID)
	return file, nil
}

func (e *recordingProbeEnsurer) EnsureProbeOnly(_ context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	e.probeCalls = append(e.probeCalls, file.ID)
	return file, nil
}

// Browse detail must never trigger the H.264 copy-safety scan: the verdict is
// not serialized into those responses, so the scan is pure warm-up and its
// read is what made first-time browsing slow on remote storage.
func TestPrepareBrowseFilesSkipsCopySafety(t *testing.T) {
	ensurer := &recordingProbeEnsurer{}
	svc := &DetailService{probeEnsurer: ensurer}
	files := []*models.MediaFile{{ID: 1}, {ID: 2}}

	prepared := svc.prepareBrowseFiles(context.Background(), files)

	if len(prepared) != 2 {
		t.Fatalf("prepareBrowseFiles() returned %d files, want 2", len(prepared))
	}
	if len(ensurer.fullCalls) != 0 {
		t.Fatalf("browse path called Ensure for %v, want no copy-safety scans", ensurer.fullCalls)
	}
	if len(ensurer.probeCalls) != 2 {
		t.Fatalf("browse path called EnsureProbeOnly %d times, want 2 — probe repair must still run", len(ensurer.probeCalls))
	}
}

// The watch surfaces are where a play is being prepared, so they keep the
// full ensure and warm the verdict while the user looks at the Play button.
func TestPreparePlaybackFilesKeepsCopySafety(t *testing.T) {
	ensurer := &recordingProbeEnsurer{}
	svc := &DetailService{probeEnsurer: ensurer}
	files := []*models.MediaFile{{ID: 1}, {ID: 2}}

	prepared := svc.preparePlaybackFiles(context.Background(), files)

	if len(prepared) != 2 {
		t.Fatalf("preparePlaybackFiles() returned %d files, want 2", len(prepared))
	}
	if len(ensurer.fullCalls) != 2 {
		t.Fatalf("watch path called Ensure %d times, want 2", len(ensurer.fullCalls))
	}
	if len(ensurer.probeCalls) != 0 {
		t.Fatalf("watch path called EnsureProbeOnly for %v, want the full ensure", ensurer.probeCalls)
	}
}

func TestPrepareFilesWithoutEnsurerPassesFilesThrough(t *testing.T) {
	svc := &DetailService{}
	files := []*models.MediaFile{{ID: 1}, nil, {ID: 2}}

	if got := len(svc.prepareBrowseFiles(context.Background(), files)); got != 2 {
		t.Fatalf("prepareBrowseFiles() returned %d files, want 2 (nil entries dropped)", got)
	}
	if got := len(svc.preparePlaybackFiles(context.Background(), files)); got != 2 {
		t.Fatalf("preparePlaybackFiles() returned %d files, want 2 (nil entries dropped)", got)
	}
}

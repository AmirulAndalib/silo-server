package tonemap

import (
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// TestSourceRevisionRoundTripAndPathValidation verifies encoded revisions retain stable filesystem facts.
func TestSourceRevisionRoundTripAndPathValidation(t *testing.T) {
	path := t.TempDir() + "/source.mkv"
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	modified := normalizeRevisionTime(info.ModTime())
	probed := time.Now().UTC()
	file := &models.MediaFile{
		ID: 42, FileSize: info.Size(), FileModifiedAt: &modified, FileHash: "hash", ProbeUpdatedAt: &probed,
		VideoTracks: []models.VideoTrack{{
			Codec: "hevc", Profile: "Main 10", BitDepth: 10, PixelFormat: "yuv420p10le",
			DVProfile: 7, DVBLCompatID: 6, DVConfigPresent: true, DVBLCompatIDPresent: true, DVBLPresent: true, DVRPUPresent: true,
			ColorRange: "tv", ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
		}},
	}
	revision := RevisionForFile(file)
	if !revision.Stable() {
		t.Fatalf("revision = %#v, want stable", revision)
	}
	decoded, err := DecodeSourceRevision(revision.Encode())
	if err != nil || decoded != revision {
		t.Fatalf("round trip = %#v, %v; want %#v", decoded, err, revision)
	}
	if err := revision.ValidatePath(path); err != nil {
		t.Fatalf("ValidatePath(original) = %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := revision.ValidatePath(path); err == nil {
		t.Fatal("ValidatePath accepted replacement bytes")
	}
}

// TestRevisionForFileChangesWithDolbyVisionPresenceFacts verifies metadata presence affects source identity.
func TestRevisionForFileChangesWithDolbyVisionPresenceFacts(t *testing.T) {
	modified := time.Now().UTC().Truncate(time.Microsecond)
	file := &models.MediaFile{ID: 1, FileSize: 100, FileModifiedAt: &modified, VideoTracks: []models.VideoTrack{{
		Codec: "hevc", DVProfile: 7, DVBLCompatID: 6, DVConfigPresent: true, DVBLCompatIDPresent: true, DVBLPresent: true,
	}}}
	before := RevisionForFile(file)
	file.VideoTracks[0].DVRPUPresent = true
	after := RevisionForFile(file)
	if before.StreamSignature == after.StreamSignature || before.Fingerprint() == after.Fingerprint() {
		t.Fatal("Dolby Vision presence change did not invalidate the source revision")
	}
}

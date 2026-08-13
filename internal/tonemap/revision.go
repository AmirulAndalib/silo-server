package tonemap

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// SourceRevision freezes the catalog and filesystem facts used to validate a
// tone-map recipe. It is carried with the recipe so seeks, node restarts, and
// prepared downloads cannot silently apply a verdict to replacement bytes.
type SourceRevision struct {
	MediaFileID          int    `json:"media_file_id"`
	FileSize             int64  `json:"file_size"`
	FileModifiedUnixNano int64  `json:"file_modified_unix_nano,omitempty"`
	FileHash             string `json:"file_hash,omitempty"`
	ProbeUpdatedUnixNano int64  `json:"probe_updated_unix_nano,omitempty"`
	VideoStreamIndex     int    `json:"video_stream_index"`
	StreamSignature      string `json:"stream_signature"`
}

func RevisionForFile(file *models.MediaFile) SourceRevision {
	if file == nil {
		return SourceRevision{}
	}
	revision := SourceRevision{
		MediaFileID:      file.ID,
		FileSize:         file.FileSize,
		FileHash:         strings.TrimSpace(file.FileHash),
		VideoStreamIndex: 0,
	}
	if file.FileModifiedAt != nil {
		revision.FileModifiedUnixNano = normalizeRevisionTime(*file.FileModifiedAt).UnixNano()
	}
	if file.ProbeUpdatedAt != nil {
		revision.ProbeUpdatedUnixNano = file.ProbeUpdatedAt.UTC().UnixNano()
	}
	if len(file.VideoTracks) > 0 {
		track := file.VideoTracks[0]
		revision.StreamSignature = hashRevisionValue(fmt.Sprintf(
			"%s|%s|%d|%d|%dx%d|%s|%d|%d|%t|%t|%t|%t|%s|%s|%s|%s|%d|%s",
			track.Codec, track.Profile, track.Level, track.DVProfile, track.Width, track.Height,
			track.FrameRate, track.DVLevel, track.DVBLCompatID, track.DVConfigPresent,
			track.DVBLCompatIDPresent, track.DVBLPresent, track.DVRPUPresent,
			track.ColorRange, track.ColorPrimaries, track.ColorTransfer, track.ColorSpace,
			track.BitDepth, track.PixelFormat,
		))
	}
	return revision
}

func (r SourceRevision) Stable() bool {
	return r.MediaFileID > 0 && r.FileSize > 0 && r.FileModifiedUnixNano > 0 &&
		r.FileHash != "" && r.ProbeUpdatedUnixNano > 0 && r.StreamSignature != ""
}

func (r SourceRevision) IsZero() bool {
	return r.MediaFileID == 0 && r.FileSize == 0 && r.FileModifiedUnixNano == 0 &&
		r.FileHash == "" && r.ProbeUpdatedUnixNano == 0 && r.StreamSignature == ""
}

func (r SourceRevision) Fingerprint() string {
	data, _ := json.Marshal(r)
	return hashRevisionValue(string(data))
}

func (r SourceRevision) Encode() string {
	if r.IsZero() {
		return ""
	}
	data, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func DecodeSourceRevision(value string) (SourceRevision, error) {
	if strings.TrimSpace(value) == "" {
		return SourceRevision{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return SourceRevision{}, fmt.Errorf("decode source revision: %w", err)
	}
	var revision SourceRevision
	if err := json.Unmarshal(data, &revision); err != nil {
		return SourceRevision{}, fmt.Errorf("decode source revision: %w", err)
	}
	return revision, nil
}

// ValidatePath rejects a frozen recipe when the source bytes visible to the
// executor no longer match the scanner revision. FileHash is catalog-owned and
// participates in cache identity; size and mtime are rechecked on the node.
func (r SourceRevision) ValidatePath(path string) error {
	if r.IsZero() {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat tone-map source: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != r.FileSize {
		return fmt.Errorf("tone-map source revision changed")
	}
	if r.FileModifiedUnixNano > 0 && normalizeRevisionTime(info.ModTime()).UnixNano() != r.FileModifiedUnixNano {
		return fmt.Errorf("tone-map source revision changed")
	}
	return nil
}

func normalizeRevisionTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func hashRevisionValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

package scanner

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"golang.org/x/sync/singleflight"
)

// NeedsCriticalProbeRepair reports whether playback-critical probe metadata is
// missing and the file should be reprobed before making playback decisions.
func NeedsCriticalProbeRepair(file *models.MediaFile) bool {
	if file == nil {
		return true
	}
	// Ebook/comic files (epub, pdf, cbz, cbr — including manga chapters, which
	// are BaseType "ebook") are read directly by the reader and never go through
	// the transcode/playback probe pipeline. ffprobe yields nothing useful for
	// them, so requiring probe metadata re-ran ffprobe on every detail/watch
	// load and never converged.
	if file.BaseType == "ebook" {
		return false
	}
	if file.HasLegacyAttachedPictureVideo() {
		return true
	}
	if strings.TrimSpace(file.ProbeSource) == "" || file.ProbeUpdatedAt == nil {
		return true
	}
	if file.Duration <= 0 {
		return true
	}
	// Legacy probes could turn malformed multi-day container timestamps into a
	// few seconds by treating ffprobe's seconds as microseconds. Reprobe the
	// narrow, physically implausible shape produced by that conversion.
	if needsLegacyDurationRepair(file) {
		return true
	}
	if strings.TrimSpace(file.Container) == "" {
		return true
	}
	hasVideo := strings.TrimSpace(file.CodecVideo) != "" || len(file.VideoTracks) > 0
	hasAudio := strings.TrimSpace(file.CodecAudio) != "" || len(file.AudioTracks) > 0
	if !hasVideo && !hasAudio {
		return true
	}
	if hasAudio && (strings.TrimSpace(file.CodecAudio) == "" || len(file.AudioTracks) == 0) {
		return true
	}
	if !hasVideo && hasAudio && !file.IsAudioOnly() {
		return true
	}
	// Video metadata is playback-critical only for files that actually carry a
	// video stream. Audio-only files (audiobooks, music) legitimately probe to
	// zero video tracks and an empty video codec/resolution; treating that as
	// "needs repair" re-ran ffprobe on every playback decision (applyProbeData
	// only populates video fields under a "video" stream), so an audio-only
	// file would never satisfy the check. The inverse is also valid: synthetic
	// clips and some test assets carry video with no audio stream. Demand each
	// stream family's fields only when that family is present.
	if hasVideo {
		if strings.TrimSpace(file.CodecVideo) == "" || strings.TrimSpace(file.Resolution) == "" || len(file.VideoTracks) == 0 {
			return true
		}
		if videoTracksMissingColorRange(file.VideoTracks) {
			return true
		}
	}
	if file.Chapters == nil {
		return true
	}
	return false
}

func videoTracksMissingColorRange(tracks []models.VideoTrack) bool {
	for _, track := range tracks {
		if strings.TrimSpace(track.ColorRange) == "" {
			return true
		}
	}
	return false
}

// copySafetyWriter persists a multi-PPS verdict. *FileRepository satisfies it;
// the indirection keeps the ensurer testable without a database.
type copySafetyWriter interface {
	UpdateMultiplePPS(ctx context.Context, fileID int, multiplePPS bool, scanSize int64, scanMtime time.Time) error
}

// PlaybackProbeEnsurer repairs missing playback-critical probe metadata on
// demand by running a local ffprobe and persisting the result.
type PlaybackProbeEnsurer struct {
	fileRepo    *FileRepository
	ffprobePath string
	ffmpegPath  string
	timeout     time.Duration
	// copySafetyRepo persists multi-PPS verdicts. Normally the same
	// *FileRepository as fileRepo; tests substitute a double.
	copySafetyRepo copySafetyWriter
	// copySafety memoizes the multi-PPS bitstream scan per file for the life of
	// the process, in front of the persisted media_files verdict. Both layers
	// are validated against the file's current size and mtime.
	copySafety sync.Map // file ID -> copySafetyResult
	// copySafetyFlight collapses concurrent first scans of the same file so a
	// burst of playback/detail requests spawns one ffmpeg, not one each.
	copySafetyFlight singleflight.Group
}

type copySafetyResult struct {
	size  int64
	mtime *time.Time
	multi bool
}

// matches reports whether a memoized verdict still describes the given file.
func (r copySafetyResult) matches(file *models.MediaFile) bool {
	if r.size != file.FileSize {
		return false
	}
	if r.mtime == nil || file.FileModifiedAt == nil {
		// A verdict recorded without an mtime can only be trusted on size.
		return r.mtime == nil && file.FileModifiedAt == nil
	}
	return sameFileModifiedAt(r.mtime, *file.FileModifiedAt)
}

func NewPlaybackProbeEnsurer(fileRepo *FileRepository, ffprobePath, ffmpegPath string, timeout time.Duration) *PlaybackProbeEnsurer {
	e := &PlaybackProbeEnsurer{
		fileRepo:    fileRepo,
		ffprobePath: ffprobePath,
		ffmpegPath:  ffmpegPath,
		timeout:     timeout,
	}
	if fileRepo != nil {
		e.copySafetyRepo = fileRepo
	}
	return e
}

// Ensure repairs playback-critical probe metadata and resolves the H.264
// copy-safety verdict. Use it where a play is being prepared — the planner
// consumes the verdict to decide whether a video stream-copy is safe.
func (e *PlaybackProbeEnsurer) Ensure(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	current, err := e.ensureProbeRepair(ctx, file)
	if err != nil || current == nil || e == nil {
		return current, err
	}

	// Copy-safety analysis is independent of critical probe repair: an
	// already-probed file still needs its multi-PPS verdict before the planner
	// can decide whether a video stream-copy is safe.
	return e.ensureCopySafety(ctx, current)
}

// EnsureProbeOnly repairs playback-critical probe metadata and stops there.
//
// Browse surfaces (item, episode and extra detail pages) use this: they never
// consume the copy-safety verdict — VideoTrack.MultiplePPS is json:"-" and
// never reaches a client — so running the bitstream scan there was pure
// warm-up, and it is exactly what made first-time browsing slow on remote
// storage. The verdict is resolved when a play is actually being prepared.
func (e *PlaybackProbeEnsurer) EnsureProbeOnly(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	return e.ensureProbeRepair(ctx, file)
}

func (e *PlaybackProbeEnsurer) ensureProbeRepair(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	if file == nil || e == nil || e.fileRepo == nil {
		return file, nil
	}

	current := file
	if NeedsCriticalProbeRepair(file) && strings.TrimSpace(e.ffprobePath) != "" {
		timeout := e.timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		if reprobeMayScanPackets(file) && timeout < time.Minute {
			timeout = time.Minute
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		probe, err := ProbeFile(probeCtx, e.ffprobePath, file.FilePath)
		cancel()
		if err != nil || probe == nil {
			return file, err
		}
		updated := *file
		applyProbeData(&updated, probe, "local")
		repaired, err := e.fileRepo.Upsert(ctx, updated)
		if err != nil {
			return file, err
		}
		current = repaired
	}

	return current, nil
}

// ensureCopySafety resolves the multi-PPS copy-safety flag for H.264 files at
// playback start and stamps it on an in-memory copy of the file. It answers
// from the process cache first, then from the verdict persisted on the
// media_files row, and only then runs the bitstream scan — so a restart no
// longer re-reads the opening seconds of every browsed H.264 file.
func (e *PlaybackProbeEnsurer) ensureCopySafety(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
	if !needsCopySafetyProbe(file) || strings.TrimSpace(e.ffmpegPath) == "" {
		return file, nil
	}

	if cached, ok := e.copySafety.Load(file.ID); ok {
		if result, ok := cached.(copySafetyResult); ok && result.matches(file) {
			return fileWithMultiplePPS(file, result.multi), nil
		}
	}

	// A persisted verdict is self-validating: it is only honored while the
	// recorded size and mtime still describe the file, so a rewrite in place
	// falls through to a rescan without any writer having to clear it.
	if multi, ok := persistedCopySafetyVerdict(file); ok {
		e.storeCopySafety(file, multi)
		return fileWithMultiplePPS(file, multi), nil
	}

	multi, err := e.scanAndPersistCopySafety(ctx, file)
	if err != nil {
		// Unknown safety must not fail open to the video-copy path this probe is
		// intended to guard. Leave MultiplePPS unset and do not cache or persist
		// the result, so a later request retries the scan without misreporting
		// the cause.
		slog.WarnContext(ctx, "video copy-safety scan failed; disabling stream copy",
			"component", "scanner",
			"file_id", file.ID,
			"error", err,
		)
		return fileWithCopySafety(file, nil, true), nil
	}

	return fileWithMultiplePPS(file, multi), nil
}

// scanAndPersistCopySafety runs the multi-PPS bitstream scan, persists the
// verdict, and memoizes it. Concurrent callers for the same file share one
// scan; a failed database write is logged and the scan result is still used,
// since it is correct for this request and the next one will retry the write.
func (e *PlaybackProbeEnsurer) scanAndPersistCopySafety(ctx context.Context, file *models.MediaFile) (bool, error) {
	fileID := file.ID
	filePath := file.FilePath
	fileSize := file.FileSize
	fileModifiedAt := file.FileModifiedAt

	multi, err, _ := e.copySafetyFlight.Do(strconv.Itoa(fileID), func() (any, error) {
		timeout := e.timeout
		if timeout < 30*time.Second {
			timeout = 30 * time.Second
		}
		scanCtx, cancel := context.WithTimeout(ctx, timeout)
		multi, err := DetectMultiplePPSH264(scanCtx, e.ffmpegPath, filePath)
		cancel()
		if err != nil {
			return false, err
		}

		if e.copySafetyRepo != nil && fileModifiedAt != nil {
			if writeErr := e.copySafetyRepo.UpdateMultiplePPS(ctx, fileID, multi, fileSize, *fileModifiedAt); writeErr != nil {
				slog.WarnContext(ctx, "persisting video copy-safety verdict failed",
					"component", "scanner",
					"file_id", fileID,
					"error", writeErr,
				)
			}
		}
		e.storeCopySafety(file, multi)
		return multi, nil
	})
	if err != nil {
		return false, err
	}
	result, _ := multi.(bool)
	return result, nil
}

func (e *PlaybackProbeEnsurer) storeCopySafety(file *models.MediaFile, multi bool) {
	entry := copySafetyResult{size: file.FileSize, multi: multi}
	if file.FileModifiedAt != nil {
		mtime := *file.FileModifiedAt
		entry.mtime = &mtime
	}
	e.copySafety.Store(file.ID, entry)
}

// persistedCopySafetyVerdict returns the multi-PPS verdict stored on the
// media_files row, and whether it is still valid for the file as it stands. A
// verdict is valid only when it was computed from the same size and mtime the
// row now reports.
func persistedCopySafetyVerdict(file *models.MediaFile) (bool, bool) {
	if file == nil || file.MultiplePPS == nil || file.MultiplePPSScanSize == nil || file.MultiplePPSScanMtime == nil {
		return false, false
	}
	if *file.MultiplePPSScanSize != file.FileSize {
		return false, false
	}
	if file.FileModifiedAt == nil || !sameFileModifiedAt(file.MultiplePPSScanMtime, *file.FileModifiedAt) {
		return false, false
	}
	return *file.MultiplePPS, true
}

// fileWithMultiplePPS returns a shallow copy of file with the (runtime-only)
// MultiplePPS flag set on its first video track, without mutating the caller's
// file or its shared VideoTracks slice.
func fileWithMultiplePPS(file *models.MediaFile, multi bool) *models.MediaFile {
	value := multi
	return fileWithCopySafety(file, &value, multi)
}

func fileWithCopySafety(file *models.MediaFile, multiplePPS *bool, copyUnsafe bool) *models.MediaFile {
	updated := *file
	tracks := make([]models.VideoTrack, len(file.VideoTracks))
	copy(tracks, file.VideoTracks)
	tracks[0].MultiplePPS = multiplePPS
	tracks[0].VideoCopyUnsafe = copyUnsafe
	updated.VideoTracks = tracks
	return &updated
}

// needsCopySafetyProbe reports whether the file is an H.264 video whose
// multi-PPS copy-safety flag has not yet been computed.
func needsCopySafetyProbe(file *models.MediaFile) bool {
	if file == nil || len(file.VideoTracks) == 0 {
		return false
	}
	if file.VideoTracks[0].MultiplePPS != nil {
		return false
	}
	codec := strings.ToLower(strings.TrimSpace(file.VideoTracks[0].Codec))
	if codec == "" {
		codec = strings.ToLower(strings.TrimSpace(file.CodecVideo))
	}
	return codec == "h264" || codec == "avc" || codec == "avc1"
}

// reprobeMayScanPackets reports whether reprobing this file is likely to hit
// ProbeFile's packet-scan fallback, which demuxes the entire file and cannot
// finish inside the default metadata-probe timeout.
func reprobeMayScanPackets(file *models.MediaFile) bool {
	if file == nil || len(file.VideoTracks) == 0 {
		return false
	}
	return file.Duration <= 0 ||
		videoDurationImplausible(float64(file.Duration), file.FileSize, true)
}

// legacyProbeDurationFixTime marks the revision of the duration-validity rule
// in probe.go. Rows probed before it were judged by an older, weaker rule and
// are re-checked once under the current one. Rows probed after it are
// authoritative: their duration already passed the current rule, and
// re-flagging them would reprobe genuinely short clips on every playback
// decision forever.
//
// Bump this whenever videoDurationImplausible changes, or existing rows never
// re-converge on the improved rule. Last bumped when the implied-bitrate
// ceiling was added, which catches durations the absolute floor missed —
// a feature film probing as 61 seconds passed the old rule untouched.
var legacyProbeDurationFixTime = time.Date(2026, time.July, 26, 0, 0, 0, 0, time.UTC)

func needsLegacyDurationRepair(file *models.MediaFile) bool {
	if file == nil {
		return false
	}
	return legacyDurationRepairNeeded(file.Duration, file.FileSize, len(file.VideoTracks) > 0, file.ProbeUpdatedAt)
}

func legacyDurationRepairNeeded(duration int, sizeBytes int64, hasVideo bool, probeUpdatedAt *time.Time) bool {
	if !videoDurationImplausible(float64(duration), sizeBytes, hasVideo) {
		return false
	}
	return probeUpdatedAt == nil || probeUpdatedAt.Before(legacyProbeDurationFixTime)
}

package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// conflictingPPSAnnexB is a two-NAL Annex-B stream that redefines
// pic_parameter_set_id 0 with two different payloads — what
// DetectMultiplePPSH264 reports as multi-PPS. Written as printf octal escapes
// so a /bin/sh stub can emit it: 00 00 01 68 80 | 00 00 01 68 C0.
const conflictingPPSAnnexB = `\000\000\001\150\200\000\000\001\150\300`

// fakeFFmpeg writes a stub ffmpeg that appends one line to a log file per
// invocation and emits the given printf-escaped payload on stdout. It returns
// the stub's path and a func reporting how many times it ran.
func fakeFFmpeg(t *testing.T, stdoutPayload string, delay time.Duration) (string, func() int) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "invocations.log")
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	sleep := ""
	if delay > 0 {
		sleep = fmt.Sprintf("sleep %.2f\n", delay.Seconds())
	}
	script := fmt.Sprintf("#!/bin/sh\necho run >> %q\n%sprintf '%s'\n", logPath, sleep, stdoutPayload)
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return ffmpegPath, func() int {
		data, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				return 0
			}
			t.Fatalf("read fake ffmpeg log: %v", err)
		}
		runs := 0
		for _, b := range data {
			if b == '\n' {
				runs++
			}
		}
		return runs
	}
}

type recordedPPSWrite struct {
	fileID      int
	multiplePPS bool
	scanSize    int64
	scanMtime   time.Time
}

type fakeCopySafetyWriter struct {
	mu     sync.Mutex
	writes []recordedPPSWrite
	err    error
}

func (w *fakeCopySafetyWriter) UpdateMultiplePPS(_ context.Context, fileID int, multiplePPS bool, scanSize int64, scanMtime time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, recordedPPSWrite{
		fileID:      fileID,
		multiplePPS: multiplePPS,
		scanSize:    scanSize,
		scanMtime:   scanMtime,
	})
	return w.err
}

func (w *fakeCopySafetyWriter) recorded() []recordedPPSWrite {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]recordedPPSWrite(nil), w.writes...)
}

func copySafetyTestFile(mtime time.Time) *models.MediaFile {
	modified := mtime
	return &models.MediaFile{
		ID:             42,
		FilePath:       "/library/movie.mkv",
		FileSize:       1234,
		FileModifiedAt: &modified,
		CodecVideo:     "h264",
		VideoTracks:    []models.VideoTrack{{Codec: "h264"}},
	}
}

func TestEnsureCopySafetyUsesPersistedVerdictWithoutScanning(t *testing.T) {
	ffmpegPath, runs := fakeFFmpeg(t, "", 0)
	ensurer := &PlaybackProbeEnsurer{ffmpegPath: ffmpegPath}

	mtime := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	file := copySafetyTestFile(mtime)
	verdict := true
	scanSize := file.FileSize
	scanMtime := mtime
	file.MultiplePPS = &verdict
	file.MultiplePPSScanSize = &scanSize
	file.MultiplePPSScanMtime = &scanMtime

	got, err := ensurer.ensureCopySafety(context.Background(), file)
	if err != nil {
		t.Fatalf("ensureCopySafety() error = %v", err)
	}
	if runs() != 0 {
		t.Fatalf("ffmpeg ran %d times, want 0 for a valid persisted verdict", runs())
	}
	track := got.VideoTracks[0]
	if track.MultiplePPS == nil || !*track.MultiplePPS {
		t.Fatalf("MultiplePPS = %v, want true from the persisted verdict", track.MultiplePPS)
	}
	if !track.VideoCopyUnsafe {
		t.Fatal("VideoCopyUnsafe = false, want true for a multi-PPS file")
	}
	if _, ok := ensurer.copySafety.Load(file.ID); !ok {
		t.Fatal("persisted verdict was not promoted into the in-memory cache")
	}
}

func TestEnsureCopySafetyRescansStaleVerdict(t *testing.T) {
	mtime := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)

	tests := []struct {
		name      string
		scanSize  int64
		scanMtime time.Time
	}{
		{name: "size mismatch", scanSize: 999, scanMtime: mtime},
		{name: "mtime mismatch", scanSize: 1234, scanMtime: mtime.Add(time.Hour)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ffmpegPath, runs := fakeFFmpeg(t, conflictingPPSAnnexB, 0)
			ensurer := &PlaybackProbeEnsurer{ffmpegPath: ffmpegPath}

			file := copySafetyTestFile(mtime)
			verdict := false
			scanSize := tc.scanSize
			scanMtime := tc.scanMtime
			file.MultiplePPS = &verdict
			file.MultiplePPSScanSize = &scanSize
			file.MultiplePPSScanMtime = &scanMtime

			got, err := ensurer.ensureCopySafety(context.Background(), file)
			if err != nil {
				t.Fatalf("ensureCopySafety() error = %v", err)
			}
			if runs() != 1 {
				t.Fatalf("ffmpeg ran %d times, want 1 for a stale persisted verdict", runs())
			}
			track := got.VideoTracks[0]
			if track.MultiplePPS == nil || !*track.MultiplePPS {
				t.Fatalf("MultiplePPS = %v, want the rescanned true, not the stale false", track.MultiplePPS)
			}
		})
	}
}

func TestEnsureCopySafetyPersistsScanResult(t *testing.T) {
	ffmpegPath, runs := fakeFFmpeg(t, conflictingPPSAnnexB, 0)
	writer := &fakeCopySafetyWriter{}
	ensurer := &PlaybackProbeEnsurer{ffmpegPath: ffmpegPath, copySafetyRepo: writer}

	mtime := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	file := copySafetyTestFile(mtime)

	got, err := ensurer.ensureCopySafety(context.Background(), file)
	if err != nil {
		t.Fatalf("ensureCopySafety() error = %v", err)
	}
	if runs() != 1 {
		t.Fatalf("ffmpeg ran %d times, want 1", runs())
	}
	writes := writer.recorded()
	if len(writes) != 1 {
		t.Fatalf("UpdateMultiplePPS called %d times, want 1", len(writes))
	}
	want := recordedPPSWrite{fileID: 42, multiplePPS: true, scanSize: 1234, scanMtime: mtime}
	if writes[0] != want {
		t.Fatalf("UpdateMultiplePPS(%+v), want %+v", writes[0], want)
	}
	if track := got.VideoTracks[0]; track.MultiplePPS == nil || !*track.MultiplePPS {
		t.Fatalf("MultiplePPS = %v, want true", track.MultiplePPS)
	}
}

func TestEnsureCopySafetyScanSurvivesPersistFailure(t *testing.T) {
	ffmpegPath, _ := fakeFFmpeg(t, conflictingPPSAnnexB, 0)
	writer := &fakeCopySafetyWriter{err: fmt.Errorf("database unavailable")}
	ensurer := &PlaybackProbeEnsurer{ffmpegPath: ffmpegPath, copySafetyRepo: writer}

	file := copySafetyTestFile(time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC))

	got, err := ensurer.ensureCopySafety(context.Background(), file)
	if err != nil {
		t.Fatalf("ensureCopySafety() error = %v, want the scan result to be used anyway", err)
	}
	if track := got.VideoTracks[0]; track.MultiplePPS == nil || !*track.MultiplePPS {
		t.Fatalf("MultiplePPS = %v, want the scan result despite the failed write", track.MultiplePPS)
	}
}

func TestEnsureCopySafetyWithoutRepoDoesNotPanic(t *testing.T) {
	ffmpegPath, runs := fakeFFmpeg(t, conflictingPPSAnnexB, 0)
	ensurer := &PlaybackProbeEnsurer{ffmpegPath: ffmpegPath}

	file := copySafetyTestFile(time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC))

	if _, err := ensurer.ensureCopySafety(context.Background(), file); err != nil {
		t.Fatalf("ensureCopySafety() error = %v", err)
	}
	if runs() != 1 {
		t.Fatalf("ffmpeg ran %d times, want 1", runs())
	}
}

// A failed scan must stay fail-closed and stateless: nothing is written, so a
// transient error never becomes sticky state on the row and the next request
// retries cleanly.
func TestEnsureCopySafetyFailureRecordsNothing(t *testing.T) {
	writer := &fakeCopySafetyWriter{}
	ensurer := &PlaybackProbeEnsurer{
		ffmpegPath:     filepath.Join(t.TempDir(), "missing-ffmpeg"),
		copySafetyRepo: writer,
	}

	file := copySafetyTestFile(time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC))

	got, err := ensurer.ensureCopySafety(context.Background(), file)
	if err != nil {
		t.Fatalf("ensureCopySafety() error = %v", err)
	}
	if !got.VideoTracks[0].VideoCopyUnsafe {
		t.Fatal("VideoCopyUnsafe = false, want true after an inconclusive scan")
	}
	if writes := writer.recorded(); len(writes) != 0 {
		t.Fatalf("failed scan recorded %d verdicts, want 0", len(writes))
	}
}

// Browse surfaces call EnsureProbeOnly. The copy-safety verdict never reaches
// a client from those responses, so scanning there was pure warm-up — and the
// read it costs is what made first-time browsing slow on remote storage.
func TestEnsureProbeOnlySkipsCopySafetyScan(t *testing.T) {
	ffmpegPath, runs := fakeFFmpeg(t, conflictingPPSAnnexB, 0)
	writer := &fakeCopySafetyWriter{}
	ensurer := &PlaybackProbeEnsurer{ffmpegPath: ffmpegPath, copySafetyRepo: writer}

	file := copySafetyTestFile(time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC))

	got, err := ensurer.EnsureProbeOnly(context.Background(), file)
	if err != nil {
		t.Fatalf("EnsureProbeOnly() error = %v", err)
	}
	if runs() != 0 {
		t.Fatalf("ffmpeg ran %d times for a browse-detail load, want 0", runs())
	}
	if got.VideoTracks[0].MultiplePPS != nil {
		t.Fatal("EnsureProbeOnly() resolved the copy-safety verdict")
	}
	if got.VideoTracks[0].VideoCopyUnsafe {
		t.Fatal("EnsureProbeOnly() marked the file copy-unsafe")
	}
	if writes := writer.recorded(); len(writes) != 0 {
		t.Fatalf("EnsureProbeOnly() persisted %d verdicts, want 0", len(writes))
	}

	// The same ensurer still scans when a play is being prepared.
	if _, err := ensurer.Ensure(context.Background(), file); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if runs() != 1 {
		t.Fatalf("ffmpeg ran %d times for a playback load, want 1", runs())
	}
}

func TestEnsureCopySafetyConcurrentCallsScanOnce(t *testing.T) {
	ffmpegPath, runs := fakeFFmpeg(t, conflictingPPSAnnexB, 200*time.Millisecond)
	writer := &fakeCopySafetyWriter{}
	ensurer := &PlaybackProbeEnsurer{ffmpegPath: ffmpegPath, copySafetyRepo: writer}

	mtime := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)

	const callers = 8
	var wg sync.WaitGroup
	results := make([]*models.MediaFile, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = ensurer.ensureCopySafety(context.Background(), copySafetyTestFile(mtime))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("ensureCopySafety() caller %d error = %v", i, err)
		}
		if track := results[i].VideoTracks[0]; track.MultiplePPS == nil || !*track.MultiplePPS {
			t.Fatalf("caller %d MultiplePPS = %v, want true", i, track.MultiplePPS)
		}
	}
	if got := runs(); got != 1 {
		t.Fatalf("ffmpeg ran %d times for %d concurrent callers, want 1", got, callers)
	}
	if writes := writer.recorded(); len(writes) != 1 {
		t.Fatalf("UpdateMultiplePPS called %d times, want 1", len(writes))
	}
}

func TestPersistedCopySafetyVerdict(t *testing.T) {
	mtime := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	base := func() *models.MediaFile {
		file := copySafetyTestFile(mtime)
		verdict := true
		scanSize := file.FileSize
		scanMtime := mtime
		file.MultiplePPS = &verdict
		file.MultiplePPSScanSize = &scanSize
		file.MultiplePPSScanMtime = &scanMtime
		return file
	}

	tests := []struct {
		name      string
		mutate    func(*models.MediaFile)
		wantMulti bool
		wantOK    bool
	}{
		{name: "valid", mutate: func(*models.MediaFile) {}, wantMulti: true, wantOK: true},
		{name: "never scanned", mutate: func(f *models.MediaFile) { f.MultiplePPS = nil }},
		{name: "missing scan size", mutate: func(f *models.MediaFile) { f.MultiplePPSScanSize = nil }},
		{name: "missing scan mtime", mutate: func(f *models.MediaFile) { f.MultiplePPSScanMtime = nil }},
		{name: "size drifted", mutate: func(f *models.MediaFile) { f.FileSize = 4321 }},
		{
			name: "mtime drifted",
			mutate: func(f *models.MediaFile) {
				later := mtime.Add(time.Second)
				f.FileModifiedAt = &later
			},
		},
		{name: "file mtime unknown", mutate: func(f *models.MediaFile) { f.FileModifiedAt = nil }},
		{
			name: "sub-microsecond mtime drift is absorbed",
			mutate: func(f *models.MediaFile) {
				jittered := mtime.Add(17 * time.Nanosecond).Local()
				f.FileModifiedAt = &jittered
			},
			wantMulti: true,
			wantOK:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file := base()
			tc.mutate(file)
			multi, ok := persistedCopySafetyVerdict(file)
			if ok != tc.wantOK || multi != tc.wantMulti {
				t.Fatalf("persistedCopySafetyVerdict() = (%v, %v), want (%v, %v)", multi, ok, tc.wantMulti, tc.wantOK)
			}
		})
	}
}

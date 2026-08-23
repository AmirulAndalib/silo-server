package playback

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// copySafetyScanTimeout bounds one asynchronous multi-PPS scan. The scan reads
// the opening seconds of the file, which on cold remote storage is dominated by
// the read rather than the demux; a minute is generous for that and still
// guarantees the goroutine cannot outlive the session it was started for by
// much. It is deliberately not the request timeout: nothing about this work
// belongs to the HTTP request that triggered it.
const copySafetyScanTimeout = time.Minute

// copySafetyScanConcurrency caps how many copy-safety scans run at once across
// the whole replica. Per-file dedupe collapses the repeat requests for one
// popular file, but nothing bounds the number of *distinct* unknown files a
// burst of watch-page loads can name, and each one costs an ffmpeg process
// reading the opening seconds off remote storage. Excess races block their
// goroutine on the semaphore rather than being dropped: the scan is cheap to
// defer and must still happen, goroutines are cheap, ffmpeg is not.
const copySafetyScanConcurrency = 4

// CopySafetyScanner is the scanner-side half of the race: it decides whether a
// file still needs the H.264 multi-PPS scan and runs it. *scanner.PlaybackProbeEnsurer
// implements it.
type CopySafetyScanner interface {
	NeedsCopySafetyScan(file *models.MediaFile) bool
	ScanCopySafety(ctx context.Context, file *models.MediaFile) (bool, error)
}

// CopySafetyFileLoader loads the media file a race was requested for.
type CopySafetyFileLoader interface {
	GetByID(ctx context.Context, id int) (*models.MediaFile, error)
}

// CopySafetyRace runs the H.264 copy-safety scan out of band, after a plan that
// stream-copies video has already been handed to a client (or a watch page has
// been rendered for a file no session exists for yet).
//
// This is the asynchronous half of optimistic remuxing: an unknown verdict no
// longer blocks a play, so it has to be resolved behind the play instead. A
// multi-PPS verdict is both persisted — every later plan for the file excludes
// the copy route deterministically — and pushed at whatever sessions are live
// on a copy route by the time it lands.
type CopySafetyRace struct {
	scanner  CopySafetyScanner
	files    CopySafetyFileLoader
	notifier *CopySafetyNotifier
	// inFlight keeps one goroutine per file. The scanner's own singleflight
	// already collapses concurrent scans, but every start, replan and watch-page
	// load for a popular file would otherwise stack a goroutine that does
	// nothing but wait on it.
	inFlight sync.Map // file ID -> struct{}
	// slots is the replica-wide scan semaphore. A goroutine holds its per-file
	// inFlight entry while it waits for a slot, so queueing never lets a second
	// goroutine for the same file through.
	slots   chan struct{}
	timeout time.Duration
}

// NewCopySafetyRace returns a racer, or nil when it has nothing to scan with. A
// nil racer is safe to call.
func NewCopySafetyRace(scanner CopySafetyScanner, files CopySafetyFileLoader, notifier *CopySafetyNotifier) *CopySafetyRace {
	if scanner == nil || files == nil {
		return nil
	}
	return &CopySafetyRace{
		scanner:  scanner,
		files:    files,
		notifier: notifier,
		slots:    make(chan struct{}, copySafetyScanConcurrency),
		timeout:  copySafetyScanTimeout,
	}
}

// RaceScan resolves the copy-safety verdict for fileID in the background. It
// returns immediately, and does nothing when the verdict is already known, the
// file is not H.264, or a scan for the file is already running.
//
// The caller's request context is deliberately not used: the scan outlives the
// request that noticed the verdict was missing, and the whole point is that no
// client ever waits on it.
func (r *CopySafetyRace) RaceScan(fileID int) {
	if r == nil || fileID <= 0 {
		return
	}
	if _, running := r.inFlight.LoadOrStore(fileID, struct{}{}); running {
		return
	}
	go func() {
		defer r.inFlight.Delete(fileID)
		// The slot is taken before the scan's own deadline starts: time spent
		// queueing behind other files is not time the scan was given to run.
		r.acquireSlot()
		defer r.releaseSlot()
		r.scan(fileID)
	}()
}

func (r *CopySafetyRace) acquireSlot() {
	if r.slots == nil {
		return
	}
	r.slots <- struct{}{}
}

func (r *CopySafetyRace) releaseSlot() {
	if r.slots == nil {
		return
	}
	<-r.slots
}

func (r *CopySafetyRace) scan(fileID int) {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = copySafetyScanTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	file, err := r.files.GetByID(ctx, fileID)
	if err != nil || file == nil {
		if err != nil {
			slog.WarnContext(ctx, "video copy-safety race could not load the file",
				"component", "playback", "file_id", fileID, "error", err)
		}
		return
	}
	if !r.scanner.NeedsCopySafetyScan(file) {
		// Nothing left to scan, but that is not the same as nothing to do. The
		// verdict may have been reached by another replica between this race
		// being requested and the file being loaded: that replica notified its
		// own sessions and has no way to reach ours, so a persisted unsafe
		// verdict has to be applied locally even though no scan runs here. A
		// known-safe verdict is silent, as always.
		//
		// This closes the window for sessions this replica raced against another
		// replica's write. It is not distributed invalidation: a verdict that
		// lands after every replica has stopped racing still reaches only the
		// replica that reached it. Pushing invalidations across replicas —
		// Redis-backed, like the other cross-replica playback signals — is
		// follow-up work.
		if multi, known := file.PersistedVideoCopyVerdict(); known && multi {
			slog.InfoContext(ctx, "applying a persisted copy-unsafe verdict reached elsewhere",
				"component", "playback", "file_id", fileID)
			r.notifier.VideoCopyUnsafe(ctx, fileID)
		}
		return
	}

	multi, err := r.scanner.ScanCopySafety(ctx, file)
	if err != nil {
		// An inconclusive scan is not evidence of anything. Nothing is persisted
		// (the scanner only records a verdict it reached), live sessions keep
		// playing the route they were given, and a later request retries. The
		// old behavior — treating a failed scan as copy-unsafe — belonged to a
		// world where the scan ran before playback started.
		slog.WarnContext(ctx, "video copy-safety scan failed",
			"component", "playback", "file_id", fileID, "error", err)
		return
	}
	if !multi {
		return
	}

	slog.InfoContext(ctx, "video copy-safety scan disqualified the stream-copy route",
		"component", "playback", "file_id", fileID)
	r.notifier.VideoCopyUnsafe(ctx, fileID)
}

// RaceScanForPlan starts a race only when the plan actually stream-copies video
// for this file. Callers on the playback start and replan paths use it so the
// route test lives in one place.
func (r *CopySafetyRace) RaceScanForPlan(fileID int, plan *PlanV3) {
	if r == nil || plan == nil {
		return
	}
	switch plan.Delivery {
	case DeliveryRemuxHLSV3, DeliveryRemuxProgressiveV3:
	default:
		return
	}
	r.RaceScan(fileID)
}

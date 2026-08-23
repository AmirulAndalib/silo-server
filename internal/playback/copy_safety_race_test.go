package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

type fakeCopySafetyScanner struct {
	mu        sync.Mutex
	needs     bool
	multi     bool
	err       error
	scans     int
	active    int
	maxActive int
	release   chan struct{}
	scanning  chan struct{}
}

func (s *fakeCopySafetyScanner) NeedsCopySafetyScan(*models.MediaFile) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needs
}

func (s *fakeCopySafetyScanner) ScanCopySafety(context.Context, *models.MediaFile) (bool, error) {
	s.mu.Lock()
	s.scans++
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()
	if s.scanning != nil {
		s.scanning <- struct{}{}
	}
	if s.release != nil {
		<-s.release
	}
	return s.multi, s.err
}

func (s *fakeCopySafetyScanner) scanCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scans
}

// peakConcurrency is the largest number of scans that were ever running at the
// same time.
func (s *fakeCopySafetyScanner) peakConcurrency() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxActive
}

type fakeFileLoader struct {
	mu    sync.Mutex
	file  *models.MediaFile
	err   error
	loads int
}

func (l *fakeFileLoader) GetByID(context.Context, int) (*models.MediaFile, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loads++
	return l.file, l.err
}

// raceFixture wires a racer whose notifier reports into a fake control, so a
// multi-PPS verdict is observable as a session stop.
func raceFixture(t *testing.T, scanner *fakeCopySafetyScanner) (*CopySafetyRace, *SessionManager, *fakeCopySafetyControl) {
	t.Helper()
	return raceFixtureForFile(t, scanner, &models.MediaFile{
		ID:          100,
		CodecVideo:  "h264",
		VideoTracks: []models.VideoTrack{{Codec: "h264"}},
	})
}

// raceFixtureForFile is raceFixture over a specific media file, for the cases
// that care about what the row carries — a persisted verdict, above all.
func raceFixtureForFile(t *testing.T, scanner *fakeCopySafetyScanner, file *models.MediaFile) (*CopySafetyRace, *SessionManager, *fakeCopySafetyControl) {
	t.Helper()
	sessions := NewSessionManager(0, 0)
	hub := NewRealtimeHub()
	tracker := NewCommandTracker()
	t.Cleanup(tracker.Close)
	control := &fakeCopySafetyControl{}
	notifier := NewCopySafetyNotifier(sessions, nil, NewCommandDispatcher(sessions, hub, tracker), control)
	// These tests are about the race, not about waiting out the window a
	// just-started session gets before it can be stopped.
	notifier.settle = 0
	loader := &fakeFileLoader{file: file}
	return NewCopySafetyRace(scanner, loader, notifier), sessions, control
}

func waitForStop(t *testing.T, control *fakeCopySafetyControl, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		stopped := control.stoppedSessions()
		if len(stopped) == 1 && stopped[0] == sessionID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stopped = %v, want the copy-routed session %q", stopped, sessionID)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCopySafetyRaceNotifiesOnMultiPPS(t *testing.T) {
	scanner := &fakeCopySafetyScanner{needs: true, multi: true}
	race, sessions, control := raceFixture(t, scanner)
	session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	race.RaceScan(100)

	waitForStop(t, control, session.ID)
}

// A copy-safe verdict is the common case and must be silent: the plan the
// client is already running stays valid.
func TestCopySafetyRaceKeepsSessionsWhenCopySafe(t *testing.T) {
	scanner := &fakeCopySafetyScanner{needs: true, multi: false}
	race, sessions, control := raceFixture(t, scanner)
	if _, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	race.RaceScan(100)

	waitForScans(t, scanner, 1)
	time.Sleep(20 * time.Millisecond)
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want no session touched by a copy-safe verdict", stopped)
	}
}

// An inconclusive scan proves nothing, so sessions keep playing. Failing closed
// here would kill a live playback over a transient ffmpeg or storage error.
func TestCopySafetyRaceLeavesSessionsAloneOnScanError(t *testing.T) {
	scanner := &fakeCopySafetyScanner{needs: true, err: errors.New("ffmpeg exploded")}
	race, sessions, control := raceFixture(t, scanner)
	if _, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	race.RaceScan(100)

	waitForScans(t, scanner, 1)
	time.Sleep(20 * time.Millisecond)
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want live sessions untouched after an inconclusive scan", stopped)
	}
}

func TestCopySafetyRaceSkipsFilesWithNothingToScan(t *testing.T) {
	scanner := &fakeCopySafetyScanner{needs: false, multi: true}
	race, _, control := raceFixture(t, scanner)

	race.RaceScan(100)

	time.Sleep(20 * time.Millisecond)
	if got := scanner.scanCount(); got != 0 {
		t.Fatalf("scans = %d, want 0 when the verdict is already known", got)
	}
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want none", stopped)
	}
}

// Every start, replan and watch-page load for a popular file asks for the same
// race; only one goroutine may be in flight for it.
func TestCopySafetyRaceDedupesInFlightScans(t *testing.T) {
	scanner := &fakeCopySafetyScanner{
		needs:    true,
		release:  make(chan struct{}),
		scanning: make(chan struct{}, 1),
	}
	race, _, _ := raceFixture(t, scanner)

	race.RaceScan(100)
	<-scanner.scanning
	for i := 0; i < 5; i++ {
		race.RaceScan(100)
	}
	close(scanner.release)

	time.Sleep(50 * time.Millisecond)
	if got := scanner.scanCount(); got != 1 {
		t.Fatalf("scans = %d, want 1 while a scan for the file is already running", got)
	}
}

// fileWithPersistedVerdict is a media file row whose multi-PPS verdict is
// already recorded and still describes the file, as it would be on a replica
// that loads the row after another replica wrote it.
func fileWithPersistedVerdict(multi bool) *models.MediaFile {
	size := int64(4096)
	return &models.MediaFile{
		ID:                  100,
		CodecVideo:          "h264",
		VideoTracks:         []models.VideoTrack{{Codec: "h264", MultiplePPS: &multi}},
		FileSize:            size,
		MultiplePPS:         &multi,
		MultiplePPSScanSize: &size,
	}
}

// Another replica can reach the verdict first: it persists it, notifies its own
// sessions, and cannot reach ours. Loading a row that already carries an unsafe
// verdict therefore has to withdraw this replica's copy-routed sessions even
// though there is nothing left to scan.
func TestCopySafetyRaceAppliesPersistedUnsafeVerdict(t *testing.T) {
	scanner := &fakeCopySafetyScanner{needs: false}
	race, sessions, control := raceFixtureForFile(t, scanner, fileWithPersistedVerdict(true))
	session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	race.RaceScan(100)

	waitForStop(t, control, session.ID)
	if got := scanner.scanCount(); got != 0 {
		t.Fatalf("scans = %d, want 0 for a verdict that was already reached", got)
	}
}

// A persisted copy-safe verdict is the common resolved state and must stay
// silent: it is exactly the evidence that the route the session is on is fine.
func TestCopySafetyRaceIgnoresPersistedSafeVerdict(t *testing.T) {
	scanner := &fakeCopySafetyScanner{needs: false}
	race, sessions, control := raceFixtureForFile(t, scanner, fileWithPersistedVerdict(false))
	if _, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	race.RaceScan(100)

	time.Sleep(20 * time.Millisecond)
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want no session touched by a persisted copy-safe verdict", stopped)
	}
}

// Per-file dedupe bounds the races for one popular file; nothing bounds the
// number of distinct files a burst of watch-page loads names, and each scan
// costs an ffmpeg process against remote storage.
func TestCopySafetyRaceCapsConcurrentScans(t *testing.T) {
	scanner := &fakeCopySafetyScanner{needs: true, release: make(chan struct{})}
	race, _, _ := raceFixture(t, scanner)

	const files = 12
	for i := 0; i < files; i++ {
		race.RaceScan(200 + i)
	}

	waitForScans(t, scanner, copySafetyScanConcurrency)
	time.Sleep(50 * time.Millisecond)
	if got := scanner.scanCount(); got != copySafetyScanConcurrency {
		t.Fatalf("scans = %d, want %d in flight while every slot is held", got, copySafetyScanConcurrency)
	}

	// Every queued race still runs; the cap defers work rather than dropping it.
	close(scanner.release)
	waitForScans(t, scanner, files)
	if got := scanner.peakConcurrency(); got > copySafetyScanConcurrency {
		t.Fatalf("peak concurrency = %d, want at most %d", got, copySafetyScanConcurrency)
	}
}

// The route test lives with the racer so start and replan cannot disagree: only
// a plan that actually stream-copies video is worth chasing.
func TestCopySafetyRaceForPlanOnlyChasesCopyRoutes(t *testing.T) {
	tests := []struct {
		delivery  DeliveryV3
		wantScans int
	}{
		{delivery: DeliveryRemuxHLSV3, wantScans: 1},
		{delivery: DeliveryRemuxProgressiveV3, wantScans: 1},
		{delivery: DeliveryTranscodeHLSV3, wantScans: 0},
		{delivery: DeliveryOriginalHTTPV3, wantScans: 0},
	}

	for _, tc := range tests {
		t.Run(string(tc.delivery), func(t *testing.T) {
			scanner := &fakeCopySafetyScanner{needs: true}
			race, _, _ := raceFixture(t, scanner)

			race.RaceScanForPlan(100, &PlanV3{PlanID: "plan-1", Delivery: tc.delivery})

			if tc.wantScans > 0 {
				waitForScans(t, scanner, tc.wantScans)
				return
			}
			time.Sleep(20 * time.Millisecond)
			if got := scanner.scanCount(); got != 0 {
				t.Fatalf("scans = %d, want 0 for delivery %q", got, tc.delivery)
			}
		})
	}
}

func TestCopySafetyRaceNilIsSafe(t *testing.T) {
	var race *CopySafetyRace
	race.RaceScan(100)
	race.RaceScanForPlan(100, &PlanV3{Delivery: DeliveryRemuxHLSV3})
	if NewCopySafetyRace(nil, nil, nil) != nil {
		t.Fatal("NewCopySafetyRace() with no dependencies = non-nil, want nil")
	}
}

func waitForScans(t *testing.T, scanner *fakeCopySafetyScanner, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if scanner.scanCount() >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("scans = %d, want %d", scanner.scanCount(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

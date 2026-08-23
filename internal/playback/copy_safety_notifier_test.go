package playback

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type fakeCopySafetyControl struct {
	mu         sync.Mutex
	remembered []playbackCommandNote
	forgotten  []string
	stopped    []string
}

type playbackCommandNote struct {
	commandID string
	sessionID string
	name      CommandName
}

func (c *fakeCopySafetyControl) RememberRealtimeCommand(commandID, sessionID string, name CommandName) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remembered = append(c.remembered, playbackCommandNote{commandID: commandID, sessionID: sessionID, name: name})
}

func (c *fakeCopySafetyControl) ForgetRealtimeCommand(commandID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgotten = append(c.forgotten, commandID)
}

func (c *fakeCopySafetyControl) StopSession(_ context.Context, sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = append(c.stopped, sessionID)
	return nil
}

func (c *fakeCopySafetyControl) stoppedSessions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.stopped...)
}

func (c *fakeCopySafetyControl) trackedCommands() []playbackCommandNote {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]playbackCommandNote(nil), c.remembered...)
}

type fakeAttemptLookup struct {
	records map[string]*AttemptRecordV3
}

func (l *fakeAttemptLookup) GetAttempt(_ context.Context, sessionID string) (*AttemptRecordV3, error) {
	record, ok := l.records[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return record, nil
}

func remuxAttempt(sessionID, planID string, features ...string) *AttemptRecordV3 {
	return &AttemptRecordV3{
		SessionID:     sessionID,
		CurrentPlanID: planID,
		CurrentPlan: PlanV3{
			PlanID:   planID,
			Delivery: DeliveryRemuxHLSV3,
		},
		NormalizedRequest: StartRequestV3{ClientFeatures: features},
	}
}

func newCopySafetyFixture(t *testing.T) (*SessionManager, *RealtimeHub, *CommandTracker, *fakeCopySafetyControl) {
	t.Helper()
	sessions := NewSessionManager(0, 0)
	hub := NewRealtimeHub()
	tracker := NewCommandTracker()
	t.Cleanup(tracker.Close)
	return sessions, hub, tracker, &fakeCopySafetyControl{}
}

// A negotiated, connected client is told to replan; the session keeps playing
// until it reports back.
func TestCopySafetyNotifierPushesPlanInvalidated(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := sessions.SetRealtimeConnection(session.ID, true); err != nil {
		t.Fatalf("SetRealtimeConnection: %v", err)
	}
	conn := &dispatchTestConn{}
	reg := hub.Register(session.ID, conn)
	defer hub.Unregister(reg)

	attempts := &fakeAttemptLookup{records: map[string]*AttemptRecordV3{
		session.ID: remuxAttempt(session.ID, "plan-abc", FeaturePlanInvalidatedV3),
	}}
	notifier := NewCopySafetyNotifier(sessions, attempts, NewCommandDispatcher(sessions, hub, tracker), control)

	notifier.VideoCopyUnsafe(context.Background(), 100)

	if len(conn.messages) != 1 {
		t.Fatalf("messages = %d, want 1 plan_invalidated command", len(conn.messages))
	}
	command, ok := conn.messages[0].(CommandEnvelope)
	if !ok {
		t.Fatalf("message type = %T, want CommandEnvelope", conn.messages[0])
	}
	if command.Type != RealtimeMessageTypeCommand || command.Name != CommandPlanInvalidated {
		t.Fatalf("command = %#v, want a plan_invalidated command", command)
	}
	if command.DeadlineMS != int(CopySafetyInvalidationDeadline/time.Millisecond) {
		t.Fatalf("deadline_ms = %d, want %d", command.DeadlineMS, int(CopySafetyInvalidationDeadline/time.Millisecond))
	}
	var payload PlanInvalidatedPayload
	if err := json.Unmarshal(command.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload): %v", err)
	}
	if payload.Reason != PlanInvalidatedVideoCopyUnsafe || payload.PlanID != "plan-abc" {
		t.Fatalf("payload = %#v, want the invalidated plan and the copy-unsafe reason", payload)
	}
	tracked := control.trackedCommands()
	if len(tracked) != 1 || tracked[0].sessionID != session.ID || tracked[0].name != CommandPlanInvalidated {
		t.Fatalf("tracked commands = %#v, want one plan_invalidated for the session", tracked)
	}
	if tracked[0].commandID != command.CommandID {
		t.Fatalf("tracked command id = %q, want the dispatched %q", tracked[0].commandID, command.CommandID)
	}
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped %v, want the session left running until the client reports back", stopped)
	}
}

// Everything that cannot be told to replan is stopped instead: that fallback is
// the whole backwards-compatibility story for clients shipped before the token.
func TestCopySafetyNotifierStopsSessionsItCannotTell(t *testing.T) {
	tests := []struct {
		name      string
		features  []string
		connected bool
		attempt   bool
	}{
		{name: "feature not negotiated", features: []string{FeatureSeekReanchorV3}, connected: true, attempt: true},
		{name: "no realtime connection", features: []string{FeaturePlanInvalidatedV3}, connected: false, attempt: true},
		{name: "no durable attempt", connected: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sessions, hub, tracker, control := newCopySafetyFixture(t)
			session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
			if err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			if tc.connected {
				if err := sessions.SetRealtimeConnection(session.ID, true); err != nil {
					t.Fatalf("SetRealtimeConnection: %v", err)
				}
			}
			conn := &dispatchTestConn{}
			reg := hub.Register(session.ID, conn)
			defer hub.Unregister(reg)

			attempts := &fakeAttemptLookup{records: map[string]*AttemptRecordV3{}}
			if tc.attempt {
				attempts.records[session.ID] = remuxAttempt(session.ID, "plan-abc", tc.features...)
			}
			notifier := NewCopySafetyNotifier(sessions, attempts, NewCommandDispatcher(sessions, hub, tracker), control)
			notifier.settle = 0

			notifier.VideoCopyUnsafe(context.Background(), 100)

			if len(conn.messages) != 0 {
				t.Fatalf("messages = %d, want no command pushed", len(conn.messages))
			}
			if stopped := control.stoppedSessions(); len(stopped) != 1 || stopped[0] != session.ID {
				t.Fatalf("stopped = %v, want the session terminated", stopped)
			}
		})
	}
}

// Only sessions actually stream-copying video for the file are touched: a
// transcode is already safe, and a direct play never repackages into fMP4.
func TestCopySafetyNotifierIgnoresNonCopyRoutes(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	transcoding, err := sessions.StartSession(1, "profile-1", 100, PlayTranscode, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	direct, err := sessions.StartSession(2, "profile-2", 100, PlayDirect, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	otherFile, err := sessions.StartSession(3, "profile-3", 101, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	transcodeAttempt := remuxAttempt(transcoding.ID, "plan-transcode", FeaturePlanInvalidatedV3)
	transcodeAttempt.CurrentPlan.Delivery = DeliveryTranscodeHLSV3
	directAttempt := remuxAttempt(direct.ID, "plan-direct", FeaturePlanInvalidatedV3)
	directAttempt.CurrentPlan.Delivery = DeliveryOriginalHTTPV3

	attempts := &fakeAttemptLookup{records: map[string]*AttemptRecordV3{
		transcoding.ID: transcodeAttempt,
		direct.ID:      directAttempt,
		otherFile.ID:   remuxAttempt(otherFile.ID, "plan-other", FeaturePlanInvalidatedV3),
	}}
	notifier := NewCopySafetyNotifier(sessions, attempts, NewCommandDispatcher(sessions, hub, tracker), control)

	notifier.VideoCopyUnsafe(context.Background(), 100)

	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want no session touched", stopped)
	}
	if tracked := control.trackedCommands(); len(tracked) != 0 {
		t.Fatalf("tracked = %#v, want no command pushed", tracked)
	}
}

// A session with no v3 attempt at all (a reconstructed session, or one whose
// attempt expired) is classified by its live play method, and stopped because
// it can be told nothing.
func TestCopySafetyNotifierUsesSessionPlayMethodWithoutAnAttempt(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	remuxing, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	transcoding, err := sessions.StartSession(2, "profile-2", 100, PlayTranscode, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	notifier := NewCopySafetyNotifier(sessions, nil, NewCommandDispatcher(sessions, hub, tracker), control)
	notifier.settle = 0
	notifier.VideoCopyUnsafe(context.Background(), 100)

	stopped := control.stoppedSessions()
	if len(stopped) != 1 || stopped[0] != remuxing.ID {
		t.Fatalf("stopped = %v, want only the remuxing session %q (not %q)", stopped, remuxing.ID, transcoding.ID)
	}
}

// The Jellyfin-protocol surface picks direct stream from the device profile
// alone, so a compat client reconnects onto the identical remux. Stopping it
// would be a mid-stream interruption that buys nothing.
func TestCopySafetyNotifierLeavesJellyfinCompatSessionsAlone(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	ctx := WithClientInfo(context.Background(), ClientInfo{Name: "Infuse", IsCompat: true})
	compat, err := sessions.StartSessionWithFilesContext(ctx, 1, "profile-1", 100, 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSessionWithFilesContext: %v", err)
	}
	if !compat.IsJellyfinCompat {
		t.Fatal("fixture session is not a compat session; the test would prove nothing")
	}
	native, err := sessions.StartSession(2, "profile-2", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	notifier := NewCopySafetyNotifier(sessions, nil, NewCommandDispatcher(sessions, hub, tracker), control)
	notifier.settle = 0
	notifier.VideoCopyUnsafe(context.Background(), 100)

	stopped := control.stoppedSessions()
	if len(stopped) != 1 || stopped[0] != native.ID {
		t.Fatalf("stopped = %v, want only the native session %q (not the compat session %q)", stopped, native.ID, compat.ID)
	}
}

// A session is registered with the manager before its start handler has written
// the attempt record and long before the client has opened a realtime channel.
// A verdict landing in that window must not stop a session that is still being
// built: the notifier waits out the settle window and looks again.
func TestCopySafetyNotifierWaitsOutTheSettleWindowBeforeStopping(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	conn := &dispatchTestConn{}
	reg := hub.Register(session.ID, conn)
	defer hub.Unregister(reg)

	attempts := &fakeAttemptLookup{records: map[string]*AttemptRecordV3{}}
	notifier := NewCopySafetyNotifier(sessions, attempts, NewCommandDispatcher(sessions, hub, tracker), control)
	notifier.settle = 60 * time.Millisecond

	notifier.VideoCopyUnsafe(context.Background(), 100)

	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want the still-establishing session left alone", stopped)
	}

	// The start finishes inside the window: the attempt lands and the client
	// connects, so the second look finds a session it can tell instead of kill.
	attempts.records[session.ID] = remuxAttempt(session.ID, "plan-abc", FeaturePlanInvalidatedV3)
	if err := sessions.SetRealtimeConnection(session.ID, true); err != nil {
		t.Fatalf("SetRealtimeConnection: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(conn.sent()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no command pushed after the settle window; stopped = %v", control.stoppedSessions())
		}
		time.Sleep(time.Millisecond)
	}
	sent := conn.sent()
	if command, ok := sent[0].(CommandEnvelope); !ok || command.Name != CommandPlanInvalidated {
		t.Fatalf("message = %#v, want a plan_invalidated command", sent[0])
	}
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want the settled session told rather than terminated", stopped)
	}
}

// A session that never becomes reachable is still stopped, just one settle
// window later than an already-settled one.
func TestCopySafetyNotifierStopsAfterTheSettleWindow(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	notifier := NewCopySafetyNotifier(sessions, nil, NewCommandDispatcher(sessions, hub, tracker), control)
	notifier.settle = 20 * time.Millisecond

	notifier.VideoCopyUnsafe(context.Background(), 100)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if stopped := control.stoppedSessions(); len(stopped) == 1 && stopped[0] == session.ID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stopped = %v, want the unreachable session terminated after the settle window", control.stoppedSessions())
		}
		time.Sleep(time.Millisecond)
	}
}

// The session lookup also matches the requested file, which after a 4K guard or
// a version replan is not the file being streamed. A verdict about that
// inactive edition must not touch a session remuxing a different, copy-safe one.
func TestCopySafetyNotifierIgnoresSessionsServingAnotherFile(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	session, err := sessions.StartSessionWithFiles(1, "profile-1", 11, 10, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSessionWithFiles: %v", err)
	}
	if err := sessions.SetRealtimeConnection(session.ID, true); err != nil {
		t.Fatalf("SetRealtimeConnection: %v", err)
	}
	conn := &dispatchTestConn{}
	reg := hub.Register(session.ID, conn)
	defer hub.Unregister(reg)

	record := remuxAttempt(session.ID, "plan-abc", FeaturePlanInvalidatedV3)
	record.RequestedMediaFileID = 10
	record.EffectiveMediaFileID = 11
	attempts := &fakeAttemptLookup{records: map[string]*AttemptRecordV3{session.ID: record}}
	notifier := NewCopySafetyNotifier(sessions, attempts, NewCommandDispatcher(sessions, hub, tracker), control)
	notifier.settle = 0

	// File 10 is the requested edition the session abandoned; file 11 is playing.
	notifier.VideoCopyUnsafe(context.Background(), 10)

	if len(conn.messages) != 0 {
		t.Fatalf("messages = %d, want nothing pushed for an edition the session is not streaming", len(conn.messages))
	}
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want the session on the other edition left alone", stopped)
	}

	notifier.VideoCopyUnsafe(context.Background(), 11)
	if len(conn.messages) != 1 {
		t.Fatalf("messages = %d, want the effective file's verdict to reach the session", len(conn.messages))
	}
}

// The command carries a deadline: a client that acks and then goes quiet loses
// its session, so it cannot keep decoding a stream the server withdrew.
func TestCopySafetyNotifierDeadlineStopsUnansweredSession(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := sessions.SetRealtimeConnection(session.ID, true); err != nil {
		t.Fatalf("SetRealtimeConnection: %v", err)
	}
	conn := &dispatchTestConn{}
	reg := hub.Register(session.ID, conn)
	defer hub.Unregister(reg)

	attempts := &fakeAttemptLookup{records: map[string]*AttemptRecordV3{
		session.ID: remuxAttempt(session.ID, "plan-abc", FeaturePlanInvalidatedV3),
	}}
	notifier := NewCopySafetyNotifier(sessions, attempts, NewCommandDispatcher(sessions, hub, tracker), control)
	notifier.deadline = 10 * time.Millisecond

	notifier.VideoCopyUnsafe(context.Background(), 100)

	if len(conn.messages) != 1 {
		t.Fatalf("messages = %d, want the command delivered first", len(conn.messages))
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if stopped := control.stoppedSessions(); len(stopped) == 1 && stopped[0] == session.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stopped = %v, want the unanswered session terminated by the deadline", control.stoppedSessions())
		}
		time.Sleep(time.Millisecond)
	}
	// An acked-then-completed command cancels the deadline; here nothing
	// answered, so the tracker must have released the command as well.
	if _, tracked := tracker.Status("unused"); tracked {
		t.Fatal("tracker reported an unknown command as tracked")
	}
}

// An acked command whose result completes leaves the session alone: the client
// replanned itself.
func TestCopySafetyNotifierCompletedResultKeepsSession(t *testing.T) {
	sessions, hub, tracker, control := newCopySafetyFixture(t)
	session, err := sessions.StartSession(1, "profile-1", 100, PlayRemux, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := sessions.SetRealtimeConnection(session.ID, true); err != nil {
		t.Fatalf("SetRealtimeConnection: %v", err)
	}
	conn := &dispatchTestConn{}
	reg := hub.Register(session.ID, conn)
	defer hub.Unregister(reg)

	attempts := &fakeAttemptLookup{records: map[string]*AttemptRecordV3{
		session.ID: remuxAttempt(session.ID, "plan-abc", FeaturePlanInvalidatedV3),
	}}
	notifier := NewCopySafetyNotifier(sessions, attempts, NewCommandDispatcher(sessions, hub, tracker), control)
	notifier.deadline = 50 * time.Millisecond

	notifier.VideoCopyUnsafe(context.Background(), 100)
	command := conn.messages[0].(CommandEnvelope)
	tracker.Ack(command.CommandID)
	tracker.Result(command.CommandID)

	time.Sleep(150 * time.Millisecond)
	if stopped := control.stoppedSessions(); len(stopped) != 0 {
		t.Fatalf("stopped = %v, want the session kept after a completed replan", stopped)
	}
}

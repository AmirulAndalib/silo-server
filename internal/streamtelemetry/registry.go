package streamtelemetry

import (
	"context"
	"hash/maphash"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/httpstream"
)

const shardCount = 32

var now = time.Now

type sessionShard struct {
	sync.RWMutex
	sessions map[string]*logicalSession
}

type Registry struct {
	cfg    Config
	store  SnapshotStore
	logger *slog.Logger
	seed   maphash.Seed
	shards [shardCount]sessionShard

	transfersMu sync.RWMutex
	transfers   map[string]*transfer

	sessionReservations      atomic.Int64
	transferReservations     atomic.Int64
	observationReservations  atomic.Int64
	droppedObservations      atomic.Int64
	droppedBytes             atomic.Int64
	unattributedObservations atomic.Int64
	unattributedBytes        atomic.Int64
	truncated                atomic.Bool
	lastWarnUnixNano         atomic.Int64
	lastPublishWarnUnixNano  atomic.Int64
	sequence                 atomic.Uint64
	startOnce                sync.Once
	stopOnce                 sync.Once
	stop                     chan struct{}
	done                     chan struct{}
	started                  atomic.Bool
	leaveMu                  sync.Mutex
	left                     bool
}

func NewRegistry(cfg Config, store SnapshotStore, logger *slog.Logger) *Registry {
	if cfg.PublisherID == "" {
		cfg.PublisherID = uuid.NewString()
	}
	if cfg.PublisherEpoch == 0 {
		cfg.PublisherEpoch = now().UnixNano()
	}
	if store == nil {
		store = NewLocalStore()
	}
	if logger == nil {
		logger = slog.Default()
	}
	r := &Registry{cfg: cfg, store: store, logger: logger, seed: maphash.MakeSeed(), transfers: make(map[string]*transfer), stop: make(chan struct{}), done: make(chan struct{})}
	for i := range r.shards {
		r.shards[i].sessions = make(map[string]*logicalSession)
	}
	return r
}

func (r *Registry) Enabled() bool { return r != nil && r.cfg.Enabled }

func (r *Registry) Store() SnapshotStore {
	if r == nil {
		return nil
	}
	return r.store
}

func reserve(counter *atomic.Int64, max int64) bool {
	for {
		current := counter.Load()
		if current >= max {
			return false
		}
		if counter.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (r *Registry) begin(route MediaRoute, capture CaptureSet) *Observation {
	obs := newObservation(r, route, capture)
	if reserve(&r.observationReservations, r.cfg.MaxObservations) {
		obs.reserved = true
	} else {
		obs.countingOnly = true
		r.drop("observation capacity exhausted")
	}
	return obs
}

func (r *Registry) attach(obs *Observation, attachment Attachment) {
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.released || obs.countingOnly {
		return
	}
	observedAt := obs.Capture.ReceivedAt
	if observedAt.IsZero() {
		observedAt = now()
	}
	if obs.attachment != nil {
		if obs.target.session != nil {
			s := obs.target.session
			s.mu.Lock()
			s.recordConflicts(attachment, observedAt, r.cfg.MaxIdentityConflictsPerSession)
			s.mu.Unlock()
		}
		return
	}
	if attachment.TokenIssuedAt.IsZero() && !obs.Capture.TokenIssuedAt.IsZero() {
		attachment.TokenIssuedAt = obs.Capture.TokenIssuedAt
		attachment.TokenIssuedAtSource = obs.Capture.TokenIssuedFrom
	}
	if attachment.TokenIssuedAtSource == "" {
		attachment.TokenIssuedAtSource = TokenIssuedAtSourceNone
	}
	if obs.route.Class == ClassTransfer {
		if !reserve(&r.transferReservations, r.cfg.MaxTransfers) {
			obs.countingOnly = true
			r.drop("transfer capacity exhausted")
			return
		}
		t := &transfer{id: obs.id, subject: attachment.Subject, profileID: attachment.ProfileID,
			mediaFileID: attachment.MediaFileID, openObservations: 1, requestCount: 1,
			route: obs.route, capture: obs.Capture, observation: obs,
			outcomes: make(map[httpstream.StreamOutcome]int64)}
		r.transfersMu.Lock()
		r.transfers[t.id] = t
		r.transfersMu.Unlock()
		obs.attachment = &attachment
		obs.target.transfer = t
		return
	}
	if attachment.SessionID == "" {
		obs.countingOnly = true
		r.drop("attachment has no canonical session id")
		return
	}
	shard := r.shard(attachment.SessionID)
	shard.Lock()
	s := shard.sessions[attachment.SessionID]
	if s == nil {
		if !reserve(&r.sessionReservations, r.cfg.MaxSessions) {
			shard.Unlock()
			obs.countingOnly = true
			r.drop("session capacity exhausted")
			return
		}
		s = newLogicalSession(attachment, r.cfg, observedAt)
		shard.sessions[attachment.SessionID] = s
	}
	s.mu.Lock()
	if len(s.observations) >= r.cfg.MaxObservationsPerSession {
		s.mu.Unlock()
		shard.Unlock()
		obs.countingOnly = true
		r.drop("per-session observation capacity exhausted")
		return
	}
	s.recordConflicts(attachment, observedAt, r.cfg.MaxIdentityConflictsPerSession)
	key := routeID(obs.Capture.Method, obs.Capture.Pattern)
	activity := s.routes[key]
	if activity == nil {
		if len(s.routes) >= r.cfg.MaxRoutesPerSession {
			s.routesOverflowed = true
		} else {
			activity = &routeActivity{Method: obs.Capture.Method, Pattern: obs.Capture.Pattern,
				Role: obs.route.Role, Class: obs.route.Class, CapRelevant: obs.route.CapRelevant}
			s.routes[key] = activity
		}
	}
	s.observations[obs.id] = obs
	s.openObservations++
	s.requestCount++
	if activity != nil {
		activity.Open++
		activity.Requests++
	}
	if obs.Capture.ViewerIP != "" {
		s.viewerIPs.add(obs.Capture.ViewerIP)
	}
	if obs.Capture.DeviceID != "" {
		s.deviceIDs.add(obs.Capture.DeviceID)
	}
	if obs.Capture.Client != (ClientVariant{}) {
		s.clientVariants.add(obs.Capture.Client)
	}
	if obs.Capture.UserAgent != "" {
		s.userAgents.add(obs.Capture.UserAgent)
	}
	s.tokenIssuedSources[attachment.TokenIssuedAtSource]++
	if !attachment.TokenIssuedAt.IsZero() {
		s.tokenIssuedAts.add(attachment.TokenIssuedAt.UnixNano())
	}
	s.mu.Unlock()
	shard.Unlock()
	obs.attachment = &attachment
	obs.target.session = s
}

func (r *Registry) release(obs *Observation, outcome httpstream.StreamOutcome) {
	obs.mu.Lock()
	if obs.released {
		obs.mu.Unlock()
		return
	}
	obs.released = true
	target := obs.target
	attached := obs.attachment != nil
	countingOnly := obs.countingOnly
	obs.mu.Unlock()
	bytes := obs.BytesAccepted()
	if countingOnly {
		r.droppedBytes.Add(bytes)
	} else if !attached {
		r.unattributedObservations.Add(1)
		r.unattributedBytes.Add(bytes)
	} else if target.transfer != nil {
		t := target.transfer
		t.mu.Lock()
		t.bytesFolded += bytes
		t.openObservations--
		t.lastObservationEnd = now()
		t.outcomes[outcome]++
		t.observation = nil
		t.mu.Unlock()
	} else if target.session != nil {
		s := target.session
		s.mu.Lock()
		delete(s.observations, obs.id)
		s.bytesFolded += bytes
		s.openObservations--
		s.lastObservationEnd = now()
		s.outcomes[outcome]++
		if activity := s.routes[routeID(obs.Capture.Method, obs.Capture.Pattern)]; activity != nil {
			activity.Open--
			activity.BytesFolded += bytes
			activity.LastObservationEnd = s.lastObservationEnd
		}
		s.mu.Unlock()
	}
	if obs.reserved {
		r.observationReservations.Add(-1)
	}
}

func (r *Registry) drop(reason string) {
	r.truncated.Store(true)
	r.droppedObservations.Add(1)
	r.warnRateLimited(reason, &r.lastWarnUnixNano)
}

func (r *Registry) warnRateLimited(message string, stamp *atomic.Int64, attrs ...any) {
	n := now().UnixNano()
	for {
		previous := stamp.Load()
		if previous != 0 && n-previous < int64(time.Minute) {
			return
		}
		if stamp.CompareAndSwap(previous, n) {
			attrs = append([]any{"component", "stream_telemetry"}, attrs...)
			attrs = append([]any{"reason", message}, attrs...)
			r.logger.Warn("stream telemetry warning", attrs...)
			return
		}
	}
}

func (r *Registry) shard(id string) *sessionShard {
	var h maphash.Hash
	h.SetSeed(r.seed)
	h.WriteString(id)
	return &r.shards[h.Sum64()%shardCount]
}

func (r *Registry) SetRealtimeConnection(sessionID string, connected bool) {
	if r == nil || !r.cfg.Enabled || sessionID == "" {
		return
	}
	shard := r.shard(sessionID)
	shard.RLock()
	s := shard.sessions[sessionID]
	if s != nil {
		s.mu.Lock()
		s.realtimeAlive = connected
		s.mu.Unlock()
	}
	shard.RUnlock()
}

func (r *Registry) Start(ctx context.Context) {
	if r == nil || !r.cfg.Enabled {
		return
	}
	r.startOnce.Do(func() {
		r.started.Store(true)
		go func() {
			defer close(r.done)
			ticker := time.NewTicker(r.cfg.SweepInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-r.stop:
					return
				case sweepStart := <-ticker.C:
					snapshot := r.sweep(sweepStart)
					snapshot.Sequence = r.sequence.Add(1)
					if err := r.store.Publish(ctx, snapshot); err != nil {
						r.warnRateLimited("failed to publish stream telemetry snapshot", &r.lastPublishWarnUnixNano, "error", err)
					}
				}
			}
		}()
	})
}

func (r *Registry) Stop(ctx context.Context) error {
	if r == nil || !r.cfg.Enabled || !r.started.Load() {
		return nil
	}
	r.stopOnce.Do(func() { close(r.stop) })
	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	global, ok := r.store.(GlobalSnapshotStore)
	if !ok {
		return nil
	}
	r.leaveMu.Lock()
	defer r.leaveMu.Unlock()
	if r.left {
		return nil
	}
	if err := global.Leave(ctx); err != nil {
		return err
	}
	r.left = true
	return nil
}

func (r *Registry) Sweep() Snapshot { return r.sweep(now()) }

func (r *Registry) sweep(sweepStart time.Time) Snapshot {
	for i := range r.shards {
		shard := &r.shards[i]
		shard.Lock()
		for id, s := range shard.sessions {
			s.mu.Lock()
			total := s.bytesFolded
			routeTotals := make(map[string]int64, len(s.routes))
			for key, activity := range s.routes {
				routeTotals[key] = activity.BytesFolded
			}
			for _, obs := range s.observations {
				bytes := obs.BytesAccepted()
				total += bytes
				key := routeID(obs.Capture.Method, obs.Capture.Pattern)
				if _, tracked := s.routes[key]; tracked {
					routeTotals[key] += bytes
				}
			}
			if total > s.lastSweptBytes {
				s.lastByteAccepted = sweepStart
			}
			s.lastSweptBytes = total
			for key, totalForRoute := range routeTotals {
				activity := s.routes[key]
				if totalForRoute > activity.LastSweptBytes {
					activity.LastByteAccepted = sweepStart
				}
				activity.LastSweptBytes = totalForRoute
			}
			prune := s.openObservations == 0 && !s.lastObservationEnd.IsZero() && sweepStart.Sub(s.lastObservationEnd) >= r.cfg.Retention
			s.mu.Unlock()
			if prune {
				delete(shard.sessions, id)
				r.sessionReservations.Add(-1)
			}
		}
		shard.Unlock()
	}
	r.transfersMu.Lock()
	for id, t := range r.transfers {
		t.mu.Lock()
		total := t.bytesFolded
		if t.observation != nil {
			total += t.observation.BytesAccepted()
		}
		if total > t.lastSweptBytes {
			t.lastByteAccepted = sweepStart
		}
		t.lastSweptBytes = total
		prune := t.openObservations == 0 && !t.lastObservationEnd.IsZero() && sweepStart.Sub(t.lastObservationEnd) >= r.cfg.Retention
		t.mu.Unlock()
		if prune {
			delete(r.transfers, id)
			r.transferReservations.Add(-1)
		}
	}
	r.transfersMu.Unlock()
	return r.SnapshotAt(sweepStart)
}

func (r *Registry) Snapshot() Snapshot { return r.SnapshotAt(now()) }

func (r *Registry) SnapshotAt(capturedAt time.Time) Snapshot {
	view := Snapshot{PublisherID: r.cfg.PublisherID, NodeID: r.cfg.NodeID, PublisherEpoch: r.cfg.PublisherEpoch, Sequence: r.sequence.Load(), CapturedAt: capturedAt,
		Truncated: r.truncated.Load(), DroppedObservations: r.droppedObservations.Load(),
		DroppedBytes: r.droppedBytes.Load(), UnattributedObservations: r.unattributedObservations.Load(),
		UnattributedBytes: r.unattributedBytes.Load()}
	for i := range r.shards {
		shard := &r.shards[i]
		shard.RLock()
		for _, s := range shard.sessions {
			s.mu.Lock()
			view.Sessions = append(view.Sessions, sessionViewOf(s))
			s.mu.Unlock()
		}
		shard.RUnlock()
	}
	r.transfersMu.RLock()
	for _, t := range r.transfers {
		t.mu.Lock()
		view.Transfers = append(view.Transfers, transferViewOf(t))
		t.mu.Unlock()
	}
	r.transfersMu.RUnlock()
	sort.Slice(view.Sessions, func(i, j int) bool { return view.Sessions[i].SessionID < view.Sessions[j].SessionID })
	sort.Slice(view.Transfers, func(i, j int) bool { return view.Transfers[i].ID < view.Transfers[j].ID })
	return cloneSnapshot(view)
}

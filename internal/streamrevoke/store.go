// Package streamrevoke implements a stream-revocation "kill switch": a small
// set of revoked stream sessions/users that edge nodes consult on the hot path
// via an in-memory cache, backed by Redis for multi-node propagation with an
// optional durable Postgres mirror.
//
// The hot path (IsRevoked) is a pure in-memory read with no I/O. Revocations
// are propagated to other nodes over Redis pub/sub for immediate application
// and mirrored to Redis keys (with a TTL) so late-joining or restarting nodes
// can reconcile via a periodic SCAN. A durable mirror, when configured, lets
// kills survive a Redis flush.
package streamrevoke

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/redis/go-redis/v9"
)

const (
	// keyPrefix is the Redis key namespace for revocation mirror keys, e.g.
	// silo:revoked:sess:{id} and silo:revoked:user:{id}.
	keyPrefix = "silo:revoked:"

	// scanPattern matches every revocation mirror key for cache warming and
	// periodic reconciliation.
	scanPattern = keyPrefix + "*"

	// EventStreamRevoked is the cache.Event.Type published on cache.ChannelAdmin
	// when a revocation is created so other nodes apply it immediately.
	EventStreamRevoked = "stream_revoked"
	// EventStreamUnrevoked is published when an operator removes a revocation.
	EventStreamUnrevoked = "stream_unrevoked"

	defaultPollInterval       = 60 * time.Second
	defaultPropagationTimeout = 5 * time.Second
	// defaultTTL is intentionally >= the playback recipe-card lifetime
	// (playback.MaxTokenTTL, 24h): a kill must never expire before the session it
	// kills can be reconstructed, or PR #174's restart-resilient playback could
	// rebuild and re-serve a stream whose revocation had already lapsed. This is
	// an invariant, stated in words to avoid importing the playback package here
	// (which would create an import cycle); keep the two values coupled.
	defaultTTL = 24 * time.Hour
)

// Kind identifies what a revocation targets.
type Kind string

const (
	// KindSession revokes a single stream session id.
	KindSession Kind = "sess"
	// KindUser revokes every stream belonging to a user id.
	KindUser Kind = "user"
)

// Key uniquely identifies a revocation in the in-memory cache and Redis.
type Key struct {
	Kind Kind
	ID   string
}

// Revocation is a single kill-switch entry.
type Revocation struct {
	Kind      Kind      `json:"kind"`
	ID        string    `json:"id"`
	Reason    string    `json:"reason,omitempty"`
	RevokedAt time.Time `json:"revoked_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Tombstone is a bounded durable unrevocation marker. It prevents a stale
// replica or restart warm from resurrecting a revocation that an operator
// explicitly removed.
type Tombstone struct {
	Kind        Kind
	ID          string
	UnrevokedAt time.Time
	ExpiresAt   time.Time
}

func (t Tombstone) key() Key {
	return Key{Kind: t.Kind, ID: t.ID}
}

// DurableState is one consistent snapshot of the durable state for every key.
// A key appears as either a live revocation or a live tombstone, never both.
type DurableState struct {
	Revocations []Revocation
	Tombstones  []Tombstone
}

// key returns the in-memory cache key for this revocation.
func (r Revocation) key() Key {
	return Key{Kind: r.Kind, ID: r.ID}
}

// expired reports whether the revocation is no longer in force at now.
func (r Revocation) expired(now time.Time) bool {
	return !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt)
}

// DurableStore is an optional Postgres-backed mirror so kills survive a Redis
// flush. A concrete implementation lives elsewhere; this package only consumes
// the interface when one is provided.
type DurableStore interface {
	Upsert(ctx context.Context, r Revocation) error
	UpsertTombstone(ctx context.Context, t Tombstone) error
	List(ctx context.Context) (DurableState, error)
	Prune(ctx context.Context) error
}

// Options configures a Store.
type Options struct {
	Redis              *redis.Client  // nil => memory-only (integrated single-node)
	Bus                cache.EventBus // nil => no push propagation
	Durable            DurableStore   // nil => no durable mirror
	PollInterval       time.Duration  // default 60s
	WatchInterval      time.Duration  // default 5s
	DefaultTTL         time.Duration  // default 24h
	PropagationTimeout time.Duration  // default 5s
}

// Store holds the in-memory revocation cache and its propagation plumbing.
type Store struct {
	rdb                *redis.Client
	bus                cache.EventBus
	durable            DurableStore
	pollInterval       time.Duration
	defaultTTL         time.Duration
	watchInterval      time.Duration
	propagationTimeout time.Duration

	opMu             sync.Mutex
	mu               sync.RWMutex
	items            map[Key]Revocation
	tombstones       map[Key]time.Time
	tombstoneExpires map[Key]time.Time
}

// New builds a Store, applying defaults for any unset Options.
func New(opts Options) *Store {
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.DefaultTTL <= 0 {
		opts.DefaultTTL = defaultTTL
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = 5 * time.Second
	}
	if opts.PropagationTimeout <= 0 {
		opts.PropagationTimeout = defaultPropagationTimeout
	}
	return &Store{
		rdb:                opts.Redis,
		bus:                opts.Bus,
		durable:            opts.Durable,
		pollInterval:       opts.PollInterval,
		defaultTTL:         opts.DefaultTTL,
		watchInterval:      opts.WatchInterval,
		propagationTimeout: opts.PropagationTimeout,
		items:              make(map[Key]Revocation),
		tombstones:         make(map[Key]time.Time),
		tombstoneExpires:   make(map[Key]time.Time),
	}
}

// redisKey returns the Redis mirror key for a revocation key.
func redisKey(k Key) string {
	return keyPrefix + string(k.Kind) + ":" + k.ID
}

// userKey returns the cache key for a numeric user id.
func userKey(userID int) Key {
	return Key{Kind: KindUser, ID: strconv.Itoa(userID)}
}

// Refuse is the single shared enforcement point for every serve surface (edge
// proxy, transcode node, native api, jellycompat): if the session or user is
// revoked it writes a 403 and returns true, and the caller must stop serving.
// Centralizing the check + response here keeps one section per concern instead
// of duplicating "IsRevoked → 403" at each surface. It does NOT hang up an
// in-flight connection — long-pour paths pair this with a connection-cut helper.
// startedAt is when the request's stream credential was issued (see IsRevoked).
func (s *Store) Refuse(w http.ResponseWriter, sessionID string, userID int, startedAt time.Time) bool {
	if s == nil || !s.IsRevoked(sessionID, userID, startedAt) {
		return false
	}
	http.Error(w, "stream revoked", http.StatusForbidden)
	return true
}

// WatchAndCut watches a long-lived pour (a single long-GET direct-play/remux) and
// once the session/user is revoked, forces the in-flight write to fail via
// SetWriteDeadline — hanging up the socket even though the 24h token is still
// valid (cutting an open connection is a socket action, not a token revocation).
// It checks immediately on entry (so a pour that began the instant before the
// kill is cut without waiting a tick) and then every 5s. Returns a stop func the
// caller defers when the request finishes normally. This is the shared in-flight
// cut used by every long-pour serve surface (edge proxy, native api,
// jellycompat), so the cut logic lives in one place. HLS/transcode paths don't
// need it — per-segment Refuse stops them within one segment.
//
// If the ResponseWriter chain doesn't support write deadlines, the failure is
// logged and the stream still stops on its next request via Refuse. A context
// cut latch also prevents rolling deadline writers from re-arming the socket.
// This helper never wraps the writer, so it does not disable sendfile.
// startedAt follows IsRevoked's contract; a pour in flight when a user kill
// lands always predates that kill, so passing the request's credential/entry
// time makes mid-pour user kills cut correctly on every surface.
func (s *Store) WatchAndCut(w http.ResponseWriter, sessionID string, userID int, startedAt time.Time) func() {
	return s.WatchAndCutContext(context.Background(), w, sessionID, userID, startedAt)
}

// WatchAndCutContext is WatchAndCut with the request context used for logging
// and for resolving the rolling-deadline cut latch.
func (s *Store) WatchAndCutContext(ctx context.Context, w http.ResponseWriter, sessionID string, userID int, startedAt time.Time) func() {
	if s == nil {
		return func() {}
	}
	latch := httpstream.CutLatchFrom(ctx)
	var warnOnce sync.Once
	cut := func() {
		latch.Cut()
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now()); err != nil {
			warnOnce.Do(func() {
				slog.WarnContext(ctx, "stream cut could not set write deadline; in-flight pour continues until its next request",
					"component", "streamrevoke",
					"session", sessionID,
					"user", userID,
					"error", err,
				)
			})
		}
	}
	if s.IsRevoked(sessionID, userID, startedAt) {
		cut()
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(s.watchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if s.IsRevoked(sessionID, userID, startedAt) {
					cut()
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// IsRevoked is the HOT PATH: a pure in-memory cache read with no I/O. It
// returns true if the session id is currently revoked, or if the user id is
// revoked AND the stream's credential predates that user revocation.
//
// startedAt is when the request's stream credential was issued: the stream
// token's iat on token-bearing surfaces (edge proxy, transcode node, native
// ?st=, ABS bearer), the persisted playback-session start for public ABS
// tracks, or the feed creation time for public RSS capabilities. A user
// revocation is a CUTOFF, not a ban:
// it kills streams whose credential predates it, while a stream authorized
// after it (which required passing auth that the revocation just reset) plays
// normally. Without the cutoff, the OnUserSessionsRevoked hook — fired by any
// admin edit of password/role/enabled/permissions/quality — would 403 the
// user's playback for the full 24h TTL even after they re-authenticate unless
// an operator explicitly unrevoked it. A zero startedAt never
// matches a user revocation (fail open, matching the enforcer's "never kill on
// uncertainty"); session revocations are exact-id kills and ignore startedAt.
func (s *Store) IsRevoked(sessionID string, userID int, startedAt time.Time) bool {
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sessionID != "" {
		if r, ok := s.items[Key{Kind: KindSession, ID: sessionID}]; ok && !r.expired(now) {
			return true
		}
	}
	// userID <= 0 is the "no resolved owner" sentinel (e.g. the transcode node's
	// session-only check). Never match it against a KindUser entry: a stray
	// user:"0" revocation must not read as "every ownerless request is revoked".
	if userID > 0 {
		if r, ok := s.items[userKey(userID)]; ok && !r.expired(now) &&
			!startedAt.IsZero() && startedAt.Before(r.RevokedAt) {
			return true
		}
	}
	return false
}

// effectiveExpiry returns the instant a revocation lapses, treating a zero
// ExpiresAt as "never" (far future) so monotonic comparisons order a permanent
// kill above any bounded one.
func (r Revocation) effectiveExpiry() time.Time {
	if r.ExpiresAt.IsZero() {
		return permanentExpiry
	}
	return r.ExpiresAt
}

// applyLocal independently merges a revocation's two monotonic dimensions:
// expiry never moves earlier, while the user-cutoff RevokedAt never moves
// backward. Reason follows the record that supplied the newer cutoff. Keeping
// the fields independent matters when a newer admin cutoff has a shorter
// horizon than an existing kill. The caller must not hold s.mu.
func (s *Store) applyLocal(r Revocation) {
	s.mu.Lock()
	if issuedAt, ok := s.tombstones[r.key()]; ok {
		if expiresAt := s.tombstoneExpires[r.key()]; !time.Now().Before(expiresAt) {
			delete(s.tombstones, r.key())
			delete(s.tombstoneExpires, r.key())
		} else if !r.RevokedAt.After(issuedAt) {
			s.mu.Unlock()
			return
		} else {
			delete(s.tombstones, r.key())
			delete(s.tombstoneExpires, r.key())
		}
	}
	if existing, ok := s.items[r.key()]; ok {
		if r.effectiveExpiry().After(existing.effectiveExpiry()) {
			existing.ExpiresAt = r.ExpiresAt
		}
		if r.RevokedAt.After(existing.RevokedAt) {
			existing.RevokedAt = r.RevokedAt
			existing.Reason = r.Reason
		}
		s.items[r.key()] = existing
	} else {
		s.items[r.key()] = r
	}
	s.mu.Unlock()
}

// effective returns the currently-stored revocation for a key — the
// independently merged copy applyLocal kept. Zero value if absent.
func (s *Store) effective(k Key) Revocation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.items[k]
}

func (s *Store) isTombstoned(k Key) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.tombstones[k]
	return ok
}

// Revoke adds a revocation to the local cache, writes it to Redis (with a TTL
// of until-now) when configured, mirrors it to the durable store when
// configured, and publishes a pub/sub event so other nodes apply it
// immediately.
func (s *Store) Revoke(ctx context.Context, key Key, reason string, until time.Time) error {
	_, err := s.RevokeWithWarnings(ctx, key, reason, until)
	return err
}

// RevokeWithWarnings applies a revocation locally and reports propagation
// failures without weakening the local kill. Revoke is the compatibility
// wrapper used by existing callers that only need local-success semantics.
func (s *Store) RevokeWithWarnings(ctx context.Context, key Key, reason string, until time.Time) ([]string, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.revokeWithWarningsLocked(ctx, key, reason, until)
}

// revokeWithWarningsLocked performs RevokeWithWarnings while the caller holds
// opMu, allowing revoke-if-absent to make its presence check and write atomic.
func (s *Store) revokeWithWarningsLocked(ctx context.Context, key Key, reason string, until time.Time) ([]string, error) {
	now := time.Now()
	r := Revocation{
		Kind:      key.Kind,
		ID:        key.ID,
		Reason:    reason,
		RevokedAt: now,
		ExpiresAt: until,
	}

	// Local cache first: the hot path must reflect the kill immediately even if
	// downstream propagation fails.
	s.applyLocal(r)
	var warnings []string

	// Propagation must not die with the caller: an admin terminate rides its
	// HTTP request's context, and an abort after applyLocal would strand the
	// kill in this process's memory — never reaching Redis (edges), pub/sub, or
	// the durable mirror (lost on restart). Bound the detached work so one slow
	// dependency cannot hold opMu and block every later operation indefinitely.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.propagationTimeout)
	defer cancel()
	merged := s.effective(r.key())

	// Redis mirror for multi-node propagation to edges. Mirror the merged copy
	// applyLocal kept. This SET is deliberately serialized by opMu in this
	// process. Separate central replicas can still race and overwrite a newer
	// value because Redis does not atomically merge records; that is deferred to
	// the multi-replica work, not addressed by narrowing this lock.
	if err := s.mirrorToRedis(ctx, merged); err != nil {
		warnings = append(warnings, "Redis mirror was not updated")
	}

	// Pub/sub push so already-connected nodes apply the kill immediately (the
	// warm loops deliberately skip this — SCAN reconcile handles late-joiners).
	// Publish the merged record too, so pub/sub-only delivery preserves both the
	// newest cutoff and the longest expiry when Redis is unavailable.
	if s.bus != nil {
		data, err := json.Marshal(merged)
		if err != nil {
			slog.WarnContext(ctx, "streamrevoke marshal failed", "error", err, "kind", r.Kind, "id", r.ID)
			return warnings, err
		}
		evt := cache.Event{Type: EventStreamRevoked, Payload: string(data)}
		if err := s.bus.Publish(ctx, cache.ChannelAdmin, evt); err != nil {
			slog.WarnContext(ctx, "streamrevoke publish failed", "error", err, "kind", r.Kind, "id", r.ID)
			warnings = append(warnings, "revocation event was not published")
		}
	}

	// Durable mirror last: an unhealthy Postgres must not delay the urgent edge
	// Redis kill or its pub/sub push.
	if s.durable != nil {
		if err := s.durable.Upsert(ctx, merged); err != nil {
			slog.WarnContext(ctx, "streamrevoke durable upsert failed", "error", err, "kind", r.Kind, "id", r.ID)
			warnings = append(warnings, "durable mirror was not updated")
		}
	}

	return warnings, nil
}

// mirrorToRedis writes (or deletes) the Redis mirror key for a revocation with a
// TTL of the time remaining until r.ExpiresAt. It is the single shared
// Redis-arming path, used both by Revoke and by the durable warm loops: edge
// nodes (proxy/transcode) have no durable store and learn kills ONLY from
// Redis, so re-arming Redis from the durable source of truth after a Redis flush
// + central restart is what lets edges reconverge via their SCAN reconcile.
// Best-effort: failures are logged and returned as propagation warnings. No-op
// when Redis is absent.
func (s *Store) mirrorToRedis(ctx context.Context, r Revocation) error {
	if s.rdb == nil {
		return nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		slog.WarnContext(ctx, "streamrevoke marshal failed", "error", err, "kind", r.Kind, "id", r.ID)
		return err
	}
	if r.ExpiresAt.IsZero() {
		// Permanent kill: set without a TTL, matching applyLocal/effectiveExpiry
		// treating a zero ExpiresAt as "never". A TTL of time.Until(zero) would be
		// hugely negative and drop the key, so late-joining edges would miss it.
		if err := s.rdb.Set(ctx, redisKey(r.key()), data, 0).Err(); err != nil {
			slog.WarnContext(ctx, "streamrevoke redis set failed", "error", err, "kind", r.Kind, "id", r.ID)
			return err
		}
		return nil
	}
	ttl := time.Until(r.ExpiresAt)
	if ttl <= 0 {
		// Already expired: nothing to mirror in Redis.
		if err := s.rdb.Del(ctx, redisKey(r.key())).Err(); err != nil {
			slog.DebugContext(ctx, "streamrevoke redis del failed", "error", err, "kind", r.Kind, "id", r.ID)
			return err
		}
		return nil
	}
	if err := s.rdb.Set(ctx, redisKey(r.key()), data, ttl).Err(); err != nil {
		slog.WarnContext(ctx, "streamrevoke redis set failed", "error", err, "kind", r.Kind, "id", r.ID)
		return err
	}
	return nil
}

type unrevocation struct {
	Kind      Kind      `json:"kind"`
	ID        string    `json:"id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (u unrevocation) key() Key {
	return Key{Kind: u.Kind, ID: u.ID}
}

func (s *Store) normalizeUnrevocation(u unrevocation) unrevocation {
	maxExpiry := u.IssuedAt.Add(s.defaultTTL)
	if u.ExpiresAt.IsZero() || u.ExpiresAt.After(maxExpiry) {
		u.ExpiresAt = maxExpiry
	}
	return u
}

// applyUnrevocation installs a bounded tombstone before deleting the local
// kill. A delayed unrevoke event never removes a newer revocation.
func (s *Store) applyUnrevocation(u unrevocation) {
	u = s.normalizeUnrevocation(u)
	k := u.key()
	s.mu.Lock()
	if existing, ok := s.items[k]; ok && existing.RevokedAt.After(u.IssuedAt) {
		s.mu.Unlock()
		return
	}
	if previous, ok := s.tombstones[k]; ok && previous.After(u.IssuedAt) {
		s.mu.Unlock()
		return
	}
	s.tombstones[k] = u.IssuedAt
	s.tombstoneExpires[k] = u.ExpiresAt
	delete(s.items, k)
	s.mu.Unlock()
}

// Unrevoke removes a kill locally and from every configured mirror. A failed
// publish fails safe: other processes may retain the kill until expiry.
func (s *Store) Unrevoke(ctx context.Context, key Key) error {
	_, err := s.UnrevokeWithWarnings(ctx, key)
	return err
}

// UnrevokeWithWarnings is the admin-facing form of Unrevoke.
func (s *Store) UnrevokeWithWarnings(ctx context.Context, key Key) ([]string, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	issuedAt := time.Now()
	s.mu.RLock()
	existing := s.items[key]
	s.mu.RUnlock()
	u := s.normalizeUnrevocation(unrevocation{Kind: key.Kind, ID: key.ID, IssuedAt: issuedAt, ExpiresAt: existing.ExpiresAt})
	s.applyUnrevocation(u)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.propagationTimeout)
	defer cancel()
	var warnings []string
	if s.durable != nil {
		t := Tombstone{Kind: key.Kind, ID: key.ID, UnrevokedAt: u.IssuedAt, ExpiresAt: u.ExpiresAt}
		if err := s.durable.UpsertTombstone(ctx, t); err != nil {
			slog.WarnContext(ctx, "streamrevoke durable tombstone upsert failed", "error", err, "kind", key.Kind, "id", key.ID)
			warnings = append(warnings, "durable unrevocation tombstone was not updated")
		}
	}
	if s.rdb != nil {
		if err := s.rdb.Del(ctx, redisKey(key)).Err(); err != nil {
			slog.WarnContext(ctx, "streamrevoke redis delete failed", "error", err, "kind", key.Kind, "id", key.ID)
			warnings = append(warnings, "Redis mirror was not deleted")
		}
	}
	if s.bus != nil {
		data, err := json.Marshal(u)
		if err != nil {
			return warnings, err
		}
		if err := s.bus.Publish(ctx, cache.ChannelAdmin, cache.Event{Type: EventStreamUnrevoked, Payload: string(data)}); err != nil {
			slog.WarnContext(ctx, "streamrevoke unrevoke publish failed", "error", err, "kind", key.Kind, "id", key.ID)
			warnings = append(warnings, "unrevocation event was not published; other processes may retain the kill until it expires")
		}
	}
	return warnings, nil
}

// RevokeSession revokes a single session id for DefaultTTL.
func (s *Store) RevokeSession(ctx context.Context, sessionID, reason string) error {
	return s.Revoke(ctx, Key{Kind: KindSession, ID: sessionID}, reason, time.Now().Add(s.defaultTTL))
}

// RevokeSessionWithWarnings is the admin-facing form of RevokeSession.
func (s *Store) RevokeSessionWithWarnings(ctx context.Context, sessionID, reason string) ([]string, error) {
	return s.RevokeWithWarnings(ctx, Key{Kind: KindSession, ID: sessionID}, reason, time.Now().Add(s.defaultTTL))
}

// RevokeSessionFor revokes a single session id for a caller-chosen TTL. The
// async enforcer uses a short TTL so a transient over-count (e.g. a ghost
// session lingering next to a fresh reconnect) self-heals; a persistent abuser
// is simply re-revoked on the next evaluation pass. ttl <= 0 falls back to
// DefaultTTL.
func (s *Store) RevokeSessionFor(ctx context.Context, sessionID, reason string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	return s.Revoke(ctx, Key{Kind: KindSession, ID: sessionID}, reason, time.Now().Add(ttl))
}

// RevokeSessionForIfAbsent adds a bounded session revocation only when that key
// has no live revocation. It is for recurring automated enforcement: repeated
// observations of the same over-cap session must not slide a false positive's
// expiry forward forever. Admin Revoke methods intentionally retain their
// monotonic merge behavior. The bool reports whether a new kill was written.
func (s *Store) RevokeSessionForIfAbsent(
	ctx context.Context,
	sessionID, reason string,
	ttl time.Duration,
) (bool, error) {
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	key := Key{Kind: KindSession, ID: sessionID}
	s.opMu.Lock()
	defer s.opMu.Unlock()

	now := time.Now()
	s.mu.RLock()
	existing, ok := s.items[key]
	s.mu.RUnlock()
	if ok && !existing.expired(now) {
		return false, nil
	}
	_, err := s.revokeWithWarningsLocked(ctx, key, reason, now.Add(ttl))
	return err == nil, err
}

// RevokeUser revokes the user's streams for DefaultTTL. It is a CUTOFF, not a
// ban: enforcement (IsRevoked) only matches streams whose credential was issued
// before this revocation, so playback the user starts after re-authenticating
// is unaffected. This is what makes it safe for OnUserSessionsRevoked to call
// on every auth-session revocation (admin edits included), while still cutting
// every stream that rode a pre-revocation credential.
func (s *Store) RevokeUser(ctx context.Context, userID int, reason string) error {
	// IsRevoked never matches userID <= 0 against a KindUser entry, so a kill for
	// such an id would be silently ineffective. Refuse it rather than report a
	// success that has no teeth.
	if userID <= 0 {
		return fmt.Errorf("streamrevoke: invalid userID %d", userID)
	}
	return s.Revoke(ctx, userKey(userID), reason, time.Now().Add(s.defaultTTL))
}

// RevokeUserWithWarnings applies the default TTL while preserving propagation
// warnings for the admin API.
func (s *Store) RevokeUserWithWarnings(ctx context.Context, userID int, reason string) ([]string, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("streamrevoke: invalid userID %d", userID)
	}
	return s.RevokeWithWarnings(ctx, userKey(userID), reason, time.Now().Add(s.defaultTTL))
}

// List returns the currently-active revocations, pruning expired entries on
// read.
func (s *Store) List() []Revocation {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Revocation, 0, len(s.items))
	for k, r := range s.items {
		if r.expired(now) {
			delete(s.items, k)
			continue
		}
		out = append(out, r)
	}
	return out
}

// pruneExpired removes expired entries from the in-memory cache.
func (s *Store) pruneExpired() {
	now := time.Now()
	s.mu.Lock()
	for k, r := range s.items {
		if r.expired(now) {
			delete(s.items, k)
		}
	}
	for k, expiresAt := range s.tombstoneExpires {
		if !now.Before(expiresAt) {
			delete(s.tombstones, k)
			delete(s.tombstoneExpires, k)
		}
	}
	s.mu.Unlock()
}

// StartSync warms the cache (durable store then a Redis SCAN of silo:revoked:*)
// and subscribes to cache.ChannelAdmin to apply EventStreamRevoked events. The
// initial warm and subscribe run SYNCHRONOUSLY (inline) before StartSync
// returns, so kills recorded durably or in Redis are already enforced before
// the first stream is served. Only the PollInterval reconcile/prune loop runs
// in a spawned goroutine. With a nil Redis client the Store is memory-only and
// only the local prune (plus durable maintenance, if configured) runs on the
// tick.
func (s *Store) StartSync(ctx context.Context) {
	// Warm from the durable mirror first so kills survive a Redis flush. Each
	// non-expired entry is also re-armed into Redis (mirrorToRedis no-ops when
	// Redis is absent) so edge nodes — which learn kills only from Redis —
	// reconverge via their SCAN reconcile after a Redis flush + central restart.
	if s.durable != nil {
		warmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.propagationTimeout)
		state, err := s.warmFromDurable(warmCtx)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "streamrevoke durable warm failed", "error", err)
		} else {
			for _, t := range state.Tombstones {
				s.applyUnrevocation(unrevocation{
					Kind: t.Kind, ID: t.ID, IssuedAt: t.UnrevokedAt, ExpiresAt: t.ExpiresAt,
				})
			}
			now := time.Now()
			for _, r := range state.Revocations {
				if !r.expired(now) {
					s.applyLocal(r)
					if !s.isTombstoned(r.key()) {
						_ = s.mirrorToRedis(ctx, s.effective(r.key()))
					}
				}
			}
		}
	}

	// Warm from Redis and subscribe for push updates.
	if s.rdb != nil {
		s.reconcileFromRedis(ctx)
	}
	if s.bus != nil {
		if err := s.bus.Subscribe(ctx, cache.ChannelAdmin, s.handleEvent); err != nil {
			slog.WarnContext(ctx, "streamrevoke subscribe failed", "error", err)
		}
	}

	go func() {
		ticker := time.NewTicker(s.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.maintain(ctx)
			}
		}
	}()
}

// warmFromDurable loads the active key state for the boot-time cache warm,
// retrying a bounded number of times with short backoff. A single transient DB
// error at startup (with Redis also flushed) would otherwise leave the kill
// list empty until the first poll tick 60s later; the retry closes that window.
// It is strictly bounded and honors ctx cancellation, so startup never blocks
// indefinitely. Only the boot warm needs this — the recurring maintain tick is
// its own backstop.
func (s *Store) warmFromDurable(ctx context.Context) (DurableState, error) {
	backoffs := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond}
	var state DurableState
	var err error
	for attempt := 0; ; attempt++ {
		if state, err = s.durable.List(ctx); err == nil {
			return state, nil
		}
		if attempt >= len(backoffs) {
			return DurableState{}, err
		}
		select {
		case <-ctx.Done():
			return DurableState{}, err
		case <-time.After(backoffs[attempt]):
		}
	}
}

// maintain runs one reconcile/prune pass: it is the body of the poll tick,
// factored out so tests can exercise it directly without a live ticker. All
// steps are best-effort — a failure in one is logged and does not block the
// others or the next tick.
func (s *Store) maintain(ctx context.Context) {
	if s.rdb != nil {
		s.reconcileFromRedis(ctx)
	}
	// Durable maintenance: re-warm from the durable mirror so a mid-life Redis
	// flush self-heals, re-arming Redis for edge nodes too, then physically
	// reclaim expired rows.
	if s.durable != nil {
		s.opMu.Lock()
		maintainCtx, cancel := context.WithTimeout(ctx, s.propagationTimeout)

		if state, err := s.durable.List(maintainCtx); err != nil {
			slog.WarnContext(ctx, "streamrevoke durable reconcile failed", "error", err)
		} else {
			for _, t := range state.Tombstones {
				s.applyUnrevocation(unrevocation{
					Kind: t.Kind, ID: t.ID, IssuedAt: t.UnrevokedAt, ExpiresAt: t.ExpiresAt,
				})
			}
			now := time.Now()
			durable := make(map[Key]Revocation, len(state.Revocations))
			for _, r := range state.Revocations {
				if !r.expired(now) {
					durable[r.key()] = r
					s.applyLocal(r)
					if !s.isTombstoned(r.key()) {
						_ = s.mirrorToRedis(maintainCtx, s.effective(r.key()))
					}
				}
			}
			s.mu.RLock()
			local := make([]Revocation, 0, len(s.items))
			for k, r := range s.items {
				if _, tombstoned := s.tombstones[k]; !tombstoned && !r.expired(now) {
					local = append(local, r)
				}
			}
			s.mu.RUnlock()
			for _, r := range local {
				durableCopy, ok := durable[r.key()]
				if !ok ||
					durableCopy.effectiveExpiry().Before(r.effectiveExpiry()) ||
					durableCopy.RevokedAt.Before(r.RevokedAt) {
					if err := s.durable.Upsert(maintainCtx, r); err != nil {
						slog.WarnContext(ctx, "streamrevoke durable self-heal failed", "error", err, "kind", r.Kind, "id", r.ID)
					}
				}
			}
		}
		if err := s.durable.Prune(maintainCtx); err != nil {
			slog.WarnContext(ctx, "streamrevoke durable prune failed", "error", err)
		}
		cancel()
		s.opMu.Unlock()
	}
	s.pruneExpired()
}

// handleEvent applies an EventStreamRevoked event to the local cache. Other
// event types on the channel are ignored.
func (s *Store) handleEvent(evt cache.Event) {
	switch evt.Type {
	case EventStreamRevoked:
		var r Revocation
		if err := json.Unmarshal([]byte(evt.Payload), &r); err != nil {
			slog.Debug("streamrevoke event unmarshal failed", "error", err)
			return
		}
		if !r.expired(time.Now()) {
			s.applyLocal(r)
		}
	case EventStreamUnrevoked:
		var u unrevocation
		if err := json.Unmarshal([]byte(evt.Payload), &u); err != nil {
			slog.Debug("streamrevoke unrevoke event unmarshal failed", "error", err)
			return
		}
		s.applyUnrevocation(u)
	}
}

// reconcileFromRedis SCANs the revocation namespace and applies every live
// entry to the local cache. Redis is authoritative for entries it holds; TTL
// expiry there is mirrored by prune-on-read locally.
func (s *Store) reconcileFromRedis(ctx context.Context) {
	var cursor uint64
	now := time.Now()
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, scanPattern, 256).Result()
		if err != nil {
			slog.Warn("streamrevoke redis scan failed", "error", err)
			return
		}
		for _, k := range keys {
			val, err := s.rdb.Get(ctx, k).Result()
			if err != nil {
				// Key may have expired between SCAN and GET; skip it.
				continue
			}
			var r Revocation
			if err := json.Unmarshal([]byte(val), &r); err != nil {
				slog.DebugContext(ctx, "streamrevoke redis unmarshal failed", "error", err, "key", k)
				continue
			}
			if !r.expired(now) {
				s.applyLocal(r)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

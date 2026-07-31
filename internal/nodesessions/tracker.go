package nodesessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// KeyPrefix is the Redis key namespace for node session records
	// (silo:sessions:{nodeHash}:{sessionID}). Exported so readers of the
	// monitoring picture (streammonitor, the admin session list) SCAN the same
	// namespace this tracker writes instead of duplicating the literal.
	KeyPrefix = "silo:sessions:"

	sessionTTL           = 60 * time.Second
	refreshInt           = 30 * time.Second
	transcodeIdleTTL     = 180 * time.Second
	sessionTypeTranscode = "transcode"
)

// SessionInfo represents an active streaming session stored in Redis.
type SessionInfo struct {
	SessionID string `json:"session_id"`
	// LogicalSessionID is the stable playback-session identity when SessionID is
	// a replaceable transport generation. It is opaque monitoring metadata;
	// SessionID remains the node-addressing key.
	LogicalSessionID string `json:"logical_session_id,omitempty"`
	NodeURL          string `json:"node_url"`
	NodeName         string `json:"node_name"`
	UserID           string `json:"user_id,omitempty"`
	MediaItemID      string `json:"media_item_id,omitempty"`
	MediaTitle       string `json:"media_title,omitempty"`
	Type             string `json:"type"` // "direct_play", "remux", "transcode"
	CodecVideo       string `json:"codec_video,omitempty"`
	CodecAudio       string `json:"codec_audio,omitempty"`
	Resolution       string `json:"resolution,omitempty"`
	HWAccel          string `json:"hw_accel,omitempty"`
	StartedAt        string `json:"started_at"`

	// LastServedAt / BytesServed are the authoritative, server-observed liveness
	// signals: they are refreshed only when the node actually serves bytes for this
	// session (a segment written, a direct-play/remux pour advancing), never from a
	// client progress report. A "quiet" stream that keeps pulling stays fresh here;
	// one that stops pulling ages out. This is the anti-abuse monitoring surface.
	LastServedAt string `json:"last_served_at,omitempty"`
	BytesServed  int64  `json:"bytes_served,omitempty"`

	// AuthUserID / ProfileID / MediaFileID are the numeric ownership keys the
	// node copies from the verified stream token. They enrich the live admin
	// "active streams" view (served by SCANning these records) so it can answer
	// *who* is watching *what* on each node, not just session id + node + type;
	// the string UserID/MediaItemID/MediaTitle fields remain the display labels.
	AuthUserID  int    `json:"auth_user_id,omitempty"`
	ProfileID   string `json:"profile_id,omitempty"`
	MediaFileID int    `json:"media_file_id,omitempty"`

	// Route is the origin protocol ("native" | "jellycompat"). Type is the play
	// method (direct_play/remux/transcode); Route is orthogonal — a jellycompat
	// stream and a native one share a Type but differ in Route. Client* are
	// best-effort viewer identity for oversight and abuse heuristics (e.g. one
	// re-streaming client fanning out to many viewers). Position is the last
	// known playback position in seconds (secondary timing).
	Route      string  `json:"route,omitempty"`
	ClientIP   string  `json:"client_ip,omitempty"`
	ClientName string  `json:"client_name,omitempty"`
	Position   float64 `json:"position,omitempty"`
}

// Lease identifies one request-scoped reference to a tracked session. Its
// fields are deliberately private: only the Tracker that issued it can release
// it, and stale leases from an invalidated generation are harmless.
type Lease struct {
	sessionID string
	epoch     uint64
	id        uint64
}

type sessionRefs struct {
	epoch  uint64
	leases map[uint64]struct{}
}

// Tracker manages session lifecycle in Redis for a single node.
type Tracker struct {
	rdb      *redis.Client
	nodeURL  string
	nodeName string
	nodeType string
	nodeHash string // first 8 chars of SHA-256 of nodeURL

	mu        sync.Mutex
	sessions  map[string]sessionRefs // explicitly tracked sessions and request leases
	ephemeral map[string]time.Time   // ephemeral sessions by last-observed request time
	touched   map[string]time.Time   // last successful serve time
	records   map[string]SessionInfo // last-written record per session, for enriched refresh
	bytes     map[string]int64       // cumulative bytes served per session (monitoring only)
	nextEpoch uint64
	nextLease uint64
}

// NewTracker creates a session tracker for the given node.
// rdb may be nil, in which case all operations are no-ops.
func NewTracker(rdb *redis.Client, nodeURL, nodeName, nodeType string) *Tracker {
	return &Tracker{
		rdb:       rdb,
		nodeURL:   nodeURL,
		nodeName:  nodeName,
		nodeType:  nodeType,
		nodeHash:  namespaceHash(nodeURL),
		sessions:  make(map[string]sessionRefs),
		ephemeral: make(map[string]time.Time),
		touched:   make(map[string]time.Time),
		records:   make(map[string]SessionInfo),
		bytes:     make(map[string]int64),
	}
}

// redisKey returns the full Redis key for a session.
func (tr *Tracker) redisKey(sessionID string) string {
	return sessionRedisKey(tr.nodeHash, sessionID)
}

func namespaceHash(namespace string) string {
	h := sha256.Sum256([]byte(namespace))
	return hex.EncodeToString(h[:4])
}

func sessionRedisKey(nodeHash, sessionID string) string {
	return KeyPrefix + nodeHash + ":" + sessionID
}

// NodeHash returns the node's hash prefix used in Redis keys.
func (tr *Tracker) NodeHash() string {
	return tr.nodeHash
}

// NodeURL returns the node's URL.
func (tr *Tracker) NodeURL() string {
	return tr.nodeURL
}

// NodeName returns the node's display name.
func (tr *Tracker) NodeName() string {
	return tr.nodeName
}

// ActiveCount returns the number of active sessions tracked by this node,
// including ephemeral sessions touched within the session TTL.
func (tr *Tracker) ActiveCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	now := time.Now()
	count := len(tr.sessions)
	for id, last := range tr.ephemeral {
		if _, dup := tr.sessions[id]; dup {
			continue
		}
		if now.Sub(last) <= idleTTLFor(tr.records[id]) {
			count++
		}
	}
	return count
}

// Snapshot returns a copy of the currently-known session records on this node
// (including ephemeral ones still within their TTL), stamped with the latest
// LastServedAt/BytesServed. Used for the node /status view; central aggregates
// the equivalent picture by reading Redis.
func (tr *Tracker) Snapshot() []SessionInfo {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	now := time.Now()
	out := make([]SessionInfo, 0, len(tr.records))
	for id, rec := range tr.records {
		// Session-backed entries are always live until Remove; only ephemeral
		// (non-session) entries age out by idle timeout.
		if refs, isSession := tr.sessions[id]; !isSession || len(refs.leases) == 0 {
			if last, ok := tr.ephemeral[id]; !ok || now.Sub(last) > idleTTLFor(rec) {
				continue
			}
		}
		out = append(out, tr.enrichLocked(id, rec))
	}
	return out
}

// enrichLocked stamps a record with the latest observed liveness/bytes. Caller
// holds tr.mu.
func (tr *Tracker) enrichLocked(id string, rec SessionInfo) SessionInfo {
	if last, ok := tr.touched[id]; ok {
		rec.LastServedAt = last.UTC().Format(time.RFC3339)
	}
	if b, ok := tr.bytes[id]; ok {
		rec.BytesServed = b
	}
	return rec
}

// Track registers an active session in Redis with a TTL and returns a lease.
// Request-scoped callers must release it exactly once. Lifecycle-owned callers
// may instead use Remove for unconditional teardown.
func (tr *Tracker) Track(ctx context.Context, info SessionInfo) Lease {
	if tr.rdb == nil {
		return Lease{}
	}
	now := time.Now()
	if info.LastServedAt == "" {
		info.LastServedAt = now.UTC().Format(time.RFC3339)
	}

	// Explicitly-tracked sessions (direct play / remux) live in tr.sessions for
	// the whole connection. touched holds their last-served time so LastServedAt
	// reflects real activity; refreshAll only idle-prunes ephemeral (non-session)
	// touched entries, so a long quiet pour is never expired while its connection
	// is open.
	tr.mu.Lock()
	tr.preserveStartedAtLocked(&info)
	refs, exists := tr.sessions[info.SessionID]
	if !exists {
		tr.nextEpoch++
		refs = sessionRefs{epoch: tr.nextEpoch, leases: make(map[uint64]struct{})}
	}
	tr.nextLease++
	lease := Lease{sessionID: info.SessionID, epoch: refs.epoch, id: tr.nextLease}
	refs.leases[lease.id] = struct{}{}
	tr.sessions[info.SessionID] = refs
	delete(tr.ephemeral, info.SessionID)
	tr.records[info.SessionID] = info
	tr.touched[info.SessionID] = now
	enriched := tr.enrichLocked(info.SessionID, info)
	tr.mu.Unlock()
	if exists {
		return lease
	}

	data, err := json.Marshal(enriched)
	if err != nil {
		slog.DebugContext(ctx, "session track marshal failed", "component", "nodesessions", "error", err)
		return lease
	}
	key := tr.redisKey(info.SessionID)
	if err := tr.rdb.Set(ctx, key, data, sessionTTL).Err(); err != nil {
		slog.DebugContext(ctx, "session track set failed", "component", "nodesessions", "error", err, "session", info.SessionID)
	}
	return lease
}

// EnsureEphemeral makes a short-lived session visible without claiming that any
// bytes were served. The first write is synchronous; later liveness/byte updates
// are flushed by the refresh loop.
func (tr *Tracker) EnsureEphemeral(ctx context.Context, info SessionInfo) {
	if tr.rdb == nil {
		return
	}
	now := time.Now()
	tr.mu.Lock()
	tr.preserveStartedAtLocked(&info)
	_, known := tr.records[info.SessionID]
	tr.ephemeral[info.SessionID] = now
	tr.records[info.SessionID] = info
	enriched := tr.enrichLocked(info.SessionID, info)
	tr.mu.Unlock()
	if known {
		return
	}

	data, err := json.Marshal(enriched)
	if err != nil {
		slog.DebugContext(ctx, "session ensure marshal failed", "component", "nodesessions", "error", err)
		return
	}
	if err := tr.rdb.Set(ctx, tr.redisKey(info.SessionID), data, sessionTTL).Err(); err != nil {
		slog.DebugContext(ctx, "session ensure set failed", "component", "nodesessions", "error", err, "session", info.SessionID)
	}
}

// Touch registers or refreshes an ephemeral session that has no explicit end,
// such as HLS manifest/segment fetches flowing through a proxy. The session is
// written to Redis on first touch and drops out of the active count after
// sessionTTL without further touches (pruned by the refresh loop). Subsequent
// touches update LastServedAt in memory; the body is re-flushed on the refresh
// tick so the monitoring record reflects real serve activity.
func (tr *Tracker) Touch(ctx context.Context, info SessionInfo) {
	if tr.rdb == nil {
		return
	}
	tr.EnsureEphemeral(ctx, info)
	tr.mu.Lock()
	if _, known := tr.records[info.SessionID]; known {
		tr.touched[info.SessionID] = time.Now()
	}
	tr.mu.Unlock()
}

// preserveStartedAtLocked keeps the first-seen StartedAt when a record is
// re-written for a session we already track. Track re-fires on every range
// reconnect and Touch stamps a fresh sessionInfo per segment fetch; without
// this, StartedAt degrades to "time of last request", corrupting the admin
// view and the enforcer's StartedAt tie-break in victim selection. Caller
// holds tr.mu.
func (tr *Tracker) preserveStartedAtLocked(info *SessionInfo) {
	if prev, ok := tr.records[info.SessionID]; ok && prev.StartedAt != "" {
		info.StartedAt = prev.StartedAt
	}
}

// AddBytes records that n bytes were served for a session and marks it as active
// now. Cheap and lock-guarded; the accumulated value is flushed to Redis on the
// refresh tick. Bytes attribution is best-effort monitoring and never gates the
// hot path. Bytes for a session with no live record are dropped: a late tally
// arriving after the record was idle-pruned (or removed) must not recreate a
// bytes entry nothing will ever clean up — that was a slow permanent leak on
// busy edges.
func (tr *Tracker) AddBytes(sessionID string, n int64) {
	if tr.rdb == nil || n <= 0 {
		return
	}
	tr.mu.Lock()
	if _, known := tr.records[sessionID]; known {
		tr.bytes[sessionID] += n
		// Mark real serve activity so LastServedAt advances for every session
		// type, including long direct-play/remux pours.
		tr.touched[sessionID] = time.Now()
	}
	tr.mu.Unlock()
}

// MarkServed records real serve activity for a session without byte
// attribution, advancing LastServedAt (flushed on the refresh tick). Used by
// the transcode node's own serve paths, where wrapping ServeFile for byte
// counting would cost the sendfile fast path for a signal the fronting proxy
// already measures — without it the node record's LastServedAt stays frozen at
// start time, which starves the enforcer's freshness ordering and the admin
// view of the node-side truth.
func (tr *Tracker) MarkServed(sessionID string) {
	if tr == nil || tr.rdb == nil {
		return
	}
	tr.mu.Lock()
	if _, known := tr.records[sessionID]; known {
		tr.touched[sessionID] = time.Now()
	}
	tr.mu.Unlock()
}

// Release drops one request-scoped lease. A stale, duplicate, or already
// invalidated release is a no-op. The final live lease tears the record down.
func (tr *Tracker) Release(ctx context.Context, lease Lease) {
	if tr.rdb == nil || lease.sessionID == "" {
		return
	}
	tr.mu.Lock()
	refs, ok := tr.sessions[lease.sessionID]
	if !ok || refs.epoch != lease.epoch {
		tr.mu.Unlock()
		slog.DebugContext(ctx, "stale session lease release ignored", "component", "nodesessions", "session", lease.sessionID)
		return
	}
	if _, ok := refs.leases[lease.id]; !ok {
		tr.mu.Unlock()
		slog.DebugContext(ctx, "duplicate session lease release ignored", "component", "nodesessions", "session", lease.sessionID)
		return
	}
	delete(refs.leases, lease.id)
	if len(refs.leases) > 0 {
		tr.sessions[lease.sessionID] = refs
		tr.mu.Unlock()
		return
	}
	delete(tr.sessions, lease.sessionID)
	delete(tr.ephemeral, lease.sessionID)
	delete(tr.touched, lease.sessionID)
	delete(tr.records, lease.sessionID)
	delete(tr.bytes, lease.sessionID)
	tr.mu.Unlock()

	if err := tr.rdb.Del(ctx, tr.redisKey(lease.sessionID)).Err(); err != nil {
		slog.DebugContext(ctx, "session release failed", "component", "nodesessions", "error", err, "session", lease.sessionID)
	}
}

// Remove deletes a session unconditionally and invalidates every outstanding
// lease for its generation.
func (tr *Tracker) Remove(ctx context.Context, sessionID string) {
	if tr.rdb == nil {
		return
	}
	tr.mu.Lock()
	tr.nextEpoch++
	delete(tr.sessions, sessionID)
	delete(tr.ephemeral, sessionID)
	delete(tr.touched, sessionID)
	delete(tr.records, sessionID)
	delete(tr.bytes, sessionID)
	tr.mu.Unlock()

	if err := tr.rdb.Del(ctx, tr.redisKey(sessionID)).Err(); err != nil {
		slog.DebugContext(ctx, "session remove failed", "component", "nodesessions", "error", err, "session", sessionID)
	}
}

// Cleanup deletes all session keys for this node. Called on graceful shutdown.
func (tr *Tracker) Cleanup(ctx context.Context) {
	if tr.rdb == nil {
		return
	}
	tr.mu.Lock()
	ids := make([]string, 0, len(tr.sessions)+len(tr.ephemeral))
	for id := range tr.sessions {
		ids = append(ids, id)
	}
	for id := range tr.ephemeral {
		if _, dup := tr.sessions[id]; !dup {
			ids = append(ids, id)
		}
	}
	tr.nextEpoch++
	tr.sessions = make(map[string]sessionRefs)
	tr.ephemeral = make(map[string]time.Time)
	tr.touched = make(map[string]time.Time)
	tr.records = make(map[string]SessionInfo)
	tr.bytes = make(map[string]int64)
	tr.mu.Unlock()

	if len(ids) == 0 {
		return
	}

	pipe := tr.rdb.Pipeline()
	for _, id := range ids {
		pipe.Del(ctx, tr.redisKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.DebugContext(ctx, "session cleanup pipeline failed", "component", "nodesessions", "error", err)
	}
}

// StartRefresh starts a background goroutine that refreshes TTLs for all
// active sessions every 30 seconds. Stops when ctx is cancelled.
func (tr *Tracker) StartRefresh(ctx context.Context) {
	if tr.rdb == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(refreshInt)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tr.refreshAll(ctx)
			}
		}
	}()
}

func (tr *Tracker) refreshAll(ctx context.Context) {
	now := time.Now()
	type flush struct {
		id   string
		data []byte
	}
	tr.mu.Lock()
	ids := make([]string, 0, len(tr.sessions))
	for id := range tr.sessions {
		ids = append(ids, id)
	}
	for id, last := range tr.ephemeral {
		_, isSession := tr.sessions[id]
		if !isSession && now.Sub(last) > idleTTLFor(tr.records[id]) {
			// Idle ephemeral session: stop refreshing and let the Redis key
			// expire on its own. Session-backed entries (direct/remux) are pruned
			// on Remove, never by idle timeout, so a quiet-but-open pour stays live.
			delete(tr.ephemeral, id)
			delete(tr.touched, id)
			delete(tr.records, id)
			delete(tr.bytes, id)
			continue
		}
		if !isSession {
			ids = append(ids, id)
		}
	}
	flushes := make([]flush, 0, len(ids))
	for _, id := range ids {
		rec, ok := tr.records[id]
		if !ok {
			continue
		}
		data, err := json.Marshal(tr.enrichLocked(id, rec))
		if err != nil {
			continue
		}
		flushes = append(flushes, flush{id: id, data: data})
	}
	tr.mu.Unlock()

	if len(flushes) == 0 {
		return
	}

	// Re-SET (not just EXPIRE) so LastServedAt/BytesServed stay current in the
	// monitoring record. 30s cadence keeps this cheap.
	pipe := tr.rdb.Pipeline()
	for _, f := range flushes {
		pipe.Set(ctx, tr.redisKey(f.id), f.data, sessionTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.DebugContext(ctx, "session refresh pipeline failed", "component", "nodesessions", "error", err)
	}
}

func idleTTLFor(rec SessionInfo) time.Duration {
	if rec.Type == sessionTypeTranscode {
		return transcodeIdleTTL
	}
	return sessionTTL
}

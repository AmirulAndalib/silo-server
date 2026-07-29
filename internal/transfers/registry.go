// Package transfers tracks active download-class file pours in memory.
package transfers

import (
	"errors"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultMaxEntries   = 10_000
	fullWarningInterval = time.Minute
	maxDownloadIDLength = 256
	maxProfileIDLength  = 256
	maxRouteLength      = 64
	maxClientIPLength   = 128
	maxClientNameLength = 256
)

var (
	ErrRegistryFull = errors.New("transfer registry is full")
	ErrInvalidID    = errors.New("transfer id is required")
	ErrDuplicateID  = errors.New("transfer id is already active")
)

// Transfer describes one active HTTP file pour. DownloadID is correlation
// metadata only: ID uniquely identifies this request, including concurrent
// Range requests for the same download row.
type Transfer struct {
	ID           string    `json:"id"`
	DownloadID   string    `json:"download_id"`
	UserID       int       `json:"user_id"`
	ProfileID    string    `json:"profile_id"`
	MediaFileID  int       `json:"media_file_id"`
	Route        string    `json:"route"`
	ClientIP     string    `json:"client_ip"`
	ClientName   string    `json:"client_name"`
	StartedAt    time.Time `json:"started_at"`
	LastServedAt time.Time `json:"last_served_at"`
	BytesServed  int64     `json:"bytes_served"`
}

// Options customizes a Registry. Zero values use production defaults.
type Options struct {
	MaxEntries int
	Now        func() time.Time
}

// Registry is a bounded, process-local collection of active transfers.
type Registry struct {
	mu              sync.RWMutex
	items           map[string]Transfer
	maxEntries      int
	now             func() time.Time
	lastFullWarning time.Time
}

// New creates a Registry with production limits.
func New() *Registry {
	return NewWithOptions(Options{})
}

// NewWithOptions creates a Registry with explicit limits, primarily for tests.
func NewWithOptions(opts Options) *Registry {
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = defaultMaxEntries
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Registry{
		items:      make(map[string]Transfer),
		maxEntries: opts.MaxEntries,
		now:        opts.Now,
	}
}

// Begin records an active transfer. Strings derived from request metadata are
// normalized and clamped so the entry limit also provides a useful memory bound.
func (r *Registry) Begin(t Transfer) error {
	if r == nil {
		return nil
	}
	t.ID = strings.TrimSpace(t.ID)
	if t.ID == "" {
		return ErrInvalidID
	}
	t.DownloadID = clamp(t.DownloadID, maxDownloadIDLength)
	t.ProfileID = clamp(t.ProfileID, maxProfileIDLength)
	t.Route = clamp(t.Route, maxRouteLength)
	t.ClientIP = clamp(t.ClientIP, maxClientIPLength)
	t.ClientName = clamp(t.ClientName, maxClientNameLength)
	if t.StartedAt.IsZero() {
		t.StartedAt = r.now()
	}
	t.StartedAt = t.StartedAt.UTC()
	t.LastServedAt = t.StartedAt
	t.BytesServed = 0

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[t.ID]; exists {
		return ErrDuplicateID
	}
	if len(r.items) >= r.maxEntries {
		now := r.now()
		if r.lastFullWarning.IsZero() || now.Sub(r.lastFullWarning) >= fullWarningInterval {
			r.lastFullWarning = now
			slog.Warn("transfer registry full; serving without monitoring", "component", "transfers", "max_entries", r.maxEntries)
		}
		return ErrRegistryFull
	}
	r.items[t.ID] = t
	return nil
}

// AddServedBytes implements playback.ServedBytesRecorder. Updates for unknown
// transfers are deliberately discarded, and non-positive values are ignored.
// The metered writer reports in coarse chunks (currently 1 MiB and on Close),
// so LastServedAt is intentionally a coarse monitoring timestamp.
func (r *Registry) AddServedBytes(id string, n int64) error {
	if r == nil || n <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.items[id]
	if !ok {
		return nil
	}
	if n > math.MaxInt64-t.BytesServed {
		t.BytesServed = math.MaxInt64
	} else {
		t.BytesServed += n
	}
	t.LastServedAt = r.now().UTC()
	r.items[id] = t
	return nil
}

// End removes an active transfer. It is idempotent.
func (r *Registry) End(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.items, id)
	r.mu.Unlock()
}

// Snapshot returns a stable copy of the active transfers.
func (r *Registry) Snapshot() []Transfer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]Transfer, 0, len(r.items))
	for _, t := range r.items {
		out = append(out, t)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out
}

func clamp(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}

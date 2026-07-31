// Package transfers tracks active download-class file pours in memory.
package transfers

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultMaxEntries   = 10_000
	defaultMaxPerUser   = 24
	fullWarningInterval = time.Minute
	maxDownloadIDLength = 256
	maxProfileIDLength  = 256
	maxRouteLength      = 64
	maxClientIPLength   = 128
	maxClientNameLength = 256
)

var (
	ErrRegistryFull      = errors.New("transfer registry is full")
	ErrUserTransferLimit = errors.New("user transfer limit reached")
	ErrInvalidID         = errors.New("transfer id is required")
	ErrDuplicateID       = errors.New("transfer id is already active")
)

const MaxPerUserSetting = "playback.max_user_concurrent_transfers"

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
	MaxPerUser *int
	Now        func() time.Time
	Logger     *slog.Logger
}

type userTransferState struct {
	count       int
	lastWarning time.Time
}

// Registry is a bounded, process-local collection of active transfers.
type Registry struct {
	mu              sync.RWMutex
	items           map[string]Transfer
	perUser         map[int]userTransferState
	maxEntries      int
	maxPerUser      int
	now             func() time.Time
	logger          *slog.Logger
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
	maxPerUser := defaultMaxPerUser
	if opts.MaxPerUser != nil {
		maxPerUser = max(0, *opts.MaxPerUser)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Registry{
		items:      make(map[string]Transfer),
		perUser:    make(map[int]userTransferState),
		maxEntries: opts.MaxEntries,
		maxPerUser: maxPerUser,
		now:        opts.Now,
		logger:     opts.Logger,
	}
}

// ParseMaxPerUser parses the process-start setting. Empty input selects the
// production default; zero explicitly disables the per-user cap.
func ParseMaxPerUser(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultMaxPerUser, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > defaultMaxEntries {
		return 0, fmt.Errorf("%s must be an integer between 0 and %d", MaxPerUserSetting, defaultMaxEntries)
	}
	return value, nil
}

// MaxPerUser returns the configured per-user concurrent-transfer cap. Zero
// means unlimited.
func (r *Registry) MaxPerUser() int {
	if r == nil {
		return 0
	}
	return r.maxPerUser
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
	userState := r.perUser[t.UserID]
	if r.maxPerUser > 0 && userState.count >= r.maxPerUser {
		now := r.now()
		if userState.lastWarning.IsZero() || now.Sub(userState.lastWarning) >= fullWarningInterval {
			userState.lastWarning = now
			r.perUser[t.UserID] = userState
			r.logger.Warn("user transfer limit reached",
				"component", "transfers",
				"route", t.Route,
				"user_id", t.UserID,
				"max_per_user", r.maxPerUser,
			)
		}
		return ErrUserTransferLimit
	}
	if len(r.items) >= r.maxEntries {
		now := r.now()
		if r.lastFullWarning.IsZero() || now.Sub(r.lastFullWarning) >= fullWarningInterval {
			r.lastFullWarning = now
			r.logger.Warn("transfer registry full",
				"component", "transfers",
				"route", t.Route,
				"user_id", t.UserID,
				"max_entries", r.maxEntries,
			)
		}
		return ErrRegistryFull
	}
	r.items[t.ID] = t
	userState.count++
	r.perUser[t.UserID] = userState
	return nil
}

// Annotate adds correlation metadata after a handler-owned Begin. Unknown IDs
// are ignored so late resolution cannot create an unbounded registry entry.
func (r *Registry) Annotate(id, downloadID string, mediaFileID int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	t, ok := r.items[id]
	if ok {
		t.DownloadID = clamp(downloadID, maxDownloadIDLength)
		t.MediaFileID = mediaFileID
		r.items[id] = t
	}
	r.mu.Unlock()
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
	if t, ok := r.items[id]; ok {
		delete(r.items, id)
		state := r.perUser[t.UserID]
		state.count--
		if state.count <= 0 {
			delete(r.perUser, t.UserID)
		} else {
			r.perUser[t.UserID] = state
		}
	}
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

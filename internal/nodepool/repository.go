package nodepool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// NodeTypeProxy identifies a proxy stream node.
	NodeTypeProxy = "proxy"
	// NodeTypeTranscode identifies a transcode stream node.
	NodeTypeTranscode = "transcode"
)

// Node represents a stream node in the database.
type Node struct {
	ID               int        `json:"id"`
	Name             string     `json:"name"`
	Type             string     `json:"type"`
	URL              string     `json:"url"`
	Enabled          bool       `json:"enabled"`
	Healthy          bool       `json:"healthy"`
	ActiveJobs       int        `json:"active_jobs"`
	Group            *string    `json:"group"`              // co-location group; nil = ungrouped
	MaxJobs          *int       `json:"max_jobs"`           // concurrent job cap; nil = unlimited
	MaxBandwidthKbps *int       `json:"max_bandwidth_kbps"` // egress cap in kilobits/s; nil = unlimited
	EgressKbps       int        `json:"egress_kbps"`        // health-reported rolling egress average
	LastHealthCheck  *time.Time `json:"last_health_check"`
	CreatedAt        time.Time  `json:"created_at"`
	// Capabilities is the node's last stored capability report, verbatim as the
	// node served it. Kept opaque here: nodepool must not depend on playback,
	// and readers that need fields parse the ones they need.
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	// CapabilitiesHash identifies Capabilities. The health sweep compares it
	// against the hash a node reports to decide whether to refetch.
	CapabilitiesHash *string `json:"capabilities_hash,omitempty"`
	// CapabilitiesRefreshedAt is when Capabilities was last fetched — the age of
	// the inventory, not of the last health check.
	CapabilitiesRefreshedAt *time.Time `json:"capabilities_refreshed_at,omitempty"`
	// LastStats is the node's resource sample from the last health check —
	// {"system":…,"gpu":…} — kept opaque for the same reason as Capabilities.
	// It is written by the same 30s health update that writes ActiveJobs, so it
	// is exactly as fresh as LastHealthCheck and never fresher. Absent for a
	// node that reports no sample.
	LastStats json.RawMessage `json:"last_stats,omitempty"`
	// HWAccelOverride and HWDeviceOverride are this node's own acceleration
	// policy. nil means the node inherits the cluster-wide playback.hw_accel /
	// playback.hw_device settings, which is the normal case; a value here is
	// what the node itself resolves against once it has reloaded its config.
	HWAccelOverride  *string `json:"hw_accel_override,omitempty"`
	HWDeviceOverride *string `json:"hw_device_override,omitempty"`
}

// EffectiveHWAccel is the acceleration backend this node runs under: its own
// override when it carries one, and otherwise the cluster-wide setting passed
// in. It is what a dispatch path names in a start request so the request, the
// recipe card, and what the node actually runs agree.
//
// Deliberately not derived from the node's stored capability report: that
// report is a snapshot up to a capability-refresh interval old, and naming its
// resolved backend would pin a stale answer *and* suppress the node's own
// start-time resolution — a node honors a named backend verbatim, so "auto"
// has to survive this far to reach live device enumeration on the node.
func (n *Node) EffectiveHWAccel(clusterHWAccel string) string {
	if n == nil || n.HWAccelOverride == nil {
		return clusterHWAccel
	}
	if override := strings.TrimSpace(*n.HWAccelOverride); override != "" {
		return override
	}
	return clusterHWAccel
}

// CreateNodeInput holds the fields for creating a new node.
type CreateNodeInput struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	URL              string `json:"url"`
	Group            string `json:"group"`              // empty = ungrouped
	MaxJobs          *int   `json:"max_jobs"`           // nil or <= 0 = unlimited
	MaxBandwidthKbps *int   `json:"max_bandwidth_kbps"` // nil or <= 0 = unlimited
}

// Validate checks required fields and allowed values.
func (i CreateNodeInput) Validate() error {
	if i.Name == "" {
		return errors.New("name is required")
	}
	if i.Type != NodeTypeProxy && i.Type != NodeTypeTranscode {
		return fmt.Errorf("type must be %q or %q", NodeTypeProxy, NodeTypeTranscode)
	}
	if i.URL == "" {
		return errors.New("url is required")
	}
	return nil
}

// UpdateNodeInput holds the fields for updating a node.
// The optional fields distinguish "leave unchanged" (nil) from "clear":
// an empty-string Group clears the group, an empty-string HWAccelOverride or
// HWDeviceOverride restores inheritance of the cluster-wide setting, and a
// non-positive MaxJobs or MaxBandwidthKbps clears that cap.
type UpdateNodeInput struct {
	Name             *string `json:"name,omitempty"`
	URL              *string `json:"url,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty"`
	Group            *string `json:"group,omitempty"`
	MaxJobs          *int    `json:"max_jobs,omitempty"`
	MaxBandwidthKbps *int    `json:"max_bandwidth_kbps,omitempty"`
	HWAccelOverride  *string `json:"hw_accel_override,omitempty"`
	HWDeviceOverride *string `json:"hw_device_override,omitempty"`
}

// UnmarshalJSON decodes an update body, mapping an explicit JSON null on the
// two acceleration overrides onto the empty-string clear sentinel the rest of
// this type uses. Plain decoding leaves a *string nil for an omitted field and
// for an explicit null alike, which would silently turn "go back to inheriting
// the cluster setting" into a no-op. Every other field decodes normally.
func (i *UpdateNodeInput) UnmarshalJSON(data []byte) error {
	type plain UpdateNodeInput
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*i = UpdateNodeInput(decoded)
	if isJSONNull(raw["hw_accel_override"]) {
		i.HWAccelOverride = new(string)
	}
	if isJSONNull(raw["hw_device_override"]) {
		i.HWDeviceOverride = new(string)
	}
	return nil
}

// isJSONNull reports whether a field was present in the body with the literal
// value null (an absent field decodes to a nil RawMessage instead).
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// Validate checks the values an update may set. Only the acceleration override
// has a closed set of values; the database CHECK enforces the same list, and
// rejecting here turns a constraint violation into an operator-readable error.
func (i UpdateNodeInput) Validate() error {
	if i.HWAccelOverride == nil {
		return nil
	}
	value := normalizeHWAccelOverride(*i.HWAccelOverride)
	if value == nil {
		// The clear sentinel: inherit the cluster-wide setting again.
		return nil
	}
	if !slices.Contains(hwAccelOverrideValues, *value) {
		return fmt.Errorf("%w: hw_accel_override must be one of %s", ErrInvalidNodeInput,
			strings.Join(hwAccelOverrideValues, ", "))
	}
	return nil
}

// hwAccelOverrideValues mirrors the playback.hw_accel enum in
// internal/config/admin_settings.go and the CHECK constraint on
// stream_nodes.hw_accel_override: a per-node override may only name a backend
// the cluster-wide setting could also name.
var hwAccelOverrideValues = []string{hwAccelAuto, hwAccelQSV, hwAccelVAAPI, hwAccelNVENC, hwAccelNone}

const (
	// hwAccelAuto asks the node to resolve its own backend against live
	// hardware at session start; dispatch passes it through untouched for
	// exactly that reason.
	hwAccelAuto  = "auto"
	hwAccelQSV   = "qsv"
	hwAccelVAAPI = "vaapi"
	hwAccelNVENC = "nvenc"
	hwAccelNone  = "none"
)

// normalizeGroup trims a group label and converts empty to NULL.
func normalizeGroup(group string) *string {
	g := strings.TrimSpace(group)
	if g == "" {
		return nil
	}
	return &g
}

// normalizeOverride trims an override value and converts empty to NULL, which
// is how a node goes back to inheriting the cluster-wide setting. Case is
// preserved: a render device path is a filesystem path, not an enum.
func normalizeOverride(value string) *string {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	return &v
}

// normalizeHWAccelOverride is normalizeOverride for the acceleration enum,
// which is also lowercased. The cluster-wide playback.hw_accel accepts any
// casing (config.normalizeAdminEnum lowercases before comparing), and
// docs/admin-api.md promises the override takes the same values, so "QSV" from
// a third-party admin client must not be a 400 here when it is a 200 there.
func normalizeHWAccelOverride(value string) *string {
	return normalizeOverride(strings.ToLower(value))
}

// normalizeCap converts non-positive capacity values to NULL (unlimited).
func normalizeCap(v *int) *int {
	if v == nil || *v <= 0 {
		return nil
	}
	return v
}

// Repository provides CRUD operations for stream nodes.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new node repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const nodeColumns = `id, name, type, url, enabled, healthy, active_jobs, node_group, max_jobs, max_bandwidth_kbps, egress_kbps, last_health_check, created_at, capabilities, capabilities_hash, capabilities_refreshed_at, last_stats, hw_accel_override, hw_device_override`

func scanNode(row pgx.Row) (*Node, error) {
	var n Node
	// jsonb is scanned as raw bytes rather than into json.RawMessage directly so
	// a NULL column stays nil instead of decoding through the JSON codec.
	var capabilities, lastStats []byte
	err := row.Scan(
		&n.ID, &n.Name, &n.Type, &n.URL,
		&n.Enabled, &n.Healthy, &n.ActiveJobs,
		&n.Group, &n.MaxJobs,
		&n.MaxBandwidthKbps, &n.EgressKbps,
		&n.LastHealthCheck, &n.CreatedAt,
		&capabilities, &n.CapabilitiesHash, &n.CapabilitiesRefreshedAt,
		&lastStats,
		&n.HWAccelOverride, &n.HWDeviceOverride,
	)
	if err != nil {
		return nil, err
	}
	if len(capabilities) > 0 {
		n.Capabilities = json.RawMessage(capabilities)
	}
	if len(lastStats) > 0 {
		n.LastStats = json.RawMessage(lastStats)
	}
	return &n, nil
}

func scanNodes(rows pgx.Rows) ([]*Node, error) {
	var nodes []*Node
	for rows.Next() {
		// pgx.Rows satisfies pgx.Row, so both paths share one column list.
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// List returns all nodes ordered by type then name.
func (r *Repository) List(ctx context.Context) ([]*Node, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+nodeColumns+` FROM stream_nodes ORDER BY type, name`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// ListEnabled returns all enabled nodes of a given type.
func (r *Repository) ListEnabled(ctx context.Context, nodeType string) ([]*Node, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+nodeColumns+` FROM stream_nodes WHERE type = $1 AND enabled = true ORDER BY name`,
		nodeType)
	if err != nil {
		return nil, fmt.Errorf("list enabled nodes: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// GetByID returns a single node by ID.
func (r *Repository) GetByID(ctx context.Context, id int) (*Node, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+nodeColumns+` FROM stream_nodes WHERE id = $1`, id)
	n, err := scanNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return n, nil
}

// Create inserts a new node and returns it.
func (r *Repository) Create(ctx context.Context, input CreateNodeInput) (*Node, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	row := r.pool.QueryRow(ctx,
		`INSERT INTO stream_nodes (name, type, url, node_group, max_jobs, max_bandwidth_kbps)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+nodeColumns,
		input.Name, input.Type, input.URL, normalizeGroup(input.Group),
		normalizeCap(input.MaxJobs), normalizeCap(input.MaxBandwidthKbps))
	return scanNode(row)
}

// Update modifies a node's mutable fields. The optional fields use sentinel
// values to clear: an empty-string group, an empty-string acceleration
// override, and non-positive caps set the column to NULL (see UpdateNodeInput).
func (r *Repository) Update(ctx context.Context, id int, input UpdateNodeInput) (*Node, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	var group *string
	if input.Group != nil {
		group = normalizeGroup(*input.Group)
	}
	var maxJobs, maxBandwidth *int
	if input.MaxJobs != nil {
		maxJobs = normalizeCap(input.MaxJobs)
	}
	if input.MaxBandwidthKbps != nil {
		maxBandwidth = normalizeCap(input.MaxBandwidthKbps)
	}
	var hwAccelOverride, hwDeviceOverride *string
	if input.HWAccelOverride != nil {
		hwAccelOverride = normalizeHWAccelOverride(*input.HWAccelOverride)
	}
	if input.HWDeviceOverride != nil {
		hwDeviceOverride = normalizeOverride(*input.HWDeviceOverride)
	}
	row := r.pool.QueryRow(ctx,
		`UPDATE stream_nodes SET
			name = COALESCE($2, name),
			url = COALESCE($3, url),
			enabled = COALESCE($4, enabled),
			node_group = CASE WHEN $5::boolean THEN $6::text ELSE node_group END,
			max_jobs = CASE WHEN $7::boolean THEN $8::integer ELSE max_jobs END,
			max_bandwidth_kbps = CASE WHEN $9::boolean THEN $10::integer ELSE max_bandwidth_kbps END,
			hw_accel_override = CASE WHEN $11::boolean THEN $12::text ELSE hw_accel_override END,
			hw_device_override = CASE WHEN $13::boolean THEN $14::text ELSE hw_device_override END
		 WHERE id = $1
		 RETURNING `+nodeColumns,
		id, input.Name, input.URL, input.Enabled,
		input.Group != nil, group,
		input.MaxJobs != nil, maxJobs,
		input.MaxBandwidthKbps != nil, maxBandwidth,
		input.HWAccelOverride != nil, hwAccelOverride,
		input.HWDeviceOverride != nil, hwDeviceOverride)
	n, err := scanNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update node: %w", err)
	}
	return n, nil
}

// Delete removes a node by ID.
func (r *Repository) Delete(ctx context.Context, id int) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM stream_nodes WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNodeNotFound
	}
	return nil
}

// UpdateHealth updates a node's health status, active job count, reported
// egress bandwidth, and last resource sample.
//
// A nil lastStats writes NULL, which is what a node that reports no sample —
// an older build, or a non-Linux host — must produce. Passing the previous
// value through instead would leave a dead node's numbers on screen looking
// current.
func (r *Repository) UpdateHealth(ctx context.Context, id int, healthy bool, activeJobs, egressKbps int, lastStats []byte) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE stream_nodes SET healthy = $2, active_jobs = $3, egress_kbps = $4, last_stats = $5, last_health_check = NOW()
		 WHERE id = $1`,
		id, healthy, activeJobs, egressKbps, lastStats)
	if err != nil {
		return fmt.Errorf("update node health: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNodeNotFound
	}
	return nil
}

// UpdateCapabilities persists a freshly fetched capability report together with
// the hash that identifies it. The three columns are written in one statement
// so a reader never sees a payload beside a hash from a different report.
func (r *Repository) UpdateCapabilities(ctx context.Context, id int, capabilities []byte, hash string, refreshedAt time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE stream_nodes SET capabilities = $2, capabilities_hash = $3, capabilities_refreshed_at = $4
		 WHERE id = $1`,
		id, capabilities, hash, refreshedAt)
	if err != nil {
		return fmt.Errorf("update node capabilities: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNodeNotFound
	}
	return nil
}

// Sentinel errors.
var (
	ErrNodeNotFound = errors.New("stream node not found")
	// ErrInvalidNodeInput marks a caller-supplied value the store refuses, so
	// an API layer can answer 400 without string-matching the message.
	ErrInvalidNodeInput = errors.New("invalid node input")
)

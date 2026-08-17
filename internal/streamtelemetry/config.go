package streamtelemetry

import (
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	enabledEnv            = "SILO_STREAM_TELEMETRY_ENABLED"
	familiesEnv           = "SILO_STREAM_TELEMETRY_FAMILIES"
	sweepIntervalEnv      = "SILO_STREAM_TELEMETRY_SWEEP_INTERVAL"
	retentionEnv          = "SILO_STREAM_TELEMETRY_RETENTION"
	maxSessionsEnv        = "SILO_STREAM_TELEMETRY_MAX_SESSIONS"
	maxTransfersEnv       = "SILO_STREAM_TELEMETRY_MAX_TRANSFERS"
	maxObservationsEnv    = "SILO_STREAM_TELEMETRY_MAX_OBSERVATIONS"
	distributedEnv        = "SILO_STREAM_TELEMETRY_DISTRIBUTED"
	freshnessEnv          = "SILO_STREAM_TELEMETRY_FRESHNESS"
	membershipTTLEnv      = "SILO_STREAM_TELEMETRY_MEMBERSHIP_TTL"
	keyPrefixEnv          = "SILO_STREAM_TELEMETRY_KEY_PREFIX"
	fullResyncEveryEnv    = "SILO_STREAM_TELEMETRY_FULL_RESYNC_EVERY"
	maxPublishersEnv      = "SILO_STREAM_TELEMETRY_MAX_PUBLISHERS"
	maxMergedSessionsEnv  = "SILO_STREAM_TELEMETRY_MAX_MERGED_SESSIONS"
	maxMergedTransfersEnv = "SILO_STREAM_TELEMETRY_MAX_MERGED_TRANSFERS"
	viewTTLEnv            = "SILO_STREAM_TELEMETRY_VIEW_TTL"
)

// defaultObservedFamilies is the set observed when SILO_STREAM_TELEMETRY_FAMILIES
// is unset. It is deliberately NOT "every declared family": jellycompat and ABS
// share the API process with native, so defaulting them on would widen
// instrumentation across a live byte path on upgrade alone, which is exactly what
// §6's one-family-at-a-time rollout exists to prevent. Proxy and transcode node
// are separate processes, so their own SILO_STREAM_TELEMETRY_ENABLED already gates
// them and they stay in the default set. Name a family in the variable to observe
// it; move it in here once it has run in production, and delete this set when all
// five have.
var defaultObservedFamilies = map[Family]bool{
	FamilyNative:        true,
	FamilyProxy:         true,
	FamilyTranscodeNode: true,
}

type Config struct {
	Enabled        bool
	NodeID         string
	PublisherID    string
	PublisherEpoch int64
	Distributed    bool
	// Families narrows which route families are observed. Empty means
	// defaultObservedFamilies. It is a kill switch as much as a rollout control:
	// one misbehaving family can be dropped without losing all observation.
	Families map[Family]bool

	SweepInterval time.Duration
	Retention     time.Duration
	Freshness     time.Duration
	MembershipTTL time.Duration
	KeyPrefix     string
	// ViewTTL bounds how stale a served merged view may be. It gates a rebuild
	// that measured ~347 ms at the 50 000-session cap, so it is a cost control
	// rather than a freshness preference.
	ViewTTL            time.Duration
	FullResyncEvery    int
	MaxPublishers      int
	MaxMergedSessions  int
	MaxMergedTransfers int

	MaxSessions                    int64
	MaxTransfers                   int64
	MaxObservations                int64
	MaxObservationsPerSession      int
	MaxViewerIPsPerSession         int
	MaxIdentityConflictsPerSession int
	MaxDeviceIDsPerSession         int
	MaxClientVariantsPerSession    int
	MaxMediaFileIDsPerSession      int
	MaxPlayMethodsPerSession       int
	MaxTokenIssuedAtPerSession     int
	MaxRoutesPerSession            int
}

func DefaultConfig(nodeID string) Config {
	return Config{
		NodeID: nodeID, SweepInterval: time.Second, Retention: 5 * time.Minute,
		Freshness: 5 * time.Second, MembershipTTL: time.Minute, KeyPrefix: "silo:stelem",
		ViewTTL:         DefaultViewTTL,
		FullResyncEvery: 60, MaxPublishers: 256, MaxMergedSessions: 50_000, MaxMergedTransfers: 50_000,
		MaxSessions: 10_000, MaxTransfers: 10_000, MaxObservations: 50_000,
		MaxObservationsPerSession: 64, MaxViewerIPsPerSession: 32,
		MaxIdentityConflictsPerSession: 16, MaxDeviceIDsPerSession: 32,
		MaxClientVariantsPerSession: 16, MaxMediaFileIDsPerSession: 32,
		MaxPlayMethodsPerSession: 16, MaxTokenIssuedAtPerSession: 32,
		MaxRoutesPerSession: 32,
	}
}

// ConfigFromEnv returns a safe configuration. Invalid core settings disable
// telemetry; invalid distributed-only settings retain local telemetry.
func ConfigFromEnv(nodeID string) Config {
	cfg := DefaultConfig(nodeID)
	cfg.Enabled = envEnabled(os.Getenv(enabledEnv))
	coreInvalid := make([]string, 0)
	distributedInvalid := make([]string, 0)
	cfg.Distributed = envEnabled(os.Getenv(distributedEnv))
	parseDuration := func(name string, dst *time.Duration) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return
		}
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			coreInvalid = append(coreInvalid, name)
			return
		}
		*dst = parsed
	}
	parseDistributedDuration := func(name string, dst *time.Duration) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return
		}
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			distributedInvalid = append(distributedInvalid, name)
			return
		}
		*dst = parsed
	}
	parsePositive := func(name string, dst *int64) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			coreInvalid = append(coreInvalid, name)
			return
		}
		*dst = parsed
	}
	parseDistributedPositive := func(name string, dst *int) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			distributedInvalid = append(distributedInvalid, name)
			return
		}
		*dst = parsed
	}
	parseDuration(sweepIntervalEnv, &cfg.SweepInterval)
	parseDuration(retentionEnv, &cfg.Retention)
	parsePositive(maxSessionsEnv, &cfg.MaxSessions)
	parsePositive(maxTransfersEnv, &cfg.MaxTransfers)
	parsePositive(maxObservationsEnv, &cfg.MaxObservations)
	parseDistributedDuration(freshnessEnv, &cfg.Freshness)
	parseDistributedDuration(membershipTTLEnv, &cfg.MembershipTTL)
	parseDistributedDuration(viewTTLEnv, &cfg.ViewTTL)
	parseDistributedPositive(fullResyncEveryEnv, &cfg.FullResyncEvery)
	parseDistributedPositive(maxPublishersEnv, &cfg.MaxPublishers)
	parseDistributedPositive(maxMergedSessionsEnv, &cfg.MaxMergedSessions)
	parseDistributedPositive(maxMergedTransfersEnv, &cfg.MaxMergedTransfers)
	if value := strings.TrimSpace(os.Getenv(familiesEnv)); value != "" {
		if families, ok := parseFamilies(value); ok {
			cfg.Families = families
		} else {
			coreInvalid = append(coreInvalid, familiesEnv)
		}
	}
	if value := os.Getenv(keyPrefixEnv); value != "" {
		if strings.TrimSpace(value) == "" || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
			distributedInvalid = append(distributedInvalid, keyPrefixEnv)
		} else {
			cfg.KeyPrefix = value
		}
	}
	if cfg.SweepInterval > time.Duration(1<<63-1)/3 || cfg.Freshness < 3*cfg.SweepInterval {
		distributedInvalid = append(distributedInvalid, freshnessEnv)
	}
	if cfg.MembershipTTL <= cfg.Freshness {
		distributedInvalid = append(distributedInvalid, membershipTTLEnv)
	}
	if cfg.MembershipTTL > time.Duration(1<<63-1)/10 {
		distributedInvalid = append(distributedInvalid, membershipTTLEnv)
	}
	if len(coreInvalid) > 0 {
		if cfg.Enabled {
			cfg.Enabled = false
			slog.Error("stream telemetry disabled because configuration is invalid", "variables", strings.Join(coreInvalid, ","))
		} else {
			slog.Warn("ignoring invalid disabled stream telemetry configuration", "variables", strings.Join(coreInvalid, ","))
		}
	}
	if len(distributedInvalid) > 0 {
		if cfg.Distributed {
			cfg.Distributed = false
			slog.Error("stream telemetry distributed mode disabled because configuration is invalid", "variables", strings.Join(distributedInvalid, ","))
		} else {
			slog.Warn("ignoring invalid distributed stream telemetry configuration", "variables", strings.Join(distributedInvalid, ","))
		}
	}
	return cfg
}

// ObservesFamily reports whether routes in this family are wrapped. It is read
// once per route at mount time, never on the hot path.
func (c Config) ObservesFamily(family Family) bool {
	if len(c.Families) == 0 {
		return defaultObservedFamilies[family]
	}
	return c.Families[family]
}

// ObservedFamilies lists the observed families in a stable order, for the
// startup log that makes the resolved set visible.
func (c Config) ObservedFamilies() []string {
	set := c.Families
	if len(set) == 0 {
		set = defaultObservedFamilies
	}
	names := make([]string, 0, len(set))
	for family, observed := range set {
		if observed {
			names = append(names, string(family))
		}
	}
	sort.Strings(names)
	return names
}

// parseFamilies decodes the comma-separated family list. An unrecognized name is
// a core-invalid setting rather than a distributed-only one: a typo that silently
// observed nothing would be worse than no telemetry at all.
func parseFamilies(value string) (map[Family]bool, bool) {
	families := make(map[Family]bool)
	for _, entry := range strings.Split(value, ",") {
		name := strings.ToLower(strings.TrimSpace(entry))
		if name == "" {
			continue
		}
		family := Family(name)
		switch family {
		case FamilyNative, FamilyJellycompat, FamilyProxy, FamilyABS, FamilyTranscodeNode:
			families[family] = true
		default:
			return nil, false
		}
	}
	return families, true
}

func envEnabled(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

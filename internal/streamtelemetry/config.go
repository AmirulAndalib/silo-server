package streamtelemetry

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	enabledEnv            = "SILO_STREAM_TELEMETRY_ENABLED"
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
)

type Config struct {
	Enabled        bool
	NodeID         string
	PublisherID    string
	PublisherEpoch int64
	Distributed    bool

	SweepInterval      time.Duration
	Retention          time.Duration
	Freshness          time.Duration
	MembershipTTL      time.Duration
	KeyPrefix          string
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
	parseDistributedPositive(fullResyncEveryEnv, &cfg.FullResyncEvery)
	parseDistributedPositive(maxPublishersEnv, &cfg.MaxPublishers)
	parseDistributedPositive(maxMergedSessionsEnv, &cfg.MaxMergedSessions)
	parseDistributedPositive(maxMergedTransfersEnv, &cfg.MaxMergedTransfers)
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

func envEnabled(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

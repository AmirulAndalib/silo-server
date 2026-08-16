package streamtelemetry

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	enabledEnv         = "SILO_STREAM_TELEMETRY_ENABLED"
	sweepIntervalEnv   = "SILO_STREAM_TELEMETRY_SWEEP_INTERVAL"
	retentionEnv       = "SILO_STREAM_TELEMETRY_RETENTION"
	maxSessionsEnv     = "SILO_STREAM_TELEMETRY_MAX_SESSIONS"
	maxTransfersEnv    = "SILO_STREAM_TELEMETRY_MAX_TRANSFERS"
	maxObservationsEnv = "SILO_STREAM_TELEMETRY_MAX_OBSERVATIONS"
)

type Config struct {
	Enabled     bool
	NodeID      string
	PublisherID string

	SweepInterval time.Duration
	Retention     time.Duration

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
		MaxSessions: 10_000, MaxTransfers: 10_000, MaxObservations: 50_000,
		MaxObservationsPerSession: 64, MaxViewerIPsPerSession: 32,
		MaxIdentityConflictsPerSession: 16, MaxDeviceIDsPerSession: 32,
		MaxClientVariantsPerSession: 16, MaxMediaFileIDsPerSession: 32,
		MaxPlayMethodsPerSession: 16, MaxTokenIssuedAtPerSession: 32,
		MaxRoutesPerSession: 32,
	}
}

// ConfigFromEnv returns a safe configuration. Invalid telemetry settings are
// ignored while disabled; while enabled they disable telemetry and are logged.
func ConfigFromEnv(nodeID string) Config {
	cfg := DefaultConfig(nodeID)
	cfg.Enabled = envEnabled(os.Getenv(enabledEnv))
	invalid := make([]string, 0)
	parseDuration := func(name string, dst *time.Duration) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return
		}
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			invalid = append(invalid, name)
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
			invalid = append(invalid, name)
			return
		}
		*dst = parsed
	}
	parseDuration(sweepIntervalEnv, &cfg.SweepInterval)
	parseDuration(retentionEnv, &cfg.Retention)
	parsePositive(maxSessionsEnv, &cfg.MaxSessions)
	parsePositive(maxTransfersEnv, &cfg.MaxTransfers)
	parsePositive(maxObservationsEnv, &cfg.MaxObservations)
	if len(invalid) > 0 {
		if cfg.Enabled {
			cfg.Enabled = false
			slog.Error("stream telemetry disabled because configuration is invalid", "variables", strings.Join(invalid, ","))
		} else {
			slog.Warn("ignoring invalid disabled stream telemetry configuration", "variables", strings.Join(invalid, ","))
		}
	}
	return cfg
}

func envEnabled(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

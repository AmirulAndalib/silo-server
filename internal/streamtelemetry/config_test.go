package streamtelemetry

import (
	"testing"
	"time"
)

func TestConfigFromEnvValidation(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		clearConfigEnv(t)
		cfg := ConfigFromEnv("node")
		if cfg.Enabled || cfg.SweepInterval != time.Second || cfg.Retention != 5*time.Minute || cfg.MaxObservations != 50_000 {
			t.Fatalf("defaults = %+v", cfg)
		}
	})
	t.Run("valid enabled overrides", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(sweepIntervalEnv, "250ms")
		t.Setenv(retentionEnv, "6m")
		t.Setenv(maxSessionsEnv, "12")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled || cfg.SweepInterval != 250*time.Millisecond || cfg.Retention != 6*time.Minute || cfg.MaxSessions != 12 {
			t.Fatalf("overrides = %+v", cfg)
		}
	})
	t.Run("invalid enabled disables", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(sweepIntervalEnv, "0s")
		if cfg := ConfigFromEnv("node"); cfg.Enabled {
			t.Fatalf("invalid config remained enabled: %+v", cfg)
		}
	})
	t.Run("invalid disabled is ignored", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(maxTransfersEnv, "not-a-number")
		cfg := ConfigFromEnv("node")
		if cfg.Enabled || cfg.MaxTransfers != 10_000 {
			t.Fatalf("disabled invalid config = %+v", cfg)
		}
	})
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{enabledEnv, sweepIntervalEnv, retentionEnv, maxSessionsEnv, maxTransfersEnv, maxObservationsEnv} {
		t.Setenv(name, "")
	}
}

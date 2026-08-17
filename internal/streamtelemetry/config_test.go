package streamtelemetry

import (
	"testing"
	"time"
)

func TestConfigFromEnvValidation(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		clearConfigEnv(t)
		cfg := ConfigFromEnv("node")
		if cfg.Enabled || cfg.Distributed || cfg.SweepInterval != time.Second || cfg.Retention != 5*time.Minute || cfg.MaxObservations != 50_000 ||
			cfg.Freshness != 5*time.Second || cfg.MembershipTTL != time.Minute || cfg.KeyPrefix != "silo:stelem" || cfg.FullResyncEvery != 60 || cfg.MaxPublishers != 256 || cfg.MaxMergedSessions != 50_000 || cfg.MaxMergedTransfers != 50_000 {
			t.Fatalf("defaults = %+v", cfg)
		}
	})
	t.Run("valid distributed overrides", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(distributedEnv, "true")
		t.Setenv(sweepIntervalEnv, "2s")
		t.Setenv(freshnessEnv, "7s")
		t.Setenv(membershipTTLEnv, "20s")
		t.Setenv(keyPrefixEnv, "custom:telemetry")
		t.Setenv(fullResyncEveryEnv, "7")
		t.Setenv(maxPublishersEnv, "8")
		t.Setenv(maxMergedSessionsEnv, "9")
		t.Setenv(maxMergedTransfersEnv, "10")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled || !cfg.Distributed || cfg.Freshness != 7*time.Second || cfg.MembershipTTL != 20*time.Second || cfg.KeyPrefix != "custom:telemetry" || cfg.FullResyncEvery != 7 || cfg.MaxPublishers != 8 || cfg.MaxMergedSessions != 9 || cfg.MaxMergedTransfers != 10 {
			t.Fatalf("distributed overrides = %+v", cfg)
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
	for name, variable := range map[string]string{
		"freshness": freshnessEnv, "membership ttl": membershipTTLEnv, "full resync": fullResyncEveryEnv,
		"max publishers": maxPublishersEnv, "max sessions": maxMergedSessionsEnv, "max transfers": maxMergedTransfersEnv,
	} {
		t.Run("invalid distributed "+name+" falls back local", func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(enabledEnv, "true")
			t.Setenv(distributedEnv, "true")
			t.Setenv(variable, "invalid")
			cfg := ConfigFromEnv("node")
			if !cfg.Enabled || cfg.Distributed {
				t.Fatalf("invalid distributed config = %+v", cfg)
			}
		})
	}
	t.Run("invalid distributed while disabled warns and stays disabled", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(maxPublishersEnv, "0")
		cfg := ConfigFromEnv("node")
		if cfg.Enabled || cfg.Distributed {
			t.Fatalf("disabled config = %+v", cfg)
		}
	})
	t.Run("freshness below three sweeps", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(distributedEnv, "true")
		t.Setenv(sweepIntervalEnv, "2s")
		t.Setenv(freshnessEnv, "5s")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled || cfg.Distributed {
			t.Fatalf("config = %+v", cfg)
		}
	})
	t.Run("membership not above freshness", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(distributedEnv, "true")
		t.Setenv(freshnessEnv, "10s")
		t.Setenv(membershipTTLEnv, "10s")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled || cfg.Distributed {
			t.Fatalf("config = %+v", cfg)
		}
	})
	t.Run("whitespace prefix", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(distributedEnv, "true")
		t.Setenv(keyPrefixEnv, "bad prefix")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled || cfg.Distributed {
			t.Fatalf("config = %+v", cfg)
		}
	})
	t.Run("membership expiry overflow", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(distributedEnv, "true")
		t.Setenv(membershipTTLEnv, "2562047h47m16s")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled || cfg.Distributed {
			t.Fatalf("config = %+v", cfg)
		}
	})
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{enabledEnv, sweepIntervalEnv, retentionEnv, maxSessionsEnv, maxTransfersEnv, maxObservationsEnv,
		distributedEnv, freshnessEnv, membershipTTLEnv, keyPrefixEnv, fullResyncEveryEnv, maxPublishersEnv, maxMergedSessionsEnv, maxMergedTransfersEnv,
		familiesEnv} {
		t.Setenv(name, "")
	}
}

// The family gate is what makes a shared-process family (jellycompat, ABS) safe
// to enroll: it is both the staged-rollout control §6 asks for and a kill switch
// for one misbehaving family that keeps the rest observing.
func TestConfigFamilyGate(t *testing.T) {
	t.Run("unset observes the shipped set only", func(t *testing.T) {
		clearConfigEnv(t)
		cfg := ConfigFromEnv("node")
		if len(cfg.Families) != 0 {
			t.Fatalf("families = %+v, want unset", cfg.Families)
		}
		for _, family := range []Family{FamilyNative, FamilyProxy, FamilyTranscodeNode} {
			if !cfg.ObservesFamily(family) {
				t.Fatalf("%s not observed by default", family)
			}
		}
		// Widening the default would instrument two more live byte paths in the
		// API process on upgrade alone. That has to be an explicit opt-in.
		for _, family := range []Family{FamilyJellycompat, FamilyABS} {
			if cfg.ObservesFamily(family) {
				t.Fatalf("%s observed by default", family)
			}
		}
	})
	t.Run("explicit list narrows and widens", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(familiesEnv, " Native , jellycompat ,, ABS ")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled {
			t.Fatalf("config = %+v", cfg)
		}
		for _, family := range []Family{FamilyNative, FamilyJellycompat, FamilyABS} {
			if !cfg.ObservesFamily(family) {
				t.Fatalf("%s not observed", family)
			}
		}
		for _, family := range []Family{FamilyProxy, FamilyTranscodeNode} {
			if cfg.ObservesFamily(family) {
				t.Fatalf("%s observed despite an explicit list omitting it", family)
			}
		}
		if got := cfg.ObservedFamilies(); len(got) != 3 || got[0] != "abs" || got[1] != "jellycompat" || got[2] != "native" {
			t.Fatalf("observed families = %v", got)
		}
	})
	t.Run("unknown family disables telemetry", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(familiesEnv, "native,not_a_family")
		cfg := ConfigFromEnv("node")
		if cfg.Enabled {
			t.Fatal("a typo in the family list must disable telemetry rather than silently observe nothing")
		}
	})
	t.Run("only whitespace falls back to the default set", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv(enabledEnv, "true")
		t.Setenv(familiesEnv, "  ,  ")
		cfg := ConfigFromEnv("node")
		if !cfg.Enabled || !cfg.ObservesFamily(FamilyNative) || cfg.ObservesFamily(FamilyABS) {
			t.Fatalf("config = %+v", cfg)
		}
	})
}

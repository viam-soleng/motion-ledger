package motionledger

import (
	"testing"
)

// TestApplyDefaults_EmptyConfig confirms that fields left zero/empty after
// the RDK's rawConf → NativeConfig re-parse get sensible defaults. This is
// the contract NewLedger relies on so that WriteAtomic never receives an
// empty path (regression guard for the
// `rename .tmp : no such file or directory` bug).
func TestApplyDefaults_EmptyConfig(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if cfg.LedgerPath != defaultLedgerPath {
		t.Errorf("LedgerPath: got %q, want %q", cfg.LedgerPath, defaultLedgerPath)
	}
	if cfg.RetentionHours != defaultRetentionHours {
		t.Errorf("RetentionHours: got %d, want %d", cfg.RetentionHours, defaultRetentionHours)
	}
	// PollIntervalSeconds intentionally has no default — 0 means "off".
	if cfg.PollIntervalSeconds != 0 {
		t.Errorf("PollIntervalSeconds: got %d, want 0", cfg.PollIntervalSeconds)
	}
}

// TestApplyDefaults_PreservesExplicitValues confirms that defaults do not
// stomp on values the operator explicitly set in the JSON config.
func TestApplyDefaults_PreservesExplicitValues(t *testing.T) {
	cfg := &Config{
		LedgerPath:          "/tmp/custom.json",
		RetentionHours:      12,
		PollIntervalSeconds: 30,
	}
	applyDefaults(cfg)

	if cfg.LedgerPath != "/tmp/custom.json" {
		t.Errorf("LedgerPath was overwritten: got %q", cfg.LedgerPath)
	}
	if cfg.RetentionHours != 12 {
		t.Errorf("RetentionHours was overwritten: got %d", cfg.RetentionHours)
	}
	if cfg.PollIntervalSeconds != 30 {
		t.Errorf("PollIntervalSeconds was overwritten: got %d", cfg.PollIntervalSeconds)
	}
}

// TestValidate_RejectsNegativePollInterval guards against accidental
// "disable polling by setting a negative number" usage; 0 is the off switch.
func TestValidate_RejectsNegativePollInterval(t *testing.T) {
	cfg := &Config{
		PollIntervalSeconds: -1,
		MotionDetectors: []DetectorConfig{
			{Name: "vision-1", Camera: "camera-1"},
		},
	}
	if _, _, err := cfg.Validate("components.0"); err == nil {
		t.Fatal("expected error for negative poll_interval_seconds, got nil")
	}
}

// TestValidate_AcceptsZeroPollInterval confirms that 0 (the default,
// meaning "no internal polling") passes validation.
func TestValidate_AcceptsZeroPollInterval(t *testing.T) {
	cfg := &Config{
		PollIntervalSeconds: 0,
		MotionDetectors: []DetectorConfig{
			{Name: "vision-1", Camera: "camera-1"},
		},
	}
	required, _, err := cfg.Validate("components.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(required) != 1 || required[0] != "vision-1" {
		t.Errorf("required deps: got %v, want [vision-1]", required)
	}
}

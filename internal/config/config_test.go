package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/karabulut18/scale-guard/internal/config"
)

// Helper: save and restore all env vars we care about.
func saveAndRestore(t *testing.T, vars ...string) (restore func()) {
	saved := make(map[string]string)
	for _, v := range vars {
		saved[v] = os.Getenv(v)
	}
	return func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}
}

// TestLoad_RequiredField: DatabaseURL is required.
func TestLoad_RequiredField(t *testing.T) {
	defer saveAndRestore(t, "SCALE_GUARD_DB_URL")()
	os.Unsetenv("SCALE_GUARD_DB_URL")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when SCALE_GUARD_DB_URL is missing")
	}
}

// TestLoad_Defaults: unset optional fields should use sensible defaults.
func TestLoad_Defaults(t *testing.T) {
	vars := []string{
		"SCALE_GUARD_DB_URL", "SCALE_GUARD_GRPC_PORT", "SCALE_GUARD_HEALTH_PORT",
		"SCALE_GUARD_FLUSH_INTERVAL", "SCALE_GUARD_REFRESH_INTERVAL", "SCALE_GUARD_LOG_LEVEL",
	}
	defer saveAndRestore(t, vars...)()

	os.Setenv("SCALE_GUARD_DB_URL", "postgres://localhost/test")
	for _, v := range vars[1:] {
		os.Unsetenv(v)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected success with defaults, got error: %v", err)
	}

	if cfg.GRPCPort != 50051 {
		t.Errorf("expected GRPCPort=50051, got %d", cfg.GRPCPort)
	}
	if cfg.HealthPort != 8080 {
		t.Errorf("expected HealthPort=8080, got %d", cfg.HealthPort)
	}
	if cfg.FlushInterval != 100*time.Millisecond {
		t.Errorf("expected FlushInterval=100ms, got %v", cfg.FlushInterval)
	}
	if cfg.RefreshInterval != 30*time.Second {
		t.Errorf("expected RefreshInterval=30s, got %v", cfg.RefreshInterval)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel=info, got %s", cfg.LogLevel)
	}
}

// TestLoad_CustomValues: environment variables override defaults.
func TestLoad_CustomValues(t *testing.T) {
	vars := []string{
		"SCALE_GUARD_DB_URL", "SCALE_GUARD_GRPC_PORT", "SCALE_GUARD_HEALTH_PORT",
		"SCALE_GUARD_FLUSH_INTERVAL", "SCALE_GUARD_REFRESH_INTERVAL", "SCALE_GUARD_LOG_LEVEL",
		"SCALE_GUARD_INSTANCE_ID",
	}
	defer saveAndRestore(t, vars...)()

	os.Setenv("SCALE_GUARD_DB_URL", "postgres://custom/db")
	os.Setenv("SCALE_GUARD_GRPC_PORT", "9000")
	os.Setenv("SCALE_GUARD_HEALTH_PORT", "9001")
	os.Setenv("SCALE_GUARD_FLUSH_INTERVAL", "50ms")
	os.Setenv("SCALE_GUARD_REFRESH_INTERVAL", "60s")
	os.Setenv("SCALE_GUARD_LOG_LEVEL", "debug")
	os.Setenv("SCALE_GUARD_INSTANCE_ID", "test-instance-1")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://custom/db" {
		t.Errorf("expected custom DB URL, got %s", cfg.DatabaseURL)
	}
	if cfg.GRPCPort != 9000 {
		t.Errorf("expected GRPCPort=9000, got %d", cfg.GRPCPort)
	}
	if cfg.HealthPort != 9001 {
		t.Errorf("expected HealthPort=9001, got %d", cfg.HealthPort)
	}
	if cfg.FlushInterval != 50*time.Millisecond {
		t.Errorf("expected FlushInterval=50ms, got %v", cfg.FlushInterval)
	}
	if cfg.RefreshInterval != 60*time.Second {
		t.Errorf("expected RefreshInterval=60s, got %v", cfg.RefreshInterval)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel=debug, got %s", cfg.LogLevel)
	}
	if cfg.InstanceID != "test-instance-1" {
		t.Errorf("expected InstanceID=test-instance-1, got %s", cfg.InstanceID)
	}
}

// TestLoad_FlushIntervalTooSmall: reject flush intervals < 10ms.
func TestLoad_FlushIntervalTooSmall(t *testing.T) {
	defer saveAndRestore(t, "SCALE_GUARD_DB_URL", "SCALE_GUARD_FLUSH_INTERVAL")()

	os.Setenv("SCALE_GUARD_DB_URL", "postgres://localhost/test")
	os.Setenv("SCALE_GUARD_FLUSH_INTERVAL", "5ms")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for flush interval < 10ms")
	}
}

// TestLoad_RefreshIntervalTooSmall: reject refresh intervals < 1s.
func TestLoad_RefreshIntervalTooSmall(t *testing.T) {
	defer saveAndRestore(t, "SCALE_GUARD_DB_URL", "SCALE_GUARD_REFRESH_INTERVAL")()

	os.Setenv("SCALE_GUARD_DB_URL", "postgres://localhost/test")
	os.Setenv("SCALE_GUARD_REFRESH_INTERVAL", "500ms")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for refresh interval < 1s")
	}
}

// TestLoad_InvalidPortFallsBackToDefault: malformed port falls back to default.
func TestLoad_InvalidPortFallsBackToDefault(t *testing.T) {
	vars := []string{
		"SCALE_GUARD_DB_URL", "SCALE_GUARD_GRPC_PORT", "SCALE_GUARD_FLUSH_INTERVAL", "SCALE_GUARD_REFRESH_INTERVAL",
	}
	defer saveAndRestore(t, vars...)()

	os.Setenv("SCALE_GUARD_DB_URL", "postgres://localhost/test")
	os.Unsetenv("SCALE_GUARD_FLUSH_INTERVAL")
	os.Unsetenv("SCALE_GUARD_REFRESH_INTERVAL")
	os.Setenv("SCALE_GUARD_GRPC_PORT", "not_a_number")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected fallback to default, got error: %v", err)
	}
	if cfg.GRPCPort != 50051 {
		t.Errorf("expected fallback to default (50051), got %d", cfg.GRPCPort)
	}
}

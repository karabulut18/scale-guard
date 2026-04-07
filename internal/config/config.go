package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all service configuration loaded from environment variables.
type Config struct {
	// Database connection string. Required.
	// Example: postgres://user:password@localhost:5432/scale_guard
	DatabaseURL string

	// gRPC server port. Default: 50051.
	GRPCPort int

	// Health check HTTP port (separate from gRPC). Default: 8080.
	HealthPort int

	// Write-behind flush interval. Default: 100ms.
	// The flusher goroutine batches dirty bucket states and writes to PostgreSQL
	// at this interval. Lower = more up-to-date persistence and higher DB load.
	FlushInterval time.Duration

	// Config refresh interval. Default: 30s.
	// The config-watcher goroutine re-reads rate limit configs from PostgreSQL
	// at this interval. This picks up dynamic limit changes without restart.
	RefreshInterval time.Duration

	// Unique instance identifier for logging and metrics.
	// Default: hostname + random suffix. Can be set explicitly for testing.
	InstanceID string

	// Log level: debug, info, warn, error. Default: info.
	LogLevel string
}

// Load reads environment variables and returns a validated Config.
// Missing required fields or unparseable values return an error.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:     os.Getenv("SCALE_GUARD_DB_URL"),
		GRPCPort:        parseIntEnv("SCALE_GUARD_GRPC_PORT", 50051),
		HealthPort:      parseIntEnv("SCALE_GUARD_HEALTH_PORT", 8080),
		FlushInterval:   parseDurationEnv("SCALE_GUARD_FLUSH_INTERVAL", 100*time.Millisecond),
		RefreshInterval: parseDurationEnv("SCALE_GUARD_REFRESH_INTERVAL", 30*time.Second),
		InstanceID:      os.Getenv("SCALE_GUARD_INSTANCE_ID"),
		LogLevel:        getEnvOrDefault("SCALE_GUARD_LOG_LEVEL", "info"),
	}

	// Validation
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("SCALE_GUARD_DB_URL is required")
	}
	if cfg.InstanceID == "" {
		// TODO: generate from hostname + random suffix when integrated with logging
		cfg.InstanceID = "scale-guard-local"
	}
	if cfg.FlushInterval < 10*time.Millisecond {
		return nil, fmt.Errorf("SCALE_GUARD_FLUSH_INTERVAL must be >= 10ms, got %v", cfg.FlushInterval)
	}
	if cfg.RefreshInterval < 1*time.Second {
		return nil, fmt.Errorf("SCALE_GUARD_REFRESH_INTERVAL must be >= 1s, got %v", cfg.RefreshInterval)
	}

	return cfg, nil
}

// Helper functions
func parseIntEnv(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		// Fall back to default on parse error; caller should validate if strict checking is needed
		return defaultVal
	}
	return parsed
}

func parseDurationEnv(key string, defaultVal time.Duration) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	// Try parsing as a duration string first (e.g., "100ms", "30s")
	parsed, err := time.ParseDuration(val)
	if err == nil {
		return parsed
	}
	// Fall back to default on parse error
	return defaultVal
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

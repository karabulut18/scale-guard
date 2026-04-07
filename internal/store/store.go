// Package store defines the database layer interface and its PostgreSQL implementation.
//
// The Store interface abstracts all database operations so that:
//  1. The limiter package doesn't depend directly on PostgreSQL (easier to test with a mock).
//  2. The hot path (CheckLimit) never touches the store — only background goroutines do.
//
// The PostgreSQL implementation is in postgres.go.
package store

import (
	"context"
	"time"
)

// ClientConfig represents a single rate-limit configuration for a (tenant, client) pair.
// Loaded from the rate_limit_configs table at startup and periodically refreshed.
type ClientConfig struct {
	TenantID   string
	ClientID   string
	Capacity   float64
	RefillRate float64
}

// BucketState represents a single bucket's persisted state to write to PostgreSQL.
// Used by the write-behind flusher every 100ms.
type BucketState struct {
	TenantID     string
	ClientID     string
	Tokens       float64
	LastRefillAt time.Time
}

// Store is the database abstraction. Implementations may be PostgreSQL, in-memory (for testing), etc.
type Store interface {
	// LoadConfigs loads all active configurations for a given tenant.
	// Called on startup and every 30 seconds by the config-watcher goroutine.
	// Returns a slice of configurations, or an error if the query fails.
	LoadConfigs(ctx context.Context, tenantID string) ([]*ClientConfig, error)

	// LoadBucketStates loads the last persisted token counts for all clients of a tenant.
	// Called once at startup so instances restore their previous state instead of
	// giving every client a fresh full bucket after a restart.
	LoadBucketStates(ctx context.Context, tenantID string) ([]*BucketState, error)

	// SaveBucketState upserts a batch of bucket states to the database.
	// Called every 100ms by the write-behind flusher.
	// Uses the ON CONFLICT pattern from the schema to handle both inserts and updates.
	SaveBucketState(ctx context.Context, states []*BucketState) error

	// Health checks the database connection and returns nil if healthy, or an error otherwise.
	Health(ctx context.Context) error

	// Close closes the database connection. Called on service shutdown.
	Close() error
}

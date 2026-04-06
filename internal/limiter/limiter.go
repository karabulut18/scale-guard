// Package limiter orchestrates the full rate-limiting lifecycle:
//   - Holds all in-memory token buckets in a sync.Map (tenant+client → *bucket.TokenBucket)
//   - Runs the config-watcher goroutine (refreshes limits from PostgreSQL every 30s)
//   - Runs the write-behind flusher goroutine (persists dirty buckets to PostgreSQL every 100ms)
//   - Implements the fail-open degraded mode when the DB is unreachable
//
// This package is Phase 2 — implemented after the store interface is defined.
package limiter

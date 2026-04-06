// Package store defines the database interface and its PostgreSQL implementation.
//
// The interface exists so the limiter package can be tested without a real DB
// (swap in a fake store). The real implementation uses pgx/v5.
//
// This package is Phase 2 — implemented after the bucket package is complete.
package store

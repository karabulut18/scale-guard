// Package bucket implements a thread-safe in-memory token bucket.
//
// This is the hot-path core of scale-guard. Every CheckRateLimit call
// hits this code and nothing else — no DB, no network, no allocations
// after startup.
//
// Concurrency model:
//   - Each bucket carries its own sync.Mutex. This is NOT a global mutex;
//     lock contention is isolated to a single (tenant, client) pair.
//     Two different clients never contend with each other.
//   - The dirty flag uses atomic.Bool so the write-behind flusher can
//     scan all buckets for dirtiness without acquiring any bucket lock.
//
// This mirrors the C++ per-shard lock pattern: instead of one global lock
// that serializes everything, you have N fine-grained locks with zero
// cross-client contention.
package bucket

import (
	"sync"
	"sync/atomic"
	"time"
)

// TokenBucket is a single rate-limit identity's token bucket.
// Zero value is not valid — use New or NewWithState.
type TokenBucket struct {
	mu           sync.Mutex
	tokens       float64
	lastRefillAt time.Time
	capacity     float64
	refillRate   float64 // tokens per second

	// dirty is set true by Allow() and cleared by the flusher via Snapshot().
	// atomic.Bool avoids acquiring mu just to check if a flush is needed.
	dirty atomic.Bool
}

// New creates a TokenBucket starting at full capacity.
// capacity  = max tokens the bucket can hold (burst ceiling).
// refillRate = tokens added per second (sustained throughput ceiling).
func New(capacity, refillRate float64) *TokenBucket {
	return &TokenBucket{
		tokens:       capacity,
		lastRefillAt: time.Now(),
		capacity:     capacity,
		refillRate:   refillRate,
	}
}

// NewWithState restores a TokenBucket from persisted state loaded on startup.
// It immediately applies the refill for time elapsed since lastRefillAt,
// so the first incoming request sees an accurate token count.
func NewWithState(capacity, refillRate, tokens float64, lastRefillAt time.Time) *TokenBucket {
	tb := &TokenBucket{
		tokens:       tokens,
		lastRefillAt: lastRefillAt,
		capacity:     capacity,
		refillRate:   refillRate,
	}
	tb.mu.Lock()
	tb.refill() // catch up on elapsed time before first request
	tb.mu.Unlock()
	return tb
}

// Allow consumes one token and reports whether the request is permitted.
// This is the only method called on the hot path. It is safe for concurrent use.
func (b *TokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refill()

	if b.tokens < 1.0 {
		return false
	}
	b.tokens--
	b.dirty.Store(true)
	return true
}

// Snapshot returns the current token count and last refill time for the
// write-behind flusher, then clears the dirty flag.
// The flusher calls this to get the values to persist to PostgreSQL.
func (b *TokenBucket) Snapshot() (tokens float64, lastRefillAt time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dirty.Store(false)
	return b.tokens, b.lastRefillAt
}

// IsDirty reports whether the bucket has changed since the last Snapshot.
// The flusher uses this to skip unchanged buckets in the 100ms flush cycle.
func (b *TokenBucket) IsDirty() bool {
	return b.dirty.Load()
}

// refill adds tokens proportional to elapsed time since the last refill.
// Tokens are capped at capacity — you can't accumulate beyond the burst ceiling.
// Must be called with mu held.
func (b *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(b.lastRefillAt).Seconds()
	b.tokens = min(b.capacity, b.tokens+elapsed*b.refillRate)
	b.lastRefillAt = now
}

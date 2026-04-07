// Package limiter orchestrates the full rate-limiting lifecycle.
//
// Architecture recap (from criteria.md §0):
//   - Hot path: pure in-memory, zero DB I/O per request.
//   - Write-behind: background goroutine flushes dirty buckets to PostgreSQL every 100ms.
//   - Config-watcher: background goroutine refreshes limits from PostgreSQL every 30s.
//   - Fail-open: if the store is unreachable, continue with last known in-memory state.
package limiter

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karabulut18/scale-guard/internal/bucket"
	"github.com/karabulut18/scale-guard/internal/config"
	"github.com/karabulut18/scale-guard/internal/store"
)

// Limiter holds all in-memory token buckets and manages their lifecycle.
type Limiter struct {
	// buckets is the hot-path lookup table.
	// Key: bucketKey(tenantID, clientID) → *bucket.TokenBucket
	// sync.Map is used because reads vastly outnumber writes (new clients are rare).
	buckets sync.Map

	st      store.Store
	cfg     *config.Config
	tenants []string

	// configsMu protects the configs map, which is written by the config-watcher
	// and read by loadAll/refreshConfigs. Not on the hot path.
	configsMu sync.Mutex
	configs   map[string]*store.ClientConfig

	// Metrics: atomic counters exposed for the health/metrics endpoint.
	// Using atomic.Int64 (not a mutex-protected map) keeps the hot path lock-free.
	allowCount atomic.Int64
	denyCount  atomic.Int64
}

// New creates a Limiter. Call Start to load state and begin background operations.
// tenants is the list of tenant IDs this instance is responsible for.
func New(cfg *config.Config, st store.Store, tenants []string) *Limiter {
	return &Limiter{
		st:      st,
		cfg:     cfg,
		tenants: tenants,
		configs: make(map[string]*store.ClientConfig),
	}
}

// Start loads initial state from the store and launches the flusher and config-watcher
// goroutines. They run until ctx is cancelled.
//
// If the store is unreachable at startup, Start logs a CRITICAL alert and continues
// with empty in-memory state (fail-open). Unknown clients will be allowed.
func (l *Limiter) Start(ctx context.Context) error {
	if err := l.loadAll(ctx); err != nil {
		log.Printf("CRITICAL: failed to load initial state: %v — starting in degraded (fail-open) mode", err)
	}

	go l.runFlusher(ctx)
	go l.runConfigWatcher(ctx)

	return nil
}

// Shutdown performs a final write-behind flush before the service exits.
// Call this after cancelling the context passed to Start, to avoid losing
// the last ~100ms of token-count changes.
func (l *Limiter) Shutdown(ctx context.Context) {
	l.flush(ctx)
}

// Allow reports whether the request from (tenantID, clientID) is within its rate limit.
//
// This is the only method on the hot path. It never touches the database.
// It is safe for concurrent use by thousands of goroutines simultaneously.
func (l *Limiter) Allow(_ context.Context, tenantID, clientID string) bool {
	val, ok := l.buckets.Load(bucketKey(tenantID, clientID))
	if !ok {
		// Client has no configured bucket — fail-open.
		// This can happen if the DB was unavailable at startup/refresh.
		log.Printf("WARN: unknown client %s/%s — allowing (fail-open)", tenantID, clientID)
		l.allowCount.Add(1)
		return true
	}

	allowed := val.(*bucket.TokenBucket).Allow()
	if allowed {
		l.allowCount.Add(1)
	} else {
		l.denyCount.Add(1)
	}
	return allowed
}

// AllowCount returns the total number of permitted requests since startup.
func (l *Limiter) AllowCount() int64 { return l.allowCount.Load() }

// DenyCount returns the total number of denied requests since startup.
func (l *Limiter) DenyCount() int64 { return l.denyCount.Load() }

// ── Background goroutines ──────────────────────────────────────────────────

func (l *Limiter) runFlusher(ctx context.Context) {
	ticker := time.NewTicker(l.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.flush(ctx)
		}
	}
}

func (l *Limiter) runConfigWatcher(ctx context.Context) {
	ticker := time.NewTicker(l.cfg.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.refreshConfigs(ctx); err != nil {
				log.Printf("CRITICAL: config refresh failed: %v — continuing with last known config", err)
			}
		}
	}
}

// ── Internal helpers ───────────────────────────────────────────────────────

// loadAll loads configs and persisted bucket states for all managed tenants,
// then populates the in-memory sync.Map. Called once at startup.
func (l *Limiter) loadAll(ctx context.Context) error {
	for _, tenantID := range l.tenants {
		configs, err := l.st.LoadConfigs(ctx, tenantID)
		if err != nil {
			return fmt.Errorf("loading configs for tenant %q: %w", tenantID, err)
		}

		states, err := l.st.LoadBucketStates(ctx, tenantID)
		if err != nil {
			return fmt.Errorf("loading states for tenant %q: %w", tenantID, err)
		}

		// Index states by clientID for O(1) lookup below.
		stateByClient := make(map[string]*store.BucketState, len(states))
		for _, s := range states {
			stateByClient[s.ClientID] = s
		}

		l.configsMu.Lock()
		for _, cfg := range configs {
			key := bucketKey(cfg.TenantID, cfg.ClientID)
			l.configs[key] = cfg

			var tb *bucket.TokenBucket
			if s, ok := stateByClient[cfg.ClientID]; ok {
				// Restore from persisted state. NewWithState applies elapsed-time
				// refill immediately so the bucket is accurate before the first request.
				tb = bucket.NewWithState(cfg.Capacity, cfg.RefillRate, s.Tokens, s.LastRefillAt)
			} else {
				// No prior state — start at full capacity.
				tb = bucket.New(cfg.Capacity, cfg.RefillRate)
			}
			l.buckets.Store(key, tb)
		}
		l.configsMu.Unlock()
	}
	return nil
}

// refreshConfigs reloads rate-limit configs from the store.
// New clients get a fresh bucket. Existing clients are not disturbed —
// their live token count is preserved; only the config map is updated.
func (l *Limiter) refreshConfigs(ctx context.Context) error {
	for _, tenantID := range l.tenants {
		configs, err := l.st.LoadConfigs(ctx, tenantID)
		if err != nil {
			return err
		}

		l.configsMu.Lock()
		for _, cfg := range configs {
			key := bucketKey(cfg.TenantID, cfg.ClientID)
			l.configs[key] = cfg

			if _, exists := l.buckets.Load(key); !exists {
				// Newly configured client — add a fresh bucket.
				l.buckets.Store(key, bucket.New(cfg.Capacity, cfg.RefillRate))
				log.Printf("INFO: new client %s/%s registered with capacity=%.0f rate=%.2f/s",
					cfg.TenantID, cfg.ClientID, cfg.Capacity, cfg.RefillRate)
			}
		}
		l.configsMu.Unlock()
	}
	return nil
}

// flush collects all dirty buckets and writes them to the store in one batched call.
// If the store is unavailable, the batch is silently discarded (per architectural decision:
// no retry queue to prevent unbounded memory growth during outages).
func (l *Limiter) flush(ctx context.Context) {
	var states []*store.BucketState

	l.buckets.Range(func(k, v any) bool {
		b := v.(*bucket.TokenBucket)
		if !b.IsDirty() {
			return true // skip unchanged buckets
		}
		tokens, lastRefillAt := b.Snapshot() // clears the dirty flag
		tenantID, clientID := splitKey(k.(string))
		states = append(states, &store.BucketState{
			TenantID:     tenantID,
			ClientID:     clientID,
			Tokens:       tokens,
			LastRefillAt: lastRefillAt,
		})
		return true
	})

	if len(states) == 0 {
		return
	}

	if err := l.st.SaveBucketState(ctx, states); err != nil {
		log.Printf("CRITICAL: write-behind flush failed (%d buckets discarded): %v", len(states), err)
	}
}

// bucketKey produces the sync.Map key for a (tenantID, clientID) pair.
// A null byte separator is used because it cannot appear in valid UTF-8 input.
func bucketKey(tenantID, clientID string) string {
	return tenantID + "\x00" + clientID
}

// splitKey reverses bucketKey.
func splitKey(key string) (tenantID, clientID string) {
	idx := strings.IndexByte(key, '\x00')
	if idx < 0 {
		return key, ""
	}
	return key[:idx], key[idx+1:]
}

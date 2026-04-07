package limiter_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/karabulut18/scale-guard/internal/config"
	"github.com/karabulut18/scale-guard/internal/limiter"
	"github.com/karabulut18/scale-guard/internal/store"
)

// ── Mock store ─────────────────────────────────────────────────────────────

// mockStore is an in-memory implementation of store.Store for testing.
// It lets us control configs and injected errors without a real database.
type mockStore struct {
	mu      sync.Mutex
	configs []*store.ClientConfig
	states  []*store.BucketState
	saved   []*store.BucketState // captured by SaveBucketState for assertions

	loadConfigsErr    error
	loadStatesErr     error
	saveBucketStateErr error
}

func (m *mockStore) LoadConfigs(_ context.Context, tenantID string) ([]*store.ClientConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadConfigsErr != nil {
		return nil, m.loadConfigsErr
	}
	var result []*store.ClientConfig
	for _, c := range m.configs {
		if c.TenantID == tenantID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *mockStore) LoadBucketStates(_ context.Context, tenantID string) ([]*store.BucketState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadStatesErr != nil {
		return nil, m.loadStatesErr
	}
	var result []*store.BucketState
	for _, s := range m.states {
		if s.TenantID == tenantID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockStore) SaveBucketState(_ context.Context, states []*store.BucketState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveBucketStateErr != nil {
		return m.saveBucketStateErr
	}
	m.saved = append(m.saved, states...)
	return nil
}

func (m *mockStore) Health(_ context.Context) error { return nil }
func (m *mockStore) Close() error                   { return nil }

func (m *mockStore) savedStates() []*store.BucketState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saved
}

// ── Helpers ────────────────────────────────────────────────────────────────

func testConfig() *config.Config {
	return &config.Config{
		FlushInterval:   50 * time.Millisecond,
		RefreshInterval: 1 * time.Hour, // effectively disabled in most tests
		InstanceID:      "test",
		LogLevel:        "info",
	}
}

// ── Tests ──────────────────────────────────────────────────────────────────

// TestAllow_PermitsWithinLimit: requests within capacity are allowed.
func TestAllow_PermitsWithinLimit(t *testing.T) {
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: 5, RefillRate: 1.0},
		},
	}
	ctx := context.Background()
	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	for i := range 5 {
		if !l.Allow(ctx, "t1", "c1") {
			t.Fatalf("expected allow on request %d", i+1)
		}
	}
}

// TestAllow_DeniesWhenExhausted: once the bucket is empty, requests are denied.
func TestAllow_DeniesWhenExhausted(t *testing.T) {
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: 3, RefillRate: 0.01},
		},
	}
	ctx := context.Background()
	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	l.Allow(ctx, "t1", "c1")
	l.Allow(ctx, "t1", "c1")
	l.Allow(ctx, "t1", "c1")

	if l.Allow(ctx, "t1", "c1") {
		t.Fatal("expected deny after bucket exhausted")
	}
}

// TestAllow_FailOpenForUnknownClient: unknown clients are allowed (fail-open).
func TestAllow_FailOpenForUnknownClient(t *testing.T) {
	ms := &mockStore{} // no configs
	ctx := context.Background()
	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	if !l.Allow(ctx, "t1", "unknown") {
		t.Fatal("expected allow for unconfigured client (fail-open)")
	}
}

// TestAllow_FailOpenWhenStoreDown: if the store fails at startup,
// the limiter starts in degraded mode and still allows all requests.
func TestAllow_FailOpenWhenStoreDown(t *testing.T) {
	ms := &mockStore{loadConfigsErr: fmt.Errorf("connection refused")}
	ctx := context.Background()
	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx) // should not return error — logs CRITICAL and continues

	if !l.Allow(ctx, "t1", "c1") {
		t.Fatal("expected allow in degraded mode (fail-open)")
	}
}

// TestMetrics_AllowDenyCounters: allow/deny counters reflect real traffic.
func TestMetrics_AllowDenyCounters(t *testing.T) {
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: 2, RefillRate: 0.01},
		},
	}
	ctx := context.Background()
	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	l.Allow(ctx, "t1", "c1") // allowed
	l.Allow(ctx, "t1", "c1") // allowed
	l.Allow(ctx, "t1", "c1") // denied

	if l.AllowCount() != 2 {
		t.Errorf("expected AllowCount=2, got %d", l.AllowCount())
	}
	if l.DenyCount() != 1 {
		t.Errorf("expected DenyCount=1, got %d", l.DenyCount())
	}
}

// TestWriteBehind_FlushesAfterInterval: dirty buckets are written to the store
// within one flush interval.
func TestWriteBehind_FlushesAfterInterval(t *testing.T) {
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: 100, RefillRate: 10.0},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	l.Allow(ctx, "t1", "c1") // makes bucket dirty

	// Wait for two flush intervals to be safe
	time.Sleep(2 * testConfig().FlushInterval)

	saved := ms.savedStates()
	if len(saved) == 0 {
		t.Fatal("expected write-behind to flush bucket state to store")
	}
}

// TestWriteBehind_SkipsCleanBuckets: buckets not touched since last flush are not written.
func TestWriteBehind_SkipsCleanBuckets(t *testing.T) {
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: 100, RefillRate: 10.0},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	// Do NOT call Allow — bucket stays clean
	time.Sleep(2 * testConfig().FlushInterval)

	if len(ms.savedStates()) != 0 {
		t.Fatal("expected no flush for untouched bucket")
	}
}

// TestStateRestore_ResumesFromLastKnownTokens: on startup with persisted state,
// buckets are restored to their saved token count (not reset to full capacity).
func TestStateRestore_ResumesFromLastKnownTokens(t *testing.T) {
	lastRefill := time.Now().Add(-1 * time.Second) // 1 second ago
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: 100, RefillRate: 1.0},
		},
		states: []*store.BucketState{
			// Persisted with only 2 tokens remaining
			{TenantID: "t1", ClientID: "c1", Tokens: 2, LastRefillAt: lastRefill},
		},
	}
	ctx := context.Background()
	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	// Should allow ~3 requests (2 persisted + ~1 from 1s elapsed at 1 tok/s)
	// and then deny — NOT allow 100 (full capacity).
	allowed := 0
	for range 10 {
		if l.Allow(ctx, "t1", "c1") {
			allowed++
		}
	}
	if allowed > 5 {
		t.Errorf("expected restored bucket (~3 tokens), got %d allows — looks like full-capacity reset", allowed)
	}
	if allowed == 0 {
		t.Error("expected at least 2 allows from persisted state")
	}
}

// TestConcurrentAllow_Race: race detector validates no data races under load.
func TestConcurrentAllow_Race(t *testing.T) {
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: 10_000, RefillRate: 1_000.0},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := limiter.New(testConfig(), ms, []string{"t1"})
	l.Start(ctx)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				l.Allow(ctx, "t1", "c1")
			}
		}()
	}
	wg.Wait()
}

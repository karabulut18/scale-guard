package limiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/karabulut18/scale-guard/internal/config"
	"github.com/karabulut18/scale-guard/internal/limiter"
	"github.com/karabulut18/scale-guard/internal/store"
)

// newBenchLimiter creates a limiter with a single high-capacity bucket
// pre-loaded, suitable for throughput benchmarks.
func newBenchLimiter(b *testing.B) *limiter.Limiter {
	b.Helper()
	ms := &mockStore{
		configs: []*store.ClientConfig{
			{TenantID: "t1", ClientID: "c1", Capacity: float64(b.N) + 1e9, RefillRate: 1e9},
		},
	}
	cfg := &config.Config{
		FlushInterval:   1 * time.Hour, // disable flusher during bench
		RefreshInterval: 1 * time.Hour,
		InstanceID:      "bench",
	}
	ctx := context.Background()
	l := limiter.New(cfg, ms, []string{"t1"})
	l.Start(ctx)
	return l
}

// BenchmarkLimiter_Allow_SingleClient measures the end-to-end Allow() call
// through the limiter (sync.Map lookup + bucket.Allow()).
// This is the number that matters for the >5,000 req/sec requirement.
func BenchmarkLimiter_Allow_SingleClient(b *testing.B) {
	l := newBenchLimiter(b)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		l.Allow(ctx, "t1", "c1")
	}
}

// BenchmarkLimiter_Allow_Parallel measures throughput under concurrent load
// across GOMAXPROCS goroutines hitting the same client bucket.
func BenchmarkLimiter_Allow_Parallel(b *testing.B) {
	l := newBenchLimiter(b)
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Allow(ctx, "t1", "c1")
		}
	})
}

// BenchmarkLimiter_Allow_ManyClients measures the sync.Map lookup cost
// when the map contains many entries. Scales from 1 to 10,000 clients.
// A well-implemented sync.Map should show near-constant lookup time.
func BenchmarkLimiter_Allow_ManyClients(b *testing.B) {
	clientCounts := []int{1, 100, 1_000, 10_000}

	for _, n := range clientCounts {
		b.Run("clients="+itoa(n), func(b *testing.B) {
			configs := make([]*store.ClientConfig, n)
			for i := range n {
				configs[i] = &store.ClientConfig{
					TenantID:   "t1",
					ClientID:   "client-" + itoa(i),
					Capacity:   1e9,
					RefillRate: 1e9,
				}
			}
			ms := &mockStore{configs: configs}
			cfg := &config.Config{
				FlushInterval:   1 * time.Hour,
				RefreshInterval: 1 * time.Hour,
				InstanceID:      "bench",
			}
			ctx := context.Background()
			l := limiter.New(cfg, ms, []string{"t1"})
			l.Start(ctx)

			b.ResetTimer()
			for i := range b.N {
				// Rotate through all clients to exercise the full map
				l.Allow(ctx, "t1", "client-"+itoa(i%n))
			}
		})
	}
}

// itoa is a minimal int-to-string helper to avoid importing strconv
// just for benchmark sub-test naming.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

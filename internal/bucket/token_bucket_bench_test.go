package bucket_test

import (
	"testing"
	"time"

	"github.com/karabulut18/scale-guard/internal/bucket"
)

// BenchmarkAllow_HotPath measures a single Allow() call on a bucket that
// always has tokens (i.e. never denied). This is the steady-state case
// for a well-behaved client — the most common scenario in production.
//
// This maps directly to the sub-2ms p99 requirement. The benchmark
// reports ns/op — divide by 1000 to get microseconds.
func BenchmarkAllow_HotPath(b *testing.B) {
	tb := bucket.New(float64(b.N)+1000, 1e9) // effectively infinite capacity
	b.ResetTimer()
	for range b.N {
		tb.Allow()
	}
}

// BenchmarkAllow_Parallel measures Allow() under concurrent load.
// b.RunParallel spins up GOMAXPROCS goroutines, each calling Allow()
// in a tight loop. This is the realistic production scenario where
// many requests arrive simultaneously.
func BenchmarkAllow_Parallel(b *testing.B) {
	tb := bucket.New(float64(b.N)+1000, 1e9)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow()
		}
	})
}

// BenchmarkAllow_WithContention simulates N goroutines all hitting the
// same bucket at once. Unlike RunParallel (which uses GOMAXPROCS),
// this explicitly controls the goroutine count to model a specific
// traffic pattern: 100 concurrent clients sharing one bucket.
func BenchmarkAllow_WithContention(b *testing.B) {
	tb := bucket.New(float64(b.N)+1000, 1e9)
	b.SetParallelism(100)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow()
		}
	})
}

// BenchmarkSnapshot measures the write-behind flusher's per-bucket cost.
// Called every 100ms per dirty bucket — should be negligible compared to Allow().
func BenchmarkSnapshot(b *testing.B) {
	tb := bucket.New(1e9, 1e9)
	b.ResetTimer()
	for range b.N {
		tb.Snapshot()
	}
}

// BenchmarkNewWithState measures the startup restore path.
// Called once per bucket at startup — not on the hot path, but
// good to document for capacity planning on large tenant counts.
func BenchmarkNewWithState(b *testing.B) {
	ts := time.Now().Add(-1 * time.Second)
	b.ResetTimer()
	for range b.N {
		bucket.NewWithState(100, 10.0, 50.0, ts)
	}
}

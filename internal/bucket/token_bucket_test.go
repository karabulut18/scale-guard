package bucket_test

import (
	"sync"
	"testing"
	"time"

	"github.com/karabulut18/scale-guard/internal/bucket"
)

// TestAllow_FullBucket: a fresh bucket should allow exactly `capacity` requests.
func TestAllow_FullBucket(t *testing.T) {
	tb := bucket.New(5, 1.0)
	for i := range 5 {
		if !tb.Allow() {
			t.Fatalf("expected allow on request %d", i+1)
		}
	}
}

// TestAllow_EmptyBucket: once drained, further requests must be denied.
func TestAllow_EmptyBucket(t *testing.T) {
	tb := bucket.New(2, 0.01) // near-zero refill so time doesn't interfere
	tb.Allow()
	tb.Allow()
	if tb.Allow() {
		t.Fatal("expected deny on empty bucket")
	}
}

// TestAllow_RefillOverTime: tokens must be replenished after enough time passes.
func TestAllow_RefillOverTime(t *testing.T) {
	// 100 tokens/sec = 1 token per 10ms
	tb := bucket.New(1, 100.0)
	tb.Allow() // drain

	time.Sleep(25 * time.Millisecond) // wait for ~2 tokens to refill

	if !tb.Allow() {
		t.Fatal("expected allow after refill window elapsed")
	}
}

// TestAllow_Capacity: tokens must never accumulate beyond capacity.
func TestAllow_Capacity(t *testing.T) {
	tb := bucket.New(5, 1000.0) // very fast refill rate
	time.Sleep(20 * time.Millisecond)

	// Drain fully and count — must be exactly capacity (5), not more.
	allowed := 0
	for tb.Allow() {
		allowed++
		if allowed > 10 {
			t.Fatal("bucket exceeded capacity — refill is not capped")
		}
	}
	if allowed != 5 {
		t.Fatalf("expected capacity=5 tokens, got %d", allowed)
	}
}

// TestDirtyFlag: Allow must mark the bucket dirty; Snapshot must clear it.
func TestDirtyFlag(t *testing.T) {
	tb := bucket.New(10, 1.0)

	if tb.IsDirty() {
		t.Fatal("new bucket should not be dirty")
	}

	tb.Allow()
	if !tb.IsDirty() {
		t.Fatal("bucket should be dirty after Allow()")
	}

	tb.Snapshot()
	if tb.IsDirty() {
		t.Fatal("bucket should be clean after Snapshot()")
	}
}

// TestNewWithState_AppliesRefill: restoring from DB state must catch up elapsed time.
func TestNewWithState_AppliesRefill(t *testing.T) {
	// Simulate: bucket was persisted 1 second ago with 0 tokens, refill=10/sec.
	// On restore it should immediately have ~10 tokens.
	lastRefill := time.Now().Add(-1 * time.Second)
	tb := bucket.NewWithState(100, 10.0, 0, lastRefill)

	allowed := 0
	for range 15 {
		if tb.Allow() {
			allowed++
		}
	}
	// Allow for slight timing imprecision — at least 9 of the ~10 tokens.
	if allowed < 9 {
		t.Fatalf("expected ~10 tokens after 1s refill on restore, got %d", allowed)
	}
}

// TestConcurrentAllow_Race: run with `go test -race` to detect data races.
// 100 goroutines each firing 50 Allow() calls — the race detector will catch
// any unsynchronised access to the bucket's internal state.
func TestConcurrentAllow_Race(t *testing.T) {
	tb := bucket.New(10_000, 1_000.0)
	var wg sync.WaitGroup

	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				tb.Allow()
			}
		}()
	}
	wg.Wait()
}

// TestSnapshot_Concurrent: Snapshot and Allow racing — no panic, no data race.
func TestSnapshot_Concurrent(t *testing.T) {
	tb := bucket.New(10_000, 1_000.0)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			tb.Allow()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 10 {
			tb.Snapshot()
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
}

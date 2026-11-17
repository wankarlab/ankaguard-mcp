package hft

import (
	"testing"
	"time"
)

func TestOrderPool(t *testing.T) {
	pool := NewOrderPool()

	// Test acquire
	order := pool.Acquire()
	if order == nil {
		t.Fatal("Expected non-nil order")
	}

	// Test populate
	order.ID = "test-123"
	order.Symbol = "BTCUSDT"
	order.Side = "buy"
	order.Amount = 1.0
	order.Tags = append(order.Tags, "test")
	order.Metadata["key"] = "value"

	// Test release
	pool.Release(order)

	// Acquire again - should get reset order
	order2 := pool.Acquire()
	if order2.ID != "" {
		t.Errorf("Expected empty ID after reset, got %s", order2.ID)
	}
	if len(order2.Tags) != 0 {
		t.Errorf("Expected empty tags after reset, got %d", len(order2.Tags))
	}
	if len(order2.Metadata) != 0 {
		t.Errorf("Expected empty metadata after reset, got %d", len(order2.Metadata))
	}
}

func TestOrderClone(t *testing.T) {
	pool := NewOrderPool()
	order := pool.Acquire()

	order.ID = "test-123"
	order.Symbol = "BTCUSDT"
	order.Tags = append(order.Tags, "tag1", "tag2")
	order.Metadata["key"] = "value"

	clone := order.Clone()

	if clone.ID != order.ID {
		t.Errorf("Expected ID %s, got %s", order.ID, clone.ID)
	}
	if len(clone.Tags) != len(order.Tags) {
		t.Errorf("Expected %d tags, got %d", len(order.Tags), len(clone.Tags))
	}
	if clone.Metadata["key"] != order.Metadata["key"] {
		t.Error("Metadata not cloned correctly")
	}

	// Modify original - clone should not be affected
	order.ID = "modified"
	if clone.ID == "modified" {
		t.Error("Clone was affected by original modification")
	}
}

func TestOrderLatencyCalculation(t *testing.T) {
	pool := NewOrderPool()
	order := pool.Acquire()

	order.SubmittedAt = time.Now()
	time.Sleep(time.Microsecond * 100)
	order.ExecutedAt = time.Now()

	order.CalculateLatency()

	if order.LatencyUs == 0 {
		t.Error("Expected non-zero latency")
	}
	if order.LatencyUs < 100 {
		t.Errorf("Expected latency >= 100us, got %d", order.LatencyUs)
	}
}

func BenchmarkOrderPoolAcquireRelease(b *testing.B) {
	pool := NewOrderPool()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		order := pool.Acquire()
		pool.Release(order)
	}
}

func BenchmarkOrderPoolConcurrent(b *testing.B) {
	pool := NewOrderPool()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			order := pool.Acquire()
			order.ID = "test"
			order.Symbol = "BTCUSDT"
			pool.Release(order)
		}
	})
}

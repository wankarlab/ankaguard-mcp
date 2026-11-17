package universe

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// countingLister is a fake PairLister that counts how many times
// GetTradablePairs is called and returns a fixed pair list.
type countingLister struct {
	calls int64
	pairs []string
	err   error
}

func (c *countingLister) GetTradablePairs(_ context.Context, _, _ string) ([]string, error) {
	atomic.AddInt64(&c.calls, 1)
	return c.pairs, c.err
}

func (c *countingLister) count() int64 {
	return atomic.LoadInt64(&c.calls)
}

func TestDynamic_CacheHit_OnlyOneUnderlyingCall(t *testing.T) {
	lister := &countingLister{pairs: []string{"BTC-USDT", "ETH-USDT"}}
	d := NewDynamicExchangeUniverse(lister, "spot", 10*time.Second)

	ctx := context.Background()

	// First call — should hit the lister.
	p1, err := d.Pairs(ctx, "kucoin")
	if err != nil {
		t.Fatalf("first Pairs() unexpected error: %v", err)
	}
	if len(p1) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(p1))
	}

	// Second call within TTL — must NOT hit the lister again.
	p2, err := d.Pairs(ctx, "kucoin")
	if err != nil {
		t.Fatalf("second Pairs() unexpected error: %v", err)
	}
	if len(p2) != 2 {
		t.Fatalf("expected 2 pairs from cache, got %d", len(p2))
	}

	if got := lister.count(); got != 1 {
		t.Fatalf("expected exactly 1 underlying call (cache hit), got %d", got)
	}
}

func TestDynamic_CacheMiss_AfterTTLExpiry(t *testing.T) {
	lister := &countingLister{pairs: []string{"SOL-USDT"}}
	// Very short TTL so we can expire it quickly.
	ttl := 30 * time.Millisecond
	d := NewDynamicExchangeUniverse(lister, "spot", ttl)

	ctx := context.Background()

	// First call — populates cache.
	if _, err := d.Pairs(ctx, "kucoin"); err != nil {
		t.Fatalf("first Pairs() unexpected error: %v", err)
	}

	// Wait for TTL to expire.
	time.Sleep(ttl + 10*time.Millisecond)

	// Second call — cache is stale, must fetch again.
	if _, err := d.Pairs(ctx, "kucoin"); err != nil {
		t.Fatalf("second Pairs() unexpected error: %v", err)
	}

	if got := lister.count(); got != 2 {
		t.Fatalf("expected 2 underlying calls after TTL expiry, got %d", got)
	}
}

func TestDynamic_ZeroTTL_AlwaysFetches(t *testing.T) {
	lister := &countingLister{pairs: []string{"DOGE-USDT"}}
	// ttl <= 0 → no caching.
	d := NewDynamicExchangeUniverse(lister, "spot", 0)

	ctx := context.Background()

	const calls = 3
	for i := 0; i < calls; i++ {
		if _, err := d.Pairs(ctx, "kucoin"); err != nil {
			t.Fatalf("Pairs() call %d unexpected error: %v", i, err)
		}
	}

	if got := lister.count(); got != calls {
		t.Fatalf("expected %d underlying calls with zero TTL, got %d", calls, got)
	}
}

func TestDynamic_ListerError_ReturnsStaleOnHit(t *testing.T) {
	lister := &countingLister{pairs: []string{"XRP-USDT"}}
	ttl := 30 * time.Millisecond
	d := NewDynamicExchangeUniverse(lister, "spot", ttl)

	ctx := context.Background()

	// Prime the cache.
	if _, err := d.Pairs(ctx, "kucoin"); err != nil {
		t.Fatalf("prime call unexpected error: %v", err)
	}

	// Let cache expire, then inject an error.
	time.Sleep(ttl + 10*time.Millisecond)
	lister.err = errors.New("exchange unavailable")

	// Should return stale data instead of propagating the error.
	pairs, err := d.Pairs(ctx, "kucoin")
	if err != nil {
		t.Fatalf("expected stale data on lister error, got error: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != "XRP-USDT" {
		t.Fatalf("expected stale [XRP-USDT], got %v", pairs)
	}
}

func TestDynamic_ListerError_NoCachePropagates(t *testing.T) {
	boom := errors.New("no data at all")
	lister := &countingLister{err: boom}
	d := NewDynamicExchangeUniverse(lister, "spot", 10*time.Second)

	_, err := d.Pairs(context.Background(), "kucoin")
	if !errors.Is(err, boom) {
		t.Fatalf("expected original error to propagate when no cache, got %v", err)
	}
}

func TestDynamic_MultipleExchanges_IndependentCaches(t *testing.T) {
	lister := &countingLister{pairs: []string{"BTC-USDT"}}
	d := NewDynamicExchangeUniverse(lister, "spot", 10*time.Second)

	ctx := context.Background()

	// Two different exchanges — each should hit the lister once.
	if _, err := d.Pairs(ctx, "kucoin"); err != nil {
		t.Fatalf("kucoin: %v", err)
	}
	if _, err := d.Pairs(ctx, "binance"); err != nil {
		t.Fatalf("binance: %v", err)
	}
	// Second hit on each — should be cached.
	if _, err := d.Pairs(ctx, "kucoin"); err != nil {
		t.Fatalf("kucoin second: %v", err)
	}
	if _, err := d.Pairs(ctx, "binance"); err != nil {
		t.Fatalf("binance second: %v", err)
	}

	if got := lister.count(); got != 2 {
		t.Fatalf("expected 2 underlying calls (one per exchange), got %d", got)
	}
}

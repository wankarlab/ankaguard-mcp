package universe

import (
	"context"
	"sync"
	"time"
)

// PairLister is satisfied by *gctclient.Client (GetTradablePairs).
// Defined locally to keep this package free of heavy dependencies.
type PairLister interface {
	GetTradablePairs(ctx context.Context, exchange, assetType string) ([]string, error)
}

type cacheEntry struct {
	pairs     []string
	fetchedAt time.Time
}

// DynamicExchangeUniverse implements PairUniverseProvider in ModeAuto.
// It pulls live pairs from the exchange via PairLister and caches them with a TTL.
// ttl <= 0 means always-fetch (no caching).
type DynamicExchangeUniverse struct {
	lister    PairLister
	assetType string
	ttl       time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewDynamicExchangeUniverse constructs a DynamicExchangeUniverse.
func NewDynamicExchangeUniverse(lister PairLister, assetType string, ttl time.Duration) *DynamicExchangeUniverse {
	return &DynamicExchangeUniverse{
		lister:    lister,
		assetType: assetType,
		ttl:       ttl,
		cache:     make(map[string]cacheEntry),
	}
}

// Mode returns ModeAuto.
func (d *DynamicExchangeUniverse) Mode() UniverseMode {
	return ModeAuto
}

// Pairs returns the tradable pairs for the given exchange.
// If the cache is fresh (age < ttl and ttl > 0) it returns cached data.
// On lister error, stale cache is returned if available; otherwise the error propagates.
func (d *DynamicExchangeUniverse) Pairs(ctx context.Context, exchange string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry, hasCache := d.cache[exchange]
	fresh := hasCache && d.ttl > 0 && time.Since(entry.fetchedAt) < d.ttl

	if fresh {
		return entry.pairs, nil
	}

	pairs, err := d.lister.GetTradablePairs(ctx, exchange, d.assetType)
	if err != nil {
		if hasCache {
			// Return stale data rather than propagating a transient error.
			return entry.pairs, nil
		}
		return nil, err
	}

	d.cache[exchange] = cacheEntry{pairs: pairs, fetchedAt: time.Now()}
	return pairs, nil
}

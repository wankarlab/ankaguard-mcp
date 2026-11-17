package marketdata

import (
	"sync"
	"time"
)

// PairsCache provides thread-safe caching for trading pairs data
type PairsCache struct {
	mu    sync.RWMutex
	cache map[string]*CacheEntry
}

// CacheEntry represents a cached pairs result with expiration
type CacheEntry struct {
	Data      *PairsResult
	ExpiresAt time.Time
}

// NewPairsCache creates a new pairs cache
func NewPairsCache() *PairsCache {
	return &PairsCache{
		cache: make(map[string]*CacheEntry),
	}
}

// Get retrieves cached data if it exists and hasn't expired
func (c *PairsCache) Get(key string) (*PairsResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[key]
	if !exists {
		return nil, false
	}

	// Check if entry has expired
	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry.Data, true
}

// Set stores data in cache with the specified TTL
func (c *PairsCache) Set(key string, data *PairsResult, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[key] = &CacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Clear removes all entries from the cache
func (c *PairsCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*CacheEntry)
}

// CleanExpired removes expired entries from the cache
func (c *PairsCache) CleanExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.cache {
		if now.After(entry.ExpiresAt) {
			delete(c.cache, key)
		}
	}
}

// GenerateCacheKey creates a cache key from exchange and optional market type
func GenerateCacheKey(exchange string, marketType *string) string {
	if marketType != nil && *marketType != "" {
		return exchange + ":" + *marketType
	}
	return exchange + ":all"
}

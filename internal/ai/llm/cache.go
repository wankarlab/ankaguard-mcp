package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Cache provides caching for LLM responses
type Cache interface {
	// Get retrieves a cached response
	Get(key CacheKey) (*LLMResponse, bool)

	// Set stores a response in cache
	Set(key CacheKey, response *LLMResponse, ttl time.Duration)

	// Clear removes all cached entries
	Clear()

	// GetStats returns cache statistics
	GetStats() *CacheStats
}

// CacheStats holds cache statistics
type CacheStats struct {
	Hits       int64
	Misses     int64
	Entries    int
	HitRate    float64
	TotalSaved float64 // Total cost saved by cache hits
}

// memoryCache implements an in-memory cache
type memoryCache struct {
	entries map[string]*CacheEntry
	mu      sync.RWMutex
	stats   *CacheStats
}

// NewMemoryCache creates a new in-memory cache
func NewMemoryCache() Cache {
	cache := &memoryCache{
		entries: make(map[string]*CacheEntry),
		stats:   &CacheStats{},
	}

	// Start cleanup goroutine
	go cache.cleanupExpired()

	return cache
}

// Get retrieves a cached response
func (c *memoryCache) Get(key CacheKey) (*LLMResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cacheKeyStr := c.buildCacheKey(key)
	entry, exists := c.entries[cacheKeyStr]

	if !exists {
		c.stats.Misses++
		return nil, false
	}

	// Check if expired
	if time.Now().After(entry.ExpiresAt) {
		c.stats.Misses++
		return nil, false
	}

	// Cache hit!
	c.stats.Hits++
	c.stats.TotalSaved += entry.Response.Cost

	// Mark as cached
	cachedResponse := *entry.Response
	cachedResponse.Cached = true

	return &cachedResponse, true
}

// Set stores a response in cache
func (c *memoryCache) Set(key CacheKey, response *LLMResponse, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cacheKeyStr := c.buildCacheKey(key)

	entry := &CacheEntry{
		Key:       key,
		Response:  response,
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}

	c.entries[cacheKeyStr] = entry
}

// Clear removes all cached entries
func (c *memoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
}

// GetStats returns cache statistics
func (c *memoryCache) GetStats() *CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := *c.stats
	stats.Entries = len(c.entries)

	// Calculate hit rate
	total := stats.Hits + stats.Misses
	if total > 0 {
		stats.HitRate = float64(stats.Hits) / float64(total) * 100
	}

	return &stats
}

// buildCacheKey creates a string key from CacheKey
func (c *memoryCache) buildCacheKey(key CacheKey) string {
	return fmt.Sprintf("%s:%s:%s:%.2f", key.Provider, key.Model, key.PromptHash, key.Temperature)
}

// cleanupExpired removes expired entries periodically
func (c *memoryCache) cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.entries {
			if now.After(entry.ExpiresAt) {
				delete(c.entries, key)
			}
		}
		c.mu.Unlock()
	}
}

// HashPrompt creates a hash of a prompt for caching
func HashPrompt(prompt string) string {
	hash := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(hash[:])
}

// BuildCacheKey creates a cache key from request parameters
func BuildCacheKey(provider, model, prompt string, temperature float64) CacheKey {
	return CacheKey{
		Provider:    provider,
		Model:       model,
		PromptHash:  HashPrompt(prompt),
		Temperature: temperature,
	}
}

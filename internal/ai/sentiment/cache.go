package sentiment

import (
	"sync"
	"time"
)

// SentimentCache provides caching for sentiment analysis results
type SentimentCache struct {
	entries map[string]*CacheEntry
	mu      sync.RWMutex
}

// CacheEntry represents a cached sentiment score
type CacheEntry struct {
	Score     *SentimentScore
	ExpiresAt time.Time
	CreatedAt time.Time
}

// NewSentimentCache creates a new sentiment cache
func NewSentimentCache() *SentimentCache {
	cache := &SentimentCache{
		entries: make(map[string]*CacheEntry),
	}

	// Start cleanup goroutine
	go cache.cleanupExpired()

	return cache
}

// Get retrieves a cached sentiment score
func (c *SentimentCache) Get(symbol string) *SentimentScore {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.entries[symbol]
	if !exists {
		return nil
	}

	// Check if expired and remove if so
	if time.Now().After(entry.ExpiresAt) {
		delete(c.entries, symbol)
		return nil
	}

	return entry.Score
}

// Set stores a sentiment score in cache
func (c *SentimentCache) Set(symbol string, score *SentimentScore, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := &CacheEntry{
		Score:     score,
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}

	c.entries[symbol] = entry
}

// Clear removes all cached entries
func (c *SentimentCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
}

// Size returns the number of cached entries
func (c *SentimentCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// cleanupExpired removes expired entries periodically
func (c *SentimentCache) cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for symbol, entry := range c.entries {
			if now.After(entry.ExpiresAt) {
				delete(c.entries, symbol)
			}
		}
		c.mu.Unlock()
	}
}

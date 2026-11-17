package knowledge

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// ContextCache caches built contexts to avoid rebuilding
type ContextCache struct {
	cache map[string]*CachedContext
	mu    sync.RWMutex
	ttl   time.Duration
}

// CachedContext represents a cached context response
type CachedContext struct {
	Response  *ContextResponse
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewContextCache creates a new context cache
func NewContextCache(ttl time.Duration) *ContextCache {
	return &ContextCache{
		cache: make(map[string]*CachedContext),
		ttl:   ttl,
	}
}

// Get retrieves a cached context if available and not expired
func (cc *ContextCache) Get(key string) (*ContextResponse, bool) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	cached, ok := cc.cache[key]
	if !ok {
		return nil, false
	}

	// Check if expired
	if time.Now().After(cached.ExpiresAt) {
		return nil, false
	}

	return cached.Response, true
}

// Set stores a context in the cache
func (cc *ContextCache) Set(key string, response *ContextResponse) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.cache[key] = &CachedContext{
		Response:  response,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(cc.ttl),
	}
}

// GenerateKey generates a cache key from a context request
func (cc *ContextCache) GenerateKey(req *ContextRequest) string {
	// Create a unique key based on request parameters
	keyStr := fmt.Sprintf("%s:%s:%s:%s",
		req.AnalysisType,
		req.Pair,
		req.Timeframe,
		req.TradingStyle,
	)

	// Hash the key for consistent length
	hash := sha256.Sum256([]byte(keyStr))
	return fmt.Sprintf("%x", hash[:16]) // Use first 16 bytes
}

// Clear removes all cached entries
func (cc *ContextCache) Clear() {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.cache = make(map[string]*CachedContext)
}

// CleanExpired removes expired entries from the cache
func (cc *ContextCache) CleanExpired() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	now := time.Now()
	removed := 0

	for key, cached := range cc.cache {
		if now.After(cached.ExpiresAt) {
			delete(cc.cache, key)
			removed++
		}
	}

	return removed
}

// Size returns the number of cached entries
func (cc *ContextCache) Size() int {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	return len(cc.cache)
}

// GetTTLForStyle returns the appropriate TTL for a trading style
func GetTTLForStyle(style string) time.Duration {
	switch style {
	case "scalping":
		// Scalping needs fresh context frequently
		return 1 * time.Minute
	case "day_trading", "daytrading":
		// Day trading can use slightly older context
		return 5 * time.Minute
	case "swing_trading", "swingtrading":
		// Swing trading can use much older context
		return 15 * time.Minute
	default:
		// Default to moderate TTL
		return 5 * time.Minute
	}
}

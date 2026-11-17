package knowledge

import (
	"sync"
	"time"
)

// KnowledgeCache caches context responses
type KnowledgeCache struct {
	cache   map[string]*cacheEntry
	ttl     time.Duration
	enabled bool
	mu      sync.RWMutex

	hits   int64
	misses int64
}

type cacheEntry struct {
	response  *ContextResponse
	expiresAt time.Time
}

// NewKnowledgeCache creates a new knowledge cache
func NewKnowledgeCache(ttl time.Duration, enabled bool) *KnowledgeCache {
	return &KnowledgeCache{
		cache:   make(map[string]*cacheEntry),
		ttl:     ttl,
		enabled: enabled,
	}
}

// Get retrieves a cached context response
func (k *KnowledgeCache) Get(req *ContextRequest) *ContextResponse {
	if !k.enabled {
		return nil
	}

	k.mu.RLock()
	defer k.mu.RUnlock()

	key := k.buildKey(req)
	entry, ok := k.cache[key]

	if !ok || time.Now().After(entry.expiresAt) {
		k.misses++
		return nil
	}

	k.hits++
	return entry.response
}

// Set stores a context response in cache
func (k *KnowledgeCache) Set(req *ContextRequest, response *ContextResponse) {
	if !k.enabled {
		return
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	key := k.buildKey(req)
	k.cache[key] = &cacheEntry{
		response:  response,
		expiresAt: time.Now().Add(k.ttl),
	}
}

// HitRate returns the cache hit rate
func (k *KnowledgeCache) HitRate() float64 {
	k.mu.RLock()
	defer k.mu.RUnlock()

	total := k.hits + k.misses
	if total == 0 {
		return 0
	}

	return float64(k.hits) / float64(total)
}

// buildKey creates a cache key from a request
func (k *KnowledgeCache) buildKey(req *ContextRequest) string {
	return string(req.AnalysisType) + ":" + req.Pair + ":" + req.Timeframe
}

package llm

import (
	"context"
	"crypto/md5"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
)

// CachedProvider wraps a Provider and caches responses for similar market conditions.
// Cache key is built from rounded technical indicators, not exact prices, so similar
// conditions reuse the same LLM response.
type CachedProvider struct {
	inner  Provider
	cache  map[string]*cachedEntry
	ttl    time.Duration
	mu     sync.RWMutex
	log    *logrus.Logger
	hits   int64
	misses int64
}

type cachedEntry struct {
	response  *LLMAnalysis
	expiresAt time.Time
}

// NewCachedProvider wraps a provider with a response cache.
// ttl controls how long a cached response is valid (recommended: 3-5 minutes).
func NewCachedProvider(inner Provider, ttl time.Duration, log *logrus.Logger) *CachedProvider {
	return &CachedProvider{
		inner: inner,
		cache: make(map[string]*cachedEntry),
		ttl:   ttl,
		log:   log,
	}
}

// AnalyzeMarket checks the cache first; only calls the inner provider on a miss.
// It builds the LLM prompt, checks the cache, and falls back to a real API call.
func (c *CachedProvider) AnalyzeMarket(ctx context.Context, marketCtx *ai.MarketContext) (*LLMAnalysis, error) {
	key := buildCacheKey(marketCtx)

	c.mu.RLock()
	entry, ok := c.cache[key]
	c.mu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		c.mu.Lock()
		c.hits++
		c.mu.Unlock()
		if c.log != nil {
			c.log.WithField("cache_key", key[:8]).Debug("LLM cache hit - skipping API call")
		}
		return entry.response, nil
	}

	c.mu.Lock()
	c.misses++
	c.mu.Unlock()

	// Build prompt and call inner provider
	prompt := MarketAnalysisPrompt(marketCtx)
	request := &LLMRequest{
		Prompt:    prompt,
		MaxTokens: 500,
	}

	llmResp, err := c.inner.Call(ctx, request)
	if err != nil {
		return nil, err
	}

	resp, err := ParseMarketAnalysis(llmResp)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[key] = &cachedEntry{
		response:  resp,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return resp, nil
}

// Name returns the inner provider name with a cache prefix.
func (c *CachedProvider) Name() string {
	return "cached:" + c.inner.Name()
}

// Call delegates to the inner provider without caching (use AnalyzeMarket for cached calls).
func (c *CachedProvider) Call(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
	return c.inner.Call(ctx, request)
}

// GetCost delegates cost calculation to the inner provider.
func (c *CachedProvider) GetCost(inputTokens, outputTokens int) float64 {
	return c.inner.GetCost(inputTokens, outputTokens)
}

// IsAvailable delegates availability check to the inner provider.
func (c *CachedProvider) IsAvailable(ctx context.Context) bool {
	return c.inner.IsAvailable(ctx)
}

// GetStats delegates stats to the inner provider.
func (c *CachedProvider) GetStats() *ProviderStats {
	return c.inner.GetStats()
}

// CacheStats returns hit/miss counts for monitoring.
func (c *CachedProvider) CacheStats() (hits, misses int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses
}

// Purge removes all expired entries from the cache.
func (c *CachedProvider) Purge() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.cache {
		if now.After(v.expiresAt) {
			delete(c.cache, k)
		}
	}
}

// buildCacheKey builds a cache key from rounded market indicators.
// Rounds values to reduce sensitivity to noise: RSI to nearest 2, price to 2 sig figs.
func buildCacheKey(marketCtx *ai.MarketContext) string {
	if marketCtx == nil {
		return "nil"
	}

	// Use the first timeframe available in the map
	var rsi, macdTrend float64
	var trend string
	for _, tf := range marketCtx.Timeframes {
		if tf == nil || tf.Indicators == nil {
			continue
		}
		rsi = roundToNearest(tf.Indicators.RSI, 2)
		trend = tf.Trend
		if tf.Indicators.MACD != nil {
			if tf.Indicators.MACD.Histogram > 0 {
				macdTrend = 1
			} else if tf.Indicators.MACD.Histogram < 0 {
				macdTrend = -1
			}
		}
		break // use first available timeframe
	}

	// Round current price to 2 significant figures (e.g., 0.1149 → 0.11)
	priceMag := roundToSigFigs(marketCtx.CurrentPrice, 2)

	key := fmt.Sprintf("%s|rsi=%.0f|trend=%s|macd=%.0f|price=%.4f",
		marketCtx.Pair, rsi, trend, macdTrend, priceMag)

	return fmt.Sprintf("%x", md5.Sum([]byte(key)))
}

func roundToNearest(val, nearest float64) float64 {
	return math.Round(val/nearest) * nearest
}

func roundToSigFigs(val float64, figs int) float64 {
	if val == 0 {
		return 0
	}
	d := math.Ceil(math.Log10(math.Abs(val)))
	pow := math.Pow(10, float64(figs)-d)
	return math.Round(val*pow) / pow
}

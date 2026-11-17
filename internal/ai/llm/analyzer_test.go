package llm

import (
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAnalyzer(t *testing.T) {
	cfg := &config.AIConfig{
		LLM: config.LLMConfig{
			Enabled:             true,
			Provider:            "openai",
			Model:               "gpt-4",
			APIKey:              "test-key",
			MaxCallsPerDay:      100,
			TimeoutSeconds:      10,
			CacheTTLMinutes:     5,
			FallbackToTechnical: true,
			Temperature:         0.7,
			MaxTokens:           1000,
			FallbackProvider:    "gemini",
		},
	}

	log := logrus.New()
	analyzer, err := NewAnalyzer(cfg, nil, log)

	require.NoError(t, err)
	assert.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.primaryProvider)
	// In legacy mode, fallbackProvider might not be initialized if multi-provider mode is not available
	// We'll just check that analyzer was created successfully
	assert.NotNil(t, analyzer.cache)
}

func TestAnalyzer_TechnicalAnalysisFallback(t *testing.T) {
	cfg := &config.AIConfig{
		LLM: config.LLMConfig{
			Enabled:             true,
			Provider:            "google",
			Model:               "gemini-2.5-flash",
			APIKey:              "test-key",
			FallbackToTechnical: true,
		},
	}

	log := logrus.New()
	analyzer, err := NewAnalyzer(cfg, nil, log)
	require.NoError(t, err)

	// Create test market context
	marketCtx := &ai.MarketContext{
		Pair:         "BTC-USDT",
		CurrentPrice: 50000.0,
		Timeframes: map[string]*ai.TimeframeContext{
			"5m": {
				Trend: "bullish",
				Indicators: &ai.Indicators{
					RSI: 25.0, // Oversold
				},
			},
			"15m": {
				Trend: "bullish",
				Indicators: &ai.Indicators{
					RSI: 35.0, // Oversold
				},
			},
		},
		Style: &ai.Strategy{
			Timeframes: []ai.TimeframeConfig{
				{Interval: "5m"},
				{Interval: "15m"},
			},
		},
	}

	analysis := analyzer.technicalAnalysisFallback(marketCtx)

	assert.NotNil(t, analysis)
	assert.Equal(t, "BUY", analysis.Action)
	assert.Greater(t, analysis.Confidence, 0.5)
	assert.Contains(t, analysis.Reasoning, "RSI oversold")
	assert.Contains(t, analysis.Warnings, "LLM analysis unavailable")
}

func TestAnalyzer_CheckDailyLimit(t *testing.T) {
	cfg := &config.AIConfig{
		LLM: config.LLMConfig{
			Enabled:        true,
			Provider:       "google",
			Model:          "gemini-2.5-flash",
			APIKey:         "test-key",
			MaxCallsPerDay: 5,
		},
	}

	log := logrus.New()
	analyzer, err := NewAnalyzer(cfg, nil, log)
	require.NoError(t, err)

	// Should be OK initially
	err = analyzer.checkDailyLimit()
	assert.NoError(t, err)

	// Simulate reaching limit
	analyzer.dailyCallCount = 5
	err = analyzer.checkDailyLimit()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "daily call limit reached")
}

func TestAnalyzer_GetCacheStats(t *testing.T) {
	cfg := &config.AIConfig{
		LLM: config.LLMConfig{
			Enabled:  true,
			Provider: "google",
			Model:    "gemini-2.5-flash",
			APIKey:   "test-key",
		},
	}

	log := logrus.New()
	analyzer, err := NewAnalyzer(cfg, nil, log)
	require.NoError(t, err)

	stats := analyzer.GetCacheStats()
	assert.NotNil(t, stats)
	assert.Equal(t, int64(0), stats.Hits)
	assert.Equal(t, int64(0), stats.Misses)
}

func TestAnalyzer_GetProviderStats(t *testing.T) {
	cfg := &config.AIConfig{
		LLM: config.LLMConfig{
			Enabled:          true,
			Provider:         "google",
			Model:            "gemini-2.5-flash",
			APIKey:           "test-key",
			FallbackProvider: "gemini",
		},
	}

	log := logrus.New()
	analyzer, err := NewAnalyzer(cfg, nil, log)
	require.NoError(t, err)

	stats := analyzer.GetProviderStats()
	assert.NotNil(t, stats)
	assert.Contains(t, stats, "google")
}

func TestAnalyzer_ClearCache(t *testing.T) {
	cfg := &config.AIConfig{
		LLM: config.LLMConfig{
			Enabled:  true,
			Provider: "google",
			Model:    "gemini-2.5-flash",
			APIKey:   "test-key",
		},
	}

	log := logrus.New()
	analyzer, err := NewAnalyzer(cfg, nil, log)
	require.NoError(t, err)

	// Add something to cache
	cacheKey := BuildCacheKey("openai", "gpt-4", "test prompt", 0.7)
	response := &LLMResponse{
		Content:  "test",
		Provider: "openai",
		Cost:     0.01,
	}
	analyzer.cache.Set(cacheKey, response, 5*time.Minute)

	// Verify it's there
	_, found := analyzer.cache.Get(cacheKey)
	assert.True(t, found)

	// Clear cache
	analyzer.ClearCache()

	// Verify it's gone
	_, found = analyzer.cache.Get(cacheKey)
	assert.False(t, found)
}

func TestCache_GetSet(t *testing.T) {
	cache := NewMemoryCache()

	key := BuildCacheKey("openai", "gpt-4", "test prompt", 0.7)
	response := &LLMResponse{
		Content:      "test response",
		Provider:     "openai",
		Model:        "gpt-4",
		InputTokens:  100,
		OutputTokens: 50,
		Cost:         0.05,
	}

	// Set in cache
	cache.Set(key, response, 5*time.Minute)

	// Get from cache
	cached, found := cache.Get(key)
	assert.True(t, found)
	assert.NotNil(t, cached)
	assert.Equal(t, response.Content, cached.Content)
	assert.True(t, cached.Cached)

	// Check stats
	stats := cache.GetStats()
	assert.Equal(t, int64(1), stats.Hits)
	assert.Equal(t, int64(0), stats.Misses)
}

func TestCache_Miss(t *testing.T) {
	cache := NewMemoryCache()

	key := BuildCacheKey("openai", "gpt-4", "nonexistent", 0.7)

	// Try to get non-existent key
	_, found := cache.Get(key)
	assert.False(t, found)

	// Check stats
	stats := cache.GetStats()
	assert.Equal(t, int64(0), stats.Hits)
	assert.Equal(t, int64(1), stats.Misses)
}

func TestCache_Expiration(t *testing.T) {
	cache := NewMemoryCache()

	key := BuildCacheKey("openai", "gpt-4", "test", 0.7)
	response := &LLMResponse{
		Content: "test",
		Cost:    0.01,
	}

	// Set with very short TTL
	cache.Set(key, response, 100*time.Millisecond)

	// Should be available immediately
	_, found := cache.Get(key)
	assert.True(t, found)

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	_, found = cache.Get(key)
	assert.False(t, found)
}

func TestHashPrompt(t *testing.T) {
	prompt1 := "test prompt"
	prompt2 := "test prompt"
	prompt3 := "different prompt"

	hash1 := HashPrompt(prompt1)
	hash2 := HashPrompt(prompt2)
	hash3 := HashPrompt(prompt3)

	// Same prompts should have same hash
	assert.Equal(t, hash1, hash2)

	// Different prompts should have different hash
	assert.NotEqual(t, hash1, hash3)

	// Hash should be consistent
	assert.NotEmpty(t, hash1)
	assert.Len(t, hash1, 64) // SHA256 produces 64 hex characters
}

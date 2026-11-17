package sentiment

import (
	"context"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAnalyzer(t *testing.T) {
	cfg := &config.SentimentConfig{
		Enabled:         true,
		CacheTTLMinutes: 30,
		Sources: map[string]config.SourceConfig{
			"cryptopanic": {
				Enabled:  true,
				APIKey:   "test-key",
				MaxItems: 50,
				Weight:   0.4,
			},
		},
	}

	log := logrus.New()
	analyzer := NewAnalyzer(cfg, nil, log)

	assert.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.cache)
	assert.NotNil(t, analyzer.cryptoPanic)
}

func TestCalculateWeightedScore(t *testing.T) {
	cfg := &config.SentimentConfig{
		Enabled: true,
		Sources: map[string]config.SourceConfig{
			"cryptopanic": {
				Enabled: true,
				Weight:  0.4,
			},
			"twitter": {
				Enabled: true,
				Weight:  0.3,
			},
			"reddit": {
				Enabled: true,
				Weight:  0.3,
			},
		},
	}

	analyzer := NewAnalyzer(cfg, nil, nil)

	tests := []struct {
		name    string
		news    float64
		twitter float64
		reddit  float64
		want    float64
	}{
		{
			name:    "all positive",
			news:    0.8,
			twitter: 0.6,
			reddit:  0.7,
			want:    0.71, // (0.8*0.4 + 0.6*0.3 + 0.7*0.3) = 0.71
		},
		{
			name:    "all negative",
			news:    -0.8,
			twitter: -0.6,
			reddit:  -0.7,
			want:    -0.71,
		},
		{
			name:    "mixed sentiment",
			news:    0.5,
			twitter: -0.3,
			reddit:  0.2,
			want:    0.17, // (0.5*0.4 + -0.3*0.3 + 0.2*0.3) = 0.2 - 0.09 + 0.06 = 0.17
		},
		{
			name:    "all neutral",
			news:    0.0,
			twitter: 0.0,
			reddit:  0.0,
			want:    0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzer.calculateWeightedScore(tt.news, tt.twitter, tt.reddit)
			assert.InDelta(t, tt.want, got, 0.01)
		})
	}
}

func TestCalculateConfidence(t *testing.T) {
	analyzer := &Analyzer{}

	tests := []struct {
		name        string
		newsCount   int
		tweetCount  int
		redditCount int
		wantMin     float64
		wantMax     float64
	}{
		{
			name:        "no data",
			newsCount:   0,
			tweetCount:  0,
			redditCount: 0,
			wantMin:     0.0,
			wantMax:     0.0,
		},
		{
			name:        "single source, low count",
			newsCount:   10,
			tweetCount:  0,
			redditCount: 0,
			wantMin:     0.05,
			wantMax:     0.10,
		},
		{
			name:        "two sources, medium count",
			newsCount:   20,
			tweetCount:  30,
			redditCount: 0,
			wantMin:     0.30,
			wantMax:     0.50,
		},
		{
			name:        "all sources, high count",
			newsCount:   30,
			tweetCount:  40,
			redditCount: 30,
			wantMin:     0.80,
			wantMax:     1.00,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzer.calculateConfidence(tt.newsCount, tt.tweetCount, tt.redditCount)
			assert.GreaterOrEqual(t, got, tt.wantMin)
			assert.LessOrEqual(t, got, tt.wantMax)
		})
	}
}

func TestSentimentCache(t *testing.T) {
	cache := NewSentimentCache()

	// Test Set and Get
	score := &SentimentScore{
		Symbol:     "BTC",
		Overall:    0.75,
		Confidence: 0.85,
		Trend:      "bullish",
		Timestamp:  time.Now(),
	}

	cache.Set("BTC", score, 5*time.Minute)

	// Should retrieve the score
	got := cache.Get("BTC")
	require.NotNil(t, got)
	assert.Equal(t, "BTC", got.Symbol)
	assert.Equal(t, 0.75, got.Overall)
	assert.Equal(t, "bullish", got.Trend)

	// Test cache miss
	got = cache.Get("ETH")
	assert.Nil(t, got)

	// Test expiration
	cache.Set("DOGE", score, 100*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	got = cache.Get("DOGE")
	assert.Nil(t, got)

	// Test Clear
	cache.Set("BTC", score, 5*time.Minute)
	cache.Set("ETH", score, 5*time.Minute)
	assert.Equal(t, 2, cache.Size())

	cache.Clear()
	assert.Equal(t, 0, cache.Size())
}

func TestGetNewsImpact(t *testing.T) {
	// This test would require mocking the CryptoPanic client
	// For now, we'll test the structure
	t.Skip("Requires mock CryptoPanic client")
}

func TestGetSocialSentiment(t *testing.T) {
	// This test would require mocking Twitter and Reddit clients
	// For now, we'll test the structure
	t.Skip("Requires mock Twitter and Reddit clients")
}

func TestAnalyzeMarketSentiment_Disabled(t *testing.T) {
	cfg := &config.SentimentConfig{
		Enabled: false,
	}

	analyzer := NewAnalyzer(cfg, nil, nil)
	ctx := context.Background()

	_, err := analyzer.AnalyzeMarketSentiment(ctx, "BTC")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestAnalyzeMarketSentiment_Cache(t *testing.T) {
	cfg := &config.SentimentConfig{
		Enabled:         true,
		CacheTTLMinutes: 30,
		Sources:         map[string]config.SourceConfig{},
	}

	analyzer := NewAnalyzer(cfg, nil, nil)
	ctx := context.Background()

	// Pre-populate cache
	cachedScore := &SentimentScore{
		Symbol:     "BTC",
		Overall:    0.5,
		Confidence: 0.8,
		Trend:      "bullish",
		Timestamp:  time.Now(),
	}
	analyzer.cache.Set("BTC", cachedScore, 5*time.Minute)

	// Should return cached result
	score, err := analyzer.AnalyzeMarketSentiment(ctx, "BTC")
	require.NoError(t, err)
	assert.Equal(t, cachedScore.Overall, score.Overall)
	assert.Equal(t, cachedScore.Trend, score.Trend)
}

func TestMin(t *testing.T) {
	tests := []struct {
		name string
		a    int
		b    int
		want int
	}{
		{"a smaller", 5, 10, 5},
		{"b smaller", 10, 5, 5},
		{"equal", 7, 7, 7},
		{"negative", -5, 3, -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := min(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func BenchmarkCalculateWeightedScore(b *testing.B) {
	cfg := &config.SentimentConfig{
		Enabled: true,
		Sources: map[string]config.SourceConfig{
			"cryptopanic": {Enabled: true, Weight: 0.4},
			"twitter":     {Enabled: true, Weight: 0.3},
			"reddit":      {Enabled: true, Weight: 0.3},
		},
	}

	analyzer := NewAnalyzer(cfg, nil, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.calculateWeightedScore(0.8, 0.6, 0.7)
	}
}

func BenchmarkCalculateConfidence(b *testing.B) {
	analyzer := &Analyzer{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.calculateConfidence(30, 40, 30)
	}
}

package sentiment

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/config"
	"github.com/sirupsen/logrus"
)

// Analyzer orchestrates sentiment analysis from multiple sources
type Analyzer struct {
	cryptoPanic *CryptoPanicClient
	twitter     *TwitterClient
	reddit      *RedditClient
	llmAnalyzer *llm.Analyzer
	cache       *SentimentCache
	config      *config.SentimentConfig
	log         *logrus.Logger
}

// SentimentScore represents the overall sentiment analysis result
type SentimentScore struct {
	Symbol      string    `json:"symbol"`
	Overall     float64   `json:"overall"`      // -1.0 (bearish) to +1.0 (bullish)
	Confidence  float64   `json:"confidence"`   // 0.0 to 1.0
	Trend       string    `json:"trend"`        // "bullish", "bearish", "neutral"
	Sources     []string  `json:"sources"`      // Sources consulted
	Keywords    []string  `json:"keywords"`     // Key terms identified
	NewsCount   int       `json:"news_count"`   // Number of news articles
	TweetCount  int       `json:"tweet_count"`  // Number of tweets
	RedditCount int       `json:"reddit_count"` // Number of Reddit posts
	Breakdown   Breakdown `json:"breakdown"`    // Detailed breakdown
	Timestamp   time.Time `json:"timestamp"`
}

// Breakdown provides detailed sentiment by source
type Breakdown struct {
	News    float64 `json:"news"`    // Sentiment from news
	Twitter float64 `json:"twitter"` // Sentiment from Twitter
	Reddit  float64 `json:"reddit"`  // Sentiment from Reddit
}

// NewsImpact represents the impact of recent news
type NewsImpact struct {
	Symbol    string    `json:"symbol"`
	Impact    string    `json:"impact"`    // "positive", "negative", "neutral"
	Severity  float64   `json:"severity"`  // 0.0 to 1.0
	Headlines []string  `json:"headlines"` // Top headlines
	Summary   string    `json:"summary"`   // LLM-generated summary
	Timestamp time.Time `json:"timestamp"`
}

// SocialSentiment represents sentiment from social media
type SocialSentiment struct {
	Symbol       string    `json:"symbol"`
	TwitterScore float64   `json:"twitter_score"`
	RedditScore  float64   `json:"reddit_score"`
	VolumeChange float64   `json:"volume_change"` // Change in mention volume
	TrendingRank int       `json:"trending_rank"` // Ranking in trending
	Timestamp    time.Time `json:"timestamp"`
}

// NewAnalyzer creates a new sentiment analyzer
func NewAnalyzer(
	cfg *config.SentimentConfig,
	llmAnalyzer *llm.Analyzer,
	log *logrus.Logger,
) *Analyzer {
	if log == nil {
		log = logrus.New()
	}

	analyzer := &Analyzer{
		llmAnalyzer: llmAnalyzer,
		cache:       NewSentimentCache(),
		config:      cfg,
		log:         log,
	}

	// Initialize CryptoPanic client if enabled
	if cryptoPanicCfg, ok := cfg.Sources["cryptopanic"]; ok && cryptoPanicCfg.Enabled {
		analyzer.cryptoPanic = NewCryptoPanicClient(
			cryptoPanicCfg.APIKey,
			log,
		)
	}

	// Initialize Twitter client if enabled
	if twitterCfg, ok := cfg.Sources["twitter"]; ok && twitterCfg.Enabled {
		analyzer.twitter = NewTwitterClient(
			twitterCfg.APIKey,
			log,
		)
	}

	// Initialize Reddit client if enabled
	if redditCfg, ok := cfg.Sources["reddit"]; ok && redditCfg.Enabled {
		analyzer.reddit = NewRedditClient(
			redditCfg.ClientID,
			redditCfg.ClientSecret,
			"TradingBot/1.0",
			log,
		)
	}

	return analyzer
}

// AnalyzeMarketSentiment analyzes sentiment from all available sources
func (a *Analyzer) AnalyzeMarketSentiment(ctx context.Context, symbol string) (*SentimentScore, error) {
	if !a.config.Enabled {
		return nil, fmt.Errorf("sentiment analysis is disabled")
	}

	// Check cache first
	if cached := a.cache.Get(symbol); cached != nil {
		a.log.WithField("symbol", symbol).Debug("Returning cached sentiment")
		return cached, nil
	}

	// Fetch data from all sources in parallel
	type sourceResult struct {
		name  string
		score float64
		count int
		err   error
	}

	results := make(chan sourceResult, 3)
	var wg sync.WaitGroup

	// Fetch news
	if a.cryptoPanic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			articles, err := a.cryptoPanic.GetNews(ctx, symbol, 24)
			if err != nil {
				// CryptoPanic client already handles rate-limited WARN logging
				// for repeated failures; log at DEBUG here to avoid duplication.
				a.log.WithError(err).Debug("Failed to fetch news")
				results <- sourceResult{name: "news", err: err}
				return
			}
			score := a.cryptoPanic.GetSentimentScore(articles)
			results <- sourceResult{name: "news", score: score, count: len(articles)}
		}()
	}

	// Fetch tweets
	if a.twitter != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			maxItems := 1000 // Default
			if twitterCfg, ok := a.config.Sources["twitter"]; ok {
				maxItems = twitterCfg.MaxItems
			}
			tweets, err := a.twitter.SearchTweets(ctx, symbol, maxItems)
			if err != nil {
				a.log.WithError(err).Warn("Failed to fetch tweets")
				results <- sourceResult{name: "twitter", err: err}
				return
			}
			score := a.twitter.GetSentimentScore(tweets)
			results <- sourceResult{name: "twitter", score: score, count: len(tweets)}
		}()
	}

	// Fetch Reddit posts
	if a.reddit != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			maxItems := 100 // Default
			if redditCfg, ok := a.config.Sources["reddit"]; ok {
				maxItems = redditCfg.MaxItems
			}
			posts, err := a.reddit.GetPosts(ctx, symbol, maxItems)
			if err != nil {
				a.log.WithError(err).Warn("Failed to fetch Reddit posts")
				results <- sourceResult{name: "reddit", err: err}
				return
			}
			score := a.reddit.GetSentimentScore(posts)
			results <- sourceResult{name: "reddit", score: score, count: len(posts)}
		}()
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var newsScore, twitterScore, redditScore float64
	var newsCount, tweetCount, redditCount int
	var sources []string

	for result := range results {
		if result.err != nil {
			continue
		}

		sources = append(sources, result.name)

		switch result.name {
		case "news":
			newsScore = result.score
			newsCount = result.count
		case "twitter":
			twitterScore = result.score
			tweetCount = result.count
		case "reddit":
			redditScore = result.score
			redditCount = result.count
		}
	}

	// Calculate weighted overall score
	overall := a.calculateWeightedScore(newsScore, twitterScore, redditScore)

	// Determine trend
	trend := "neutral"
	if overall > 0.3 {
		trend = "bullish"
	} else if overall < -0.3 {
		trend = "bearish"
	}

	// Calculate confidence based on data availability
	confidence := a.calculateConfidence(newsCount, tweetCount, redditCount)

	// Build sentiment score
	score := &SentimentScore{
		Symbol:      symbol,
		Overall:     overall,
		Confidence:  confidence,
		Trend:       trend,
		Sources:     sources,
		Keywords:    []string{}, // TODO: Extract keywords using LLM
		NewsCount:   newsCount,
		TweetCount:  tweetCount,
		RedditCount: redditCount,
		Breakdown: Breakdown{
			News:    newsScore,
			Twitter: twitterScore,
			Reddit:  redditScore,
		},
		Timestamp: time.Now(),
	}

	// Cache the result
	ttl := time.Duration(a.config.CacheTTLMinutes) * time.Minute
	a.cache.Set(symbol, score, ttl)

	a.log.WithFields(logrus.Fields{
		"symbol":     symbol,
		"overall":    overall,
		"trend":      trend,
		"confidence": confidence,
		"sources":    len(sources),
	}).Info("Sentiment analysis completed")

	return score, nil
}

// calculateWeightedScore calculates weighted average of sentiment scores
func (a *Analyzer) calculateWeightedScore(news, twitter, reddit float64) float64 {
	// Get weights from config (with defaults)
	newsWeight := 1.0
	twitterWeight := 1.0
	redditWeight := 1.0

	if cryptoPanicCfg, ok := a.config.Sources["cryptopanic"]; ok {
		newsWeight = cryptoPanicCfg.Weight
	}
	if twitterCfg, ok := a.config.Sources["twitter"]; ok {
		twitterWeight = twitterCfg.Weight
	}
	if redditCfg, ok := a.config.Sources["reddit"]; ok {
		redditWeight = redditCfg.Weight
	}

	// Calculate weighted sum
	totalWeight := 0.0
	weightedSum := 0.0

	if a.cryptoPanic != nil && newsWeight > 0 {
		weightedSum += news * newsWeight
		totalWeight += newsWeight
	}

	if a.twitter != nil && twitterWeight > 0 {
		weightedSum += twitter * twitterWeight
		totalWeight += twitterWeight
	}

	if a.reddit != nil && redditWeight > 0 {
		weightedSum += reddit * redditWeight
		totalWeight += redditWeight
	}

	if totalWeight == 0 {
		return 0.0
	}

	return weightedSum / totalWeight
}

// calculateConfidence calculates confidence based on data availability
func (a *Analyzer) calculateConfidence(newsCount, tweetCount, redditCount int) float64 {
	// Base confidence on amount of data
	totalItems := newsCount + tweetCount + redditCount

	if totalItems == 0 {
		return 0.0
	}

	// More data = higher confidence, up to 1.0
	confidence := float64(totalItems) / 100.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	// Reduce confidence if only one source
	sourcesCount := 0
	if newsCount > 0 {
		sourcesCount++
	}
	if tweetCount > 0 {
		sourcesCount++
	}
	if redditCount > 0 {
		sourcesCount++
	}

	if sourcesCount == 1 {
		confidence *= 0.6
	} else if sourcesCount == 2 {
		confidence *= 0.8
	}

	return confidence
}

// GetNewsImpact evaluates the impact of recent news
func (a *Analyzer) GetNewsImpact(ctx context.Context, symbol string, hours int) (*NewsImpact, error) {
	if a.cryptoPanic == nil {
		return nil, fmt.Errorf("CryptoPanic client not configured")
	}

	// Fetch news
	articles, err := a.cryptoPanic.GetNews(ctx, symbol, hours)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch news: %w", err)
	}

	if len(articles) == 0 {
		return &NewsImpact{
			Symbol:    symbol,
			Impact:    "neutral",
			Severity:  0.0,
			Headlines: []string{},
			Summary:   "No recent news found",
			Timestamp: time.Now(),
		}, nil
	}

	// Calculate sentiment score
	score := a.cryptoPanic.GetSentimentScore(articles)

	// Determine impact
	impact := "neutral"
	severity := 0.0

	if score > 0.3 {
		impact = "positive"
		severity = score
	} else if score < -0.3 {
		impact = "negative"
		severity = -score
	}

	// Extract top headlines
	headlines := make([]string, 0, min(5, len(articles)))
	for i := 0; i < min(5, len(articles)); i++ {
		headlines = append(headlines, articles[i].Title)
	}

	// TODO: Use LLM to generate summary
	summary := fmt.Sprintf("Analyzed %d news articles. Overall sentiment: %s", len(articles), impact)

	return &NewsImpact{
		Symbol:    symbol,
		Impact:    impact,
		Severity:  severity,
		Headlines: headlines,
		Summary:   summary,
		Timestamp: time.Now(),
	}, nil
}

// GetSocialSentiment gets sentiment specifically from social media
func (a *Analyzer) GetSocialSentiment(ctx context.Context, symbol string) (*SocialSentiment, error) {
	var twitterScore, redditScore float64

	// Fetch Twitter sentiment
	if a.twitter != nil {
		maxItems := 1000 // Default
		if twitterCfg, ok := a.config.Sources["twitter"]; ok {
			maxItems = twitterCfg.MaxItems
		}
		tweets, err := a.twitter.SearchTweets(ctx, symbol, maxItems)
		if err != nil {
			a.log.WithError(err).Warn("Failed to fetch tweets")
		} else {
			twitterScore = a.twitter.GetSentimentScore(tweets)
		}
	}

	// Fetch Reddit sentiment
	if a.reddit != nil {
		maxItems := 100 // Default
		if redditCfg, ok := a.config.Sources["reddit"]; ok {
			maxItems = redditCfg.MaxItems
		}
		posts, err := a.reddit.GetPosts(ctx, symbol, maxItems)
		if err != nil {
			a.log.WithError(err).Warn("Failed to fetch Reddit posts")
		} else {
			redditScore = a.reddit.GetSentimentScore(posts)
		}
	}

	return &SocialSentiment{
		Symbol:       symbol,
		TwitterScore: twitterScore,
		RedditScore:  redditScore,
		VolumeChange: 0.0, // TODO: Calculate volume change
		TrendingRank: 0,   // TODO: Calculate trending rank
		Timestamp:    time.Now(),
	}, nil
}

// ClearCache clears the sentiment cache
func (a *Analyzer) ClearCache() {
	a.cache.Clear()
	a.log.Debug("Sentiment cache cleared")
}

// GetCacheStats returns cache statistics
func (a *Analyzer) GetCacheStats() map[string]interface{} {
	return map[string]interface{}{
		"entries": a.cache.Size(),
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

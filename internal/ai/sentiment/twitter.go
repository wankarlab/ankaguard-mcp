package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/sirupsen/logrus"
)

// TwitterClient handles interactions with Twitter/X API
type TwitterClient struct {
	apiKey      string
	bearerToken string
	baseURL     string
	httpClient  *http.Client
	log         *logrus.Logger
	maxRetries  int
	retryDelay  time.Duration
}

// Tweet represents a tweet from Twitter API
type Tweet struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
	Metrics   struct {
		Retweets int `json:"retweet_count"`
		Replies  int `json:"reply_count"`
		Likes    int `json:"like_count"`
		Quotes   int `json:"quote_count"`
	} `json:"public_metrics"`
	Entities struct {
		Hashtags []struct {
			Tag string `json:"tag"`
		} `json:"hashtags"`
		Cashtags []struct {
			Tag string `json:"tag"`
		} `json:"cashtags"`
	} `json:"entities"`
}

// TwitterResponse represents the API response
type TwitterResponse struct {
	Data []Tweet `json:"data"`
	Meta struct {
		ResultCount int    `json:"result_count"`
		NextToken   string `json:"next_token"`
	} `json:"meta"`
}

// NewTwitterClient creates a new Twitter API client
func NewTwitterClient(bearerToken string, log *logrus.Logger) *TwitterClient {
	if log == nil {
		log = logrus.New()
	}

	return &TwitterClient{
		bearerToken: bearerToken,
		baseURL:     "https://api.twitter.com/2",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log:        log,
		maxRetries: 3,
		retryDelay: 2 * time.Second,
	}
}

// SearchTweets searches for tweets mentioning a specific symbol
func (t *TwitterClient) SearchTweets(ctx context.Context, symbol string, maxResults int) ([]Tweet, error) {
	if t.bearerToken == "" {
		return nil, fmt.Errorf("Twitter bearer token not configured")
	}

	// Limit max results to Twitter API limits
	if maxResults > 100 {
		maxResults = 100 // Twitter API v2 limit per request
	}

	// Build search query
	// Search for symbol as cashtag ($BTC) or hashtag (#Bitcoin)
	query := fmt.Sprintf("($%s OR #%s) -is:retweet lang:en", symbol, symbol)

	// Build query parameters
	params := url.Values{}
	params.Add("query", query)
	params.Add("max_results", fmt.Sprintf("%d", maxResults))
	params.Add("tweet.fields", "created_at,public_metrics,entities,author_id")
	params.Add("expansions", "author_id")

	// Build URL
	apiURL := fmt.Sprintf("%s/tweets/search/recent?%s", t.baseURL, params.Encode())

	// Make request with retry
	var tweets []Tweet
	var lastErr error

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if attempt > 0 {
			t.log.WithFields(logrus.Fields{
				"attempt": attempt,
				"symbol":  symbol,
			}).Warn("Retrying Twitter API request")

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(t.retryDelay * time.Duration(attempt)):
			}
		}

		tweets, lastErr = t.makeRequest(ctx, apiURL)
		if lastErr == nil {
			t.log.WithFields(logrus.Fields{
				"symbol": symbol,
				"count":  len(tweets),
			}).Debug("Successfully fetched tweets from Twitter")
			return tweets, nil
		}

		// Check if error is retryable
		if !isRetryableError(lastErr) {
			break
		}
	}

	return nil, fmt.Errorf("failed to fetch tweets after %d attempts: %w", t.maxRetries+1, lastErr)
}

// makeRequest makes the actual HTTP request
func (t *TwitterClient) makeRequest(ctx context.Context, apiURL string) ([]Tweet, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+t.bearerToken)
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp TwitterResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return apiResp.Data, nil
}

// SearchTweetsWithPagination searches for tweets with pagination support
func (t *TwitterClient) SearchTweetsWithPagination(ctx context.Context, symbol string, maxTotal int) ([]Tweet, error) {
	if t.bearerToken == "" {
		return nil, fmt.Errorf("Twitter bearer token not configured")
	}

	var allTweets []Tweet
	nextToken := ""
	perPage := 100 // Max per request

	for len(allTweets) < maxTotal {
		// Calculate how many to fetch in this request
		remaining := maxTotal - len(allTweets)
		if remaining < perPage {
			perPage = remaining
		}

		// Build query
		query := fmt.Sprintf("($%s OR #%s) -is:retweet lang:en", symbol, symbol)
		params := url.Values{}
		params.Add("query", query)
		params.Add("max_results", fmt.Sprintf("%d", perPage))
		params.Add("tweet.fields", "created_at,public_metrics,entities,author_id")

		if nextToken != "" {
			params.Add("next_token", nextToken)
		}

		apiURL := fmt.Sprintf("%s/tweets/search/recent?%s", t.baseURL, params.Encode())

		// Make request
		tweets, err := t.makeRequest(ctx, apiURL)
		if err != nil {
			return allTweets, err
		}

		allTweets = append(allTweets, tweets...)

		// Check if there are more results
		if len(tweets) < perPage {
			break // No more results
		}

		// TODO: Extract next_token from response metadata
		// For now, we'll just break after one page
		break
	}

	t.log.WithFields(logrus.Fields{
		"symbol": symbol,
		"count":  len(allTweets),
	}).Debug("Fetched tweets with pagination")

	return allTweets, nil
}

// GetSentimentScore calculates an overall sentiment score from tweets
func (t *TwitterClient) GetSentimentScore(tweets []Tweet) float64 {
	if len(tweets) == 0 {
		return 0.0
	}

	// Simple sentiment based on engagement
	// High engagement (likes, retweets) suggests positive sentiment
	var totalScore float64

	for _, tweet := range tweets {
		// Calculate engagement score
		engagement := tweet.Metrics.Likes + tweet.Metrics.Retweets*2 + tweet.Metrics.Quotes

		// Normalize to -1 to +1 range
		// This is a simplified approach - in production, use NLP sentiment analysis
		if engagement > 100 {
			totalScore += 1.0
		} else if engagement > 50 {
			totalScore += 0.5
		} else if engagement > 10 {
			totalScore += 0.2
		} else {
			totalScore += 0.0
		}
	}

	// Return average score
	return totalScore / float64(len(tweets))
}

// ExtractMentionedSymbols extracts cryptocurrency symbols from tweets
func (t *TwitterClient) ExtractMentionedSymbols(tweets []Tweet) map[string]int {
	symbols := make(map[string]int)

	for _, tweet := range tweets {
		// Extract from cashtags
		for _, cashtag := range tweet.Entities.Cashtags {
			symbols[cashtag.Tag]++
		}

		// Extract from hashtags (common crypto hashtags)
		for _, hashtag := range tweet.Entities.Hashtags {
			tag := hashtag.Tag
			// Check if it's a known crypto symbol
			if isCryptoSymbol(tag) {
				symbols[tag]++
			}
		}
	}

	return symbols
}

// isCryptoSymbol checks if a hashtag is a known cryptocurrency symbol
func isCryptoSymbol(tag string) bool {
	// Common cryptocurrency symbols
	cryptoSymbols := map[string]bool{
		"BTC": true, "Bitcoin": true,
		"ETH": true, "Ethereum": true,
		"DOGE": true, "Dogecoin": true,
		"ADA": true, "Cardano": true,
		"SOL": true, "Solana": true,
		"XRP": true, "Ripple": true,
		"DOT": true, "Polkadot": true,
		"MATIC": true, "Polygon": true,
		"AVAX": true, "Avalanche": true,
		"LINK": true, "Chainlink": true,
	}

	return cryptoSymbols[tag]
}

// GetTrendingSymbols returns the most mentioned symbols from tweets
func (t *TwitterClient) GetTrendingSymbols(tweets []Tweet, topN int) []string {
	symbols := t.ExtractMentionedSymbols(tweets)

	// Sort by count
	type symbolCount struct {
		symbol string
		count  int
	}

	var sorted []symbolCount
	for symbol, count := range symbols {
		sorted = append(sorted, symbolCount{symbol, count})
	}

	// Simple bubble sort (good enough for small lists)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Return top N
	var result []string
	for i := 0; i < topN && i < len(sorted); i++ {
		result = append(result, sorted[i].symbol)
	}

	return result
}

package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// CryptoPanicClient handles interactions with CryptoPanic API
type CryptoPanicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	log        *logrus.Logger
	maxRetries int
	retryDelay time.Duration

	// Error log rate limiting
	errMu             sync.Mutex
	lastErrorLoggedAt time.Time
	lastErrorStatus   string
}

// permanentError signals a non-retryable client-side (4xx) failure.
// It carries the HTTP status code so callers can decide how loud to log.
type permanentError struct {
	statusCode int
	statusText string
	bodyPeek   string
}

func (e *permanentError) Error() string {
	if e.bodyPeek != "" {
		return fmt.Sprintf("API returned %d %s: %s", e.statusCode, e.statusText, e.bodyPeek)
	}
	return fmt.Sprintf("API returned %d %s", e.statusCode, e.statusText)
}

// isPermanent reports whether the given error is a known non-retryable client error.
func isPermanent(err error) bool {
	_, ok := err.(*permanentError)
	return ok
}

// NewsArticle represents a news article from CryptoPanic
type NewsArticle struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Source      string    `json:"source"`
	PublishedAt time.Time `json:"published_at"`
	Sentiment   string    `json:"sentiment"` // positive, negative, neutral
	Votes       struct {
		Positive int `json:"positive"`
		Negative int `json:"negative"`
		Neutral  int `json:"neutral"`
	} `json:"votes"`
	Currencies []struct {
		Code  string `json:"code"`
		Title string `json:"title"`
	} `json:"currencies"`
}

// CryptoPanicResponse represents the API response
type CryptoPanicResponse struct {
	Count   int           `json:"count"`
	Next    string        `json:"next"`
	Results []NewsArticle `json:"results"`
}

// NewCryptoPanicClient creates a new CryptoPanic API client
func NewCryptoPanicClient(apiKey string, log *logrus.Logger) *CryptoPanicClient {
	if log == nil {
		log = logrus.New()
	}

	return &CryptoPanicClient{
		apiKey: apiKey,
		// CryptoPanic deprecated the old /api/v1 path (now returns 404). The
		// current REST API is /api/{plan}/v2/posts/ with the same `results`
		// selector; "developer" is the free/developer plan tier. A valid
		// CRYPTOPANIC_API_KEY must be configured (the .env default is a
		// placeholder, which is why sentiment falls back to neutral).
		baseURL: "https://cryptopanic.com/api/developer/v2",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log:        log,
		maxRetries: 3,
		retryDelay: 2 * time.Second,
	}
}

// shouldLogError returns true if an error should be logged loudly (WARN level).
// Suppresses duplicate WARNs within a 5-minute window to keep logs clean when
// the CryptoPanic API is persistently failing (e.g. 404, invalid key).
// When it returns true it updates the internal timestamp.
func (c *CryptoPanicClient) shouldLogError() bool {
	c.errMu.Lock()
	defer c.errMu.Unlock()

	if time.Since(c.lastErrorLoggedAt) > 5*time.Minute {
		c.lastErrorLoggedAt = time.Now()
		return true
	}
	return false
}

// recordErrorStatus stores the last observed error status for summary logging.
func (c *CryptoPanicClient) recordErrorStatus(status string) {
	c.errMu.Lock()
	c.lastErrorStatus = status
	c.errMu.Unlock()
}

// GetNews fetches news articles for a specific symbol within a time range
func (c *CryptoPanicClient) GetNews(ctx context.Context, symbol string, hoursBack int) ([]NewsArticle, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("CryptoPanic API key not configured")
	}

	// Build query parameters
	params := url.Values{}
	params.Add("auth_token", c.apiKey)
	params.Add("currencies", symbol)
	params.Add("kind", "news")     // Only news, not media
	params.Add("filter", "rising") // Rising news

	// Calculate time range
	since := time.Now().Add(-time.Duration(hoursBack) * time.Hour)
	params.Add("public", "true")

	// Build URL
	apiURL := fmt.Sprintf("%s/posts/?%s", c.baseURL, params.Encode())

	// Make request with retry, but bail out fast on permanent 4xx errors
	var articles []NewsArticle
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// Only retries reach this branch; log at DEBUG to avoid noise on repeated failures
			c.log.WithFields(logrus.Fields{
				"attempt": attempt,
				"symbol":  symbol,
			}).Debug("Retrying CryptoPanic API request")

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.retryDelay * time.Duration(attempt)):
			}
		}

		articles, lastErr = c.makeRequest(ctx, apiURL, since)
		if lastErr == nil {
			c.log.WithFields(logrus.Fields{
				"symbol": symbol,
				"count":  len(articles),
				"hours":  hoursBack,
			}).Debug("Successfully fetched news from CryptoPanic")
			return articles, nil
		}

		// Permanent client-side errors (4xx) — do not retry, fail fast
		if isPermanent(lastErr) {
			break
		}

		// Check if error is retryable (network errors, 5xx, rate limits)
		if !isRetryableError(lastErr) {
			break
		}
	}

	// Capture status string for summary logging
	statusLabel := "unknown"
	if pe, ok := lastErr.(*permanentError); ok {
		statusLabel = fmt.Sprintf("%d", pe.statusCode)
	}
	c.recordErrorStatus(statusLabel)

	// Rate-limited logging: one WARN per 5 min window, DEBUG otherwise
	if c.shouldLogError() {
		c.log.WithFields(logrus.Fields{
			"symbol":      symbol,
			"last_status": statusLabel,
			"error":       lastErr,
		}).Warn("CryptoPanic API failing repeatedly (sentiment falling back to neutral)")
	} else {
		c.log.WithFields(logrus.Fields{
			"symbol":      symbol,
			"last_status": statusLabel,
		}).Debug("CryptoPanic API request failed (suppressed)")
	}

	return nil, fmt.Errorf("cryptopanic: %w", lastErr)
}

// makeRequest makes the actual HTTP request.
// Returns *permanentError for 4xx responses so callers can skip retries.
func (c *CryptoPanicClient) makeRequest(ctx context.Context, apiURL string, since time.Time) ([]NewsArticle, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// For non-2xx responses, read only a peek of the body to avoid logging
	// huge HTML 404 pages. For success responses, read the whole body.
	if resp.StatusCode != http.StatusOK {
		// Read at most 256 bytes — enough for a short JSON error, not enough for HTML spam
		peek, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		bodyPeek := strings.TrimSpace(string(peek))
		if len(bodyPeek) > 80 {
			bodyPeek = bodyPeek[:80]
		}
		// Strip newlines so it stays a single log line
		bodyPeek = strings.ReplaceAll(bodyPeek, "\n", " ")
		bodyPeek = strings.ReplaceAll(bodyPeek, "\r", " ")

		// 4xx are permanent: bad key, wrong endpoint, forbidden — do not retry
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, &permanentError{
				statusCode: resp.StatusCode,
				statusText: http.StatusText(resp.StatusCode),
				bodyPeek:   bodyPeek,
			}
		}

		// 5xx and others: transient, short message (no body dump)
		return nil, fmt.Errorf("API returned status %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	// Success: read full body and parse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response
	var apiResp CryptoPanicResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Filter by time
	var filtered []NewsArticle
	for _, article := range apiResp.Results {
		if article.PublishedAt.After(since) {
			filtered = append(filtered, article)
		}
	}

	return filtered, nil
}

// GetNewsByFilter fetches news with custom filters
func (c *CryptoPanicClient) GetNewsByFilter(ctx context.Context, filter NewsFilter) ([]NewsArticle, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("CryptoPanic API key not configured")
	}

	// Build query parameters
	params := url.Values{}
	params.Add("auth_token", c.apiKey)

	if filter.Currencies != "" {
		params.Add("currencies", filter.Currencies)
	}

	if filter.Kind != "" {
		params.Add("kind", filter.Kind)
	}

	if filter.Filter != "" {
		params.Add("filter", filter.Filter)
	}

	if filter.Regions != "" {
		params.Add("regions", filter.Regions)
	}

	// Build URL
	apiURL := fmt.Sprintf("%s/posts/?%s", c.baseURL, params.Encode())

	// Make request
	articles, err := c.makeRequest(ctx, apiURL, time.Time{})
	if err != nil {
		return nil, err
	}

	// Apply limit if specified
	if filter.Limit > 0 && len(articles) > filter.Limit {
		articles = articles[:filter.Limit]
	}

	return articles, nil
}

// NewsFilter defines filters for news queries
type NewsFilter struct {
	Currencies string // Comma-separated currency codes (e.g., "BTC,ETH")
	Kind       string // news, media, or all
	Filter     string // rising, hot, bullish, bearish, important, saved, lol
	Regions    string // Comma-separated regions (e.g., "en,de")
	Limit      int    // Maximum number of articles to return
}

// GetSentimentScore calculates an overall sentiment score from articles
func (c *CryptoPanicClient) GetSentimentScore(articles []NewsArticle) float64 {
	if len(articles) == 0 {
		return 0.0
	}

	var totalScore float64
	var totalVotes int

	for _, article := range articles {
		votes := article.Votes.Positive + article.Votes.Negative + article.Votes.Neutral
		if votes == 0 {
			continue
		}

		// Calculate score: positive votes contribute +1, negative -1, neutral 0
		score := float64(article.Votes.Positive-article.Votes.Negative) / float64(votes)
		totalScore += score
		totalVotes++
	}

	if totalVotes == 0 {
		return 0.0
	}

	// Return average score normalized to -1 to +1
	return totalScore / float64(totalVotes)
}

// isRetryableError checks if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Permanent errors (4xx) are never retryable
	if isPermanent(err) {
		return false
	}

	errStr := err.Error()

	// Don't retry context errors
	if errStr == "context canceled" || errStr == "context deadline exceeded" {
		return false
	}

	// Retry rate limits, timeouts, and server errors
	return true
}

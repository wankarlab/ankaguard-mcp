package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// RedditClient handles interactions with Reddit API
type RedditClient struct {
	clientID     string
	clientSecret string
	userAgent    string
	accessToken  string
	baseURL      string
	httpClient   *http.Client
	log          *logrus.Logger
	maxRetries   int
	retryDelay   time.Duration
}

// RedditPost represents a post from Reddit
type RedditPost struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Selftext    string  `json:"selftext"`
	Author      string  `json:"author"`
	Subreddit   string  `json:"subreddit"`
	Score       int     `json:"score"`
	Upvotes     int     `json:"ups"`
	Downvotes   int     `json:"downs"`
	NumComments int     `json:"num_comments"`
	CreatedUTC  float64 `json:"created_utc"`
	URL         string  `json:"url"`
	Permalink   string  `json:"permalink"`
}

// RedditResponse represents the API response
type RedditResponse struct {
	Data struct {
		Children []struct {
			Data RedditPost `json:"data"`
		} `json:"children"`
		After  string `json:"after"`
		Before string `json:"before"`
	} `json:"data"`
}

// NewRedditClient creates a new Reddit API client
func NewRedditClient(clientID, clientSecret, userAgent string, log *logrus.Logger) *RedditClient {
	if log == nil {
		log = logrus.New()
	}

	return &RedditClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		userAgent:    userAgent,
		baseURL:      "https://oauth.reddit.com",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log:        log,
		maxRetries: 3,
		retryDelay: 2 * time.Second,
	}
}

// Authenticate obtains an access token from Reddit
func (r *RedditClient) Authenticate(ctx context.Context) error {
	if r.clientID == "" || r.clientSecret == "" {
		return fmt.Errorf("Reddit client ID and secret not configured")
	}

	// Build request
	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, "POST", "https://www.reddit.com/api/v1/access_token", strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create auth request: %w", err)
	}

	req.SetBasicAuth(r.clientID, r.clientSecret)
	req.Header.Set("User-Agent", r.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var authResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return fmt.Errorf("failed to parse auth response: %w", err)
	}

	r.accessToken = authResp.AccessToken
	r.log.Debug("Successfully authenticated with Reddit API")

	return nil
}

// GetPosts fetches posts from specified subreddits mentioning a symbol
func (r *RedditClient) GetPosts(ctx context.Context, symbol string, maxResults int) ([]RedditPost, error) {
	if r.accessToken == "" {
		if err := r.Authenticate(ctx); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
	}

	// Default cryptocurrency subreddits
	subreddits := []string{
		"cryptocurrency",
		"CryptoMarkets",
		"Bitcoin",
		"ethereum",
		"altcoin",
		"CryptoCurrency",
	}

	var allPosts []RedditPost

	// Search in each subreddit
	for _, subreddit := range subreddits {
		if len(allPosts) >= maxResults {
			break
		}

		posts, err := r.searchSubreddit(ctx, subreddit, symbol, maxResults-len(allPosts))
		if err != nil {
			r.log.WithFields(logrus.Fields{
				"subreddit": subreddit,
				"error":     err,
			}).Warn("Failed to fetch posts from subreddit")
			continue
		}

		allPosts = append(allPosts, posts...)
	}

	r.log.WithFields(logrus.Fields{
		"symbol": symbol,
		"count":  len(allPosts),
	}).Debug("Successfully fetched posts from Reddit")

	return allPosts, nil
}

// searchSubreddit searches for posts in a specific subreddit
func (r *RedditClient) searchSubreddit(ctx context.Context, subreddit, symbol string, limit int) ([]RedditPost, error) {
	// Build query parameters
	params := url.Values{}
	params.Add("q", symbol)
	params.Add("limit", fmt.Sprintf("%d", min(limit, 100))) // Reddit API limit
	params.Add("sort", "new")
	params.Add("restrict_sr", "true") // Restrict to this subreddit
	params.Add("t", "day")            // Time filter: day

	// Build URL
	apiURL := fmt.Sprintf("%s/r/%s/search?%s", r.baseURL, subreddit, params.Encode())

	// Make request with retry
	var posts []RedditPost
	var lastErr error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			r.log.WithFields(logrus.Fields{
				"attempt":   attempt,
				"subreddit": subreddit,
			}).Warn("Retrying Reddit API request")

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(r.retryDelay * time.Duration(attempt)):
			}
		}

		posts, lastErr = r.makeRequest(ctx, apiURL)
		if lastErr == nil {
			return posts, nil
		}

		// Check if error is retryable
		if !isRetryableError(lastErr) {
			break
		}
	}

	return nil, fmt.Errorf("failed to fetch posts after %d attempts: %w", r.maxRetries+1, lastErr)
}

// makeRequest makes the actual HTTP request
func (r *RedditClient) makeRequest(ctx context.Context, apiURL string) ([]RedditPost, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+r.accessToken)
	req.Header.Set("User-Agent", r.userAgent)

	resp, err := r.httpClient.Do(req)
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
	if resp.StatusCode == http.StatusUnauthorized {
		// Token expired, clear it
		r.accessToken = ""
		return nil, fmt.Errorf("unauthorized - token may have expired")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp RedditResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract posts
	var posts []RedditPost
	for _, child := range apiResp.Data.Children {
		posts = append(posts, child.Data)
	}

	return posts, nil
}

// GetHotPosts fetches hot posts from cryptocurrency subreddits
func (r *RedditClient) GetHotPosts(ctx context.Context, subreddit string, limit int) ([]RedditPost, error) {
	if r.accessToken == "" {
		if err := r.Authenticate(ctx); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
	}

	// Build URL
	params := url.Values{}
	params.Add("limit", fmt.Sprintf("%d", min(limit, 100)))

	apiURL := fmt.Sprintf("%s/r/%s/hot?%s", r.baseURL, subreddit, params.Encode())

	return r.makeRequest(ctx, apiURL)
}

// GetSentimentScore calculates an overall sentiment score from posts
func (r *RedditClient) GetSentimentScore(posts []RedditPost) float64 {
	if len(posts) == 0 {
		return 0.0
	}

	var totalScore float64

	for _, post := range posts {
		// Calculate sentiment based on upvote ratio
		totalVotes := post.Upvotes + post.Downvotes
		if totalVotes == 0 {
			continue
		}

		// Upvote ratio: 1.0 = all upvotes, 0.0 = all downvotes
		ratio := float64(post.Upvotes) / float64(totalVotes)

		// Convert to -1 to +1 scale
		score := (ratio * 2) - 1

		// Weight by engagement (score and comments)
		weight := float64(post.Score + post.NumComments)
		if weight < 1 {
			weight = 1
		}

		totalScore += score * weight
	}

	// Normalize
	if len(posts) == 0 {
		return 0.0
	}

	return totalScore / float64(len(posts))
}

// FilterBySymbol filters posts that mention a specific symbol
func (r *RedditClient) FilterBySymbol(posts []RedditPost, symbol string) []RedditPost {
	var filtered []RedditPost

	symbolLower := strings.ToLower(symbol)

	for _, post := range posts {
		titleLower := strings.ToLower(post.Title)
		textLower := strings.ToLower(post.Selftext)

		if strings.Contains(titleLower, symbolLower) || strings.Contains(textLower, symbolLower) {
			filtered = append(filtered, post)
		}
	}

	return filtered
}

// GetTopDiscussedSymbols returns the most discussed symbols from posts
func (r *RedditClient) GetTopDiscussedSymbols(posts []RedditPost, topN int) map[string]int {
	symbols := make(map[string]int)

	// Common crypto symbols to look for
	cryptoSymbols := []string{
		"BTC", "Bitcoin",
		"ETH", "Ethereum",
		"DOGE", "Dogecoin",
		"ADA", "Cardano",
		"SOL", "Solana",
		"XRP", "Ripple",
		"DOT", "Polkadot",
		"MATIC", "Polygon",
		"AVAX", "Avalanche",
		"LINK", "Chainlink",
	}

	for _, post := range posts {
		text := strings.ToLower(post.Title + " " + post.Selftext)

		for _, symbol := range cryptoSymbols {
			if strings.Contains(text, strings.ToLower(symbol)) {
				symbols[symbol]++
			}
		}
	}

	return symbols
}

// min returns the minimum of two integers
/* func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
*/

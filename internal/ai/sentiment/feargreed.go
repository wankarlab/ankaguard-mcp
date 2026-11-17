package sentiment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const fearGreedURL = "https://api.alternative.me/fng/?limit=1&format=json"

// FearGreedIndex is the numeric score (0=Extreme Fear, 100=Extreme Greed).
type FearGreedIndex struct {
	Value           int    `json:"value"`
	Classification  string `json:"value_classification"` // "Extreme Fear", "Fear", "Neutral", "Greed", "Extreme Greed"
	Timestamp       int64  `json:"timestamp,string"`
	FetchedAt       time.Time
}

// FearGreedClient fetches the Crypto Fear & Greed Index from alternative.me.
// The endpoint is public and requires no API key. Results are cached for
// CacheTTL (default 15 min) to avoid hammering the free endpoint.
type FearGreedClient struct {
	httpClient *http.Client
	log        *logrus.Logger
	CacheTTL   time.Duration

	mu        sync.Mutex
	cached    *FearGreedIndex
	cacheTime time.Time
}

// NewFearGreedClient creates a new FearGreedClient with sane defaults.
func NewFearGreedClient(log *logrus.Logger) *FearGreedClient {
	if log == nil {
		log = logrus.New()
	}
	return &FearGreedClient{
		httpClient: &http.Client{Timeout: 8 * time.Second},
		log:        log,
		CacheTTL:   15 * time.Minute,
	}
}

type fngAPIResponse struct {
	Data []struct {
		Value               string `json:"value"`
		ValueClassification string `json:"value_classification"`
		Timestamp           string `json:"timestamp"`
	} `json:"data"`
}

// Get returns the latest Fear & Greed index, using a cached value when fresh.
func (c *FearGreedClient) Get(ctx context.Context) (*FearGreedIndex, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cached != nil && time.Since(c.cacheTime) < c.CacheTTL {
		return c.cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fearGreedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("feargreed: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feargreed: http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feargreed: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("feargreed: read body: %w", err)
	}

	var apiResp fngAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("feargreed: parse json: %w", err)
	}
	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("feargreed: empty data array")
	}

	d := apiResp.Data[0]
	score := 0
	fmt.Sscanf(d.Value, "%d", &score)

	ts := int64(0)
	fmt.Sscanf(d.Timestamp, "%d", &ts)

	idx := &FearGreedIndex{
		Value:          score,
		Classification: d.ValueClassification,
		Timestamp:      ts,
		FetchedAt:      time.Now(),
	}
	c.cached = idx
	c.cacheTime = time.Now()

	c.log.WithFields(logrus.Fields{
		"score":          score,
		"classification": d.ValueClassification,
	}).Debug("fear & greed index fetched")

	return idx, nil
}

// IsFearExtreme returns true when score <= 20 (Extreme Fear — avoid opening longs).
func (f *FearGreedIndex) IsFearExtreme() bool { return f.Value <= 20 }

// IsGreedExtreme returns true when score >= 80 (Extreme Greed — avoid opening shorts).
func (f *FearGreedIndex) IsGreedExtreme() bool { return f.Value >= 80 }

// Summary returns a short human-readable string for LLM context injection.
func (f *FearGreedIndex) Summary() string {
	return fmt.Sprintf("Crypto Fear & Greed Index: %d/100 (%s)", f.Value, f.Classification)
}

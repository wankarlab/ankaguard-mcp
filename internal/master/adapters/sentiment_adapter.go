package adapters

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai/sentiment"
	"github.com/rantipay/trading-mcp/internal/master"
)

// SentimentAdapter implements master.SentimentSource on top of the existing
// sentiment.Analyzer. It caches results per pair to avoid hitting the
// external sources on every Master scan tick.
type SentimentAdapter struct {
	log      *logrus.Logger
	analyzer *sentiment.Analyzer

	cacheTTL time.Duration

	mu    sync.RWMutex
	cache map[string]*cachedSentiment
}

type cachedSentiment struct {
	score     float64
	fetchedAt time.Time
}

// NewSentimentAdapter creates the adapter. analyzer may be nil — in that case
// GetSentimentScore returns 0 (neutral) to avoid breaking callers.
func NewSentimentAdapter(log *logrus.Logger, analyzer *sentiment.Analyzer, cacheTTL time.Duration) *SentimentAdapter {
	if log == nil {
		log = logrus.New()
	}
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	return &SentimentAdapter{
		log:      log,
		analyzer: analyzer,
		cacheTTL: cacheTTL,
		cache:    make(map[string]*cachedSentiment),
	}
}

// GetSentimentScore returns -1..+1. Cached for cacheTTL per pair.
// On error, returns 0 (neutral) and logs a debug message — sentiment is
// non-critical for the Master Agent loop.
func (s *SentimentAdapter) GetSentimentScore(ctx context.Context, pair string) (float64, error) {
	if s.analyzer == nil {
		return 0, nil
	}

	s.mu.RLock()
	if entry, ok := s.cache[pair]; ok && time.Since(entry.fetchedAt) < s.cacheTTL {
		score := entry.score
		s.mu.RUnlock()
		return score, nil
	}
	s.mu.RUnlock()

	// Fetch fresh
	score, err := s.fetchScore(ctx, pair)
	if err != nil {
		s.log.WithError(err).WithField("pair", pair).Debug("sentiment fetch failed")
		return 0, nil // graceful degradation
	}

	s.mu.Lock()
	s.cache[pair] = &cachedSentiment{score: score, fetchedAt: time.Now()}
	s.mu.Unlock()

	return score, nil
}

// fetchScore wraps the actual call to the analyzer.
//
// The real sentiment.Analyzer exposes:
//
//	AnalyzeMarketSentiment(ctx, symbol string) (*SentimentScore, error)
//
// where SentimentScore.Overall is already in the -1..+1 range
// (bearish ↔ bullish), which is exactly what master.SentimentSource expects.
func (s *SentimentAdapter) fetchScore(ctx context.Context, pair string) (float64, error) {
	result, err := s.analyzer.AnalyzeMarketSentiment(ctx, pair)
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}

	score := result.Overall
	// Defensive clamp in case an upstream source returns out-of-range values.
	if score > 1.0 {
		score = 1.0
	} else if score < -1.0 {
		score = -1.0
	}
	return score, nil
}

// Compile-time check that SentimentAdapter satisfies master.SentimentSource
var _ master.SentimentSource = (*SentimentAdapter)(nil)

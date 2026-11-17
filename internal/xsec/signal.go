package xsec

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/storage"
)

// Ranker ranks pairs by trailing daily momentum from the candle_archive.
type Ranker struct {
	repo      *storage.CandleArchiveRepository
	timeframe string
	log       *logrus.Logger
}

// NewMomentumRanker reads candles of the given timeframe (typically "1d") from
// the candle_archive table in db.
func NewMomentumRanker(db *sql.DB, timeframe string) *Ranker {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	return &Ranker{
		repo:      storage.NewCandleArchiveRepository(db, log),
		timeframe: timeframe,
		log:       log,
	}
}

// pairMomentum carries a pair's trailing momentum and the timestamp of its
// latest candle, used both for ranking and to derive the basket AsOf.
type pairMomentum struct {
	pair     string
	momentum float64
	latest   time.Time
}

// Momentum computes trailing lookbackDays-bar momentum
// (close_now/close_lookback - 1) for each pair as of its latest candle.
// Pairs lacking enough history are omitted. The returned time is the most
// recent candle timestamp across the scored pairs.
func (r *Ranker) Momentum(pairs []string, lookbackDays int) (map[string]float64, time.Time, error) {
	if lookbackDays < 1 {
		return nil, time.Time{}, fmt.Errorf("lookbackDays must be >= 1, got %d", lookbackDays)
	}
	scores := make(map[string]float64, len(pairs))
	var asOf time.Time
	for _, p := range pairs {
		candles, err := r.repo.LoadRange(p, r.timeframe, time.Time{}, time.Now())
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("load candles for %s: %w", p, err)
		}
		// Need the latest bar plus lookbackDays bars of history before it.
		if len(candles) < lookbackDays+1 {
			continue
		}
		latest := candles[len(candles)-1]
		past := candles[len(candles)-1-lookbackDays]
		if past.Close <= 0 {
			continue
		}
		scores[p] = latest.Close/past.Close - 1
		if latest.Timestamp.After(asOf) {
			asOf = latest.Timestamp
		}
	}
	return scores, asOf, nil
}

// Rank computes trailing momentum for each pair, sorts descending, and
// returns the top-k pairs as Longs and the bottom-k as Shorts.
func (r *Ranker) Rank(pairs []string, lookbackDays, k int) (Baskets, error) {
	if k < 1 {
		return Baskets{}, fmt.Errorf("k must be >= 1, got %d", k)
	}
	scores, asOf, err := r.Momentum(pairs, lookbackDays)
	if err != nil {
		return Baskets{}, err
	}
	if len(scores) < 2*k {
		return Baskets{}, fmt.Errorf(
			"not enough pairs with %d bars of history: have %d, need %d for k=%d",
			lookbackDays+1, len(scores), 2*k, k)
	}
	// Rank has no notion of "previous holdings" (used by xsec-live's stateless
	// Rank(pairs, lb, k) call sites); band is always off here.
	return BuildBaskets(scores, k, asOf, 0, nil, nil), nil
}

// LatestClose returns the most recent close price per pair (for paper marks /
// position sizing). Pairs with no candles are omitted from the result.
func (r *Ranker) LatestClose(pairs []string) (map[string]float64, error) {
	out := make(map[string]float64, len(pairs))
	for _, p := range pairs {
		candles, err := r.repo.LoadRange(p, r.timeframe, time.Time{}, time.Now())
		if err != nil {
			return nil, fmt.Errorf("load candles for %s: %w", p, err)
		}
		if len(candles) == 0 {
			continue
		}
		out[p] = candles[len(candles)-1].Close
	}
	return out, nil
}

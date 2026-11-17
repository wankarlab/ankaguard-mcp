package master

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/regime"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/sirupsen/logrus"
)

// deprecatedKuCoinPairs contains pairs that have been delisted or renamed on
// KuCoin. The scanner will silently skip them instead of logging errors on
// every scan cycle. Value is the suggested replacement pair (empty if none).
var deprecatedKuCoinPairs = map[string]string{
	"MATIC-USDT": "POL-USDT", // renamed to Polygon (POL) in 2024
}

// MarketScanner scans an exchange and ranks pairs by opportunity.
type MarketScanner struct {
	log               *logrus.Logger
	gctClient         GCTAdapter
	regimeDetector    RegimeAnalyzer
	sentimentAnalyzer SentimentSource
	btcMonitor        BTCMonitor
	weights           *ScoringWeights
	feedback          map[string]*PairFeedback
	feedbackMu        sync.RWMutex
}

// NewMarketScanner constructs a scanner. Sentiment and BTC monitors are optional.
func NewMarketScanner(
	log *logrus.Logger,
	gctClient GCTAdapter,
	regimeDetector RegimeAnalyzer,
	sentiment SentimentSource,
	btc BTCMonitor,
	weights *ScoringWeights,
) *MarketScanner {
	if log == nil {
		log = logrus.New()
	}
	if weights == nil {
		weights = &ScoringWeights{
			Volatility:            0.15,
			Volume:                0.10,
			TrendStrength:         0.20,
			Sentiment:             0.05, // sentiment source is a placeholder — weight kept low
			RegimeMatch:           0.15,
			BTCCorrelationPenalty: 0.05,
			RecentLossPenalty:     0.20, // raised from 0.05 — recent losses dominate over structural metrics
			ProfitFactor:          0.10, // expectancy-per-trade bonus/penalty
		}
	}
	return &MarketScanner{
		log:               log,
		gctClient:         gctClient,
		regimeDetector:    regimeDetector,
		sentimentAnalyzer: sentiment,
		btcMonitor:        btc,
		weights:           weights,
		feedback:          make(map[string]*PairFeedback),
	}
}

// Scan returns the top opportunities sorted by score descending.
func (ms *MarketScanner) Scan(ctx context.Context, exchange string, topN int) ([]*MarketOpportunity, error) {
	if ms.gctClient == nil {
		return nil, fmt.Errorf("market scanner: gct client is nil")
	}
	if ms.regimeDetector == nil {
		return nil, fmt.Errorf("market scanner: regime detector is nil")
	}

	// Pair universe is now sourced dynamically inside the adapter's stage-1 funnel
	// (live discovery + exclude/volume filters); the scanner just requests top-N.
	tickers, err := ms.gctClient.GetTopPairsByVolume(ctx, exchange, topN)
	if err != nil {
		return nil, fmt.Errorf("get top pairs: %w", err)
	}

	out := make([]*MarketOpportunity, 0, len(tickers))
	for _, t := range tickers {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}

		// Skip pairs that are known to be deprecated or renamed on this exchange.
		// This avoids noisy ERROR logs on every scan cycle for unavailable pairs.
		if replacement, deprecated := deprecatedKuCoinPairs[t.Pair]; deprecated {
			ms.log.WithFields(logrus.Fields{
				"pair":        t.Pair,
				"replacement": replacement,
			}).Debug("scanner: skipping deprecated pair")
			continue
		}

		candles, err := ms.gctClient.GetCandles(ctx, exchange, t.Pair, "60", 200)
		if err != nil {
			ms.log.WithError(err).WithField("pair", t.Pair).Debug("scanner: candles fetch failed, skipping")
			continue
		}
		if len(candles) < 50 {
			ms.log.WithField("pair", t.Pair).Debug("scanner: insufficient candles, skipping")
			continue
		}

		analysis, err := ms.regimeDetector.Analyze(candles)
		if err != nil {
			ms.log.WithError(err).WithField("pair", t.Pair).Debug("scanner: regime analysis failed, skipping")
			continue
		}

		opp := ms.scorePair(t, candles, analysis)
		// Tag with the scanned exchange argument so the (pair × exchange) matrix
		// has a reliable exchange label. ticker.Exchange may be empty; fall back
		// to it only when the scanned exchange arg is empty.
		if exchange != "" {
			opp.Exchange = exchange
		}
		if ms.sentimentAnalyzer != nil {
			if s, err := ms.sentimentAnalyzer.GetSentimentScore(ctx, t.Pair); err == nil {
				opp.SentimentScore = s
			}
		}
		if ms.btcMonitor != nil {
			opp.BTCCorrelation = ms.btcMonitor.GetCorrelation(t.Pair)
		}

		opp.OpportunityScore = ms.computeScore(ctx, opp, analysis)
		opp.CachedAnalysis = analysis // reused by master-agent tick to avoid second fetch
		out = append(out, opp)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].OpportunityScore > out[j].OpportunityScore
	})

	ms.log.WithFields(logrus.Fields{
		"exchange": exchange,
		"top_n":    topN,
		"found":    len(out),
	}).Info("scanner: scan complete")

	return out, nil
}

// ScanMatrix scans the (pair × exchange) matrix: it runs Scan over each
// exchange, concatenates the per-exchange opportunities (each tagged with its
// own Exchange), and returns the combined slice sorted by OpportunityScore
// descending. A single-exchange failure is logged at WARN and skipped; only if
// EVERY exchange fails is the last error returned.
//
// Backward-compat invariant: when len(exchanges)==1 the result is identical
// (same order) to Scan(exchanges[0], topN).
func (ms *MarketScanner) ScanMatrix(ctx context.Context, exchanges []string, topN int) ([]*MarketOpportunity, error) {
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("market scanner: no exchanges to scan")
	}

	combined := make([]*MarketOpportunity, 0, len(exchanges)*topN)
	var lastErr error
	failures := 0

	for _, exchange := range exchanges {
		opps, err := ms.Scan(ctx, exchange, topN)
		if err != nil {
			lastErr = err
			failures++
			ms.log.WithError(err).WithField("exchange", exchange).Warn("scanner: exchange scan failed, skipping")
			continue
		}
		combined = append(combined, opps...)
	}

	// All exchanges failed → surface the last error.
	if failures == len(exchanges) {
		return nil, lastErr
	}

	// Single successful exchange short-circuits to preserve Scan's exact order.
	if len(exchanges) > 1 {
		sort.Slice(combined, func(i, j int) bool {
			return combined[i].OpportunityScore > combined[j].OpportunityScore
		})
	}

	return combined, nil
}

// DetectCrossExchangeArb is a pure, observational helper: it groups
// opportunities by Pair and, for any pair present on >=2 exchanges whose
// max-min OpportunityScore spread is >= minScoreDiff, emits one human-readable
// signal string. It executes nothing — callers may log the signals.
func DetectCrossExchangeArb(opps []*MarketOpportunity, minScoreDiff float64) []string {
	type exScore struct {
		exchange string
		score    float64
	}
	byPair := make(map[string][]exScore)
	order := make([]string, 0)
	for _, o := range opps {
		if o == nil || o.Pair == "" {
			continue
		}
		if _, seen := byPair[o.Pair]; !seen {
			order = append(order, o.Pair)
		}
		byPair[o.Pair] = append(byPair[o.Pair], exScore{exchange: o.Exchange, score: o.OpportunityScore})
	}

	signals := make([]string, 0)
	for _, pair := range order {
		entries := byPair[pair]
		if len(entries) < 2 {
			continue
		}
		min, max := entries[0].score, entries[0].score
		for _, e := range entries {
			if e.score < min {
				min = e.score
			}
			if e.score > max {
				max = e.score
			}
		}
		diff := max - min
		if diff < minScoreDiff {
			continue
		}
		parts := make([]string, 0, len(entries))
		for _, e := range entries {
			parts = append(parts, fmt.Sprintf("%s=%.1f", e.exchange, e.score))
		}
		signals = append(signals, fmt.Sprintf("%s: %s diff=%.1f", pair, strings.Join(parts, " "), diff))
	}
	return signals
}

// SetWeights updates the scoring weights at runtime.
func (ms *MarketScanner) SetWeights(w *ScoringWeights) {
	if w == nil {
		return
	}
	ms.feedbackMu.Lock()
	ms.weights = w
	ms.feedbackMu.Unlock()
}

// Weights returns a copy of the current scoring weights, or nil if unset.
// Useful for callers that need to persist the live weights without holding
// the scanner lock.
func (ms *MarketScanner) Weights() *ScoringWeights {
	ms.feedbackMu.RLock()
	defer ms.feedbackMu.RUnlock()
	if ms.weights == nil {
		return nil
	}
	cp := *ms.weights
	return &cp
}

// SetFeedback injects per-pair feedback for use in scoring.
func (ms *MarketScanner) SetFeedback(feedback map[string]*PairFeedback) {
	ms.feedbackMu.Lock()
	ms.feedback = feedback
	ms.feedbackMu.Unlock()
}

// scorePair builds the opportunity skeleton (without final score) from raw inputs.
func (ms *MarketScanner) scorePair(ticker TickerSummary, candles []ai.Candle, analysis *regime.RegimeAnalysis) *MarketOpportunity {
	opp := &MarketOpportunity{
		Pair:           ticker.Pair,
		Exchange:       ticker.Exchange,
		Volatility:     analysis.ATRPercent,
		Volume24hUSD:   ticker.Volume24h,
		TrendStrength:  analysis.ADX,
		SentimentScore: 0.0,
		BTCCorrelation: 0.0,
		RegimeMatch:    analysis.SuggestedSizeMultiplier,
		DetectedAt:     time.Now(),
	}

	// If ticker didn't provide USD volume, estimate from the last 24 hourly candles.
	if opp.Volume24hUSD == 0 && len(candles) >= 24 {
		var vol float64
		recent := candles[len(candles)-24:]
		for _, c := range recent {
			vol += c.Volume * c.Close
		}
		opp.Volume24hUSD = vol
	}

	return opp
}

// computeScore applies the weighted formula and returns a 0-100 score.
func (ms *MarketScanner) computeScore(ctx context.Context, opp *MarketOpportunity, analysis *regime.RegimeAnalysis) float64 {
	ms.feedbackMu.RLock()
	w := ms.weights
	fb := ms.feedback[opp.Pair]
	ms.feedbackMu.RUnlock()

	// Local copy of weights — never mutate the shared struct.
	weights := *w
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("master.scoring.volatility_weight"); v > 0 {
			weights.Volatility = v
		}
		if v := snap.Float("master.scoring.volume_weight"); v > 0 {
			weights.Volume = v
		}
		if v := snap.Float("master.scoring.trend_strength_weight"); v > 0 {
			weights.TrendStrength = v
		}
		if v := snap.Float("master.scoring.sentiment_weight"); v > 0 {
			weights.Sentiment = v
		}
		if v := snap.Float("master.scoring.regime_match_weight"); v > 0 {
			weights.RegimeMatch = v
		}
		if v := snap.Float("master.scoring.btc_correlation_penalty"); v > 0 {
			weights.BTCCorrelationPenalty = v
		}
		if v := snap.Float("master.scoring.recent_loss_penalty"); v > 0 {
			weights.RecentLossPenalty = v
		}
		if v := snap.Float("master.scoring.profit_factor_weight"); v > 0 {
			weights.ProfitFactor = v
		}
	}

	// Volatility score: reward 0.5%-2% ATR
	volatilityScore := math.Min(100.0, opp.Volatility*100.0*50.0)

	// Volume score: log10 scale — $100K=0, $500K≈35, $2M≈65, $10M=100.
	var volumeScore float64
	if opp.Volume24hUSD >= 100_000 {
		volumeScore = math.Log10(opp.Volume24hUSD/100_000.0) / math.Log10(100.0) * 100.0
		if volumeScore > 100 {
			volumeScore = 100
		}
	}

	// Trend strength via ADX
	trendScore := math.Min(100.0, opp.TrendStrength*1.5)

	// Sentiment normalized -1..+1 → 0..100; default 50 if unset
	sentimentScore := 50.0
	if opp.SentimentScore != 0 {
		sentimentScore = (opp.SentimentScore + 1.0) * 50.0
	}
	sentimentScore = clamp(sentimentScore, 0.0, 100.0)

	// Regime match: scale 0..1 → 0..100
	regimeMatchScore := opp.RegimeMatch * 100.0
	regimeMatchScore = clamp(regimeMatchScore, 0.0, 100.0)

	// BTC correlation penalty (subtracted)
	btcPenalty := math.Abs(opp.BTCCorrelation) * 100.0

	// Recent loss penalty: 10 per loss, capped at 50
	recentLossPenalty := 0.0
	if fb != nil && fb.RecentLossesCount > 0 {
		recentLossPenalty = math.Min(50.0, float64(fb.RecentLossesCount)*10.0)
	}
	opp.RecentLossPenalty = recentLossPenalty

	// Profit factor: expectancy %/trade, clamped to ±2%, scaled to ±50 points.
	// Requires at least 3 closed trades so a single fluke doesn't dominate.
	profitFactor := 0.0
	if fb != nil && fb.TotalTrades >= 3 {
		expectancy := fb.WinRate*fb.AvgWinPercent + (1.0-fb.WinRate)*fb.AvgLossPercent
		expectancy = clamp(expectancy, -2.0, 2.0)
		profitFactor = expectancy * 25.0
	}
	opp.ProfitFactor = profitFactor

	raw := weights.Volatility*volatilityScore +
		weights.Volume*volumeScore +
		weights.TrendStrength*trendScore +
		weights.Sentiment*sentimentScore +
		weights.RegimeMatch*regimeMatchScore -
		weights.BTCCorrelationPenalty*btcPenalty -
		weights.RecentLossPenalty*recentLossPenalty +
		weights.ProfitFactor*profitFactor

	score := clamp(raw, 0.0, 100.0)

	opp.Reasoning = fmt.Sprintf(
		"vol=%.1f vol24h=%.1f trend=%.1f sent=%.1f regime=%.1f btcPen=%.1f lossPen=%.1f profitFact=%+.1f -> %.2f",
		volatilityScore, volumeScore, trendScore, sentimentScore, regimeMatchScore, btcPenalty, recentLossPenalty, profitFactor, score,
	)
	return score
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

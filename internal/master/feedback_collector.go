package master

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// TradeRecord is a closed trade for feedback aggregation.
type TradeRecord struct {
	Pair       string
	Side       string
	EntryTime  time.Time
	ExitTime   time.Time
	EntryPrice float64
	ExitPrice  float64
	PnLPercent float64
	Won        bool
	// MarketType is the trading mode ("spot", "futures", "margin").
	// Empty string means unknown — mode is then derived from Leverage.
	MarketType string
	// Leverage is the leverage multiplier used on this trade (1 = spot/no leverage).
	Leverage float64
	// Regime is the detected market regime when this trade was opened (e.g. "trending", "ranging").
	Regime string
}

// TradeHistoryReader returns historical trades for feedback computation.
type TradeHistoryReader interface {
	GetRecentTrades(pair string, limit int) ([]TradeRecord, error)
	GetAllPairsWithTrades() ([]string, error)
}

// FeedbackCollector aggregates trade history into per-pair feedback and adjusts scoring weights.
type FeedbackCollector struct {
	log     *logrus.Logger
	reader  TradeHistoryReader
	mu      sync.RWMutex
	cache   map[string]*PairFeedback
	weights *ScoringWeights
	// modeStats holds the most recent per-mode attribution snapshot (mode -> stats).
	modeStats map[string]ModeStats
	// modeRegimeStats holds per-mode stats broken down by regime (mode -> regime -> stats).
	modeRegimeStats map[string]map[string]ModeStats
}

// NewFeedbackCollector constructs a feedback collector. weights is a pointer to scanner weights.
func NewFeedbackCollector(log *logrus.Logger, reader TradeHistoryReader, weights *ScoringWeights) *FeedbackCollector {
	if log == nil {
		log = logrus.New()
	}
	return &FeedbackCollector{
		log:             log,
		reader:          reader,
		cache:           make(map[string]*PairFeedback),
		weights:         weights,
		modeStats:       make(map[string]ModeStats),
		modeRegimeStats: make(map[string]map[string]ModeStats),
	}
}

// Collect builds per-pair feedback from the last lookbackDays of trades.
func (fc *FeedbackCollector) Collect(lookbackDays int) map[string]*PairFeedback {
	if fc.reader == nil {
		fc.log.Warn("feedback: nil reader, returning empty feedback")
		return map[string]*PairFeedback{}
	}
	pairs, err := fc.reader.GetAllPairsWithTrades()
	if err != nil {
		fc.log.WithError(err).Warn("feedback: failed to list pairs with trades")
		return map[string]*PairFeedback{}
	}

	cutoff := time.Now().Add(-time.Duration(lookbackDays) * 24 * time.Hour)
	out := make(map[string]*PairFeedback, len(pairs))

	var allTrades []TradeRecord
	for _, pair := range pairs {
		trades, err := fc.reader.GetRecentTrades(pair, 200)
		if err != nil {
			fc.log.WithError(err).WithField("pair", pair).Debug("feedback: failed to fetch trades")
			continue
		}
		fb := aggregate(pair, trades, cutoff)
		if fb.TotalTrades == 0 {
			continue
		}
		out[pair] = fb
		// Accumulate recent trades for mode attribution.
		for _, t := range trades {
			if !t.ExitTime.Before(cutoff) {
				allTrades = append(allTrades, t)
			}
		}
	}

	// Compute per-mode and per-mode/regime attribution from all collected trades.
	modeStats := AttributeByMode(allTrades)
	modeRegimeStats := AttributeByModeAndRegime(allTrades)

	fc.mu.Lock()
	fc.cache = out
	fc.modeStats = modeStats
	fc.modeRegimeStats = modeRegimeStats
	fc.mu.Unlock()

	fc.log.WithFields(logrus.Fields{
		"pairs":  len(out),
		"window": lookbackDays,
		"modes":  len(modeStats),
	}).Info("feedback: aggregation complete")

	return out
}

// AdjustWeights nudges the scanner weights based on outcome correlations.
// Adjustments are kept small (max +/-5% per cycle) to prevent oscillation.
func (fc *FeedbackCollector) AdjustWeights(feedback map[string]*PairFeedback) {
	if fc.weights == nil || len(feedback) == 0 {
		return
	}

	wins, losses, totalPnL := 0, 0, 0.0
	for _, fb := range feedback {
		wins += fb.WinningTrades
		losses += fb.LosingTrades
		totalPnL += fb.TotalPnLUSD
	}
	totalTrades := wins + losses
	if totalTrades < 5 {
		fc.log.Debug("feedback: too few trades to adjust weights")
		return
	}

	winRate := float64(wins) / float64(totalTrades)

	fc.mu.Lock()
	defer fc.mu.Unlock()

	// If overall win rate is low, the scanner is picking poor pairs:
	//   reduce volatility/volume influence (pairs may be noisy/illiquid stress)
	//   increase trend strength + regime match weight (favor confirmed regimes)
	if winRate < 0.4 {
		fc.weights.Volatility = clampWeight(fc.weights.Volatility * 0.95)
		fc.weights.Volume = clampWeight(fc.weights.Volume * 0.95)
		fc.weights.TrendStrength = clampWeight(fc.weights.TrendStrength * 1.05)
		fc.weights.RegimeMatch = clampWeight(fc.weights.RegimeMatch * 1.05)
		fc.weights.RecentLossPenalty = clampWeight(fc.weights.RecentLossPenalty * 1.05)
		fc.log.WithField("win_rate", winRate).Info("feedback: low win rate, weights tilted toward trend/regime")
		return
	}

	// If win rate is solid but PnL skewed negative, losses outsize wins:
	//   increase BTC correlation penalty (probably caught market drawdowns)
	if winRate >= 0.4 && totalPnL < 0 {
		fc.weights.BTCCorrelationPenalty = clampWeight(fc.weights.BTCCorrelationPenalty * 1.05)
		fc.weights.RecentLossPenalty = clampWeight(fc.weights.RecentLossPenalty * 1.05)
		fc.log.WithField("pnl", totalPnL).Info("feedback: negative PnL with decent win rate, raising BTC penalty")
		return
	}

	// Strong performance — reinforce volatility/volume slightly.
	if winRate >= 0.55 && totalPnL > 0 {
		fc.weights.Volatility = clampWeight(fc.weights.Volatility * 1.02)
		fc.weights.Volume = clampWeight(fc.weights.Volume * 1.02)
		fc.log.WithFields(logrus.Fields{"win_rate": winRate, "pnl": totalPnL}).Info("feedback: strong performance, slight reinforcement")
	}
}

// aggregate computes a PairFeedback summary from a slice of trades.
func aggregate(pair string, trades []TradeRecord, cutoff time.Time) *PairFeedback {
	fb := &PairFeedback{Pair: pair}
	totalWinPct, totalLossPct := 0.0, 0.0

	// Sort newest first by ExitTime to track recent losses
	// Linear pass — caller should provide trades in any order.
	recentLossWindow := 0
	for _, t := range trades {
		if t.ExitTime.Before(cutoff) {
			continue
		}
		fb.TotalTrades++
		fb.TotalPnLUSD += t.PnLPercent // assume PnLPercent is normalized USD; if not the field name is misleading
		if t.Won {
			fb.WinningTrades++
			totalWinPct += t.PnLPercent
		} else {
			fb.LosingTrades++
			totalLossPct += t.PnLPercent
			recentLossWindow++
		}
		if t.ExitTime.After(fb.LastTradeAt) {
			fb.LastTradeAt = t.ExitTime
		}
	}

	if fb.TotalTrades > 0 {
		fb.WinRate = float64(fb.WinningTrades) / float64(fb.TotalTrades)
	}
	if fb.WinningTrades > 0 {
		fb.AvgWinPercent = totalWinPct / float64(fb.WinningTrades)
	}
	if fb.LosingTrades > 0 {
		fb.AvgLossPercent = totalLossPct / float64(fb.LosingTrades)
	}
	// RecentLossesCount = losses among the last 5 trades — proxy via total losses if <5
	if fb.LosingTrades < 5 {
		fb.RecentLossesCount = fb.LosingTrades
	} else {
		fb.RecentLossesCount = 5
	}
	return fb
}

// minModeSampleSize is the minimum number of trades in a mode bucket before declaring a preference.
const minModeSampleSize = 10

// PreferredMode returns the statistically preferred trading mode for a given regime (and
// optionally pair — pass empty string for regime-only lookup). Returns ("", false) when
// there is insufficient data or no clear winner.
//
// "Clear winner" = one mode has both a higher win rate AND a higher profit factor than all
// alternatives, with at least minModeSampleSize trades.
//
// NOTE: This is a read-only signal for downstream AUTO/resolver logic. It does NOT alter
// any trading behaviour on its own.
func (fc *FeedbackCollector) PreferredMode(regime, _ string) (mode string, ok bool) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// If regime-specific stats exist, try them first.
	if regime != "" {
		if winner, found := bestModeInRegime(fc.modeRegimeStats, regime); found {
			return winner, true
		}
	}
	// Fall back to overall mode stats.
	if winner, found := bestModeOverall(fc.modeStats); found {
		return winner, true
	}
	return "", false
}

// ModeStatsSnapshot returns a copy of the most recently computed per-mode statistics.
// Callers should treat this as a point-in-time snapshot and not cache it long-term.
func (fc *FeedbackCollector) ModeStatsSnapshot() map[string]ModeStats {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	out := make(map[string]ModeStats, len(fc.modeStats))
	for k, v := range fc.modeStats {
		out[k] = v
	}
	return out
}

// bestModeInRegime returns the dominant mode for a specific regime, or ("", false) if
// no mode clears the sample-size and combined quality bar.
func bestModeInRegime(modeRegime map[string]map[string]ModeStats, regime string) (string, bool) {
	// Collect per-mode stats for this regime across all mode buckets.
	candidates := make(map[string]ModeStats)
	for mode, regimeMap := range modeRegime {
		if s, ok := regimeMap[regime]; ok && s.Trades >= minModeSampleSize {
			candidates[mode] = s
		}
	}
	return dominantMode(candidates)
}

// bestModeOverall returns the dominant mode across all regimes.
func bestModeOverall(stats map[string]ModeStats) (string, bool) {
	eligible := make(map[string]ModeStats)
	for mode, s := range stats {
		if s.Trades >= minModeSampleSize {
			eligible[mode] = s
		}
	}
	return dominantMode(eligible)
}

// dominantMode picks the mode with the best combined win-rate AND profit-factor score.
// Returns ("", false) if there are fewer than two eligible modes (no competition to resolve).
func dominantMode(candidates map[string]ModeStats) (string, bool) {
	if len(candidates) < 2 {
		return "", false
	}
	best := ""
	bestScore := -1.0
	for mode, s := range candidates {
		// Combined score: geometric mean of win_rate and profit_factor (both bounded > 0).
		pf := s.ProfitFactor
		if pf <= 0 {
			pf = 0.001
		}
		score := s.WinRate * pf
		if score > bestScore {
			bestScore = score
			best = mode
		}
	}
	// Require the winner to be meaningfully better: combined score > 1.1× any other.
	for mode, s := range candidates {
		if mode == best {
			continue
		}
		pf := s.ProfitFactor
		if pf <= 0 {
			pf = 0.001
		}
		challScore := s.WinRate * pf
		if bestScore < challScore*1.10 {
			return "", false // too close to call
		}
	}
	return best, true
}

// clampWeight keeps a single weight in [0.01, 0.5] to prevent runaway dominance.
func clampWeight(w float64) float64 {
	if w < 0.01 {
		return 0.01
	}
	if w > 0.5 {
		return 0.5
	}
	return w
}

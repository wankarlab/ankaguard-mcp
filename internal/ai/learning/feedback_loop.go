package learning

import (
	"math"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
)

// ClosedTradeRecord is a minimal view of a closed trade for FeedbackLoop warm-start.
// Defined here to break the import cycle: learning → storage → services → learning.
type ClosedTradeRecord struct {
	ID         string
	Pair       string
	Style      string // must equal "scalping" to be loaded
	Side       string
	Status     string // must equal "closed"
	EntryPrice float64
	ExitPrice  float64
	StopLoss   float64
	TakeProfit float64
	PnL        float64
	PnLPct     float64
	ExitReason string
}

// HistoryLoader is the minimal read interface needed for warm-start.
// *storage.TradeHistoryRepository satisfies it via the adapter in main.go.
type HistoryLoader interface {
	GetScalpingClosedTrades(pair string, limit int) ([]ClosedTradeRecord, error)
	GetDistinctScalpingPairs() ([]string, error)
}

// Default tunables for the feedback loop.
const (
	defaultMinSampleSize  = 5
	defaultAdjustmentRate = 0.1
	defaultMaxOutcomes    = 100

	paramStopLossATRMult     = "stop_loss_atr_mult"
	paramTakeProfitATRMult   = "take_profit_atr_mult"
	paramMinConfidenceThresh = "min_confidence_threshold"
	paramSizeMultiplier      = "size_multiplier"

	// Default starting values when caller asks GetParameter without prior state.
	defaultSLATRMult     = 1.5
	defaultTPATRMult     = 2.0
	defaultMinConfidence = 0.6
	defaultSizeMult      = 1.0

	// Bounds keep the loop from drifting into nonsense.
	minSLATRMult     = 0.5
	maxSLATRMult     = 5.0
	minTPATRMult     = 0.5
	maxTPATRMult     = 10.0
	minMinConfidence = 0.3
	maxMinConfidence = 0.95
	minSizeMult      = 0.1
	maxSizeMult      = 2.0
)

// FeedbackLoop is a continuous post-trade learning loop. It registers each
// closed trade outcome and emits incremental parameter adjustments per pair
// once enough samples are available.
//
// It is intentionally standalone: it can hold a reference to an existing
// SLTPLearningSystem for future deep integration but does not depend on its
// internal API today.
type FeedbackLoop struct {
	log            *logrus.Logger
	learningSystem *ai.SLTPLearningSystem // optional, for future integration

	minSampleSize  int
	adjustmentRate float64
	maxOutcomes    int

	parameters  map[string]map[string]float64    // pair -> param -> value
	outcomes    map[string][]*TradeOutcome       // pair -> outcomes
	adjustments map[string][]ParameterAdjustment // pair -> applied adjustments

	mu sync.RWMutex
}

// NewFeedbackLoop constructs a feedback loop. learningSystem may be nil.
func NewFeedbackLoop(log *logrus.Logger, learningSystem *ai.SLTPLearningSystem) *FeedbackLoop {
	if log == nil {
		log = logrus.New()
	}
	return &FeedbackLoop{
		log:            log,
		learningSystem: learningSystem,
		minSampleSize:  defaultMinSampleSize,
		adjustmentRate: defaultAdjustmentRate,
		maxOutcomes:    defaultMaxOutcomes,
		parameters:     make(map[string]map[string]float64),
		outcomes:       make(map[string][]*TradeOutcome),
		adjustments:    make(map[string][]ParameterAdjustment),
	}
}

// SetMinSampleSize overrides the minimum number of outcomes required before
// emitting adjustments.
func (fl *FeedbackLoop) SetMinSampleSize(n int) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if n > 0 {
		fl.minSampleSize = n
	}
}

// SetAdjustmentRate overrides the aggressiveness factor (0..1).
func (fl *FeedbackLoop) SetAdjustmentRate(rate float64) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if rate > 0 && rate <= 1.0 {
		fl.adjustmentRate = rate
	}
}

// RegisterOutcome stores a closed-trade outcome and triggers analysis when
// the sample threshold for the pair is reached.
func (fl *FeedbackLoop) RegisterOutcome(outcome *TradeOutcome) []ParameterAdjustment {
	if outcome == nil || outcome.Pair == "" {
		return nil
	}

	fl.mu.Lock()
	fl.outcomes[outcome.Pair] = append(fl.outcomes[outcome.Pair], outcome)
	if len(fl.outcomes[outcome.Pair]) > fl.maxOutcomes {
		fl.outcomes[outcome.Pair] = fl.outcomes[outcome.Pair][len(fl.outcomes[outcome.Pair])-fl.maxOutcomes:]
	}
	count := len(fl.outcomes[outcome.Pair])
	threshold := fl.minSampleSize
	fl.mu.Unlock()

	fl.log.WithFields(logrus.Fields{
		"trade_id":    outcome.TradeID,
		"pair":        outcome.Pair,
		"won":         outcome.Won,
		"pnl_pct":     outcome.PnLPercent,
		"exit_reason": outcome.ExitReason,
		"samples":     count,
	}).Info("trade outcome registered in feedback loop")

	if count < threshold {
		return nil
	}

	return fl.analyzePair(outcome.Pair)
}

// GetParameter returns the current value for a parameter for the given pair.
// If no adjustment has been applied, it returns defaultValue.
func (fl *FeedbackLoop) GetParameter(pair, param string, defaultValue float64) float64 {
	fl.mu.RLock()
	defer fl.mu.RUnlock()

	if pairParams, ok := fl.parameters[pair]; ok {
		if v, ok := pairParams[param]; ok {
			return v
		}
	}
	return defaultValue
}

// GetCurrentParams returns a copy of the learned parameter map for a pair, or nil.
func (fl *FeedbackLoop) GetCurrentParams(pair string) map[string]float64 {
	fl.mu.RLock()
	defer fl.mu.RUnlock()
	src, ok := fl.parameters[pair]
	if !ok {
		return nil
	}
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// GetAllPairs returns all pairs that have accumulated outcomes.
func (fl *FeedbackLoop) GetAllPairs() []string {
	fl.mu.RLock()
	defer fl.mu.RUnlock()
	pairs := make([]string, 0, len(fl.parameters))
	for p := range fl.parameters {
		pairs = append(pairs, p)
	}
	return pairs
}

// GetAllAdjustments returns a copy of every adjustment recorded for a pair.
func (fl *FeedbackLoop) GetAllAdjustments(pair string) []ParameterAdjustment {
	fl.mu.RLock()
	defer fl.mu.RUnlock()

	src := fl.adjustments[pair]
	if len(src) == 0 {
		return nil
	}
	out := make([]ParameterAdjustment, len(src))
	copy(out, src)
	return out
}

// GetAdjustmentsSummary returns all adjustments across all pairs, grouped by pair,
// together with the current learned parameter values. Suitable for API responses.
func (fl *FeedbackLoop) GetAdjustmentsSummary() map[string]interface{} {
	fl.mu.RLock()
	defer fl.mu.RUnlock()

	type adjItem struct {
		Parameter string    `json:"parameter"`
		OldValue  float64   `json:"old_value"`
		NewValue  float64   `json:"new_value"`
		Reason    string    `json:"reason"`
		AppliedAt time.Time `json:"applied_at"`
	}
	type pairEntry struct {
		Parameters  map[string]float64 `json:"parameters"`
		Adjustments []adjItem          `json:"adjustments"`
	}

	out := make(map[string]interface{}, len(fl.adjustments))
	for pair, adjs := range fl.adjustments {
		items := make([]adjItem, 0, len(adjs))
		for _, a := range adjs {
			items = append(items, adjItem{
				Parameter: a.Parameter,
				OldValue:  a.OldValue,
				NewValue:  a.NewValue,
				Reason:    a.Reasoning,
				AppliedAt: a.AppliedAt,
			})
		}
		params := map[string]float64{}
		if p, ok := fl.parameters[pair]; ok {
			for k, v := range p {
				params[k] = v
			}
		}
		out[pair] = pairEntry{Parameters: params, Adjustments: items}
	}
	return out
}

// analyzePair inspects the recent outcomes for a pair and produces the next
// batch of incremental adjustments.
func (fl *FeedbackLoop) analyzePair(pair string) []ParameterAdjustment {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	outcomes := fl.outcomes[pair]
	if len(outcomes) < fl.minSampleSize {
		return nil
	}

	if _, ok := fl.parameters[pair]; !ok {
		fl.parameters[pair] = map[string]float64{
			paramStopLossATRMult:     defaultSLATRMult,
			paramTakeProfitATRMult:   defaultTPATRMult,
			paramMinConfidenceThresh: defaultMinConfidence,
			paramSizeMultiplier:      defaultSizeMult,
		}
	}
	params := fl.parameters[pair]

	stats := computeStats(outcomes)
	now := time.Now()
	adjustments := make([]ParameterAdjustment, 0, 4)

	// Rule 1: SL hits dominate but MFE was >2x SL distance -> SL too tight.
	if stats.stopLossExits > 0 && stats.totalExits > 0 &&
		float64(stats.stopLossExits)/float64(stats.totalExits) >= 0.5 &&
		stats.avgMFEOnSLExits > 0 && stats.avgSLDistance > 0 &&
		stats.avgMFEOnSLExits > 2*stats.avgSLDistance {

		adj := fl.bumpParam(params, paramStopLossATRMult, +fl.adjustmentRate, minSLATRMult, maxSLATRMult)
		adj.Pair = pair
		adj.AppliedAt = now
		adj.Confidence = clamp01(float64(stats.stopLossExits) / float64(stats.totalExits))
		adj.Reasoning = "SL is hunting temporary adverse moves before favorable rebound"
		adjustments = append(adjustments, adj)
	}

	// Rule 2: low win rate -> raise confidence threshold.
	if stats.winRate >= 0 && stats.winRate < 0.4 {
		adj := fl.bumpParam(params, paramMinConfidenceThresh, +fl.adjustmentRate*0.5, minMinConfidence, maxMinConfidence)
		adj.Pair = pair
		adj.AppliedAt = now
		adj.Confidence = clamp01(0.4 - stats.winRate + 0.4)
		adj.Reasoning = "Current confidence threshold producing low-quality trades"
		adjustments = append(adjustments, adj)
	}

	// Rule 3: avg win pct < avg loss pct -> TP too tight.
	if stats.wins > 0 && stats.losses > 0 && stats.avgLossPct < 0 {
		ratio := stats.avgWinPct / math.Abs(stats.avgLossPct)
		if ratio > 0 && ratio < 1.0 {
			adj := fl.bumpParam(params, paramTakeProfitATRMult, +fl.adjustmentRate, minTPATRMult, maxTPATRMult)
			adj.Pair = pair
			adj.AppliedAt = now
			adj.Confidence = clamp01(1.0 - ratio)
			adj.Reasoning = "Exiting winners too early; reward/risk ratio below 1"
			adjustments = append(adjustments, adj)
		}
	}

	// Rule 4: MAE on wins exceeds SL in >70% of wins -> reduce size, keep SL.
	if stats.wins > 0 && stats.avgSLDistance > 0 {
		highMAERate := float64(stats.winsWithHighMAE) / float64(stats.wins)
		if highMAERate > 0.7 {
			adj := fl.bumpParam(params, paramSizeMultiplier, -fl.adjustmentRate, minSizeMult, maxSizeMult)
			adj.Pair = pair
			adj.AppliedAt = now
			adj.Confidence = clamp01(highMAERate)
			adj.Reasoning = "Wins survive deep drawdowns; reducing size to tolerate adverse excursion"
			adjustments = append(adjustments, adj)
		}
	}

	if len(adjustments) > 0 {
		fl.adjustments[pair] = append(fl.adjustments[pair], adjustments...)
		for _, a := range adjustments {
			fl.log.WithFields(logrus.Fields{
				"pair":       a.Pair,
				"parameter":  a.Parameter,
				"old":        a.OldValue,
				"new":        a.NewValue,
				"confidence": a.Confidence,
				"reasoning":  a.Reasoning,
			}).Info("feedback loop applied parameter adjustment")
		}
	}

	// TODO: feed adjustments into SLTPLearningSystem (ai.SLTPLearningSystem)
	// once that system exposes an incremental ingestion API. Today its
	// surface is batch-oriented (AnalyzeExecutions / RunWeeklyOptimization)
	// and tied to ExecutionHistoryRepository, so deeper integration is
	// deferred to a later phase.

	return adjustments
}

// bumpParam mutates params[name] by deltaFraction (relative) clamped to [min,max].
// Returns a ParameterAdjustment describing the change.
func (fl *FeedbackLoop) bumpParam(params map[string]float64, name string, deltaFraction, minVal, maxVal float64) ParameterAdjustment {
	old := params[name]
	if old == 0 {
		old = 1.0
	}
	next := old * (1.0 + deltaFraction)
	if next < minVal {
		next = minVal
	}
	if next > maxVal {
		next = maxVal
	}
	params[name] = next
	return ParameterAdjustment{
		Parameter: name,
		OldValue:  old,
		NewValue:  next,
	}
}

// outcomeStats aggregates the slice of outcomes for a pair.
type outcomeStats struct {
	totalExits      int
	stopLossExits   int
	wins            int
	losses          int
	winRate         float64
	avgWinPct       float64
	avgLossPct      float64
	avgSLDistance   float64
	avgMFEOnSLExits float64
	winsWithHighMAE int
}

func computeStats(outcomes []*TradeOutcome) outcomeStats {
	var s outcomeStats
	if len(outcomes) == 0 {
		return s
	}

	var winSum, lossSum, slDistSum, mfeOnSLSum float64
	var slDistCount int

	for _, o := range outcomes {
		s.totalExits++
		if o.Won {
			s.wins++
			winSum += o.PnLPercent
		} else {
			s.losses++
			lossSum += o.PnLPercent
		}

		entry := o.EntryPrice
		if entry > 0 && o.StopLoss > 0 {
			slDist := math.Abs(entry-o.StopLoss) / entry * 100.0
			slDistSum += slDist
			slDistCount++
			if o.Won && o.MaxAdverseExcursion >= slDist {
				s.winsWithHighMAE++
			}
		}

		if o.ExitReason == "stop_loss" {
			s.stopLossExits++
			if o.MaxFavorableExcursion > 0 {
				mfeOnSLSum += o.MaxFavorableExcursion
			}
		}
	}

	if s.totalExits > 0 {
		s.winRate = float64(s.wins) / float64(s.totalExits)
	}
	if s.wins > 0 {
		s.avgWinPct = winSum / float64(s.wins)
	}
	if s.losses > 0 {
		s.avgLossPct = lossSum / float64(s.losses)
	}
	if slDistCount > 0 {
		s.avgSLDistance = slDistSum / float64(slDistCount)
	}
	if s.stopLossExits > 0 {
		s.avgMFEOnSLExits = mfeOnSLSum / float64(s.stopLossExits)
	}
	return s
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// WarmStart loads closed trades from the SQLite trade_history table and
// replays them into the FeedbackLoop so parameter learning survives restarts.
// Only scalping trades are loaded (TradingStyle == "scalping").
// Call once at startup before the first RegisterOutcome.
func (fl *FeedbackLoop) WarmStart(loader HistoryLoader, perPairLimit int) (int, error) {
	if loader == nil {
		return 0, nil
	}
	if perPairLimit <= 0 {
		perPairLimit = fl.maxOutcomes
	}

	pairs, err := loader.GetDistinctScalpingPairs()
	if err != nil {
		return 0, err
	}

	total := 0
	for _, pair := range pairs {
		trades, err := loader.GetScalpingClosedTrades(pair, perPairLimit)
		if err != nil {
			fl.log.WithError(err).Warnf("FeedbackLoop.WarmStart: failed to load pair %s", pair)
			continue
		}

		for _, t := range trades {
			if t.ExitPrice <= 0 {
				continue
			}

			won := t.PnL >= 0
			outcome := &TradeOutcome{
				TradeID:    t.ID,
				Pair:       t.Pair,
				Won:        won,
				PnLPercent: t.PnLPct,
				ExitReason: t.ExitReason,
				EntryPrice: t.EntryPrice,
				StopLoss:   t.StopLoss,
				TakeProfit: t.TakeProfit,
			}
			// Register without triggering analysis — just populate the outcomes buffer.
			fl.mu.Lock()
			fl.outcomes[pair] = append(fl.outcomes[pair], outcome)
			if len(fl.outcomes[pair]) > fl.maxOutcomes {
				fl.outcomes[pair] = fl.outcomes[pair][len(fl.outcomes[pair])-fl.maxOutcomes:]
			}
			fl.mu.Unlock()
			total++
		}

		// Trigger analysis if we have enough samples for this pair.
		fl.mu.RLock()
		count := len(fl.outcomes[pair])
		threshold := fl.minSampleSize
		fl.mu.RUnlock()
		if count >= threshold {
			fl.analyzePair(pair)
		}
	}

	fl.log.WithFields(logrus.Fields{
		"pairs":  len(pairs),
		"trades": total,
	}).Info("FeedbackLoop warm-start complete — loaded from trade_history DB")

	return total, nil
}

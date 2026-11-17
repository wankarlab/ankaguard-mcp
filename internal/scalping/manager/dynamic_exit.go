package manager

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai/learning"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/config/dynamic"
)

// DynamicExitConfig holds parameters for trailing stop and partial take-profit.
type DynamicExitConfig struct {
	TrailingStopPct     float64
	BreakevenTriggerPct float64
	Partial1TriggerPct  float64
	Partial1SizePct     float64
	Partial2TriggerPct  float64
	Partial2SizePct     float64
	AICheckInterval     time.Duration
	AIEnabled           bool
}

// DefaultDynamicExitConfig returns sensible conservative defaults.
func DefaultDynamicExitConfig() DynamicExitConfig {
	return DynamicExitConfig{
		TrailingStopPct:     0.5,
		BreakevenTriggerPct: 0.5,
		Partial1TriggerPct:  0.50,
		Partial1SizePct:     0.25,
		Partial2TriggerPct:  0.75,
		Partial2SizePct:     0.25,
		AICheckInterval:     5 * time.Minute,
		AIEnabled:           false,
	}
}

// ExitDecision is the action the bot manager should apply to a position.
type ExitDecision struct {
	Action       string
	Quantity     float64
	Reason       string
	PartialIndex int
}

// DynamicExitEngine bridges the paper-trading cycle with trailing stop,
// partial take-profit, AI exit decisions, and the feedback loop.
// It holds no position state — all state lives in PaperPosition, mutated in place.
type DynamicExitEngine struct {
	logger       *logrus.Logger
	feedbackLoop *learning.FeedbackLoop
	consensus    *llm.ConsensusEngine
	cfg          DynamicExitConfig
}

// NewDynamicExitEngine constructs the engine. feedbackLoop and consensus may be nil.
func NewDynamicExitEngine(logger *logrus.Logger, feedbackLoop *learning.FeedbackLoop, cfg DynamicExitConfig) *DynamicExitEngine {
	if logger == nil {
		logger = logrus.New()
	}
	return &DynamicExitEngine{
		logger:       logger,
		feedbackLoop: feedbackLoop,
		cfg:          cfg,
	}
}

// SetFeedbackLoop injects the post-trade feedback loop.
func (d *DynamicExitEngine) SetFeedbackLoop(fl *learning.FeedbackLoop) {
	d.feedbackLoop = fl
}

// SetConsensus injects the multi-LLM consensus engine for AI exit decisions.
func (d *DynamicExitEngine) SetConsensus(ce *llm.ConsensusEngine) {
	d.consensus = ce
}

// Evaluate updates trailing state on pos (mutates in place) and returns
// the exit decision for this price tick.
func (d *DynamicExitEngine) Evaluate(ctx context.Context, pos *PaperPosition, currentPrice float64) ExitDecision {
	d.updateMaxFavorable(pos, currentPrice)

	if d.cfg.TrailingStopPct > 0 {
		d.applyTrailingStop(pos)
	}

	if !pos.BreakevenMoved && d.cfg.BreakevenTriggerPct > 0 {
		d.checkBreakeven(pos, currentPrice)
	}

	if hit, reason := d.isExitTriggered(pos, currentPrice); hit {
		return ExitDecision{Action: "close_all", Quantity: pos.Quantity, Reason: reason}
	}

	if decision, ok := d.checkPartialExit(pos, currentPrice); ok {
		return decision
	}

	if d.cfg.AIEnabled && d.consensus != nil {
		gainPct := d.gainPct(pos, currentPrice)
		if gainPct > 0 && time.Since(pos.LastAICheck) >= d.cfg.AICheckInterval {
			if decision, ok := d.askAI(ctx, pos, currentPrice); ok {
				return decision
			}
		}
	}

	return ExitDecision{Action: "hold"}
}

func (d *DynamicExitEngine) updateMaxFavorable(pos *PaperPosition, currentPrice float64) {
	if pos.Direction == "buy" {
		if pos.MaxFavorablePrice == 0 || currentPrice > pos.MaxFavorablePrice {
			pos.MaxFavorablePrice = currentPrice
		}
	} else {
		if pos.MaxFavorablePrice == 0 || currentPrice < pos.MaxFavorablePrice {
			pos.MaxFavorablePrice = currentPrice
		}
	}
}

func (d *DynamicExitEngine) applyTrailingStop(pos *PaperPosition) {
	if pos.MaxFavorablePrice == 0 {
		return
	}
	trail := d.cfg.TrailingStopPct / 100.0
	if pos.Direction == "buy" {
		newSL := pos.MaxFavorablePrice * (1.0 - trail)
		if newSL > pos.StopLoss {
			pos.StopLoss = newSL
		}
	} else {
		newSL := pos.MaxFavorablePrice * (1.0 + trail)
		if pos.StopLoss == 0 || newSL < pos.StopLoss {
			pos.StopLoss = newSL
		}
	}
}

func (d *DynamicExitEngine) checkBreakeven(pos *PaperPosition, currentPrice float64) {
	if pos.EntryPrice == 0 {
		return
	}
	trigger := d.cfg.BreakevenTriggerPct / 100.0
	if pos.Direction == "buy" {
		if (currentPrice-pos.EntryPrice)/pos.EntryPrice >= trigger && pos.EntryPrice > pos.StopLoss {
			pos.StopLoss = pos.EntryPrice
			pos.BreakevenMoved = true
		}
	} else {
		if (pos.EntryPrice-currentPrice)/pos.EntryPrice >= trigger && pos.EntryPrice < pos.StopLoss {
			pos.StopLoss = pos.EntryPrice
			pos.BreakevenMoved = true
		}
	}
}

func (d *DynamicExitEngine) isExitTriggered(pos *PaperPosition, currentPrice float64) (bool, string) {
	if pos.Direction == "buy" {
		if currentPrice >= pos.TakeProfit {
			return true, "take_profit"
		}
		if pos.StopLoss > 0 && currentPrice <= pos.StopLoss {
			return true, "stop_loss"
		}
	} else {
		if currentPrice <= pos.TakeProfit {
			return true, "take_profit"
		}
		if pos.StopLoss > 0 && currentPrice >= pos.StopLoss {
			return true, "stop_loss"
		}
	}
	return false, ""
}

func (d *DynamicExitEngine) gainPct(pos *PaperPosition, currentPrice float64) float64 {
	if pos.EntryPrice == 0 {
		return 0
	}
	if pos.Direction == "buy" {
		return (currentPrice - pos.EntryPrice) / pos.EntryPrice * 100.0
	}
	return (pos.EntryPrice - currentPrice) / pos.EntryPrice * 100.0
}

func (d *DynamicExitEngine) askAI(ctx context.Context, pos *PaperPosition, currentPrice float64) (ExitDecision, bool) {
	gain := d.gainPct(pos, currentPrice)
	age := time.Since(pos.OpenedAt).Round(time.Second)

	prompt := fmt.Sprintf(`You are an exit management AI for a crypto scalping bot.
Current open position:
- Pair: %s
- Direction: %s
- Entry: %.8f | Current: %.8f | Gain: %.2f%%
- Take-Profit target: %.8f | Stop-Loss: %.8f
- Max favorable price seen: %.8f
- Age: %s | Partial exits taken: P1=%v P2=%v

Based on price action and position state, respond ONLY with a JSON object:
{
  "action": "HOLD|TIGHTEN_STOP|CLOSE_PARTIAL|CLOSE_ALL",
  "confidence": <0.0-1.0>,
  "reasoning": "<one sentence>"
}

Rules:
- HOLD: momentum is intact, position healthy
- TIGHTEN_STOP: some risk of reversal but thesis still valid — protect gains
- CLOSE_PARTIAL: partial reversal signals detected — lock in 50%% of remaining
- CLOSE_ALL: reversal confirmed or momentum exhausted — exit immediately`,
		pos.Pair, pos.Direction,
		pos.EntryPrice, currentPrice, gain,
		pos.TakeProfit, pos.StopLoss,
		pos.MaxFavorablePrice,
		age, pos.Partial1Done, pos.Partial2Done,
	)

	req := &llm.LLMRequest{
		Prompt:      prompt,
		Temperature: 0.2,
		MaxTokens:   150,
	}

	result, err := d.consensus.Analyze(ctx, req)
	pos.LastAICheck = time.Now()

	if err != nil || result == nil {
		d.logger.WithError(err).Debugf("[AI-EXIT] %s: consensus call failed, holding", pos.Pair)
		return ExitDecision{}, false
	}

	action := strings.ToUpper(strings.TrimSpace(result.Action))
	d.logger.WithFields(logrus.Fields{
		"pair":       pos.Pair,
		"action":     action,
		"confidence": result.Confidence,
		"consensus":  result.AgreementRatio,
		"gain_pct":   gain,
	}).Info("[AI-EXIT] position reviewed")

	// AI-05 fix: a CLOSE_ALL/CLOSE_PARTIAL decision from LLM consensus
	// previously closed the real position regardless of how weak the
	// confidence was. Require a minimum confidence before acting on it;
	// below the threshold, hold instead (the next tick re-evaluates).
	minExitConfidence := 0.5
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("scalping.exit.min_ai_confidence"); v > 0 {
			minExitConfidence = v
		}
	}
	if (action == "CLOSE_ALL" || action == "CLOSE_PARTIAL") && result.Confidence < minExitConfidence {
		d.logger.WithFields(logrus.Fields{
			"pair":       pos.Pair,
			"action":     action,
			"confidence": result.Confidence,
			"min_conf":   minExitConfidence,
		}).Warn("[AI-EXIT] confidence below threshold — holding instead of acting on close decision")
		return ExitDecision{Action: "hold"}, true
	}

	switch action {
	case "CLOSE_ALL":
		return ExitDecision{
			Action:   "close_all",
			Quantity: pos.Quantity,
			Reason:   fmt.Sprintf("ai:close_all confidence=%.2f", result.Confidence),
		}, true

	case "CLOSE_PARTIAL":
		qty := pos.Quantity * 0.5
		if qty <= 0 {
			return ExitDecision{}, false
		}
		return ExitDecision{
			Action:       "close_partial",
			Quantity:     qty,
			Reason:       fmt.Sprintf("ai:close_partial confidence=%.2f", result.Confidence),
			PartialIndex: 0,
		}, true

	case "TIGHTEN_STOP":
		d.tightenStop(pos, currentPrice)
		d.logger.Infof("[AI-EXIT] %s: stop tightened to %.8f (ai:tighten_stop)", pos.Pair, pos.StopLoss)
		return ExitDecision{Action: "hold"}, true

	default:
		return ExitDecision{Action: "hold"}, true
	}
}

func (d *DynamicExitEngine) tightenStop(pos *PaperPosition, currentPrice float64) {
	if pos.Direction == "buy" {
		newSL := (pos.StopLoss + currentPrice) / 2.0
		if newSL > pos.StopLoss {
			pos.StopLoss = newSL
		}
	} else {
		newSL := (pos.StopLoss + currentPrice) / 2.0
		if newSL < pos.StopLoss {
			pos.StopLoss = newSL
		}
	}
}

func (d *DynamicExitEngine) checkPartialExit(pos *PaperPosition, currentPrice float64) (ExitDecision, bool) {
	var gainFraction float64
	if pos.Direction == "buy" {
		tpDist := pos.TakeProfit - pos.EntryPrice
		if tpDist <= 0 {
			return ExitDecision{}, false
		}
		gainFraction = (currentPrice - pos.EntryPrice) / tpDist
	} else {
		tpDist := pos.EntryPrice - pos.TakeProfit
		if tpDist <= 0 {
			return ExitDecision{}, false
		}
		gainFraction = (pos.EntryPrice - currentPrice) / tpDist
	}

	if gainFraction <= 0 {
		return ExitDecision{}, false
	}

	originalQty := pos.Quantity + pos.PartialQtyClosed

	if !pos.Partial2Done && pos.Partial1Done && gainFraction >= d.cfg.Partial2TriggerPct {
		qty := math.Min(originalQty*d.cfg.Partial2SizePct, pos.Quantity)
		return ExitDecision{
			Action:       "close_partial",
			Quantity:     qty,
			Reason:       fmt.Sprintf("partial2: %.0f%% of TP reached (gain=%.1f%%)", d.cfg.Partial2TriggerPct*100, gainFraction*100),
			PartialIndex: 2,
		}, true
	}

	if !pos.Partial1Done && gainFraction >= d.cfg.Partial1TriggerPct {
		qty := math.Min(originalQty*d.cfg.Partial1SizePct, pos.Quantity)
		return ExitDecision{
			Action:       "close_partial",
			Quantity:     qty,
			Reason:       fmt.Sprintf("partial1: %.0f%% of TP reached (gain=%.1f%%)", d.cfg.Partial1TriggerPct*100, gainFraction*100),
			PartialIndex: 1,
		}, true
	}

	return ExitDecision{}, false
}

// BuildAndRecordOutcome constructs a TradeOutcome from a closed position and
// feeds it into the FeedbackLoop.
func (d *DynamicExitEngine) BuildAndRecordOutcome(botID string, pos *PaperPosition, exitPrice float64, exitReason string, pnlAbsolute float64) {
	if d.feedbackLoop == nil {
		return
	}

	var pnlPct float64
	if pos.EntryPrice > 0 && pos.Quantity > 0 {
		pnlPct = pnlAbsolute / (pos.EntryPrice * pos.Quantity) * 100.0
	}

	side := "BUY"
	if pos.Direction != "buy" {
		side = "SELL"
	}

	outcome := &learning.TradeOutcome{
		TradeID:               fmt.Sprintf("%s-%s-%d", botID, pos.Pair, pos.OpenedAt.UnixNano()),
		Pair:                  pos.Pair,
		Side:                  side,
		EntryPrice:            pos.EntryPrice,
		ExitPrice:             exitPrice,
		Size:                  pos.Quantity,
		EntryTime:             pos.OpenedAt,
		ExitTime:              time.Now(),
		PnLAbsolute:           pnlAbsolute,
		PnLPercent:            pnlPct,
		Won:                   pnlAbsolute > 0,
		ExitReason:            exitReason,
		StopLoss:              pos.OriginalSL,
		TakeProfit:            pos.OriginalTP,
		MaxFavorableExcursion: calcMFEPct(pos),
		MaxAdverseExcursion:   calcMAEPct(pos, exitPrice),
	}
	d.feedbackLoop.RegisterOutcome(outcome)
}

func calcMFEPct(pos *PaperPosition) float64 {
	if pos.MaxFavorablePrice == 0 || pos.EntryPrice == 0 {
		return 0
	}
	if pos.Direction == "buy" {
		return (pos.MaxFavorablePrice - pos.EntryPrice) / pos.EntryPrice * 100.0
	}
	return (pos.EntryPrice - pos.MaxFavorablePrice) / pos.EntryPrice * 100.0
}

func calcMAEPct(pos *PaperPosition, exitPrice float64) float64 {
	if pos.EntryPrice == 0 {
		return 0
	}
	if pos.Direction == "buy" {
		if exitPrice < pos.EntryPrice {
			return (pos.EntryPrice - exitPrice) / pos.EntryPrice * 100.0
		}
		if pos.OriginalSL > 0 {
			return (pos.EntryPrice - pos.OriginalSL) / pos.EntryPrice * 100.0
		}
	} else {
		if exitPrice > pos.EntryPrice {
			return (exitPrice - pos.EntryPrice) / pos.EntryPrice * 100.0
		}
		if pos.OriginalSL > 0 {
			return (pos.OriginalSL - pos.EntryPrice) / pos.EntryPrice * 100.0
		}
	}
	return 0
}

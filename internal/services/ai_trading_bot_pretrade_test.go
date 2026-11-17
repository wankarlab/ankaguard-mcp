package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	aivalidation "github.com/rantipay/trading-mcp/internal/ai/validation"
)

// newPreTradeTestBot wires the real validation service (default config, the
// repo's rule files) and a fake capital source, as production does.
func newPreTradeTestBot(t *testing.T, capital func(ctx context.Context) (float64, float64, error)) *AITradingBot {
	t.Helper()
	cfg := aivalidation.DefaultConfig()
	cfg.RulesDir = "../../config/validation"
	svc := aivalidation.NewValidationService(cfg)
	if err := svc.Initialize(context.Background()); err != nil {
		t.Fatalf("validation service: %v", err)
	}
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	return &AITradingBot{
		log:              log,
		config:           &TradingBotConfig{TradeSize: 2, Leverage: 5},
		positionManager:  ai.NewPositionManager(nil, log),
		validationSvc:    svc,
		accountCapitalFn: capital,
	}
}

func fixedCapital(total, available float64) func(ctx context.Context) (float64, float64, error) {
	return func(context.Context) (float64, float64, error) { return total, available, nil }
}

// A small account with a 2 USD, 5x, 1.5%/3% trade and four confluence factors
// must pass: risk is 0.03 USD = 0.11% of 27 USD and margin 0.40 USD is available.
func TestPreTradeValidation_SmallAccountTradePasses(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	decision := &ai.Decision{Action: "BUY", Confidence: 0.8, Entry: 100, StopLoss: 98.5, TakeProfit: 103}
	mctx := &ai.MarketContext{Timeframes: map[string]*ai.TimeframeContext{
		"5m": {Trend: "bullish", Indicators: &ai.Indicators{RSI: 50, Volume: 1000}},
	}}
	ok, reason := bot.runPreTradeValidation(decision, mctx, "UAI-USDT", "kucoin", 2, 5)
	if !ok {
		t.Fatalf("expected the trade to pass, blocked: %s", reason)
	}
}

func TestPreTradeValidation_BlocksWhenRiskExceedsCapital(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	decision := &ai.Decision{Action: "SELL", Confidence: 0.8, Entry: 100, StopLoss: 101.5, TakeProfit: 97}
	mctx := &ai.MarketContext{Timeframes: map[string]*ai.TimeframeContext{
		"5m": {Trend: "bearish", Indicators: &ai.Indicators{RSI: 50, Volume: 1000}},
	}}
	// 2000 USD notional at 5x: risk 30 USD (110% of capital), margin 400 USD.
	ok, reason := bot.runPreTradeValidation(decision, mctx, "ETH-USDT", "kucoin", 2000, 5)
	if ok {
		t.Fatal("expected the trade to be blocked")
	}
	for _, want := range []string{"RISK_TOO_HIGH", "INSUFFICIENT_CAPITAL"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q lacks %s", reason, want)
		}
	}
}

func TestPreTradeValidation_BlocksWithoutConfluence(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	decision := &ai.Decision{Action: "BUY", Confidence: 0.6, Entry: 100, StopLoss: 98.5, TakeProfit: 103}
	ok, reason := bot.runPreTradeValidation(decision, &ai.MarketContext{}, "UAI-USDT", "kucoin", 2, 5)
	if ok || !strings.Contains(reason, "INSUFFICIENT_CONFLUENCE") {
		t.Fatalf("expected an INSUFFICIENT_CONFLUENCE block, got ok=%v reason=%q", ok, reason)
	}
	for _, unwanted := range []string{"RISK_TOO_HIGH", "INSUFFICIENT_CAPITAL"} {
		if strings.Contains(reason, unwanted) {
			t.Errorf("capital/risk must not fail on a 2 USD trade: %s", reason)
		}
	}
}

func TestPreTradeValidation_FailsClosedWithoutCapital(t *testing.T) {
	bot := newPreTradeTestBot(t, func(context.Context) (float64, float64, error) {
		return 0, 0, errors.New("kucoin timeout")
	})
	decision := &ai.Decision{Action: "BUY", Confidence: 0.8, Entry: 100, StopLoss: 98.5, TakeProfit: 103}
	ok, reason := bot.runPreTradeValidation(decision, &ai.MarketContext{}, "UAI-USDT", "kucoin", 2, 5)
	if ok || !strings.Contains(reason, "account capital unavailable") {
		t.Fatalf("expected a fail-closed block, got ok=%v reason=%q", ok, reason)
	}
}

func TestAccountCapital_CachesAndSurvivesRefreshFailure(t *testing.T) {
	calls := 0
	bot := &AITradingBot{log: logrus.New(), accountCapitalFn: func(context.Context) (float64, float64, error) {
		calls++
		if calls == 1 {
			return 27, 25, nil
		}
		return 0, 0, errors.New("down")
	}}
	if total, _, err := bot.accountCapital(context.Background()); err != nil || total != 27 {
		t.Fatalf("first read: %v %v", total, err)
	}
	// Within the TTL the source is not called again.
	if _, _, err := bot.accountCapital(context.Background()); err != nil || calls != 1 {
		t.Fatalf("expected a cached read, calls=%d err=%v", calls, err)
	}
	// After the TTL a failed refresh falls back to the last known value.
	bot.capitalAt = time.Now().Add(-2 * accountCapitalCacheTTL)
	if total, _, err := bot.accountCapital(context.Background()); err != nil || total != 27 || calls != 2 {
		t.Fatalf("expected the stale value, total=%v calls=%d err=%v", total, calls, err)
	}
	// Too old to trust: the error surfaces.
	bot.capitalAt = time.Now().Add(-2 * accountCapitalStaleMax)
	if _, _, err := bot.accountCapital(context.Background()); err == nil {
		t.Fatal("expected an error once the cached value is too old")
	}
}

// The raw technical decision carries a TP clamped to the nearest resistance
// (R:R 0.41, as seen live on AKE-USDT 2026-11-02 16:32). Normalization must
// widen it to the minimum R:R so the validator passes the order that is placed.
func TestNormalizeDecisionLevels_WidensClampedTakeProfit(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	decision := &ai.Decision{Action: "BUY", Confidence: 0.9, Entry: 100, StopLoss: 98.5, TakeProfit: 100.6}
	mctx := &ai.MarketContext{CurrentPrice: 100, Timeframes: map[string]*ai.TimeframeContext{
		"5m": {Trend: "bullish", Indicators: &ai.Indicators{RSI: 50, Volume: 1000}},
	}}
	bot.normalizeDecisionLevels(context.Background(), decision, mctx)
	if decision.TakeProfit < 103-1e-9 {
		t.Fatalf("expected TP widened to >= 103 (2x risk), got %.4f", decision.TakeProfit)
	}
	if decision.StopLoss != 98.5 {
		t.Fatalf("stop loss must not be tightened, got %.4f", decision.StopLoss)
	}
	if ok, reason := bot.runPreTradeValidation(decision, mctx, "AKE-USDT", "kucoin", 2, 5); !ok {
		t.Fatalf("normalized trade should pass validation, blocked: %s", reason)
	}
}

// A SHORT whose TP sits above entry (TP clamped to a support that is above the
// current price) is inverted: normalization must repair it and the validator
// must then accept the order.
func TestNormalizeDecisionLevels_RepairsInvertedShortTakeProfit(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	decision := &ai.Decision{Action: "SELL", Confidence: 0.85, Entry: 100, StopLoss: 101.5, TakeProfit: 100.2}
	mctx := &ai.MarketContext{CurrentPrice: 100, Timeframes: map[string]*ai.TimeframeContext{
		"5m": {Trend: "bearish", Indicators: &ai.Indicators{RSI: 50, Volume: 1000}},
	}}
	bot.normalizeDecisionLevels(context.Background(), decision, mctx)
	if decision.TakeProfit >= 100 {
		t.Fatalf("short TP must be below entry, got %.4f", decision.TakeProfit)
	}
	if decision.StopLoss <= 100 {
		t.Fatalf("short SL must stay above entry, got %.4f", decision.StopLoss)
	}
	if ok, reason := bot.runPreTradeValidation(decision, mctx, "AKE-USDT", "kucoin", 2, 5); !ok {
		t.Fatalf("normalized short should pass validation, blocked: %s", reason)
	}
}

// A decision that already meets the minimum R:R is left untouched.
func TestNormalizeDecisionLevels_KeepsGoodLevels(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	decision := &ai.Decision{Action: "BUY", Confidence: 0.8, Entry: 100, StopLoss: 98.5, TakeProfit: 104}
	bot.normalizeDecisionLevels(context.Background(), decision, &ai.MarketContext{CurrentPrice: 100})
	if decision.TakeProfit != 104 || decision.StopLoss != 98.5 {
		t.Fatalf("levels changed: sl=%.4f tp=%.4f", decision.StopLoss, decision.TakeProfit)
	}
}

// An operator manual entry carries no signal confluence; the validator must
// still accept it (the operator's order stands in) while keeping the risk,
// capital and R:R checks.
func TestPreTradeValidation_ManualEntryNeedsNoConfluence(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	decision := &ai.Decision{Action: "BUY", Confidence: 1.0, Entry: 100, StopLoss: 98.5, TakeProfit: 103,
		Size: 2, Strategy: manualDecisionStrategy("ICNT-USDT-1")}
	mctx := &ai.MarketContext{CurrentPrice: 100}
	if ok, reason := bot.runPreTradeValidation(decision, mctx, "ICNT-USDT", "kucoin", 2, 5); !ok {
		t.Fatalf("manual entry should pass without confluence, blocked: %s", reason)
	}
	// The same levels without the manual tag are blocked for lack of confluence.
	auto := &ai.Decision{Action: "BUY", Confidence: 0.6, Entry: 100, StopLoss: 98.5, TakeProfit: 103}
	if ok, _ := bot.runPreTradeValidation(auto, mctx, "ICNT-USDT", "kucoin", 2, 5); ok {
		t.Fatalf("automatic decision without confluence should be blocked")
	}
	// Risk checks still apply to manual entries: 300 USD risk on a 27 USD account.
	tooBig := &ai.Decision{Action: "BUY", Confidence: 1.0, Entry: 100, StopLoss: 98.5, TakeProfit: 103,
		Size: 20000, Strategy: manualDecisionStrategy("ICNT-USDT-2")}
	if ok, _ := bot.runPreTradeValidation(tooBig, mctx, "ICNT-USDT", "kucoin", 20000, 5); ok {
		t.Fatalf("oversized manual entry should be blocked by the risk checks")
	}
}

func TestManualEntry_TagAndWaiters(t *testing.T) {
	d := &ai.Decision{Strategy: manualDecisionStrategy("X-1")}
	if !isManualEntry(d) || manualTag(d) != "X-1" {
		t.Fatalf("manual tag not recognised: %q", manualTag(d))
	}
	if isManualEntry(&ai.Decision{Strategy: "ai_multi_timeframe_enhanced"}) {
		t.Fatalf("automatic strategy flagged as manual")
	}
	var mw manualWaiters
	ch := mw.register("X-1")
	mw.deliver(&ExecutionResult{Tag: "other"}) // nobody waits: must not block or panic
	mw.deliver(&ExecutionResult{Tag: "X-1", Success: true})
	select {
	case r := <-ch:
		if !r.Success {
			t.Fatalf("unexpected result %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatalf("result not delivered to waiter")
	}
	mw.unregister("X-1")
	mw.deliver(&ExecutionResult{Tag: "X-1"}) // after unregister: dropped silently
}

// ExecuteManualEntry refuses to run when the bot is stopped: a position opened
// then would be unmanaged (no SL/TP monitoring).
func TestExecuteManualEntry_RequiresRunningBot(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	_, err := bot.ExecuteManualEntry(context.Background(), ManualEntryRequest{Pair: "ICNT-USDT", Side: "BUY", SizeUSD: 2})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("expected not-running error, got %v", err)
	}
	bot.running = true
	if _, err := bot.ExecuteManualEntry(context.Background(), ManualEntryRequest{Side: "HOLD"}); err == nil {
		t.Fatalf("expected invalid side error")
	}
}

func TestClosePnLMemo_ConsumedOnce(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	if _, ok := bot.takeClosePnL("p1"); ok {
		t.Fatalf("empty memo must report not found")
	}
	bot.rememberClosePnL("p1", 0.0127)
	v, ok := bot.takeClosePnL("p1")
	if !ok || v != 0.0127 {
		t.Fatalf("expected 0.0127, got %v %v", v, ok)
	}
	if _, ok := bot.takeClosePnL("p1"); ok {
		t.Fatalf("memo entry must be consumed once")
	}
}

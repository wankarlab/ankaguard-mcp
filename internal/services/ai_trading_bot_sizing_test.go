package services

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/rantipay/trading-mcp/internal/ai"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/gct"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
)

func newSizingTestBot(capital func(context.Context) (float64, float64, error)) *AITradingBot {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	return &AITradingBot{
		log:              log,
		config:           &TradingBotConfig{TradeSize: 2, MaxDailyLoss: 5, Leverage: 5},
		accountCapitalFn: capital,
	}
}

func sizingCtx(values map[string]string) context.Context {
	return dynamic.WithSnapshot(context.Background(), dynamic.NewStaticSnapshot(values))
}

// The manifest defaults (base 10 %, floor 2, ceiling 25, daily 3 %, pair 0.5 %).
func defaultSizingValues() map[string]string {
	return map[string]string{
		"bot.sizing.mode":                  "capital_pct",
		"bot.sizing.base_pct":              "10",
		"bot.sizing.min_notional_usd":      "2",
		"bot.sizing.max_notional_usd":      "25",
		"bot.max_daily_loss_percent":       "3",
		"bot.max_daily_loss_usd":           "0",
		"bot.pair_pause.net_loss_pct":      "0.5",
		"bot.drawdown.use_account_capital": "true",
	}
}

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %.6f, want %.6f", name, got, want)
	}
}

func TestResolveNotional_CapitalPct_SmallAndLargeAccount(t *testing.T) {
	ctx := sizingCtx(defaultSizingValues())

	small := newSizingTestBot(fixedCapital(27.18, 25))
	n, err := small.resolveNotional(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "27 USD base notional", n, 2.718)

	large := newSizingTestBot(fixedCapital(500, 480))
	n, err = large.resolveNotional(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "500 USD base notional (capped)", n, 25) // 50 → ceiling 25
}

func TestResolveNotional_FloorApplies(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(10, 10)) // 10 % = 1 USD < floor 2
	n, err := bot.resolveNotional(sizingCtx(defaultSizingValues()), nil)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "floored notional", n, 2)
}

func TestResolveNotional_ZeroCeilingFallsBackToDefault(t *testing.T) {
	v := defaultSizingValues()
	v["bot.sizing.max_notional_usd"] = "0"
	bot := newSizingTestBot(fixedCapital(1000, 1000))
	n, err := bot.resolveNotional(sizingCtx(v), nil)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "notional with ceiling 0", n, defaultSizingMaxNotional)
}

func TestResolveNotional_TierPctAndUSDConversion(t *testing.T) {
	ctx := sizingCtx(defaultSizingValues())
	bot := newSizingTestBot(fixedCapital(27.18, 25))

	withPct := &SizingTier{Name: "high", SizeUSD: 5, SizePct: 18.5}
	n, err := bot.resolveNotional(ctx, withPct)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "tier with size_pct", n, 27.18*0.185)

	// USD-only tier: converted once against the capital of the first use and
	// then kept as a percentage when the account grows.
	usdOnly := &SizingTier{Name: "legacy", SizeUSD: 5}
	n, err = bot.resolveNotional(ctx, usdOnly)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "USD tier at first use", n, 5)
	bot.capitalMu.Lock()
	bot.capitalTotal = 54.36 // account doubled
	bot.capitalMu.Unlock()
	n, err = bot.resolveNotional(ctx, usdOnly)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "USD tier after the account doubled", n, 10)
}

func TestResolveNotional_AbsoluteMode(t *testing.T) {
	v := defaultSizingValues()
	v["bot.sizing.mode"] = "absolute"
	bot := newSizingTestBot(fixedCapital(500, 500))
	ctx := sizingCtx(v)
	n, err := bot.resolveNotional(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "absolute base", n, 2)
	n, _ = bot.resolveNotional(ctx, &SizingTier{Name: "t", SizeUSD: 3.5, SizePct: 13})
	approx(t, "absolute tier", n, 3.5)
}

func TestResolveNotional_NoSnapshotUsesYAML(t *testing.T) {
	bot := newSizingTestBot(func(context.Context) (float64, float64, error) {
		t.Fatal("YAML fallback must not need the capital")
		return 0, 0, nil
	})
	// No snapshot in ctx and no default store → absolute YAML values.
	n, err := bot.resolveNotional(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "fallback notional", n, 2)
	if p := bot.readSizingParams(context.Background()); !p.Fallback {
		t.Fatal("expected the fallback flag")
	}
}

func TestResolveNotional_CapitalUnavailableFailsClosed(t *testing.T) {
	bot := newSizingTestBot(func(context.Context) (float64, float64, error) {
		return 0, 0, errors.New("exchange down")
	})
	if _, err := bot.resolveNotional(sizingCtx(defaultSizingValues()), nil); err == nil {
		t.Fatal("expected an error when the capital is unavailable")
	}
}

func TestDailyLossLimit_RelativeAndAbsoluteCap(t *testing.T) {
	small := newSizingTestBot(fixedCapital(27.18, 25))
	approx(t, "27 USD daily cap", small.effectiveDailyLossLimit(sizingCtx(defaultSizingValues())), 0.8154)

	v := defaultSizingValues()
	v["bot.max_daily_loss_usd"] = "5"
	large := newSizingTestBot(fixedCapital(500, 500))
	approx(t, "500 USD daily cap tightened by the USD key", large.effectiveDailyLossLimit(sizingCtx(v)), 5) // 15 → 5

	v["bot.sizing.mode"] = "absolute"
	v["bot.max_daily_loss_usd"] = "0"
	abs := newSizingTestBot(fixedCapital(500, 500))
	approx(t, "absolute mode daily cap", abs.effectiveDailyLossLimit(sizingCtx(v)), 5) // YAML MaxDailyLoss
}

func TestDayStartCapital_FrozenUntilReset(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(27.18, 25))
	ctx := sizingCtx(defaultSizingValues())
	approx(t, "first cap", bot.effectiveDailyLossLimit(ctx), 0.8154)

	// The account drops intraday: the cap stays on the day-start capital.
	bot.capitalMu.Lock()
	bot.capitalTotal = 20
	bot.capitalMu.Unlock()
	approx(t, "intraday cap", bot.effectiveDailyLossLimit(ctx), 0.8154)

	bot.resetSizingDay()
	approx(t, "next-day cap", bot.effectiveDailyLossLimit(ctx), 0.6)
}

func TestPairPauseThreshold(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(500, 500))
	approx(t, "500 USD pair pause", bot.pairPauseThreshold(sizingCtx(defaultSizingValues())), -2.5)

	down := newSizingTestBot(func(context.Context) (float64, float64, error) { return 0, 0, errors.New("down") })
	approx(t, "pair pause without capital", down.pairPauseThreshold(sizingCtx(defaultSizingValues())), -legacyPairNetLossUSD)
}

func TestDrawdownFactor_NeverCompounds(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(27.18, 25))
	ctx := sizingCtx(defaultSizingValues())
	bot.setDrawdownSizeFactor(0.5)
	bot.setDrawdownSizeFactor(0.5)
	bot.setDrawdownSizeFactor(0.8) // a weaker factor does not loosen it
	n, _ := bot.resolveNotional(ctx, nil)
	approx(t, "notional with drawdown factor", n, 2) // 2.718*0.5=1.36 → floor 2
	bot.resetSizingDay()
	approx(t, "factor after reset", bot.drawdownSizeFactor(), 1)
}

// A 20 % intraday loss on a 27 USD account must reduce new entries (the old
// check divided the negative loss by a 1000 USD proxy and never fired), and the
// reduction must not compound across cycles.
func TestCheckDrawdownSafety_FiresOnLossAndDoesNotCompound(t *testing.T) {
	v := defaultSizingValues()
	v["bot.drawdown.size_reduce_threshold_pct"] = "15"
	v["bot.drawdown.pause_threshold_pct"] = "25"
	v["bot.drawdown.size_reduce_factor"] = "0.5"
	ctx := sizingCtx(v)
	bot := newSizingTestBot(fixedCapital(27.18, 25))
	bot.dailyLoss = -5.5 // 20.2 % of 27.18

	for i := 0; i < 3; i++ {
		if !bot.checkDrawdownSafety(ctx) {
			t.Fatal("20 % drawdown must reduce size, not pause")
		}
	}
	approx(t, "drawdown factor", bot.drawdownSizeFactor(), 0.5)
	approx(t, "YAML trade size untouched", bot.config.TradeSize, 2)

	bot.dailyLoss = 0
	if !bot.checkDrawdownSafety(ctx) {
		t.Fatal("no loss must not pause")
	}
}

// validation.max_risk_per_trade_pct is applied hot: a 1.5 % risk trade is
// blocked at 1 % and passes after the key is raised to 2 %.
func TestValidationThresholds_HotReload(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(27.18, 25.36))
	// 20 USD notional with a 2 % stop risks 0.40 USD = 1.47 % of 27.18.
	decision := &ai.Decision{Action: "BUY", Confidence: 0.8, Entry: 100, StopLoss: 98, TakeProfit: 104}
	mctx := &ai.MarketContext{Timeframes: map[string]*ai.TimeframeContext{
		"5m": {Trend: "bullish", Indicators: &ai.Indicators{RSI: 50, Volume: 1000}},
	}}

	bot.applyValidationThresholds(sizingCtx(map[string]string{"validation.max_risk_per_trade_pct": "1"}))
	if ok, _ := bot.runPreTradeValidation(decision, mctx, "UAI-USDT", "kucoin", 20, 5); ok {
		t.Fatal("1.47 % risk must be blocked at a 1 % limit")
	}

	bot.applyValidationThresholds(sizingCtx(map[string]string{"validation.max_risk_per_trade_pct": "2"}))
	if ok, reason := bot.runPreTradeValidation(decision, mctx, "UAI-USDT", "kucoin", 20, 5); !ok {
		t.Fatalf("1.47 %% risk must pass at a 2 %% limit, blocked: %s", reason)
	}
}

func TestHandleMinContractTooLarge_BlacklistsAndRotates(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(27.18, 25))
	bot.config.Pair = "XMR-USDT"
	bot.handleMinContractTooLarge("XMR-USDT", &gct.ErrMinContractTooLarge{
		Symbol: "XMRUSDTM", MinContractNotional: 11.6, TargetUSD: 2, Factor: 2,
	})
	if !bot.isPairBlacklisted("XMR-USDT") {
		t.Fatal("pair must be blacklisted after the min-contract refusal")
	}
	if bot.config.Pair != "" {
		t.Fatalf("current pair must be cleared for rotation, got %q", bot.config.Pair)
	}
}

type fakeCapSentinel struct {
	internalRisk.RiskSentinel
	cap float64
}

func (f fakeCapSentinel) EffectiveMaxOrderSizeUSD(context.Context) float64 { return f.cap }

// Spec scenario: the sentinel caps orders at 2 USD while the bot can produce
// 7 USD → a WARN naming both values.
func TestCheckConsistency_WarnsWhenSentinelCapBelowBotMax(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(27.18, 25))
	hook := logrustest.NewLocal(bot.log)
	bot.log.SetLevel(logrus.InfoLevel)
	bot.riskSentinel = fakeCapSentinel{cap: 2}
	v := defaultSizingValues()
	v["bot.sizing.mode"] = "absolute"
	v["dynamic_sizing.tiers"] = `[{"name":"max","min_confidence":0.9,"max_confidence":1,"size_usd":7}]`
	bot.checkConsistency(sizingCtx(v))

	var found bool
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel && strings.Contains(e.Message, "2.00") && strings.Contains(e.Message, "7.00") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an incoherence WARN with 2.00 and 7.00, got %d entries", len(hook.AllEntries()))
	}

	hook.Reset()
	bot.riskSentinel = fakeCapSentinel{cap: 10}
	bot.checkConsistency(sizingCtx(v))
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			t.Fatalf("no WARN expected when the sentinel cap covers the bot max: %s", e.Message)
		}
	}
}

// Regression 2026-11-03: a manual PIEVERSE entry at 1.1559 with the 5 %
// high-volatility default stop computed 5.0000000000000036 % and was refused by
// STOP_LOSS_MAX_DISTANCE (<= 5.0). A stop exactly on the limit must pass.
func TestPreTradeValidation_StopExactlyOnLimitPasses(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(25.32, 24))
	entry := 1.1559
	decision := &ai.Decision{Action: "BUY", Confidence: 1, Entry: entry, StopLoss: entry * (1 - 5.0/100), TakeProfit: entry * 1.10,
		Strategy: manualDecisionStrategy("t")}
	mctx := &ai.MarketContext{Timeframes: map[string]*ai.TimeframeContext{
		"5m": {Trend: "bullish", Indicators: &ai.Indicators{RSI: 50, Volume: 1000}},
	}}
	if ok, reason := bot.runPreTradeValidation(decision, mctx, "PIEVERSE-USDT", "kucoin", 2.53, 5); !ok {
		t.Fatalf("a 5 %% stop must pass the 5 %% limit, blocked: %s", reason)
	}
}

func TestApplyAutoConfiguration_CapsStopLossToValidatorLimit(t *testing.T) {
	bot := newSizingTestBot(fixedCapital(25.32, 24))
	bot.applyAutoConfiguration(&RiskParameters{StopLossPct: 7, TakeProfitPct: 10, MaxPositionSize: 2, MaxOpenPositions: 1, ConfidenceThreshold: 0.75})
	approx(t, "auto stop capped", bot.config.DefaultStopLossPct, defaultMaxStopLossPct)

	bot.applyAutoConfiguration(&RiskParameters{StopLossPct: 2, TakeProfitPct: 4, MaxPositionSize: 2, MaxOpenPositions: 1, ConfidenceThreshold: 0.65})
	approx(t, "auto stop below the limit untouched", bot.config.DefaultStopLossPct, 2)
}

// validation.max_stop_loss_pct reaches the STOP_LOSS_MAX_DISTANCE rule.
func TestValidationThresholds_MaxStopLossHotReload(t *testing.T) {
	bot := newPreTradeTestBot(t, fixedCapital(25.32, 24))
	entry := 100.0
	decision := &ai.Decision{Action: "BUY", Confidence: 1, Entry: entry, StopLoss: 94, TakeProfit: 115,
		Strategy: manualDecisionStrategy("t")}
	mctx := &ai.MarketContext{Timeframes: map[string]*ai.TimeframeContext{
		"5m": {Trend: "bullish", Indicators: &ai.Indicators{RSI: 50, Volume: 1000}},
	}}
	if ok, _ := bot.runPreTradeValidation(decision, mctx, "X-USDT", "kucoin", 2, 5); ok {
		t.Fatal("a 6 % stop must be refused at the default 5 % limit")
	}
	bot.applyValidationThresholds(sizingCtx(map[string]string{"validation.max_stop_loss_pct": "7"}))
	if ok, reason := bot.runPreTradeValidation(decision, mctx, "X-USDT", "kucoin", 2, 5); !ok {
		t.Fatalf("a 6 %% stop must pass at a 7 %% limit, blocked: %s", reason)
	}
}

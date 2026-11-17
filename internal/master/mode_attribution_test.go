package master

import (
	"testing"
	"time"
)

// helpers to build trade records concisely.
func tradeSpot(won bool, pnl float64) TradeRecord {
	return TradeRecord{
		Pair:       "BTC-USDT",
		MarketType: "spot",
		Leverage:   1,
		Won:        won,
		PnLPercent: pnl,
		ExitTime:   time.Now(),
	}
}

func tradeFutures(won bool, pnl float64) TradeRecord {
	return TradeRecord{
		Pair:       "BTC-USDT",
		MarketType: "futures",
		Leverage:   10,
		Won:        won,
		PnLPercent: pnl,
		ExitTime:   time.Now(),
	}
}

func tradeLeverageOnly(leverage float64, won bool, pnl float64) TradeRecord {
	return TradeRecord{
		Pair:       "ETH-USDT",
		Leverage:   leverage,
		Won:        won,
		PnLPercent: pnl,
		ExitTime:   time.Now(),
	}
}

// ── deriveMode ────────────────────────────────────────────────────────────────

func TestDeriveMode_ExplicitMarketType(t *testing.T) {
	tests := []struct {
		name       string
		marketType string
		leverage   float64
		want       string
	}{
		{"explicit spot", "spot", 10, "spot"},          // MarketType wins over leverage
		{"explicit futures", "futures", 1, "futures"},  // MarketType wins even at low leverage
		{"explicit margin", "margin", 5, "margin"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := TradeRecord{MarketType: tc.marketType, Leverage: tc.leverage}
			got := deriveMode(tr)
			if got != tc.want {
				t.Errorf("deriveMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeriveMode_LeverageHeuristic(t *testing.T) {
	tests := []struct {
		name     string
		leverage float64
		want     string
	}{
		{"no leverage → spot", 1, "spot"},
		{"zero leverage → spot", 0, "spot"},
		{"leverage 2 → futures", 2, "futures"},
		{"leverage 100 → futures", 100, "futures"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := TradeRecord{Leverage: tc.leverage}
			got := deriveMode(tr)
			if got != tc.want {
				t.Errorf("deriveMode(leverage=%.0f) = %q, want %q", tc.leverage, got, tc.want)
			}
		})
	}
}

// ── AttributeByMode ────────────────────────────────────────────────────────────

func TestAttributeByMode_Empty(t *testing.T) {
	result := AttributeByMode(nil)
	if len(result) != 0 {
		t.Errorf("expected empty map for nil input, got %d entries", len(result))
	}
	result = AttributeByMode([]TradeRecord{})
	if len(result) != 0 {
		t.Errorf("expected empty map for empty slice, got %d entries", len(result))
	}
}

func TestAttributeByMode_FuturesWinsSpotLoses(t *testing.T) {
	trades := []TradeRecord{
		// Futures bucket: 3 wins, 1 loss → WinRate=0.75
		tradeFutures(true, 2.0),
		tradeFutures(true, 3.0),
		tradeFutures(true, 1.5),
		tradeFutures(false, -1.0),
		// Spot bucket: 1 win, 3 losses → WinRate=0.25
		tradeSpot(true, 1.0),
		tradeSpot(false, -2.0),
		tradeSpot(false, -3.0),
		tradeSpot(false, -1.5),
	}

	result := AttributeByMode(trades)

	fut, ok := result["futures"]
	if !ok {
		t.Fatal("expected futures bucket")
	}
	if fut.Trades != 4 {
		t.Errorf("futures trades: got %d, want 4", fut.Trades)
	}
	if fut.Wins != 3 {
		t.Errorf("futures wins: got %d, want 3", fut.Wins)
	}
	if !almostEqual(fut.WinRate, 0.75, 1e-9) {
		t.Errorf("futures WinRate: got %f, want 0.75", fut.WinRate)
	}

	spot, ok := result["spot"]
	if !ok {
		t.Fatal("expected spot bucket")
	}
	if spot.Trades != 4 {
		t.Errorf("spot trades: got %d, want 4", spot.Trades)
	}
	if spot.Wins != 1 {
		t.Errorf("spot wins: got %d, want 1", spot.Wins)
	}
	if !almostEqual(spot.WinRate, 0.25, 1e-9) {
		t.Errorf("spot WinRate: got %f, want 0.25", spot.WinRate)
	}
}

func TestAttributeByMode_ProfitFactor(t *testing.T) {
	// Gross profit = 6.0, gross loss = 2.0 → PF = 3.0
	trades := []TradeRecord{
		tradeFutures(true, 2.0),
		tradeFutures(true, 4.0),
		tradeFutures(false, -2.0),
	}
	result := AttributeByMode(trades)
	fut := result["futures"]
	if !almostEqual(fut.ProfitFactor, 3.0, 1e-9) {
		t.Errorf("ProfitFactor: got %f, want 3.0", fut.ProfitFactor)
	}
}

func TestAttributeByMode_NoLossInfPF(t *testing.T) {
	// All wins → gross loss = 0 → PF effectively huge (≫ 1)
	trades := []TradeRecord{
		tradeFutures(true, 1.0),
		tradeFutures(true, 2.0),
	}
	result := AttributeByMode(trades)
	fut := result["futures"]
	if fut.ProfitFactor <= 1.0 {
		t.Errorf("expected PF >> 1 with no losses, got %f", fut.ProfitFactor)
	}
}

func TestAttributeByMode_LeverageDerivation(t *testing.T) {
	// Trades with no MarketType field — derived via leverage heuristic.
	trades := []TradeRecord{
		tradeLeverageOnly(1, true, 1.0),   // spot
		tradeLeverageOnly(0, true, 0.5),   // spot (zero leverage)
		tradeLeverageOnly(5, false, -1.0), // futures
		tradeLeverageOnly(20, true, 3.0),  // futures
	}
	result := AttributeByMode(trades)

	if result["spot"].Trades != 2 {
		t.Errorf("spot trades from leverage: got %d, want 2", result["spot"].Trades)
	}
	if result["futures"].Trades != 2 {
		t.Errorf("futures trades from leverage: got %d, want 2", result["futures"].Trades)
	}
}

// ── AttributeByModeAndRegime ───────────────────────────────────────────────────

func TestAttributeByModeAndRegime_Buckets(t *testing.T) {
	now := time.Now()
	trades := []TradeRecord{
		{MarketType: "futures", Regime: "trending", Won: true, PnLPercent: 2.0, ExitTime: now},
		{MarketType: "futures", Regime: "trending", Won: true, PnLPercent: 1.0, ExitTime: now},
		{MarketType: "spot", Regime: "trending", Won: false, PnLPercent: -1.0, ExitTime: now},
		{MarketType: "futures", Regime: "ranging", Won: false, PnLPercent: -0.5, ExitTime: now},
		{MarketType: "spot", Regime: "ranging", Won: true, PnLPercent: 0.5, ExitTime: now},
	}

	result := AttributeByModeAndRegime(trades)

	futTrending := result["futures"]["trending"]
	if futTrending.Trades != 2 || futTrending.Wins != 2 {
		t.Errorf("futures/trending: got %d trades %d wins, want 2/2", futTrending.Trades, futTrending.Wins)
	}

	spotTrending := result["spot"]["trending"]
	if spotTrending.Trades != 1 || spotTrending.Wins != 0 {
		t.Errorf("spot/trending: got %d trades %d wins, want 1/0", spotTrending.Trades, spotTrending.Wins)
	}

	futRanging := result["futures"]["ranging"]
	if futRanging.Trades != 1 {
		t.Errorf("futures/ranging: got %d trades, want 1", futRanging.Trades)
	}
}

func TestAttributeByModeAndRegime_EmptyRegime(t *testing.T) {
	// Trades with no Regime field should land in "unknown" bucket.
	trades := []TradeRecord{
		{MarketType: "spot", Won: true, PnLPercent: 1.0, ExitTime: time.Now()},
	}
	result := AttributeByModeAndRegime(trades)
	if result["spot"]["unknown"].Trades != 1 {
		t.Error("expected empty Regime to map to 'unknown' bucket")
	}
}

// almostEqual is a tolerance-based float comparison helper.
func almostEqual(a, b, tol float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tol
}

package mode

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
)

const (
	testMinLev      = 1
	testMaxLev      = 5
	testLegacyLev   = 5.0
	testHighConf    = 0.90
	testMedConf     = 0.70
	testLowATR      = 0.01  // 1 % — low volatility
	testMedATR      = 0.03  // 3 %
	testHighATR     = 0.06  // 6 % — above chaos threshold (5 %)
)

// --- regime branch tests ---

func TestDecideAutoMode_TrendingUp_ReturnsFutures(t *testing.T) {
	mc := MarketContext{Regime: "TRENDING_UP", ATRPct: testLowATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != gct.MarketTypeFutures {
		t.Fatalf("TRENDING_UP: expected futures, got %q", dec.MarketType)
	}
	if dec.Leverage < float64(testMinLev) || dec.Leverage > float64(testMaxLev) {
		t.Fatalf("TRENDING_UP: leverage %.2f outside [%d,%d]", dec.Leverage, testMinLev, testMaxLev)
	}
}

func TestDecideAutoMode_TrendingDown_ReturnsFutures(t *testing.T) {
	mc := MarketContext{Regime: "TRENDING_DOWN", ATRPct: testLowATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != gct.MarketTypeFutures {
		t.Fatalf("TRENDING_DOWN: expected futures, got %q", dec.MarketType)
	}
}

func TestDecideAutoMode_Ranging_ReturnsSpot(t *testing.T) {
	mc := MarketContext{Regime: "RANGING", ATRPct: testMedATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != gct.MarketTypeSpot {
		t.Fatalf("RANGING: expected spot, got %q", dec.MarketType)
	}
	if dec.Leverage != 1 {
		t.Fatalf("RANGING: expected leverage 1, got %.2f", dec.Leverage)
	}
}

func TestDecideAutoMode_Chaos_ReturnsSpot(t *testing.T) {
	mc := MarketContext{Regime: "CHAOS", ATRPct: testMedATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != gct.MarketTypeSpot {
		t.Fatalf("CHAOS: expected spot, got %q", dec.MarketType)
	}
	if dec.Leverage != 1 {
		t.Fatalf("CHAOS: expected leverage 1, got %.2f", dec.Leverage)
	}
}

func TestDecideAutoMode_Breakout_ReturnsFutures(t *testing.T) {
	mc := MarketContext{Regime: "BREAKOUT", ATRPct: testMedATR, Confidence: testMedConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != gct.MarketTypeFutures {
		t.Fatalf("BREAKOUT: expected futures, got %q", dec.MarketType)
	}
	if dec.Leverage < float64(testMinLev) || dec.Leverage > float64(testMaxLev) {
		t.Fatalf("BREAKOUT: leverage %.2f outside [%d,%d]", dec.Leverage, testMinLev, testMaxLev)
	}
}

func TestDecideAutoMode_Unknown_ReturnsLegacy(t *testing.T) {
	mc := MarketContext{Regime: "UNKNOWN", ATRPct: testMedATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != "" {
		t.Fatalf("UNKNOWN: expected empty MarketType (legacy), got %q", dec.MarketType)
	}
	if dec.Leverage != testLegacyLev {
		t.Fatalf("UNKNOWN: expected legacy leverage %.2f, got %.2f", testLegacyLev, dec.Leverage)
	}
}

func TestDecideAutoMode_EmptyRegime_ReturnsLegacy(t *testing.T) {
	mc := MarketContext{Regime: "", ATRPct: testMedATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != "" {
		t.Fatalf("empty regime: expected empty MarketType (legacy), got %q", dec.MarketType)
	}
	if dec.Leverage != testLegacyLev {
		t.Fatalf("empty regime: expected legacy leverage %.2f, got %.2f", testLegacyLev, dec.Leverage)
	}
}

// --- high ATR overrides regime ---

func TestDecideAutoMode_HighATR_ForcesSpot_EvenWhenTrending(t *testing.T) {
	mc := MarketContext{Regime: "TRENDING_UP", ATRPct: testHighATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.MarketType != gct.MarketTypeSpot {
		t.Fatalf("high ATR on TRENDING_UP: expected spot override, got %q", dec.MarketType)
	}
	if dec.Leverage != 1 {
		t.Fatalf("high ATR: expected leverage 1, got %.2f", dec.Leverage)
	}
}

// --- leverage scaling bounds ---

func TestDecideAutoMode_LeverageNeverExceedsMax(t *testing.T) {
	mc := MarketContext{Regime: "TRENDING_UP", ATRPct: 0.0, Confidence: 1.0}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.Leverage > float64(testMaxLev) {
		t.Fatalf("leverage %.2f exceeds maxLev %d", dec.Leverage, testMaxLev)
	}
}

func TestDecideAutoMode_LeverageNeverBelowMin(t *testing.T) {
	mc := MarketContext{Regime: "TRENDING_UP", ATRPct: chaosATRThreshold - 0.001, Confidence: 0.0}
	dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
	if dec.Leverage < float64(testMinLev) {
		t.Fatalf("leverage %.2f below minLev %d", dec.Leverage, testMinLev)
	}
}

func TestDecideAutoMode_Spot_AlwaysLeverage1(t *testing.T) {
	regimes := []string{"RANGING", "CHAOS"}
	for _, r := range regimes {
		mc := MarketContext{Regime: r, ATRPct: testLowATR, Confidence: testHighConf}
		dec := decideAutoMode(mc, testMinLev, testMaxLev, testLegacyLev)
		if dec.Leverage != 1 {
			t.Fatalf("regime %s: spot decision should have leverage 1, got %.2f", r, dec.Leverage)
		}
	}
}

func TestDecideAutoMode_LeverageScalesWithConfidence(t *testing.T) {
	mcHigh := MarketContext{Regime: "TRENDING_UP", ATRPct: testLowATR, Confidence: 1.0}
	mcLow := MarketContext{Regime: "TRENDING_UP", ATRPct: testLowATR, Confidence: 0.0}
	decHigh := decideAutoMode(mcHigh, testMinLev, testMaxLev, testLegacyLev)
	decLow := decideAutoMode(mcLow, testMinLev, testMaxLev, testLegacyLev)
	if decHigh.Leverage <= decLow.Leverage {
		t.Fatalf("higher confidence should produce higher leverage: high=%.2f low=%.2f", decHigh.Leverage, decLow.Leverage)
	}
}

func TestDecideAutoMode_LeverageScalesInverselyWithVolatility(t *testing.T) {
	mcLowVol := MarketContext{Regime: "TRENDING_UP", ATRPct: 0.01, Confidence: testHighConf}
	mcHighVol := MarketContext{Regime: "TRENDING_UP", ATRPct: 0.04, Confidence: testHighConf}
	decLowVol := decideAutoMode(mcLowVol, testMinLev, testMaxLev, testLegacyLev)
	decHighVol := decideAutoMode(mcHighVol, testMinLev, testMaxLev, testLegacyLev)
	if decLowVol.Leverage <= decHighVol.Leverage {
		t.Fatalf("lower volatility should produce higher leverage: lowVol=%.2f highVol=%.2f", decLowVol.Leverage, decHighVol.Leverage)
	}
}

// --- min == max edge case ---

func TestDecideAutoMode_MinEqualsMax_ReturnsMinLev(t *testing.T) {
	mc := MarketContext{Regime: "TRENDING_UP", ATRPct: testLowATR, Confidence: testHighConf}
	dec := decideAutoMode(mc, 3, 3, testLegacyLev)
	if dec.Leverage != 3 {
		t.Fatalf("minLev==maxLev==3: expected leverage 3, got %.2f", dec.Leverage)
	}
}

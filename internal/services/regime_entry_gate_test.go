package services

import (
	"io"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/regime"
)

// TestRegimeEntryGate verifies the hard regime entry gate (default ON): entries in
// RANGING/UNKNOWN are forced to WAIT regardless of confidence, while TRENDING and
// BREAKOUT entries pass. This is the fix for confidently-wrong shorts that the
// confidence floor could not catch (losers entered at 0.85-0.95 in RANGING).
func TestRegimeEntryGate(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	bot := &AITradingBot{log: log}

	cases := []struct {
		regime     regime.Regime
		wantWait   bool
		desc       string
	}{
		{regime.RegimeRanging, true, "RANGING blocks even high-confidence entry"},
		{regime.RegimeUnknown, true, "UNKNOWN blocks entry"},
		{regime.RegimeTrendingUp, false, "TRENDING_UP allows entry"},
		{regime.RegimeTrendingDown, false, "TRENDING_DOWN allows entry"},
		{regime.RegimeBreakout, false, "BREAKOUT allows entry"},
	}
	for _, c := range cases {
		// High confidence (0.95) — the exact miscalibrated case the floor misses.
		d := &ai.Decision{Action: "SELL", Confidence: 0.95, Reason: "bearish setup"}
		ra := &regime.RegimeAnalysis{Regime: c.regime, ADX: 15, SuggestedSizeMultiplier: 1.0}
		bot.applyRegimeFilter(d, ra)
		gotWait := d.Action == "WAIT"
		if gotWait != c.wantWait {
			t.Errorf("%s: regime %s → Action=%q (wantWait=%v)", c.desc, c.regime, d.Action, c.wantWait)
		}
	}
}

// TestRegimeAllowedByMode locks in the gate-mode semantics: "trending" trades
// trends/breakouts (historical default), "ranging" trades RANGING/UNKNOWN (the
// mean-reversion edge per the 2026-06-03 regime-segmented backtest), and an
// unrecognised mode falls back to trending. The two modes must be disjoint over
// the tradable regimes so the gate can never allow both bases at once.
func TestRegimeAllowedByMode(t *testing.T) {
	cases := []struct {
		regime regime.Regime
		mode   string
		want   bool
	}{
		// trending mode (default base)
		{regime.RegimeTrendingUp, "trending", true},
		{regime.RegimeTrendingDown, "trending", true},
		{regime.RegimeBreakout, "trending", true},
		{regime.RegimeRanging, "trending", false},
		{regime.RegimeUnknown, "trending", false},
		// ranging mode (mean-reversion base) — inverted
		{regime.RegimeRanging, "ranging", true},
		{regime.RegimeUnknown, "ranging", true},
		{regime.RegimeTrendingUp, "ranging", false},
		{regime.RegimeTrendingDown, "ranging", false},
		{regime.RegimeBreakout, "ranging", false},
		// unrecognised mode falls back to trending
		{regime.RegimeTrendingUp, "", true},
		{regime.RegimeRanging, "bogus", false},
	}
	for _, c := range cases {
		if got := regimeAllowedByMode(c.regime, c.mode); got != c.want {
			t.Errorf("regimeAllowedByMode(%s, %q) = %v, want %v", c.regime, c.mode, got, c.want)
		}
	}
}

// TestRegimeEntryGate_LeavesWaitAlone confirms a WAIT decision stays WAIT in a
// blocked regime — the gate only converts active entries, never resurrects a
// non-entry into a trade.
func TestRegimeEntryGate_LeavesWaitAlone(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	bot := &AITradingBot{log: log}

	d := &ai.Decision{Action: "WAIT", Confidence: 0.0}
	ra := &regime.RegimeAnalysis{Regime: regime.RegimeRanging, ADX: 15, SuggestedSizeMultiplier: 1.0}
	bot.applyRegimeFilter(d, ra)
	if d.Action != "WAIT" {
		t.Errorf("WAIT must stay WAIT, got %q", d.Action)
	}
}

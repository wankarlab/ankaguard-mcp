// Package mode – Wave 3-A: regime-driven AUTO mode decision (A1 + A3).
//
// decideAutoMode is a pure, deterministic function with no I/O. It maps a
// MarketContext (regime label, ATR%, confidence) to a trading-mode Decision.
// It is only called when the manifest flag bot.auto_mode_intelligence == true;
// when the flag is off the caller returns the legacy pass-through unchanged.
package mode

import (
	"strings"
	"sync"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// MarketContext carries the signals needed by the regime-driven AUTO logic.
// All fields are optional in the sense that zero values produce safe defaults
// (unknown regime → legacy pass-through).
type MarketContext struct {
	// Regime is the string label from the regime detector, e.g. "TRENDING_UP",
	// "RANGING", "CHAOS". Case-insensitive matching is applied internally.
	Regime string

	// ATRPct is ATR expressed as a fraction of price (e.g. 0.03 = 3 %).
	// Zero means unknown/not available.
	ATRPct float64

	// Confidence is the overall trading-decision confidence in [0, 1].
	// Zero means unknown.
	Confidence float64
}

// defaultChaosATRThreshold is the ATR/price fraction above which a market is
// considered too volatile for leveraged futures regardless of regime label.
// It mirrors the manifest default of regime.atr.chaos_threshold (0.05 = 5 %).
const defaultChaosATRThreshold = 0.05

// chaosATRThreshold is the live value used by decideAutoMode. It starts at the
// manifest default so behaviour is byte-identical until the dynamic config wires
// in an override via SetChaosATRThreshold. Previously this was a hardcoded const
// that silently shadowed the dynamic regime.atr.chaos_threshold key (audit P1).
//
// Wired (audit-p1): applyGlobalScalarConfig in cmd/trading-mcp-server/main.go
// calls SetChaosATRThreshold from regime.atr.chaos_threshold at startup and on
// every hot-reload. Absent a DB override it equals the manifest default.
var (
	chaosATRMu        sync.RWMutex
	chaosATRThreshold = defaultChaosATRThreshold
)

// SetChaosATRThreshold updates the chaos ATR/price threshold used by the
// regime-driven AUTO leverage logic. Values <= 0 are ignored (keep the current
// value) so a missing/zero dynamic config never disables the chaos guard.
func SetChaosATRThreshold(v float64) {
	if v <= 0 {
		return
	}
	chaosATRMu.Lock()
	chaosATRThreshold = v
	chaosATRMu.Unlock()
}

// getChaosATRThreshold returns the current chaos ATR/price threshold.
func getChaosATRThreshold() float64 {
	chaosATRMu.RLock()
	defer chaosATRMu.RUnlock()
	return chaosATRThreshold
}

// decideAutoMode maps a MarketContext to a mode Decision using the regime-driven
// logic introduced in Wave 3-A (ideas A1 + A3).
//
// Regime → mode/leverage mapping:
//
//	TRENDING_UP / TRENDING_DOWN  →  futures, leverage scaled by confidence
//	                                 and inverse-volatility toward maxLev.
//	BREAKOUT                     →  futures, leverage at midpoint (conservative
//	                                 entry; direction not yet confirmed).
//	RANGING                      →  spot, leverage 1 (mean-reversion; no
//	                                 liquidation risk in choppy markets).
//	CHAOS / high ATR (>5 %)      →  spot, leverage 1 (capital preservation).
//	UNKNOWN / empty              →  legacy pass-through (MarketType="",
//	                                 Leverage=legacyLeverage).
//
// Leverage for futures regimes is computed as:
//
//	raw = minLev + (maxLev-minLev) * confidenceFactor * (1 - volatilityFactor)
//
// where:
//   - confidenceFactor = clamp(confidence, 0, 1)
//   - volatilityFactor = clamp(ATRPct / chaosATRThreshold, 0, 1)
//
// The result is clamped to [minLev, maxLev] and rounded to the nearest integer.
// Spot decisions always return Leverage=1.
func decideAutoMode(mc MarketContext, minLev, maxLev int, legacyLeverage float64) Decision {
	regime := strings.ToUpper(strings.TrimSpace(mc.Regime))

	// Unknown/empty regime → legacy pass-through so the bot is never blocked
	// when regime data is unavailable.
	if regime == "" || regime == "UNKNOWN" {
		return Decision{Leverage: legacyLeverage}
	}

	// High ATR always overrides to spot regardless of regime label.
	if mc.ATRPct > getChaosATRThreshold() {
		return spotDecision()
	}

	switch regime {
	case "TRENDING_UP", "TRENDING_DOWN":
		lev := scaleLeverage(mc.Confidence, mc.ATRPct, minLev, maxLev)
		return Decision{MarketType: gct.MarketTypeFutures, Leverage: lev}

	case "BREAKOUT":
		// Breakout entries are promising but direction is less certain.
		// Use the conservative midpoint of the leverage range.
		mid := float64(minLev+maxLev) / 2.0
		lev := clampFloat(mid, float64(minLev), float64(maxLev))
		return Decision{MarketType: gct.MarketTypeFutures, Leverage: lev}

	case "RANGING":
		// Mean-reversion in a range: spot avoids funding fees and liquidation risk.
		return spotDecision()

	case "CHAOS":
		// Extreme volatility: capital preservation, no leverage.
		return spotDecision()

	default:
		// Any unrecognised label (future extensions) → legacy pass-through.
		return Decision{Leverage: legacyLeverage}
	}
}

// scaleLeverage computes the adaptive leverage for trending regimes.
//
//	confidenceFactor = clamp(confidence, 0, 1)
//	volatilityFactor = clamp(ATRPct / chaosATRThreshold, 0, 1)
//	raw = minLev + (maxLev - minLev) * confidenceFactor * (1 - volatilityFactor)
//
// The result is clamped to [minLev, maxLev].
func scaleLeverage(confidence, atrPct float64, minLev, maxLev int) float64 {
	if minLev >= maxLev {
		return float64(minLev)
	}
	confFactor := clampFloat(confidence, 0, 1)
	volFactor := clampFloat(atrPct/getChaosATRThreshold(), 0, 1)
	raw := float64(minLev) + float64(maxLev-minLev)*confFactor*(1-volFactor)
	return clampFloat(raw, float64(minLev), float64(maxLev))
}

// spotDecision returns a spot Decision with leverage 1.
func spotDecision() Decision {
	return Decision{MarketType: gct.MarketTypeSpot, Leverage: 1}
}

// clampFloat clamps v to [lo, hi].
func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

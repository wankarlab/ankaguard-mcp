// Package handlers — assisted parametrization for the xsec market-neutral
// basket (F4.10).
//
// GET /api/v1/xsec/suggest-params?capital=4000&leverage=3&utilization=0.8
//
// Given a capital amount the operator intends to trade, this returns the best
// feasible basket configuration instead of forcing them to hand-tune k / per-
// leg size. The recommendation is grounded, not invented:
//
//   - The base config is the OOS-selected robust one (lb14 k12 rebal5 band0,
//     vol-weights on — see project memory 2026-07-25 "robust config"); k=12 is
//     the research target, everything else is fixed by that study.
//   - k is capped by the LIVE eligible universe: BuildBaskets needs 2*k
//     scoreable pairs, so k can be at most floor(universe/2). The universe is
//     the same turnover-filtered set cmd/xsec-live trades (LiquidPairs at the
//     DB's xsec.min_turnover_usd floor). ~18-20 live pairs is why k=12 is
//     usually infeasible and the basket runs smaller.
//   - Per-leg notional = gross / (2*k) where gross = capital*leverage*util.
//     If that falls below a floor (contract rounding + the 2x oversize guard
//     make sub-$10 legs unreliable), k is reduced further so each leg is
//     tradeable — protecting micro accounts from silently-dropped legs.
//
// NOTE ON LEVERAGE: cmd/xsec-live ALWAYS runs at 1x (fixedLeverage = 1), so
// capital*leverage sizing with leverage > 1 is only meaningful for backtests;
// it is NOT executable live (per-leg notional would exceed the real margin).
// Such requests are flagged with the "live_runs_1x" warning. For a sizing that
// is actually deployable use the autoconfig job (cmd/xsec-live -autoconfig),
// which searches k against the real fill at 1x and real equity.
//
// Read-only, no auth, no money at risk — same class as GetStatus. The live
// universe fetch is best-effort: on failure the recommendation still returns,
// flagged, using the research target k without the universe cap.
package handlers

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/xsec"
)

// Research-validated defaults. See package memory project_xsec_robust_config_
// lb14k12r5_2026_07_25: this config was selected by min(Sharpe IS, OOS) over a
// 36-config sweep on the production universe and was the only PROMISING one.
const (
	suggestTargetK   = 12   // research target; clamped down by universe/min-leg
	suggestLookback  = 14   // momentum lookback (days)
	suggestRebal     = 5    // rebalance cadence (bars); daily doubles drawdown
	suggestBand      = 0    // no hysteresis band (band>0 was strictly worse OOS)
	suggestMinLegUSD = 10.0 // per-leg floor: below this, contract rounding +
	// the 2x oversize guard make legs unreliable (the live micro-book runs
	// $2.50/leg and BTC/ETH/SOL legs get dropped for min-contract reasons).
	suggestDefaultLeverage    = 3.0
	suggestDefaultUtilization = 0.80 // keep 20% margin buffer vs liquidation
)

// SuggestParamsResponse is the assisted-parametrization recommendation. All
// human-facing rationale is expressed as stable warning CODES (translated by
// the frontend) plus the raw numbers, so the API stays i18n-agnostic.
type SuggestParamsResponse struct {
	// Echo of the (normalized) inputs.
	Capital        float64 `json:"capital"`
	Leverage       float64 `json:"leverage"`
	Utilization    float64 `json:"utilization"`
	UniverseSize   int     `json:"universe_size"` // -1 when the live fetch failed
	MinTurnoverUSD float64 `json:"min_turnover_usd"`

	// Recommended backtest / basket config.
	K          int  `json:"k"`
	Lookback   int  `json:"lookback"`
	Rebal      int  `json:"rebal"`
	Band       int  `json:"band"`
	VolWeights bool `json:"vol_weights"`

	// Recommended LIVE sizing (feeds max-pos-usd / max-total-usd on deploy).
	Legs          int     `json:"legs"` // 2*k
	PerLegUSD     float64 `json:"per_leg_usd"`
	MaxPosUSD     float64 `json:"max_pos_usd"`
	MaxTotalUSD   float64 `json:"max_total_usd"`
	GrossExposure float64 `json:"gross_exposure"`
	MarginUsed    float64 `json:"margin_used"`

	Feasible bool     `json:"feasible"`
	Warnings []string `json:"warnings"` // stable codes; see the switch in the UI
}

// computeSuggestedParams is the pure recommendation logic — no network, no DB,
// fully unit-testable. universeSize < 0 means "unknown" (live fetch failed).
func computeSuggestedParams(capital, leverage, utilization float64, universeSize int, minTurnover float64) SuggestParamsResponse {
	warnings := make([]string, 0, 4)

	if leverage <= 0 {
		leverage = suggestDefaultLeverage
	}
	if utilization <= 0 || utilization > 1 {
		utilization = suggestDefaultUtilization
	}
	if leverage > 5 {
		warnings = append(warnings, "high_leverage")
	}
	if leverage > 1 {
		// Live always runs 1x: this sizing is backtest-only, not executable live.
		warnings = append(warnings, "live_runs_1x")
	}

	gross := capital * leverage * utilization

	// Start from the research target, then cap by the live universe.
	k := suggestTargetK
	if universeSize > 0 {
		kByUniverse := universeSize / 2
		if kByUniverse < k {
			k = kByUniverse
			warnings = append(warnings, "k_capped_by_universe")
		}
	} else {
		warnings = append(warnings, "universe_unavailable")
	}
	kAfterUniverse := k

	feasible := true
	if k < 1 {
		// Universe too thin to run even a 1-per-side basket.
		k = 1
		feasible = false
	}

	// Reduce k until each leg clears the min-notional floor.
	perLeg := gross / float64(2*k)
	for perLeg < suggestMinLegUSD && k > 1 {
		k--
		perLeg = gross / float64(2*k)
	}
	if k < kAfterUniverse {
		warnings = append(warnings, "k_reduced_for_min_leg")
	}
	if perLeg < suggestMinLegUSD {
		// Even a single leg per side is below the floor: capital too small.
		warnings = append(warnings, "capital_below_min")
		feasible = false
	}

	perLeg = round2(perLeg)
	// 2% headroom so validatePlan's "sum of opens <= max-total" check (which
	// prices legs at market, not exactly perLeg) doesn't reject the deploy.
	maxTotal := round2(perLeg * float64(2*k) * 1.02)

	return SuggestParamsResponse{
		Capital:        round2(capital),
		Leverage:       leverage,
		Utilization:    utilization,
		UniverseSize:   universeSize,
		MinTurnoverUSD: minTurnover,
		K:              k,
		Lookback:       suggestLookback,
		Rebal:          suggestRebal,
		Band:           suggestBand,
		VolWeights:     true,
		Legs:           2 * k,
		PerLegUSD:      perLeg,
		MaxPosUSD:      perLeg,
		MaxTotalUSD:    maxTotal,
		GrossExposure:  round2(gross),
		MarginUsed:     round2(gross / leverage),
		Feasible:       feasible,
		Warnings:       warnings,
	}
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5*sign(v))) / 100
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// SuggestParams handles GET /api/v1/xsec/suggest-params.
func (h *XSecHandler) SuggestParams(c *gin.Context) {
	capital, err := floatQuery(c, "capital", 0)
	if err != nil || capital < 10 || capital > 10_000_000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "capital must be a number in [10, 10000000]"}})
		return
	}
	leverage, err := floatQuery(c, "leverage", suggestDefaultLeverage)
	if err != nil || leverage < 1 || leverage > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "leverage must be a number in [1, 20]"}})
		return
	}
	utilization, err := floatQuery(c, "utilization", suggestDefaultUtilization)
	if err != nil || utilization <= 0 || utilization > 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "utilization must be a number in (0, 1]"}})
		return
	}

	// Effective min-turnover floor: the DB param the live basket actually runs
	// with (falls back to the xsec-bot flag default of 3M when unset).
	minTurnover := 3_000_000.0
	if h.db != nil {
		if p, err := xsec.LoadParams(h.db); err == nil {
			if v, ok := p["xsec.min_turnover_usd"]; ok {
				if f, e := strconv.ParseFloat(v, 64); e == nil && f > 0 {
					minTurnover = f
				}
			}
		}
	}

	// Live eligible universe — best-effort (KuCoin contracts/active is flaky).
	universeSize := -1
	if n, err := cachedUniverseSize(c.Request.Context(), minTurnover); err != nil {
		h.log.WithError(err).Warn("xsec suggest: live universe fetch failed; recommending without universe cap")
	} else {
		universeSize = n
	}

	c.JSON(http.StatusOK, computeSuggestedParams(capital, leverage, utilization, universeSize, minTurnover))
}

// floatQuery parses a query param as float64, returning def when absent.
func floatQuery(c *gin.Context, key string, def float64) (float64, error) {
	raw := c.Query(key)
	if raw == "" {
		return def, nil
	}
	return strconv.ParseFloat(raw, 64)
}

// suggestUniverseTTL bounds how often the public suggest-params route hits
// KuCoin contracts/active: the route is anonymous, so without a cache every
// request (or a loop of them) would trigger an outbound call.
const suggestUniverseTTL = 60 * time.Second

var suggestUniverseCache struct {
	mu          sync.Mutex
	minTurnover float64
	size        int
	fetchedAt   time.Time
}

// cachedUniverseSize returns the live liquid-universe size for minTurnover,
// reusing a successful fetch for suggestUniverseTTL. The mutex is held during
// the fetch so concurrent requests share one outbound call. Failures are not
// cached.
func cachedUniverseSize(parent context.Context, minTurnover float64) (int, error) {
	cache := &suggestUniverseCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.fetchedAt.IsZero() && cache.minTurnover == minTurnover && time.Since(cache.fetchedAt) < suggestUniverseTTL {
		return cache.size, nil
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	pairs, err := xsec.NewKuCoinUniverse(8*time.Second).LiquidPairs(ctx, minTurnover)
	if err != nil {
		return 0, err
	}
	cache.minTurnover, cache.size, cache.fetchedAt = minTurnover, len(pairs), time.Now()
	return cache.size, nil
}

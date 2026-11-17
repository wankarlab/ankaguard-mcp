package optimization

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"

	configdomain "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	configrepo "github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// ──────────────────────────────────────────────────────────────────────────────
// Pre-defined param spaces
// ──────────────────────────────────────────────────────────────────────────────

// ScoringSpace covers the main scoring weights and ATR multipliers used by
// the decision engine's signal scoring step.
var ScoringSpace = ParamSpace{
	Params: []ParamRange{
		{Key: "scoring.reversal_candle_bonus", Type: configdomain.ParamTypeFloat, Min: "0.05", Max: "0.30"},
		{Key: "scoring.rsi.primary_bonus", Type: configdomain.ParamTypeFloat, Min: "0.05", Max: "0.25"},
		{Key: "scoring.support.strong_bonus", Type: configdomain.ParamTypeFloat, Min: "0.10", Max: "0.35"},
		{Key: "scoring.volume.confirmation_bonus", Type: configdomain.ParamTypeFloat, Min: "0.05", Max: "0.20"},
		{Key: "scoring.atr.sl_multiplier", Type: configdomain.ParamTypeFloat, Min: "1.0", Max: "3.0"},
		{Key: "scoring.atr.tp_multiplier", Type: configdomain.ParamTypeFloat, Min: "1.5", Max: "5.0"},
		{Key: "decision.confidence_threshold", Type: configdomain.ParamTypeFloat, Min: "0.55", Max: "0.85"},
	},
}

// TrapSpace covers the parameters controlling bear/bull trap detection.
var TrapSpace = ParamSpace{
	Params: []ParamRange{
		{Key: "trap.wick_ratio_threshold", Type: configdomain.ParamTypeFloat, Min: "0.3", Max: "0.8"},
		{Key: "trap.volume_spike_factor", Type: configdomain.ParamTypeFloat, Min: "1.2", Max: "3.0"},
		{Key: "trap.confirmation_candles", Type: configdomain.ParamTypeInt, Min: "1", Max: "5"},
		{Key: "trap.penalty_weight", Type: configdomain.ParamTypeFloat, Min: "0.05", Max: "0.30"},
		{Key: "trap.min_body_ratio", Type: configdomain.ParamTypeFloat, Min: "0.1", Max: "0.5"},
	},
}

// IndicatorPeriodSpace covers RSI, MACD, and Bollinger Band period parameters.
var IndicatorPeriodSpace = ParamSpace{
	Params: []ParamRange{
		{Key: "indicators.rsi.period", Type: configdomain.ParamTypeInt, Min: "7", Max: "30"},
		{Key: "indicators.rsi.oversold", Type: configdomain.ParamTypeFloat, Min: "20.0", Max: "40.0"},
		{Key: "indicators.rsi.overbought", Type: configdomain.ParamTypeFloat, Min: "60.0", Max: "80.0"},
		{Key: "indicators.macd.fast_period", Type: configdomain.ParamTypeInt, Min: "5", Max: "20"},
		{Key: "indicators.macd.slow_period", Type: configdomain.ParamTypeInt, Min: "15", Max: "50"},
		{Key: "indicators.macd.signal_period", Type: configdomain.ParamTypeInt, Min: "5", Max: "15"},
		{Key: "indicators.bollinger.period", Type: configdomain.ParamTypeInt, Min: "10", Max: "30"},
		{Key: "indicators.bollinger.std_dev", Type: configdomain.ParamTypeFloat, Min: "1.5", Max: "3.0"},
	},
}

// RiskSpace covers position sizing and risk-per-trade parameters.
var RiskSpace = ParamSpace{
	Params: []ParamRange{
		{Key: "risk.max_position_size_pct", Type: configdomain.ParamTypeFloat, Min: "0.01", Max: "0.05"},
		{Key: "risk.max_daily_loss_pct", Type: configdomain.ParamTypeFloat, Min: "0.01", Max: "0.05"},
		{Key: "risk.risk_reward_ratio_min", Type: configdomain.ParamTypeFloat, Min: "1.0", Max: "3.0"},
		{Key: "risk.trailing_stop_pct", Type: configdomain.ParamTypeFloat, Min: "0.005", Max: "0.03"},
		{Key: "risk.max_open_positions", Type: configdomain.ParamTypeInt, Min: "1", Max: "5"},
	},
}

// SpacesByName maps string names to predefined spaces for API use.
var SpacesByName = map[string]ParamSpace{
	"scoring":          ScoringSpace,
	"trap":             TrapSpace,
	"indicator_period": IndicatorPeriodSpace,
	"risk":             RiskSpace,
}

// ──────────────────────────────────────────────────────────────────────────────
// Dynamic space construction
// ──────────────────────────────────────────────────────────────────────────────

// NewParamSpaceFromKeys builds a ParamSpace by looking up each key in the registry
// and using its min/max bounds as the range. Keys not found in the registry are skipped.
func NewParamSpaceFromKeys(ctx context.Context, keys []string, repo configrepo.ConfigRepository) (ParamSpace, error) {
	space := ParamSpace{Params: make([]ParamRange, 0, len(keys))}
	for _, key := range keys {
		reg, err := repo.GetRegistry(ctx, key)
		if err != nil {
			// Skip unknown keys rather than failing the whole space build.
			continue
		}
		pr := ParamRange{
			Key:  key,
			Type: reg.Type,
		}
		if reg.MinValue != nil {
			pr.Min = *reg.MinValue
		}
		if reg.MaxValue != nil {
			pr.Max = *reg.MaxValue
		}
		if len(reg.EnumValues) > 0 {
			pr.Categorical = reg.EnumValues
		}
		// Validate that we have usable bounds.
		if pr.Type != configdomain.ParamTypeString && pr.Type != configdomain.ParamTypeBool {
			if pr.Min == "" || pr.Max == "" {
				continue // cannot sample without bounds
			}
		}
		space.Params = append(space.Params, pr)
	}
	if len(space.Params) == 0 {
		return space, fmt.Errorf("no usable parameters found for keys: %v", keys)
	}
	return space, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Sampler
// ──────────────────────────────────────────────────────────────────────────────

// SampleUniform draws one uniformly random point from the given space.
// For continuous params: uniform in [min, max].
// For discrete int params: uniform integer in [min, max].
// For categorical params: uniform pick from the categories slice.
func SampleUniform(space ParamSpace) map[string]string {
	out := make(map[string]string, len(space.Params))
	for _, p := range space.Params {
		out[p.Key] = sampleOne(p)
	}
	return out
}

// sampleOne draws a single value for a ParamRange.
func sampleOne(p ParamRange) string {
	switch p.Type {
	case configdomain.ParamTypeFloat:
		minF, _ := strconv.ParseFloat(p.Min, 64)
		maxF, _ := strconv.ParseFloat(p.Max, 64)
		v := minF + rand.Float64()*(maxF-minF)
		return strconv.FormatFloat(v, 'f', 6, 64)

	case configdomain.ParamTypeInt:
		minI, _ := strconv.ParseInt(p.Min, 10, 64)
		maxI, _ := strconv.ParseInt(p.Max, 10, 64)
		if maxI <= minI {
			return p.Min
		}
		v := minI + rand.Int64N(maxI-minI+1)
		return strconv.FormatInt(v, 10)

	case configdomain.ParamTypeString:
		if len(p.Categorical) == 0 {
			return p.Min // fallback
		}
		return p.Categorical[rand.IntN(len(p.Categorical))]

	case configdomain.ParamTypeBool:
		if rand.IntN(2) == 0 {
			return "true"
		}
		return "false"

	default:
		// duration, json: return the min/default value; optimizer doesn't tune these.
		return p.Min
	}
}

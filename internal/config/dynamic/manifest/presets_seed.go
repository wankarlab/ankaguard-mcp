// Package manifest contains startup seeds for the dynamic configuration system.
package manifest

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// presetDef is an internal helper for declaring a preset at seed time.
type presetDef struct {
	name        string
	description string
	params      map[string]string
}

// builtinPresets lists the 5 built-in presets seeded on every startup.
// Empty params means "use registry defaults" (serves as a baseline / rollback target).
var builtinPresets = []presetDef{
	{
		name:        "default",
		description: "Registry defaults — all params inherit their manifest default values. Use as a rollback target to restore a clean baseline.",
		params:      map[string]string{},
	},
	{
		name:        "aggressive_scalping",
		description: "Fast RSI/MACD settings for high-frequency scalping, lower confidence threshold, relaxed liquidity filter, tight breakeven.",
		params: map[string]string{
			"indicators.rsi.period":          "9",
			"indicators.macd.fast_period":    "5",
			"indicators.macd.slow_period":    "13",
			"indicators.macd.signal_period":  "4",
			"decision.confidence_threshold":  "0.55",
			"trap.liquidity.vol_ratio_min":   "5",
			"breakeven.trigger_pct":          "0.3",
			"breakeven.buffer_pct":           "0.15",
		},
	},
	{
		name:        "conservative_macro",
		description: "Slower RSI, higher confidence gate, strict liquidity filter, single open position — suitable for ranging/uncertain macro environments.",
		params: map[string]string{
			"indicators.rsi.period":         "21",
			"decision.confidence_threshold": "0.75",
			"trap.liquidity.vol_ratio_min":  "1.5",
			"bot.max_open_positions":        "1",
		},
	},
	{
		name:        "ranging_optimized",
		description: "Tighter Bollinger (14/1.8), higher support bonus, and closer trailing stop — tuned for low-ADX ranging markets.",
		params: map[string]string{
			"indicators.bollinger.period":   "14",
			"indicators.bollinger.stddev":   "1.8",
			"scoring.support.strong_bonus":  "0.30",
			"trailing.distance_pct":         "0.6",
		},
	},
	{
		name:        "trending_optimized",
		description: "Higher MACD trend bonus, wider trailing distance, and increased ATR TP multiplier — tuned for trending markets.",
		params: map[string]string{
			"scoring.macd.trend_bonus":      "0.15",
			"scoring.support.strong_bonus":  "0.10",
			"trailing.distance_pct":         "0.6",
			"scoring.atr.tp_multiplier":     "4.0",
		},
	},
}

// SeedPresets ensures each built-in preset exists in the repository.
// It uses INSERT-IGNORE semantics: if a preset with the same name already
// exists the error is silently discarded so repeated startups are safe.
//
// Call this after manifest.Seed() and before dynamic.NewConfigStore().
func SeedPresets(ctx context.Context, repo repository.ConfigRepository, log *logrus.Logger) error {
	for _, def := range builtinPresets {
		rawJSON, err := json.Marshal(def.params)
		if err != nil {
			return err
		}

		preset := &domain.ParameterPreset{
			Name:        def.name,
			Description: def.description,
			ScopeType:   domain.ScopeGlobal,
			ScopeID:     "*",
			ParamsJSON:  json.RawMessage(rawJSON),
			CreatedBy:   "manifest",
			IsActive:    false,
		}

		if err := repo.CreatePreset(ctx, preset); err != nil {
			if isDuplicateErr(err) {
				if log != nil {
					log.WithField("preset", def.name).Debug("preset already exists, skipping seed")
				}
				continue
			}
			return err
		}

		if log != nil {
			log.WithField("preset", def.name).Debug("seeded preset")
		}
	}
	return nil
}

// isDuplicateErr returns true when err indicates a unique-constraint violation,
// covering both pgx ("duplicate key") and SQLite ("UNIQUE constraint") backends.
func isDuplicateErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "already exists")
}

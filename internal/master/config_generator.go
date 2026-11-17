package master

import (
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai/regime"
	"github.com/sirupsen/logrus"
)

// ConfigStorage abstracts the persistence backend used by ConfigGenerator to
// archive each GeneratedConfig produced. It is intentionally minimal so any
// storage layer (SQLite repo, in-memory mock, remote service, etc.) can plug
// in without dragging the storage package into this one.
type ConfigStorage interface {
	SaveGeneratedConfig(pair string, config *GeneratedConfig) error
	LoadLatestConfig(pair string) (*GeneratedConfig, error)
}

// ConfigGenerator produces a per-pair bot config tailored to the regime.
type ConfigGenerator struct {
	log             *logrus.Logger
	defaults        RiskConfig
	storage         ConfigStorage // optional; persistence is opt-in via EnableStorage
	overridesProvider func(pair string) map[string]any
}

// NewConfigGenerator constructs a generator with conservative defaults.
func NewConfigGenerator(log *logrus.Logger) *ConfigGenerator {
	if log == nil {
		log = logrus.New()
	}
	return &ConfigGenerator{
		log: log,
		defaults: RiskConfig{
			MaxPositionSizeUSD:      30.0,
			StopLossATRMultiplier:   1.2,
			TakeProfitATRMultiplier: 2.0,
			MaxLeverage:             3,
			UseTrailingStop:         false,
		},
	}
}

// SetOverridesProvider wires a per-pair override lookup. The function receives
// a pair string and returns the overrides map (may be nil if no overrides exist
// for that pair). Keys "leverage", "size_usd", "stop_loss_pct", and
// "take_profit_pct" are recognised; all values must be float64 (JSON numbers).
func (cg *ConfigGenerator) SetOverridesProvider(fn func(pair string) map[string]any) {
	cg.overridesProvider = fn
}

// EnableStorage connects a storage backend. Generated configs will be
// persisted asynchronously under the name "master:<pair>" in trading_profiles.
// Calling this with a nil storage clears any previously configured backend.
func (cg *ConfigGenerator) EnableStorage(storage ConfigStorage) {
	cg.storage = storage
}

// persistConfig is a fire-and-forget writer used by Generate. Failures are
// logged but never propagated — persistence must not block trading flow.
func (cg *ConfigGenerator) persistConfig(pair string, config *GeneratedConfig) {
	if cg.storage == nil || config == nil {
		return
	}
	if err := cg.storage.SaveGeneratedConfig(pair, config); err != nil {
		cg.log.WithError(err).WithField("pair", pair).Warn("master: failed to persist generated config")
	}
}

// Generate produces a GeneratedConfig for the given opportunity and regime.
// Returns nil for CHAOS regime — that pair is excluded.
func (cg *ConfigGenerator) Generate(opportunity *MarketOpportunity, analysis *regime.RegimeAnalysis) *GeneratedConfig {
	if opportunity == nil || analysis == nil {
		return nil
	}
	if analysis.Regime == regime.RegimeChaos {
		cg.log.WithField("pair", opportunity.Pair).Info("config_gen: chaos regime, skipping pair")
		return nil
	}

	cfg := &GeneratedConfig{
		Pair:        opportunity.Pair,
		Exchange:    opportunity.Exchange,
		GeneratedBy: "master-agent",
		GeneratedAt: time.Now(),
		Regime:      string(analysis.Regime),
		Timeframes:  []string{"1m", "5m"},
		IndicatorConfig: IndicatorConfig{
			RSIPeriod:  14,
			MACDFast:   12,
			MACDSlow:   26,
			MACDSignal: 9,
			ATRPeriod:  14,
			BBPeriod:   20,
			BBStdDev:   2.0,
		},
		LLMConfig: LLMConfig{
			ConsensusRequired:     true,
			MinConsensusProviders: 2,
			ConfidenceThreshold:   0.6,
		},
		FilterConfig: FilterConfig{
			MinVolume24hUSD:     1_000_000,
			BTCCorrelationCheck: true,
			SentimentCheck:      true,
			RegimeCheck:         true,
		},
		RiskConfig: cg.defaults,
	}

	switch analysis.Regime {
	case regime.RegimeTrendingUp, regime.RegimeTrendingDown:
		cfg.StrategyWeights = map[string]float64{
			"macd":      0.4,
			"momentum":  0.4,
			"rsi":       0.1,
			"bollinger": 0.1,
		}
		cfg.Timeframes = []string{"1m", "5m", "15m"}
		cfg.RiskConfig.StopLossATRMultiplier = 1.5
		cfg.RiskConfig.TakeProfitATRMultiplier = 3.0
		cfg.RiskConfig.UseTrailingStop = true
		cfg.Reasoning = fmt.Sprintf("trending regime (ADX=%.1f), wide SL/TP and trailing on", analysis.ADX)

	case regime.RegimeRanging:
		cfg.StrategyWeights = map[string]float64{
			"bollinger": 0.4,
			"rsi":       0.4,
			"macd":      0.1,
			"momentum":  0.1,
		}
		cfg.Timeframes = []string{"1m", "5m"}
		cfg.RiskConfig.StopLossATRMultiplier = 1.0
		cfg.RiskConfig.TakeProfitATRMultiplier = 1.5
		cfg.RiskConfig.UseTrailingStop = false
		cfg.Reasoning = "ranging regime, mean-reversion strategies, tight SL/TP"

	case regime.RegimeBreakout:
		cfg.StrategyWeights = map[string]float64{
			"momentum": 0.5,
			"macd":     0.3,
			"volume":   0.2,
		}
		cfg.Timeframes = []string{"1m", "5m", "15m"}
		cfg.RiskConfig.StopLossATRMultiplier = 0.8
		cfg.RiskConfig.TakeProfitATRMultiplier = 4.0
		cfg.RiskConfig.UseTrailingStop = true
		cfg.Reasoning = "breakout regime, tight SL but wide TP target with trailing"

	case regime.RegimeUnknown:
		cfg.StrategyWeights = map[string]float64{
			"rsi":       0.3,
			"macd":      0.3,
			"bollinger": 0.2,
			"momentum":  0.2,
		}
		cfg.RiskConfig.StopLossATRMultiplier = 1.2
		cfg.RiskConfig.TakeProfitATRMultiplier = 2.0
		cfg.RiskConfig.UseTrailingStop = false
		cfg.Reasoning = "ambiguous regime, conservative defaults"

	default:
		cfg.StrategyWeights = map[string]float64{
			"rsi":       0.3,
			"macd":      0.3,
			"bollinger": 0.2,
			"momentum":  0.2,
		}
		cfg.Reasoning = "fallback weights"
	}

	// Per-pair user overrides — applied as the final layer so they win over
	// any regime-based adjustment made above.
	if cg.overridesProvider != nil {
		ov := cg.overridesProvider(opportunity.Pair)
		if len(ov) > 0 {
			applyPairOverrides(cfg, ov, cg.log)
		}
	}

	cg.log.WithFields(logrus.Fields{
		"pair":   opportunity.Pair,
		"regime": cfg.Regime,
		"sl":     cfg.RiskConfig.StopLossATRMultiplier,
		"tp":     cfg.RiskConfig.TakeProfitATRMultiplier,
	}).Debug("config_gen: generated config")

	// Async persistence — opt-in via EnableStorage. Errors are logged inside.
	if cg.storage != nil {
		go cg.persistConfig(opportunity.Pair, cfg)
	}

	return cfg
}

// applyPairOverrides merges user-defined per-pair overrides onto cfg. Only
// keys that are present and typed as float64 (standard JSON unmarshal) are
// applied; absent or wrong-typed keys are silently skipped so callers never
// need to provide a full override set.
func applyPairOverrides(cfg *GeneratedConfig, ov map[string]any, log *logrus.Logger) {
	if cfg == nil || len(ov) == 0 {
		return
	}
	if v, ok := ov["leverage"]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			cfg.RiskConfig.MaxLeverage = int(f)
			log.WithFields(logrus.Fields{"pair": cfg.Pair, "leverage": int(f)}).Debug("config_gen: override leverage")
		}
	}
	if v, ok := ov["size_usd"]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			cfg.RiskConfig.MaxPositionSizeUSD = f
			log.WithFields(logrus.Fields{"pair": cfg.Pair, "size_usd": f}).Debug("config_gen: override size_usd")
		}
	}
	if v, ok := ov["stop_loss_pct"]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			cfg.RiskConfig.StopLossATRMultiplier = f
			log.WithFields(logrus.Fields{"pair": cfg.Pair, "stop_loss_pct": f}).Debug("config_gen: override stop_loss_pct")
		}
	}
	if v, ok := ov["take_profit_pct"]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			cfg.RiskConfig.TakeProfitATRMultiplier = f
			log.WithFields(logrus.Fields{"pair": cfg.Pair, "take_profit_pct": f}).Debug("config_gen: override take_profit_pct")
		}
	}
}

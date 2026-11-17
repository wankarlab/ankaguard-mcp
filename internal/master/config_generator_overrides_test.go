package master

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestApplyPairOverrides(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	newCfg := func() *GeneratedConfig {
		return &GeneratedConfig{
			Pair: "BTC-USDT",
			RiskConfig: RiskConfig{
				MaxLeverage:             3,
				MaxPositionSizeUSD:      30.0,
				StopLossATRMultiplier:   1.2,
				TakeProfitATRMultiplier: 2.0,
			},
		}
	}

	t.Run("all keys replaced", func(t *testing.T) {
		cfg := newCfg()
		ov := map[string]any{
			"leverage":       float64(10),
			"size_usd":       float64(5),
			"stop_loss_pct":  float64(0.8),
			"take_profit_pct": float64(3.5),
		}
		applyPairOverrides(cfg, ov, log)

		if cfg.RiskConfig.MaxLeverage != 10 {
			t.Errorf("leverage: want 10 got %d", cfg.RiskConfig.MaxLeverage)
		}
		if cfg.RiskConfig.MaxPositionSizeUSD != 5.0 {
			t.Errorf("size_usd: want 5.0 got %f", cfg.RiskConfig.MaxPositionSizeUSD)
		}
		if cfg.RiskConfig.StopLossATRMultiplier != 0.8 {
			t.Errorf("stop_loss_pct: want 0.8 got %f", cfg.RiskConfig.StopLossATRMultiplier)
		}
		if cfg.RiskConfig.TakeProfitATRMultiplier != 3.5 {
			t.Errorf("take_profit_pct: want 3.5 got %f", cfg.RiskConfig.TakeProfitATRMultiplier)
		}
	})

	t.Run("absent keys leave defaults", func(t *testing.T) {
		cfg := newCfg()
		ov := map[string]any{
			"leverage": float64(7),
			// size_usd, stop_loss_pct, take_profit_pct absent
		}
		applyPairOverrides(cfg, ov, log)

		if cfg.RiskConfig.MaxLeverage != 7 {
			t.Errorf("leverage: want 7 got %d", cfg.RiskConfig.MaxLeverage)
		}
		if cfg.RiskConfig.MaxPositionSizeUSD != 30.0 {
			t.Errorf("size_usd should be default 30.0 got %f", cfg.RiskConfig.MaxPositionSizeUSD)
		}
		if cfg.RiskConfig.StopLossATRMultiplier != 1.2 {
			t.Errorf("sl should be default 1.2 got %f", cfg.RiskConfig.StopLossATRMultiplier)
		}
		if cfg.RiskConfig.TakeProfitATRMultiplier != 2.0 {
			t.Errorf("tp should be default 2.0 got %f", cfg.RiskConfig.TakeProfitATRMultiplier)
		}
	})

	t.Run("wrong-typed values ignored", func(t *testing.T) {
		cfg := newCfg()
		ov := map[string]any{
			"leverage":        "not-a-number",
			"size_usd":        true,
			"stop_loss_pct":   nil,
			"take_profit_pct": 42, // int, not float64
		}
		applyPairOverrides(cfg, ov, log)

		if cfg.RiskConfig.MaxLeverage != 3 {
			t.Errorf("leverage should stay 3, got %d", cfg.RiskConfig.MaxLeverage)
		}
		if cfg.RiskConfig.MaxPositionSizeUSD != 30.0 {
			t.Errorf("size_usd should stay 30.0, got %f", cfg.RiskConfig.MaxPositionSizeUSD)
		}
		if cfg.RiskConfig.StopLossATRMultiplier != 1.2 {
			t.Errorf("sl should stay 1.2, got %f", cfg.RiskConfig.StopLossATRMultiplier)
		}
		if cfg.RiskConfig.TakeProfitATRMultiplier != 2.0 {
			t.Errorf("tp should stay 2.0, got %f", cfg.RiskConfig.TakeProfitATRMultiplier)
		}
	})

	t.Run("empty overrides map is no-op", func(t *testing.T) {
		cfg := newCfg()
		applyPairOverrides(cfg, map[string]any{}, log)
		if cfg.RiskConfig.MaxLeverage != 3 {
			t.Errorf("leverage should stay 3, got %d", cfg.RiskConfig.MaxLeverage)
		}
	})

	t.Run("nil cfg is no-op", func(t *testing.T) {
		// Should not panic
		applyPairOverrides(nil, map[string]any{"leverage": float64(5)}, log)
	})
}

package adapters

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/master"
	"github.com/rantipay/trading-mcp/internal/scalping/manager"
)

// BotManagerSpawner adapts manager.BotManager to master.BotSpawner.
type BotManagerSpawner struct {
	log       *logrus.Logger
	mgr       *manager.BotManager
	paperMode bool
}

// NewBotManagerSpawner constructs a spawner adapter.
func NewBotManagerSpawner(log *logrus.Logger, mgr *manager.BotManager) *BotManagerSpawner {
	if log == nil {
		log = logrus.New()
	}
	return &BotManagerSpawner{log: log, mgr: mgr}
}

// SetPaperMode toggles paper mode for ALL bots spawned thereafter. When true,
// each spawned bot receives BotConfig.PaperMode=true and will simulate
// execution rather than placing real orders. Default is false (live trading).
func (b *BotManagerSpawner) SetPaperMode(enabled bool) {
	b.paperMode = enabled
	if b.log != nil {
		b.log.WithField("paper_mode", enabled).Info("master_adapter: paper mode toggled for new bots")
	}
}

// IsPaperMode returns whether the spawner is currently in paper mode.
// Only affects bots spawned after the most recent SetPaperMode call.
func (b *BotManagerSpawner) IsPaperMode() bool {
	return b.paperMode
}

// SpawnBot maps a master.GeneratedConfig into a scalping.BotConfig and starts a bot.
// Defaults applied where GeneratedConfig has no equivalent field.
func (b *BotManagerSpawner) SpawnBot(pair string, config *master.GeneratedConfig, capitalUSD float64) error {
	if b.mgr == nil {
		return fmt.Errorf("bot manager not initialized")
	}
	if config == nil {
		return fmt.Errorf("nil generated config for pair %s", pair)
	}

	exchange := config.Exchange
	if exchange == "" {
		exchange = "kucoin" // fallback for legacy configs
	}
	confidence := 0.7
	if config.LLMConfig.ConfidenceThreshold > 0 {
		confidence = config.LLMConfig.ConfidenceThreshold
	}

	// Translate ATR multipliers into rough percent levels for the scalping config.
	// TODO: replace with proper ATR-based stops once BotConfig accepts ATR multipliers directly.
	stopLossPct := 1.0
	takeProfitPct := 2.0
	if config.RiskConfig.StopLossATRMultiplier > 0 {
		stopLossPct = config.RiskConfig.StopLossATRMultiplier
	}
	if config.RiskConfig.TakeProfitATRMultiplier > 0 {
		takeProfitPct = config.RiskConfig.TakeProfitATRMultiplier
	}

	maxPositionUSD := capitalUSD
	if maxPositionUSD <= 0 {
		maxPositionUSD = config.RiskConfig.MaxPositionSizeUSD
	}
	if maxPositionUSD <= 0 {
		maxPositionUSD = 10.0
	}

	// Strategies are derived from non-zero StrategyWeights. Fallback to a generic mix.
	strategies := strategiesFromWeights(config.StrategyWeights)
	if len(strategies) == 0 {
		strategies = []string{"momentum", "mean_reversion"}
	}

	maxDailyTrades := 50
	if b.paperMode {
		maxDailyTrades = 500 // paper bots don't risk real capital; allow more trades per day
	}
	cfg := manager.BotConfig{
		Exchange:            exchange,
		Pairs:               []string{pair},
		MarketType:          "futures",
		Strategies:          strategies,
		MaxPositionSize:     maxPositionUSD,
		MaxDailyTrades:      maxDailyTrades,
		StopLoss:            stopLossPct,
		TakeProfit:          takeProfitPct,
		ConfidenceThreshold: confidence,
		PaperMode:           b.paperMode,
	}
	if config.RiskConfig.MaxLeverage > 0 {
		lev := config.RiskConfig.MaxLeverage
		cfg.Leverage = &lev
	}

	// Pass context.Background() — the bot manages its own lifecycle context internally.
	// A spawn-time timeout context would cancel the bot goroutine when SpawnBot returns.
	botID, err := b.mgr.StartBot(context.Background(), cfg)
	if err != nil {
		b.log.WithError(err).WithField("pair", pair).Warn("master_adapter: spawn bot failed")
		return err
	}

	b.log.WithFields(logrus.Fields{
		"pair":        pair,
		"bot_id":      botID,
		"capital_usd": maxPositionUSD,
		"regime":      config.Regime,
	}).Info("master_adapter: bot spawned")
	return nil
}

// SetBotPaperMode toggles paper mode on a specific running bot.
// The pair may be a trading pair string (e.g. "SUI-USDT") or a bot UUID.
// Returns an error if no matching bot is found.
func (b *BotManagerSpawner) SetBotPaperMode(pair string, enabled bool) error {
	if b.mgr == nil {
		return fmt.Errorf("bot manager not initialized")
	}
	return b.mgr.SetBotPaperMode(pair, enabled)
}

// KillBot delegates to BotManager.KillBot which resolves by pair or bot ID.
func (b *BotManagerSpawner) KillBot(pair string, reason string) error {
	if b.mgr == nil {
		return fmt.Errorf("bot manager not initialized")
	}
	return b.mgr.KillBot(pair, reason)
}

// ListActiveBots converts BotManager's active bots into master.ActiveBotInfo entries.
// One entry is emitted per pair the bot is configured for (multi-pair bots become several).
func (b *BotManagerSpawner) ListActiveBots() []master.ActiveBotInfo {
	if b.mgr == nil {
		return nil
	}
	bots := b.mgr.ListActiveBots()
	out := make([]master.ActiveBotInfo, 0, len(bots))
	for _, info := range bots {
		if info == nil {
			continue
		}
		pairs := info.Pairs
		if len(pairs) == 0 && info.Pair != "" {
			pairs = []string{info.Pair}
		}
		for _, p := range pairs {
			out = append(out, master.ActiveBotInfo{
				Pair:        p,
				StartedAt:   info.StartedAt,
				Status:      info.Status,
				TradesToday: info.TradesToday,
			})
		}
	}
	return out
}

// ListRawBots returns the underlying BotManager bot summaries (for handler /api/v1/bots).
func (b *BotManagerSpawner) ListRawBots() []*manager.BotInfo {
	if b.mgr == nil {
		return nil
	}
	return b.mgr.ListActiveBots()
}

// strategiesFromWeights returns the keys of weights with positive value.
func strategiesFromWeights(weights map[string]float64) []string {
	if len(weights) == 0 {
		return nil
	}
	out := make([]string, 0, len(weights))
	for name, w := range weights {
		if w > 0 {
			out = append(out, name)
		}
	}
	return out
}

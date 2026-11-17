package manager

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	ai "github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/learning"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/ai/regime"
	"github.com/rantipay/trading-mcp/internal/ai/sentiment"
	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/coordinator"
	"github.com/rantipay/trading-mcp/internal/scalping/executor"
	"github.com/rantipay/trading-mcp/internal/scalping/models"
	"github.com/rantipay/trading-mcp/internal/scalping/risk"
	"github.com/rantipay/trading-mcp/internal/scalping/strategies"
	"github.com/rantipay/trading-mcp/internal/storage"
	gctorder "github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// BotManager manages multiple scalping bot instances.
type BotManager struct {
	mu             sync.RWMutex
	logger         *logrus.Logger
	gctClient      gct.GCTClient
	orchestrator   *strategies.StrategyOrchestrator
	riskManager    *risk.AdaptiveRiskManager
	executor       *executor.HighFrequencyExecutor
	bots           map[string]*BotInstance
	maxBots        int
	lifecycleHooks []LifecycleHook
	dynamicExit    *DynamicExitEngine
	pairCoord      *coordinator.PairCoordinator
	configStore    *dynamic.ConfigStore
	regimeDetector *regime.Detector
	fearGreed      *sentiment.FearGreedClient
	cryptoPanic    *sentiment.CryptoPanicClient
	tradeHistory   *storage.TradeHistoryRepository

	// warnedMissingKeys throttles the "config key missing → using hardcoded
	// default" WARN logs so a missing dynamic key is visible exactly once per
	// key instead of spamming every tick (audit P1: silent-fallback visibility).
	warnedMissingKeys sync.Map // key string → struct{}
}

// warnMissingConfigKeyOnce logs a one-time WARN naming a dynamic config key that
// could not be resolved (no config store wired, or no snapshot/override), so the
// bot fell back to a hardcoded default. Subsequent calls for the same key are
// suppressed. This makes missing config observable (like the funding-stub gap)
// without flooding the logs.
func (bm *BotManager) warnMissingConfigKeyOnce(key string, fallback interface{}) {
	if _, loaded := bm.warnedMissingKeys.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	bm.logger.WithFields(logrus.Fields{
		"key":      key,
		"fallback": fallback,
	}).Warn("config: dynamic key not resolved — using hardcoded default (set it in parameter_values to override)")
}

// NewBotManager creates a new bot manager with default configuration.
func NewBotManager(
	logger *logrus.Logger,
	gctClient gct.GCTClient,
	orchestrator *strategies.StrategyOrchestrator,
	riskManager *risk.AdaptiveRiskManager,
	exec *executor.HighFrequencyExecutor,
) *BotManager {
	return &BotManager{
		logger:         logger,
		gctClient:      gctClient,
		orchestrator:   orchestrator,
		riskManager:    riskManager,
		executor:       exec,
		bots:           make(map[string]*BotInstance),
		maxBots:        5,
		lifecycleHooks: make([]LifecycleHook, 0),
	}
}

// SetPairCoordinator attaches a cross-component pair coordinator.
// When set, StartBot rejects any pair already claimed by another actor (e.g. AITradingBot).
func (bm *BotManager) SetPairCoordinator(c *coordinator.PairCoordinator) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.pairCoord = c
}

// SetConfigStore wires the dynamic config store so the bot reads live parameters
// (regime filter thresholds, volatility sizing, order book filters, etc.).
func (bm *BotManager) SetConfigStore(s *dynamic.ConfigStore) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.configStore = s
}

// SetRegimeDetector wires the market regime classifier used to block entries
// in unfavorable regimes (RANGING, CHAOS) for scalping bots.
func (bm *BotManager) SetRegimeDetector(d *regime.Detector) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.regimeDetector = d
}

// SetFearGreedClient wires the Fear & Greed client for sentiment-aware entry filtering.
func (bm *BotManager) SetFearGreedClient(fg *sentiment.FearGreedClient) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.fearGreed = fg
}

// SetCryptoPanicClient wires the CryptoPanic client for news-aware entry filtering.
func (bm *BotManager) SetCryptoPanicClient(cp *sentiment.CryptoPanicClient) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.cryptoPanic = cp
}

// SetTradeHistoryRepository wires the SQLite repository so closed scalping trades
// are persisted for analysis and FeedbackLoop warm-start on next restart.
func (bm *BotManager) SetTradeHistoryRepository(repo *storage.TradeHistoryRepository) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.tradeHistory = repo
}

// NewBotManagerWithConfig creates a bot manager honoring the provided BotManagerConfig.
func NewBotManagerWithConfig(
	logger *logrus.Logger,
	gctClient gct.GCTClient,
	orchestrator *strategies.StrategyOrchestrator,
	riskManager *risk.AdaptiveRiskManager,
	exec *executor.HighFrequencyExecutor,
	cfg BotManagerConfig,
) *BotManager {
	bm := NewBotManager(logger, gctClient, orchestrator, riskManager, exec)
	if cfg.MaxBots > 0 {
		bm.maxBots = cfg.MaxBots
		logger.Infof("BotManager: maxBots overridden by config to %d", cfg.MaxBots)
	}
	liveCfg := DefaultDynamicExitConfig()
	liveCfg.AIEnabled = true
	liveCfg.AICheckInterval = 2 * time.Minute
	bm.dynamicExit = NewDynamicExitEngine(logger, nil, liveCfg)
	logger.Info("BotManager: dynamic exit engine enabled (trailing stop + partial TP + AI reversal detection)")
	return bm
}

// StartBot starts a new scalping bot.
func (bm *BotManager) StartBot(ctx context.Context, config BotConfig) (string, error) {
	bm.mu.Lock()

	runningCount := 0
	for _, b := range bm.bots {
		if b.Status == "running" || b.Status == "spawning" {
			runningCount++
		}
	}
	if runningCount >= bm.maxBots {
		currentMax := bm.maxBots
		bm.mu.Unlock()
		bm.logger.Warnf("StartBot rejected: max bots reached (%d/%d running, %d total in map)", runningCount, currentMax, len(bm.bots))
		bm.fireLifecycleEvent(LifecycleEvent{
			Type:      EventMaxBotsHit,
			Pair:      firstPair(config.Pairs),
			Timestamp: time.Now(),
			Reason:    "max bots reached",
			Metadata: map[string]interface{}{
				"max_bots":    currentMax,
				"active_bots": runningCount,
				"exchange":    config.Exchange,
				"pairs":       config.Pairs,
			},
		})
		return "", ErrMaxBotsReached
	}

	for _, reqPair := range config.Pairs {
		for _, existing := range bm.bots {
			if existing.Status != "running" && existing.Status != "spawning" {
				continue
			}
			for _, existingPair := range existing.Config.Pairs {
				if strings.EqualFold(existingPair, reqPair) {
					bm.mu.Unlock()
					bm.logger.Warnf("StartBot rejected: pair %s already active in bot %s", reqPair, existing.ID)
					return "", fmt.Errorf("pair %s already has an active bot (%s)", reqPair, existing.ID)
				}
			}
		}
	}

	// Check cross-component pair coordinator (e.g. AITradingBot may hold the pair).
	if bm.pairCoord != nil {
		for _, reqPair := range config.Pairs {
			if owner := bm.pairCoord.OwnedBy(reqPair); owner != "" {
				bm.mu.Unlock()
				bm.logger.Warnf("StartBot rejected: pair %s is already claimed by %s", reqPair, owner)
				return "", &coordinator.ErrPairConflict{Pair: reqPair, Owner: owner}
			}
		}
	}

	if err := bm.validateConfig(config); err != nil {
		bm.mu.Unlock()
		bm.fireLifecycleEvent(LifecycleEvent{
			Type:      EventBotFailed,
			Pair:      firstPair(config.Pairs),
			Timestamp: time.Now(),
			Reason:    fmt.Sprintf("invalid config: %v", err),
			Metadata: map[string]interface{}{
				"exchange": config.Exchange,
				"pairs":    config.Pairs,
			},
		})
		return "", err
	}

	botCtx, botCancel := context.WithCancel(context.Background())
	botID := uuid.New().String()
	bot := &BotInstance{
		ID:              botID,
		Config:          config,
		Status:          "running",
		StartTime:       time.Now(),
		StopChan:        make(chan struct{}),
		cancelCtx:       botCancel,
		TradesToday:     0,
		PnLToday:        0,
		TotalTrades:     0,
		WinningTrades:   0,
		LosingTrades:    0,
		ActivePositions: 0,
		LastSignals:     make([]Signal, 0, 5),
	}
	if config.PaperMode {
		bot.paperPositions = make(map[string]*PaperPosition)
	} else {
		bot.livePositionMirrors = make(map[string]*PaperPosition)
	}
	bot.pairCooldowns = make(map[string]time.Time)

	bm.bots[botID] = bot
	startedAt := bot.StartTime
	bm.mu.Unlock()

	// Claim pairs in coordinator now that the bot is registered.
	if bm.pairCoord != nil {
		for _, p := range config.Pairs {
			bm.pairCoord.TryClaim(p, "scalping:"+botID)
		}
	}

	go bm.runBot(botCtx, bot)

	bm.logger.Infof("Started bot %s for %s with %d pairs", botID, config.Exchange, len(config.Pairs))
	if config.PaperMode {
		bm.logger.Warnf("[PAPER] Bot %s running in PAPER mode — orders will be simulated, no real execution", botID)
	}

	bm.fireLifecycleEvent(LifecycleEvent{
		Type:      EventBotStarted,
		BotID:     botID,
		Pair:      firstPair(config.Pairs),
		Timestamp: startedAt,
		Reason:    "start requested",
		Metadata: map[string]interface{}{
			"exchange": config.Exchange,
			"pairs":    config.Pairs,
		},
	})

	return botID, nil
}

// StopBot stops a bot by ID.
func (bm *BotManager) StopBot(botID string, closePositions bool) error {
	return bm.stopBotInternal(botID, closePositions, EventBotStopped, "stop requested")
}

// KillBot stops a bot by pair or bot ID (called by external authorities like Master Agent).
func (bm *BotManager) KillBot(pair string, reason string) error {
	if reason == "" {
		reason = "killed by master agent"
	}

	botID := bm.resolveBotIDByPairOrID(pair)
	if botID == "" {
		return ErrBotNotFound
	}

	bm.logger.Warnf("KillBot requested for %s (resolved bot %s): %s", pair, botID, reason)
	return bm.stopBotInternal(botID, true, EventBotKilled, reason)
}

func (bm *BotManager) resolveBotIDByPairOrID(identifier string) string {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if _, ok := bm.bots[identifier]; ok {
		return identifier
	}

	for id, bot := range bm.bots {
		if bot.Status != "running" {
			continue
		}
		for _, p := range bot.Config.Pairs {
			if p == identifier {
				return id
			}
		}
	}
	return ""
}

func (bm *BotManager) stopBotInternal(botID string, closePositions bool, eventType LifecycleEventType, reason string) error {
	bm.mu.Lock()

	bot, exists := bm.bots[botID]
	if !exists {
		bm.mu.Unlock()
		return ErrBotNotFound
	}

	if bot.Status != "running" {
		bm.mu.Unlock()
		return ErrBotNotRunning
	}

	close(bot.StopChan)
	if bot.cancelCtx != nil {
		bot.cancelCtx()
	}
	bot.Status = "stopping"

	if closePositions && bot.ActivePositions > 0 {
		bm.logger.Infof("Closing %d positions for bot %s", bot.ActivePositions, botID)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for _, pair := range bot.Config.Pairs {
			if err := bm.closePositionForPair(ctx, bot.Config.Exchange, pair); err != nil {
				bm.logger.Warnf("Failed to close position for %s: %v", pair, err)
			} else {
				bot.ActivePositions--
				bm.logger.Infof("Closed position for %s", pair)
			}
		}
	}

	bot.Status = "stopped"
	totalTrades := bot.TotalTrades
	pnl := bot.PnLToday
	exchange := bot.Config.Exchange
	pairs := append([]string(nil), bot.Config.Pairs...)
	bm.mu.Unlock()

	bm.logger.Infof("Stopped bot %s (total trades: %d, PnL: %.2f)", botID, totalTrades, pnl)

	bm.fireLifecycleEvent(LifecycleEvent{
		Type:      eventType,
		BotID:     botID,
		Pair:      firstPair(pairs),
		Timestamp: time.Now(),
		Reason:    reason,
		Metadata: map[string]interface{}{
			"exchange":         exchange,
			"pairs":            pairs,
			"total_trades":     totalTrades,
			"pnl_today":        pnl,
			"closed_positions": closePositions,
		},
	})

	return nil
}

func (bm *BotManager) closePositionForPair(ctx context.Context, exchange, pair string) error {
	if err := bm.gctClient.CloseFuturesPosition(ctx, exchange, pair); err != nil {
		bm.logger.Debugf("Futures close failed for %s, trying spot sell: %v", pair, err)

		ticker, err := bm.gctClient.GetTicker(ctx, exchange, pair)
		if err != nil {
			return fmt.Errorf("failed to get ticker for closing: %w", err)
		}

		_, err = bm.gctClient.SubmitOrder(ctx, &gct.SubmitOrderRequest{
			Exchange: exchange,
			Pair:     pair,
			Side:     "sell",
			Type:     "market",
			Amount:   0,
			Price:    ticker.Last,
		})
		if err != nil {
			return fmt.Errorf("failed to submit close order: %w", err)
		}
	}

	return nil
}

// GetBotStatus returns a copy of the bot instance state.
func (bm *BotManager) GetBotStatus(botID string) (*BotInstance, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	bot, exists := bm.bots[botID]
	if !exists {
		return nil, ErrBotNotFound
	}

	return &BotInstance{
		ID:              bot.ID,
		Config:          bot.Config,
		Status:          bot.Status,
		StartTime:       bot.StartTime,
		TradesToday:     bot.TradesToday,
		PnLToday:        bot.PnLToday,
		TotalTrades:     bot.TotalTrades,
		WinningTrades:   bot.WinningTrades,
		ActivePositions: bot.ActivePositions,
		LastSignals:     append([]Signal(nil), bot.LastSignals...),
	}, nil
}

// GetAllBots returns copies of all bot instances.
func (bm *BotManager) GetAllBots() []*BotInstance {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	bots := make([]*BotInstance, 0, len(bm.bots))
	for _, bot := range bm.bots {
		bots = append(bots, &BotInstance{
			ID:              bot.ID,
			Config:          bot.Config,
			Status:          bot.Status,
			StartTime:       bot.StartTime,
			TradesToday:     bot.TradesToday,
			PnLToday:        bot.PnLToday,
			TotalTrades:     bot.TotalTrades,
			WinningTrades:   bot.WinningTrades,
			ActivePositions: bot.ActivePositions,
		})
	}

	return bots
}

// RemoveBot removes a stopped bot from the registry.
func (bm *BotManager) RemoveBot(botID string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bot, exists := bm.bots[botID]
	if !exists {
		return ErrBotNotFound
	}

	if bot.Status == "running" {
		return fmt.Errorf("cannot remove running bot, stop it first")
	}

	if bm.pairCoord != nil {
		for _, p := range bot.Config.Pairs {
			bm.pairCoord.Release(p, "scalping:"+botID)
		}
	}

	delete(bm.bots, botID)
	bm.logger.Infof("Removed bot %s from registry", botID)

	return nil
}

func (bm *BotManager) validateConfig(config BotConfig) error {
	if config.Exchange == "" {
		return ErrInvalidExchange
	}

	if len(config.Pairs) == 0 {
		return fmt.Errorf("at least one pair is required")
	}

	if len(config.Pairs) > 10 {
		return ErrTooManyPairs
	}

	if len(config.Strategies) == 0 {
		return fmt.Errorf("at least one strategy is required")
	}

	if config.MaxPositionSize <= 0 {
		return fmt.Errorf("max_position_size must be positive")
	}

	if config.MaxDailyTrades <= 0 {
		return fmt.Errorf("max_daily_trades must be positive")
	}

	return nil
}

func (bm *BotManager) runBot(ctx context.Context, bot *BotInstance) {
	tickInterval := 5 * time.Second
	if bot.Config.PaperMode {
		tickInterval = 60 * time.Second
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	bm.logger.Infof("Bot %s started, monitoring %d pairs (tick=%s)", bot.ID, len(bot.Config.Pairs), tickInterval)

	// Bootstrap mirrors for any positions already open on the exchange at startup.
	if !bot.Config.PaperMode {
		bm.bootstrapLivePositionMirrors(context.Background(), bot)
	}

	for {
		select {
		case <-bot.StopChan:
			bm.logger.Infof("Bot %s stopped", bot.ID)
			return
		case <-ctx.Done():
			bm.logger.Infof("Bot %s stopped (context done)", bot.ID)
			return
		case <-ticker.C:
			bm.processBotCycle(ctx, bot)
		}
	}
}

func (bm *BotManager) processBotCycle(ctx context.Context, bot *BotInstance) {
	if bot.TradesToday >= bot.Config.MaxDailyTrades {
		return
	}

	if bot.Config.PaperMode {
		bm.checkPaperPositionExits(ctx, bot)
	} else {
		bm.checkLivePositionExits(ctx, bot)
	}

	for _, pair := range bot.Config.Pairs {
		if bot.Config.PaperMode {
			bm.mu.RLock()
			_, hasOpen := bot.paperPositions[pair]
			bm.mu.RUnlock()
			if hasOpen {
				continue
			}
		}

		ticker, err := bm.gctClient.GetTicker(ctx, bot.Config.Exchange, pair)
		if err != nil {
			bm.logger.Warnf("Bot %s: failed to get ticker for %s: %v", bot.ID, pair, err)
			continue
		}

		candles, err := bm.gctClient.GetCandles(ctx, bot.Config.Exchange, pair, "60", 100)
		if err != nil {
			bm.logger.Warnf("Bot %s: failed to get candles for %s: %v", bot.ID, pair, err)
			candles = []*gct.Candle{
				{
					Timestamp: time.Now(),
					Open:      ticker.Last,
					High:      ticker.High,
					Low:       ticker.Low,
					Close:     ticker.Last,
					Volume:    ticker.Volume,
				},
			}
		}

		prices := make([]float64, len(candles))
		volumes := make([]float64, len(candles))
		for i, candle := range candles {
			prices[i] = candle.Close
			volumes[i] = candle.Volume
		}

		marketData := &strategies.MarketData{
			Pair:      pair,
			Exchange:  bot.Config.Exchange,
			Price:     ticker.Last,
			Volume:    ticker.Volume,
			Timestamp: time.Now(),
			Features: &models.Features{
				Prices:  prices,
				Volumes: volumes,
			},
		}

		bm.logger.Debugf("Bot %s: analyzing %s with %d candles", bot.ID, pair, len(candles))

		// ── Global entries kill-switch ────────────────────────────────────────
		snap := bm.scopedSnap(pair)
		if snap != nil {
			if enabled, ok := snap.BoolOk("scalping.bots.entries_enabled"); ok && !enabled {
				bm.logger.Debugf("Bot %s: entries disabled via scalping.bots.entries_enabled", bot.ID)
				continue
			}
		}

		// ── Regime filter ────────────────────────────────────────────────────
		if blocked, reason := bm.checkRegimeFilter(ctx, pair, candles); blocked {
			bm.logger.Debugf("Bot %s: %s regime filter — %s", bot.ID, pair, reason)
			continue
		}

		// ── Order book pre-entry check ────────────────────────────────────────
		if blocked, reason := bm.checkOrderBookFilter(ctx, bot.Config.Exchange, pair); blocked {
			bm.logger.Debugf("Bot %s: %s orderbook filter — %s", bot.ID, pair, reason)
			continue
		}

		// ── News sentiment filter (CryptoPanic) ──────────────────────────────
		if blocked, reason := bm.checkNewsFilter(ctx, pair); blocked {
			bm.logger.Debugf("Bot %s: %s news filter — %s", bot.ID, pair, reason)
			continue
		}

		signal, err := bm.orchestrator.AnalyzeMarket(ctx, pair, marketData)
		if err != nil {
			bm.logger.Debugf("Bot %s: orchestrator analysis failed for %s: %v", bot.ID, pair, err)
			signal = nil
		}

		confidenceThreshold := 0.7
		if bot.Config.PaperMode {
			confidenceThreshold = 0.5
		}
		if bot.Config.ConfidenceThreshold > 0 {
			confidenceThreshold = bot.Config.ConfidenceThreshold
		}

		if signal != nil && signal.Confidence >= confidenceThreshold {
			// ── Volatility-adjusted sizing ────────────────────────────────────
			signal = bm.applyVolatilitySizing(ctx, pair, candles, signal)

			bm.logger.Infof("Bot %s: signal detected for %s - %s (confidence: %.2f)",
				bot.ID, pair, signal.Direction, signal.Confidence)
			bm.executeSignal(ctx, bot, signal)
		}
	}
}

// checkRegimeFilter returns (true, reason) when the current market regime
// blocks entry for this pair. All thresholds are read from dynamic config.
func (bm *BotManager) checkRegimeFilter(ctx context.Context, pair string, candles []*gct.Candle) (bool, string) {
	if bm.regimeDetector == nil {
		return false, ""
	}

	snap := bm.scopedSnap(pair)
	if snap != nil {
		if enabled, ok := snap.BoolOk("scalping.regime.filter_enabled"); ok && !enabled {
			return false, ""
		}
	}

	candleLimit := 50
	if v, ok := intFromSnap(snap, "scalping.regime.candle_limit"); ok {
		candleLimit = v
	} else {
		bm.warnMissingConfigKeyOnce("scalping.regime.candle_limit", candleLimit)
	}
	if len(candles) < 5 {
		return false, ""
	}
	limit := candleLimit
	if limit > len(candles) {
		limit = len(candles)
	}

	aiCandles := make([]ai.Candle, limit)
	for i, c := range candles[len(candles)-limit:] {
		aiCandles[i] = ai.Candle{
			Timestamp: c.Timestamp,
			Open:      c.Open,
			High:      c.High,
			Low:       c.Low,
			Close:     c.Close,
			Volume:    c.Volume,
		}
	}

	analysis, err := bm.regimeDetector.Analyze(aiCandles)
	if err != nil || analysis == nil {
		return false, ""
	}

	// Build allowed regime set from dynamic config.
	allowedStr := "TRENDING_UP,TRENDING_DOWN,BREAKOUT"
	if snap != nil {
		if v := snap.String("scalping.regime.allowed_regimes"); v != "" {
			allowedStr = v
		}
	}
	allowed := make(map[string]bool)
	for _, r := range strings.Split(allowedStr, ",") {
		allowed[strings.TrimSpace(strings.ToUpper(r))] = true
	}

	current := strings.ToUpper(string(analysis.Regime))
	if !allowed[current] {
		return true, fmt.Sprintf("regime=%s not in allowed set (%s)", current, allowedStr)
	}

	// Optional minimum ADX gate.
	if snap != nil {
		if minADX, ok := snap.FloatOk("scalping.regime.min_adx"); ok && minADX > 0 {
			if analysis.ADX < minADX {
				return true, fmt.Sprintf("ADX=%.1f below min=%.1f", analysis.ADX, minADX)
			}
		}
	}

	// Fear & Greed extreme filter: block entries that conflict with extreme sentiment.
	// LONG in Extreme Fear (<=20) or SHORT in Extreme Greed (>=80) get filtered.
	// Disabled when scalping.sentiment.fg_filter_enabled=false in dynamic config.
	if bm.fearGreed != nil {
		fgEnabled := true
		if snap != nil {
			if v, ok := snap.BoolOk("scalping.sentiment.fg_filter_enabled"); ok {
				fgEnabled = v
			}
		}
		if fgEnabled {
			if idx, err := bm.fearGreed.Get(ctx); err == nil && idx != nil {
				// only block the contrarian side in extremes
				if idx.IsFearExtreme() && strings.EqualFold(string(analysis.Regime), "TRENDING_UP") {
					return true, fmt.Sprintf("fear_greed=%d (Extreme Fear) — blocking LONG bias", idx.Value)
				}
				if idx.IsGreedExtreme() && strings.EqualFold(string(analysis.Regime), "TRENDING_DOWN") {
					return true, fmt.Sprintf("fear_greed=%d (Extreme Greed) — blocking SHORT bias", idx.Value)
				}
			}
		}
	}

	return false, ""
}

// checkOrderBookFilter returns (true, reason) when spread or depth fails configured thresholds.
func (bm *BotManager) checkOrderBookFilter(ctx context.Context, exchange, pair string) (bool, string) {
	snap := bm.scopedSnap(pair)
	if snap != nil {
		if enabled, ok := snap.BoolOk("scalping.orderbook.filter_enabled"); ok && !enabled {
			return false, ""
		}
	}

	obCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	ob, err := bm.gctClient.GetOrderBookBySymbol(obCtx, exchange, pair, "futures")
	cancel()
	if err != nil {
		return false, "" // fail open — don't block on fetch error
	}
	if ob == nil || (len(ob.Bids) == 0 && len(ob.Asks) == 0) {
		return false, ""
	}

	var bestBid, bestAsk float64
	if len(ob.Bids) > 0 {
		bestBid = ob.Bids[0].Price
	}
	if len(ob.Asks) > 0 {
		bestAsk = ob.Asks[0].Price
	}
	if bestBid <= 0 || bestAsk <= 0 {
		return false, ""
	}

	mid := (bestBid + bestAsk) / 2
	spreadPct := (bestAsk - bestBid) / mid * 100

	// Resolve thresholds from the dynamic config; WARN once per key when the
	// snapshot/key is missing so a silent hardcoded fallback is observable.
	maxSpread := 0.3
	if v, ok := floatFromSnap(snap, "scalping.orderbook.max_spread_pct"); ok {
		maxSpread = v
	} else {
		bm.warnMissingConfigKeyOnce("scalping.orderbook.max_spread_pct", maxSpread)
	}
	if spreadPct > maxSpread {
		return true, fmt.Sprintf("spread=%.3f%% > max=%.3f%%", spreadPct, maxSpread)
	}

	minDepth := 5000.0
	if v, ok := floatFromSnap(snap, "scalping.orderbook.min_depth_usd"); ok {
		minDepth = v
	} else {
		bm.warnMissingConfigKeyOnce("scalping.orderbook.min_depth_usd", minDepth)
	}
	var depth float64
	top := 5
	if v, ok := intFromSnap(snap, "scalping.orderbook.depth_top_n"); ok {
		top = v
	} else {
		bm.warnMissingConfigKeyOnce("scalping.orderbook.depth_top_n", top)
	}
	for i, entry := range ob.Bids {
		if i >= top {
			break
		}
		depth += entry.Price * entry.Amount
	}
	for i, entry := range ob.Asks {
		if i >= top {
			break
		}
		depth += entry.Price * entry.Amount
	}
	if depth < minDepth {
		return true, fmt.Sprintf("depth=$%.0f < min=$%.0f", depth, minDepth)
	}

	return false, ""
}

// checkNewsFilter returns (true, reason) when CryptoPanic news sentiment is extreme
// in the direction opposite to the expected trade (very bearish news → block longs,
// very bullish news → block shorts). Returns (false, "") when the client is nil,
// the API call fails, or sentiment is within normal range.
func (bm *BotManager) checkNewsFilter(ctx context.Context, pair string) (bool, string) {
	if bm.cryptoPanic == nil {
		return false, ""
	}

	snap := bm.scopedSnap(pair)
	if snap != nil {
		if enabled, ok := snap.BoolOk("scalping.sentiment.news_filter_enabled"); ok && !enabled {
			return false, ""
		}
	}

	// Extract base currency from pair (e.g. "BTC-USDT" → "BTC", "HYPE-USDT" → "HYPE").
	symbol := pair
	if idx := strings.IndexAny(pair, "-/"); idx > 0 {
		symbol = pair[:idx]
	}

	bearishThreshold := -0.5
	bullishThreshold := 0.5
	if snap != nil {
		if v, ok := snap.FloatOk("scalping.sentiment.news_bearish_threshold"); ok {
			bearishThreshold = v
		}
		if v, ok := snap.FloatOk("scalping.sentiment.news_bullish_threshold"); ok {
			bullishThreshold = v
		}
	}

	articles, err := bm.cryptoPanic.GetNews(ctx, symbol, 4)
	if err != nil || len(articles) == 0 {
		return false, "" // fail open: no news → don't block
	}

	score := bm.cryptoPanic.GetSentimentScore(articles)

	if score < bearishThreshold {
		return true, fmt.Sprintf("news sentiment=%.2f (bearish) — blocking long bias", score)
	}
	if score > bullishThreshold {
		return true, fmt.Sprintf("news sentiment=%.2f (bullish) — blocking short bias", score)
	}
	return false, ""
}

// applyVolatilitySizing adjusts signal.Price field (used for sizing) by an ATR-based
// multiplier. The actual position size is clamped between min/max multipliers.
// Returns the signal unchanged if volatility sizing is disabled or ATR unavailable.
func (bm *BotManager) applyVolatilitySizing(ctx context.Context, pair string, candles []*gct.Candle, signal *models.ScalpingSignal) *models.ScalpingSignal {
	snap := bm.scopedSnap(pair)
	if snap != nil {
		if enabled, ok := snap.BoolOk("scalping.volatility.sizing_enabled"); ok && !enabled {
			return signal
		}
	}

	atrPeriod := 14
	if v, ok := intFromSnap(snap, "scalping.volatility.atr_period"); ok {
		atrPeriod = v
	} else {
		bm.warnMissingConfigKeyOnce("scalping.volatility.atr_period", atrPeriod)
	}
	if len(candles) < atrPeriod+1 {
		return signal
	}

	// Calculate ATR from the last atrPeriod+1 candles.
	var atrSum float64
	start := len(candles) - atrPeriod - 1
	for i := start + 1; i < len(candles); i++ {
		hl := candles[i].High - candles[i].Low
		hc := math.Abs(candles[i].High - candles[i-1].Close)
		lc := math.Abs(candles[i].Low - candles[i-1].Close)
		tr := math.Max(hl, math.Max(hc, lc))
		atrSum += tr
	}
	atr := atrSum / float64(atrPeriod)
	if atr <= 0 || signal.Price <= 0 {
		return signal
	}

	atrRatio := atr / signal.Price
	baseRatio := 0.015
	if v, ok := floatFromSnap(snap, "scalping.volatility.base_atr_ratio"); ok && v > 0 {
		baseRatio = v
	} else {
		bm.warnMissingConfigKeyOnce("scalping.volatility.base_atr_ratio", baseRatio)
	}

	// High ATR → reduce size (more volatile). Low ATR → increase size.
	mult := baseRatio / atrRatio

	minMult := 0.3
	maxMult := 2.0
	if snap != nil {
		if v, ok := snap.FloatOk("scalping.volatility.min_size_mult"); ok {
			minMult = v
		}
		if v, ok := snap.FloatOk("scalping.volatility.max_size_mult"); ok {
			maxMult = v
		}
	}
	mult = math.Max(minMult, math.Min(maxMult, mult))

	// Store multiplier in signal metadata so executor can apply it.
	if signal.Metadata == nil {
		signal.Metadata = make(map[string]interface{})
	}
	signal.Metadata["volatility_size_mult"] = mult
	signal.Metadata["atr_ratio"] = atrRatio

	bm.logger.Debugf("Bot: %s volatility sizing — ATR/price=%.4f mult=%.2fx", pair, atrRatio, mult)
	return signal
}

// scopedSnap returns a pair+global scoped snapshot from the config store, or nil.
func (bm *BotManager) scopedSnap(pair string) *dynamic.ScopedSnapshot {
	if bm.configStore == nil {
		return nil
	}
	return bm.configStore.SnapshotForScope(dynamic.BotScopeChain("", pair, ""))
}

// floatFromSnap is a nil-safe wrapper around snap.FloatOk: it returns (0,false)
// when the snapshot is nil (no config store wired) so callers can WARN-and-default
// without a nil deref.
func floatFromSnap(snap *dynamic.ScopedSnapshot, key string) (float64, bool) {
	if snap == nil {
		return 0, false
	}
	return snap.FloatOk(key)
}

// intFromSnap is the nil-safe int variant of floatFromSnap.
func intFromSnap(snap *dynamic.ScopedSnapshot, key string) (int, bool) {
	if snap == nil {
		return 0, false
	}
	return snap.IntOk(key)
}

func (bm *BotManager) executeSignal(ctx context.Context, bot *BotInstance, signal *models.ScalpingSignal) {
	// Check post-loss cooldown: don't re-enter a pair for 5 min after a stop-loss.
	bm.mu.RLock()
	cooldownUntil, onCooldown := bot.pairCooldowns[signal.Pair]
	bm.mu.RUnlock()
	if onCooldown && time.Now().Before(cooldownUntil) {
		bm.logger.Debugf("Bot %s: %s on cooldown until %s — skipping signal",
			bot.ID, signal.Pair, cooldownUntil.Format("15:04:05"))
		return
	}

	var positionSize float64

	if bot.Config.PaperMode {
		positionSize = bot.Config.MaxPositionSize
	} else {
		var err error
		positionSize, err = bm.riskManager.CalculatePositionSize(ctx, signal)
		if err != nil {
			bm.logger.Warnf("Bot %s: failed to calculate position size: %v", bot.ID, err)
			return
		}
		if positionSize > bot.Config.MaxPositionSize {
			positionSize = bot.Config.MaxPositionSize
		}
	}

	// Apply ATR-based volatility multiplier computed by applyVolatilitySizing.
	if mult, ok := signal.Metadata["volatility_size_mult"].(float64); ok && mult > 0 {
		adjusted := positionSize * mult
		bm.logger.WithFields(logrus.Fields{
			"bot_id":    bot.ID,
			"pair":      signal.Pair,
			"base_size": positionSize,
			"mult":      mult,
			"adjusted":  adjusted,
		}).Debug("volatility sizing applied")
		positionSize = adjusted
	}

	if !bot.Config.PaperMode {
		newSide := strings.ToLower(signal.Direction)
		if newSide == "long" {
			newSide = "buy"
		} else if newSide == "short" {
			newSide = "sell"
		}
		if exchangePositions, exchErr := bm.gctClient.GetFuturesPositions(ctx, bot.Config.Exchange); exchErr == nil {
			pairNormCheck := strings.ReplaceAll(strings.ReplaceAll(signal.Pair, "-", ""), "/", "")
			for _, ep := range exchangePositions {
				symNorm := strings.ToUpper(ep.Symbol)
				if !strings.HasPrefix(symNorm, strings.ToUpper(pairNormCheck)) {
					continue
				}
				epSide := strings.ToLower(ep.Side)

				if epSide == "unknown" {
					bm.logger.WithFields(logrus.Fields{
						"bot_id": bot.ID,
						"symbol": ep.Symbol,
					}).Warn("🚫 Unreadable position on exchange — skipping to avoid duplicate")
					return
				}

				sameDirection := (newSide == "sell" && (epSide == "short" || epSide == "sell")) ||
					(newSide == "buy" && (epSide == "long" || epSide == "buy"))

				if sameDirection {
					bm.logger.WithFields(logrus.Fields{
						"bot_id":        bot.ID,
						"existing_side": ep.Side,
						"existing_size": ep.Size,
						"new_signal":    newSide,
					}).Warn("🚫 Same-direction position already open on exchange — skipping to prevent pyramiding")
					return
				}

				bm.logger.WithFields(logrus.Fields{
					"bot_id":         bot.ID,
					"existing_side":  ep.Side,
					"existing_pnl":   ep.UnrealizedPnL,
					"existing_entry": ep.EntryPrice,
					"new_signal":     newSide,
				}).Info("🔄 Opposite-direction signal detected — evaluating PnL")

				if ep.UnrealizedPnL > 0 {
					bm.logger.WithFields(logrus.Fields{
						"bot_id": bot.ID,
						"symbol": ep.Symbol,
						"side":   ep.Side,
						"pnl":    fmt.Sprintf("$%.4f", ep.UnrealizedPnL),
					}).Info("💰 Existing position in profit — closing to take profit on contrarian signal")

					closeCtx, closeCancel := context.WithTimeout(ctx, 10*time.Second)
					closeErr := bm.gctClient.CloseFuturesPosition(closeCtx, bot.Config.Exchange, ep.Symbol)
					closeCancel()
					if closeErr != nil {
						bm.logger.WithError(closeErr).Errorf("Bot %s: failed to close opposite position", bot.ID)
						return
					}
					bm.logger.WithField("bot_id", bot.ID).Info("✅ Opposite position closed — next analysis cycle will decide entry")
					return
				}

				bm.logger.WithFields(logrus.Fields{
					"bot_id":        bot.ID,
					"existing_side": ep.Side,
					"existing_pnl":  ep.UnrealizedPnL,
					"new_signal":    newSide,
				}).Warn("🚫 Opposite signal but existing position in loss — letting SL/TP manage exit")
				return
			}
		}
	}

	var execution *executor.OrderExecution
	if bot.Config.PaperMode {
		execution = bm.simulatePaperExecution(bot, signal, positionSize)
	} else {
		var execErr error
		// SCALP-05 fix (a): ExecuteOrder always hardcoded leverage=1 even for
		// bots explicitly configured with higher leverage — the exchange-side
		// margin never matched what the bot's own sizing/risk math assumed.
		// Pass the bot's configured leverage through when set.
		leverage := 1.0
		if bot.Config.Leverage != nil && *bot.Config.Leverage > 1 {
			leverage = float64(*bot.Config.Leverage)
		}
		execution, execErr = bm.executor.ExecuteOrderWithLeverage(ctx, signal, positionSize, leverage)
		if execErr != nil {
			bm.logger.Errorf("Bot %s: failed to execute order: %v", bot.ID, execErr)
			return
		}
	}

	bm.mu.Lock()
	bot.TradesToday++
	bot.TotalTrades++

	if bot.Config.PaperMode {
		stopPct := bot.Config.StopLoss / 100.0
		tpPct := bot.Config.TakeProfit / 100.0
		if stopPct <= 0 {
			stopPct = 0.01
		}
		if tpPct <= 0 {
			tpPct = 0.02
		}
		var sl, tp float64
		if execution.Side == "buy" {
			sl = execution.Price * (1 - stopPct)
			tp = execution.Price * (1 + tpPct)
		} else {
			sl = execution.Price * (1 + stopPct)
			tp = execution.Price * (1 - tpPct)
		}
		bot.paperPositions[signal.Pair] = &PaperPosition{
			Pair:              signal.Pair,
			Direction:         execution.Side,
			EntryPrice:        execution.Price,
			Quantity:          execution.Amount,
			OpenedAt:          time.Now(),
			TakeProfit:        tp,
			StopLoss:          sl,
			OriginalTP:        tp,
			OriginalSL:        sl,
			MaxFavorablePrice: execution.Price,
		}
		bot.ActivePositions++
	} else {
		stopPct := bot.Config.StopLoss / 100.0
		tpPct := bot.Config.TakeProfit / 100.0
		if stopPct <= 0 {
			stopPct = 0.01
		}
		if tpPct <= 0 {
			tpPct = 0.02
		}
		var sl, tp float64
		if execution.Side == "buy" {
			sl = execution.Price * (1 - stopPct)
			tp = execution.Price * (1 + tpPct)
		} else {
			sl = execution.Price * (1 + stopPct)
			tp = execution.Price * (1 - tpPct)
		}
		if execution.Price > 0 {
			if bot.livePositionMirrors == nil {
				bot.livePositionMirrors = make(map[string]*PaperPosition)
			}
			bot.livePositionMirrors[signal.Pair] = &PaperPosition{
				Pair:              signal.Pair,
				Direction:         execution.Side,
				EntryPrice:        execution.Price,
				Quantity:          execution.Amount,
				OpenedAt:          time.Now(),
				TakeProfit:        tp,
				StopLoss:          sl,
				OriginalTP:        tp,
				OriginalSL:        sl,
				MaxFavorablePrice: execution.Price,
			}
		}
		bot.ActivePositions++
	}

	botSignal := Signal{
		Pair:       signal.Pair,
		Direction:  signal.Direction,
		Confidence: signal.Confidence,
		Strategy:   signal.Strategies[0],
		Price:      signal.Price,
		Reason:     fmt.Sprintf("Executed by bot %s", bot.ID),
		Timestamp:  time.Now().Format(time.RFC3339),
	}
	bot.LastSignals = append(bot.LastSignals, botSignal)
	if len(bot.LastSignals) > 5 {
		bot.LastSignals = bot.LastSignals[1:]
	}
	bm.mu.Unlock()

	bm.logger.Infof("Bot %s: executed %s %s at %.8f (confidence: %.2f, latency: %v)",
		bot.ID, signal.Direction, signal.Pair, execution.Price, signal.Confidence, execution.Latency)
}

func (bm *BotManager) checkPaperPositionExits(ctx context.Context, bot *BotInstance) {
	bm.mu.RLock()
	pairs := make([]string, 0, len(bot.paperPositions))
	for p := range bot.paperPositions {
		pairs = append(pairs, p)
	}
	bm.mu.RUnlock()

	for _, pair := range pairs {
		bm.mu.RLock()
		pos, ok := bot.paperPositions[pair]
		bm.mu.RUnlock()
		if !ok {
			continue
		}

		ticker, err := bm.gctClient.GetTicker(ctx, bot.Config.Exchange, pair)
		if err != nil {
			bm.logger.Debugf("Bot %s: can't get ticker for paper exit check %s: %v", bot.ID, pair, err)
			continue
		}
		currentPrice := ticker.Last

		if time.Since(pos.OpenedAt) > 30*time.Minute {
			bm.closePaperPosition(bot, pos, currentPrice, "timeout")
			continue
		}

		if bm.dynamicExit != nil {
			decision := bm.dynamicExit.Evaluate(ctx, pos, currentPrice)
			bm.applyExitDecision(bot, pos, currentPrice, decision)
		} else {
			if reason := bm.legacyCheckExit(pos, currentPrice); reason != "" {
				bm.closePaperPosition(bot, pos, currentPrice, reason)
			}
		}
	}
}

// bootstrapLivePositionMirrors creates position mirrors for any live positions already
// open on the exchange when the bot starts (e.g. after a server restart).
func (bm *BotManager) bootstrapLivePositionMirrors(ctx context.Context, bot *BotInstance) {
	exchangePositions, err := bm.gctClient.GetFuturesPositions(ctx, bot.Config.Exchange)
	if err != nil {
		bm.logger.Debugf("Bot %s: bootstrap mirrors: failed to get positions: %v", bot.ID, err)
		return
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bot.livePositionMirrors == nil {
		bot.livePositionMirrors = make(map[string]*PaperPosition)
	}

	for _, ep := range exchangePositions {
		for _, pair := range bot.Config.Pairs {
			pairNorm := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(pair, "-", ""), "/", ""))
			if !strings.HasPrefix(strings.ToUpper(ep.Symbol), pairNorm) {
				continue
			}
			if _, exists := bot.livePositionMirrors[pair]; exists {
				continue
			}

			direction := "sell"
			if ep.Side == "long" || ep.Side == "buy" {
				direction = "buy"
			}
			stopPct := bot.Config.StopLoss / 100.0
			tpPct := bot.Config.TakeProfit / 100.0
			if stopPct <= 0 {
				stopPct = 0.01
			}
			if tpPct <= 0 {
				tpPct = 0.02
			}
			var sl, tp float64
			if direction == "buy" {
				sl = ep.EntryPrice * (1 - stopPct)
				tp = ep.EntryPrice * (1 + tpPct)
			} else {
				sl = ep.EntryPrice * (1 + stopPct)
				tp = ep.EntryPrice * (1 - tpPct)
			}
			bot.livePositionMirrors[pair] = &PaperPosition{
				Pair:              pair,
				Direction:         direction,
				EntryPrice:        ep.EntryPrice,
				Quantity:          ep.Size,
				OpenedAt:          time.Now(),
				TakeProfit:        tp,
				StopLoss:          sl,
				OriginalTP:        tp,
				OriginalSL:        sl,
				MaxFavorablePrice: ep.CurrentPrice,
			}
			bm.logger.Infof("Bot %s: bootstrapped live mirror for %s (%s @ %.8f)", bot.ID, pair, ep.Side, ep.EntryPrice)
		}
	}
}

// checkLivePositionExits runs the DynamicExitEngine against real KuCoin positions.
// It mirrors each live position in bot.livePositionMirrors to track trailing/AI state,
// and places actual close orders when the engine decides to exit.
//
// SCALP-05 documented decision (part b, NOT fixed in this pass): this only
// calls GetFuturesPositions. A spot fill (leverage=1, now correctly passed
// through to ExecuteOrderWithLeverage as of the SCALP-05(a) fix above) never
// appears there, so a spot bot's live-position mirror is silently deleted on
// the very next tick by the "remove mirrors for positions that no longer
// exist on the exchange" loop below — the DynamicExitEngine then has nothing
// left to manage for that position (no trailing/AI exit review), even though
// the position is still open on the exchange as a spot balance.
//
// Not fixing this here: doing it safely requires either (1) a genuine spot
// balance/position reconciliation call (GetSpotPositions or an equivalent
// balance-delta poll) with its own matching/normalization logic mirroring
// the futures path above — a meaningfully sized addition, not a one-line
// call-site change — or (2) a product decision that spot (leverage=1)
// scalping bots are out of scope for this live-mirror/AI-exit system
// entirely and should rely solely on their entry-time SL/TP orders. Forcing
// either without verifying against a real spot-trading bot risks either
// false-negative mirror survival (stale state) or continuing to silently
// drop real spot positions from monitoring. Flagging for a follow-up change.
func (bm *BotManager) checkLivePositionExits(ctx context.Context, bot *BotInstance) {
	if bm.dynamicExit == nil {
		return
	}

	exchangePositions, err := bm.gctClient.GetFuturesPositions(ctx, bot.Config.Exchange)
	if err != nil {
		bm.logger.Debugf("Bot %s: live exit check: failed to get positions: %v", bot.ID, err)
		return
	}

	// Map exchange positions by bot pair (normalized).
	type epEntry struct {
		symbol        string
		side          string
		currentPrice  float64
		unrealizedPnL float64
	}
	epByPair := make(map[string]epEntry)
	for _, ep := range exchangePositions {
		for _, pair := range bot.Config.Pairs {
			pairNorm := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(pair, "-", ""), "/", ""))
			if strings.HasPrefix(strings.ToUpper(ep.Symbol), pairNorm) {
				epByPair[pair] = epEntry{
					symbol:        ep.Symbol,
					side:          ep.Side,
					currentPrice:  ep.CurrentPrice,
					unrealizedPnL: ep.UnrealizedPnL,
				}
				break
			}
		}
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bot.livePositionMirrors == nil {
		return
	}

	// Remove mirrors for positions that no longer exist on the exchange.
	for pair := range bot.livePositionMirrors {
		if _, exists := epByPair[pair]; !exists {
			delete(bot.livePositionMirrors, pair)
			if bot.ActivePositions > 0 {
				bot.ActivePositions--
			}
		}
	}

	for pair, mirror := range bot.livePositionMirrors {
		ep, exists := epByPair[pair]
		if !exists || ep.currentPrice <= 0 {
			continue
		}

		decision := bm.dynamicExit.Evaluate(ctx, mirror, ep.currentPrice)

		switch decision.Action {
		case "close_all":
			bm.logger.WithFields(logrus.Fields{
				"bot_id": bot.ID,
				"pair":   pair,
				"reason": decision.Reason,
				"pnl":    fmt.Sprintf("$%.4f", ep.unrealizedPnL),
				"price":  ep.currentPrice,
			}).Info("🤖 [AI-EXIT] Closing live position on reversal signal")

			closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			closeErr := bm.gctClient.CloseFuturesPosition(closeCtx, bot.Config.Exchange, ep.symbol)
			cancel()
			if closeErr != nil {
				bm.logger.WithError(closeErr).Errorf("Bot %s: AI-exit failed to close %s", bot.ID, pair)
			} else {
				delete(bot.livePositionMirrors, pair)
				if bot.ActivePositions > 0 {
					bot.ActivePositions--
				}
				// If SL was hit, apply cooldown to prevent immediate re-entry.
				if strings.Contains(decision.Reason, "stop_loss") {
					cooldown := time.Now().Add(5 * time.Minute)
					bot.pairCooldowns[pair] = cooldown
					bm.logger.WithFields(logrus.Fields{
						"bot_id":         bot.ID,
						"pair":           pair,
						"cooldown_until": cooldown.Format("15:04:05"),
					}).Info("🛑 [AI-EXIT] Post-SL cooldown set — re-entry blocked for 5 min")
				}
				bm.logger.WithFields(logrus.Fields{
					"bot_id": bot.ID,
					"pair":   pair,
					"pnl":    fmt.Sprintf("$%.4f", ep.unrealizedPnL),
				}).Info("✅ [AI-EXIT] Live position closed successfully")
				bm.persistClosedTrade(bot, mirror, ep.currentPrice, decision.Reason, ep.unrealizedPnL)
			}
		}
	}
}

func (bm *BotManager) legacyCheckExit(pos *PaperPosition, currentPrice float64) string {
	if pos.Direction == "buy" {
		if currentPrice >= pos.TakeProfit {
			return "take_profit"
		}
		if currentPrice <= pos.StopLoss {
			return "stop_loss"
		}
	} else {
		if currentPrice <= pos.TakeProfit {
			return "take_profit"
		}
		if currentPrice >= pos.StopLoss {
			return "stop_loss"
		}
	}
	return ""
}

func (bm *BotManager) applyExitDecision(bot *BotInstance, pos *PaperPosition, currentPrice float64, decision ExitDecision) {
	switch decision.Action {
	case "close_all":
		bm.closePaperPosition(bot, pos, currentPrice, decision.Reason)
	case "close_partial":
		bm.closePartialPaperPosition(bot, pos, currentPrice, decision)
	}
}

func (bm *BotManager) closePartialPaperPosition(bot *BotInstance, pos *PaperPosition, exitPrice float64, decision ExitDecision) {
	qty := decision.Quantity
	if qty <= 0 {
		return
	}

	var pnl float64
	if pos.Direction == "buy" {
		pnl = (exitPrice - pos.EntryPrice) * qty
	} else {
		pnl = (pos.EntryPrice - exitPrice) * qty
	}

	bm.mu.Lock()
	pos.Quantity -= qty
	if pos.Quantity < 0 {
		pos.Quantity = 0
	}
	pos.PartialQtyClosed += qty
	pos.PartialPnL += pnl
	bot.PnLToday += pnl
	if decision.PartialIndex == 1 {
		pos.Partial1Done = true
	} else if decision.PartialIndex == 2 {
		pos.Partial2Done = true
	}
	bm.mu.Unlock()

	bm.logger.Infof("[PAPER] Bot %s: partial exit %s %s qty=%.8f at %.8f pnl=%.4f %s",
		bot.ID, pos.Direction, pos.Pair, qty, exitPrice, pnl, decision.Reason)
}

func (bm *BotManager) closePaperPosition(bot *BotInstance, pos *PaperPosition, exitPrice float64, reason string) {
	var pnl float64
	if pos.Direction == "buy" {
		pnl = (exitPrice - pos.EntryPrice) * pos.Quantity
	} else {
		pnl = (pos.EntryPrice - exitPrice) * pos.Quantity
	}
	totalPnL := pnl + pos.PartialPnL

	bm.mu.Lock()
	delete(bot.paperPositions, pos.Pair)
	bot.ActivePositions--
	if bot.ActivePositions < 0 {
		bot.ActivePositions = 0
	}
	bot.PnLToday += pnl
	if totalPnL >= 0 {
		bot.WinningTrades++
	} else {
		bot.LosingTrades++
	}
	bm.mu.Unlock()

	bm.logger.Infof("[PAPER] Bot %s: closed %s %s entry=%.8f exit=%.8f pnl=%.4f partial=%.4f total=%.4f reason=%s age=%s",
		bot.ID, pos.Direction, pos.Pair, pos.EntryPrice, exitPrice, pnl, pos.PartialPnL, totalPnL, reason, time.Since(pos.OpenedAt).Round(time.Second))

	if bm.dynamicExit != nil {
		bm.dynamicExit.BuildAndRecordOutcome(bot.ID, pos, exitPrice, reason, totalPnL)
	}

	bm.persistClosedTrade(bot, pos, exitPrice, reason, totalPnL)
}

// persistClosedTrade saves a closed scalping trade to the SQLite trade_history table.
// Called for both paper and live position closes so the FeedbackLoop can warm-start
// from DB on next restart.
func (bm *BotManager) persistClosedTrade(bot *BotInstance, pos *PaperPosition, exitPrice float64, exitReason string, pnlAbsolute float64) {
	if bm.tradeHistory == nil {
		return
	}
	if pos.EntryPrice <= 0 || exitPrice <= 0 {
		return
	}

	pnlPct := 0.0
	if pos.EntryPrice > 0 {
		if pos.Direction == "buy" {
			pnlPct = (exitPrice - pos.EntryPrice) / pos.EntryPrice * 100.0
		} else {
			pnlPct = (pos.EntryPrice - exitPrice) / pos.EntryPrice * 100.0
		}
	}

	side := "buy"
	if strings.ToLower(pos.Direction) == "sell" || strings.ToLower(pos.Direction) == "short" {
		side = "sell"
	}

	now := time.Now()
	t := &storage.Trade{
		ID:               fmt.Sprintf("scalp-%s-%d", pos.Pair, pos.OpenedAt.UnixNano()),
		Pair:             pos.Pair,
		Exchange:         bot.Config.Exchange,
		Side:             side,
		TradingStyle:     "scalping",
		TradingStyleType: "scalping_bot",
		PrimaryTimeframe: "5m",
		EntryPrice:       pos.EntryPrice,
		EntryTime:        pos.OpenedAt,
		Size:             pos.Quantity,
		Leverage:         1,
		ExitPrice:        exitPrice,
		ExitTime:         now,
		ExitReason:       exitReason,
		StopLoss:         pos.StopLoss,
		TakeProfit:       pos.TakeProfit,
		PnL:              pnlAbsolute,
		PnLPct:           pnlPct,
		NetPnL:           pnlAbsolute,
		Status:           "closed",
		CreatedAt:        pos.OpenedAt,
		UpdatedAt:        now,
	}

	if err := bm.tradeHistory.Save(t); err != nil {
		bm.logger.WithError(err).Debugf("persistClosedTrade: failed to save %s", pos.Pair)
	}

	// SCALP-04 fix: UpdatePerformance previously had zero real call sites, so
	// the circuit breaker's dailyLoss/consecutiveLosses state (checked by
	// checkRiskLimits) never reflected real trade outcomes. persistClosedTrade
	// is the single point both paper and live closes already flow through, so
	// wiring it here covers both without duplicating call sites.
	if bm.riskManager != nil {
		side := gctorder.Buy
		if strings.ToLower(pos.Direction) == "sell" || strings.ToLower(pos.Direction) == "short" {
			side = gctorder.Sell
		}
		bm.riskManager.UpdatePerformance(&models.ScalpingTrade{
			ID:         t.ID,
			Pair:       pos.Pair,
			Exchange:   bot.Config.Exchange,
			Side:       side,
			EntryPrice: pos.EntryPrice,
			ExitPrice:  exitPrice,
			Amount:     pos.Quantity,
			PnL:        pnlAbsolute,
			PnLPercent: pnlPct,
			OpenedAt:   pos.OpenedAt,
			ClosedAt:   now,
			Status:     "closed",
		})
	}
}

// SetDynamicExitFeedbackLoop wires the post-trade learning loop.
func (bm *BotManager) SetDynamicExitFeedbackLoop(fl *learning.FeedbackLoop) {
	if bm.dynamicExit == nil || fl == nil {
		return
	}
	bm.dynamicExit.SetFeedbackLoop(fl)
	bm.logger.Info("BotManager: dynamic exit feedback loop wired")
}

// SetDynamicExitConsensus wires the multi-LLM consensus engine.
func (bm *BotManager) SetDynamicExitConsensus(ce *llm.ConsensusEngine) {
	if bm.dynamicExit == nil || ce == nil {
		return
	}
	bm.dynamicExit.SetConsensus(ce)
	bm.logger.Info("BotManager: dynamic exit AI consensus engine wired")
}

// SetMaxBots changes the concurrent bot limit at runtime.
func (bm *BotManager) SetMaxBots(newMax int) error {
	if newMax <= 0 {
		return fmt.Errorf("maxBots must be positive, got %d", newMax)
	}

	bm.mu.Lock()
	previous := bm.maxBots
	active := len(bm.bots)
	bm.maxBots = newMax
	bm.mu.Unlock()

	if newMax < active {
		bm.logger.Warnf("SetMaxBots: new limit %d is below active bots %d; new spawns will be rejected until bots terminate", newMax, active)
	} else {
		bm.logger.Infof("SetMaxBots: updated limit from %d to %d (active: %d)", previous, newMax, active)
	}
	return nil
}

// GetMaxBots returns the current maximum bot limit.
func (bm *BotManager) GetMaxBots() int {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.maxBots
}

// RegisterLifecycleHook adds a lifecycle callback. Multiple callbacks are supported.
func (bm *BotManager) RegisterLifecycleHook(hook LifecycleHook) {
	if hook == nil {
		return
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.lifecycleHooks = append(bm.lifecycleHooks, hook)
}

func (bm *BotManager) fireLifecycleEvent(event LifecycleEvent) {
	bm.mu.RLock()
	hooks := make([]LifecycleHook, len(bm.lifecycleHooks))
	copy(hooks, bm.lifecycleHooks)
	bm.mu.RUnlock()

	if len(hooks) == 0 {
		return
	}

	for _, h := range hooks {
		hook := h
		go func() {
			defer func() {
				if r := recover(); r != nil {
					bm.logger.Errorf("lifecycle hook panic recovered: %v (event=%s bot=%s)", r, event.Type, event.BotID)
				}
			}()
			hook(event)
		}()
	}
}

// ListActiveBots returns a summary of all running or stopping bots.
func (bm *BotManager) ListActiveBots() []*BotInfo {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	infos := make([]*BotInfo, 0, len(bm.bots))
	for _, bot := range bm.bots {
		if bot.Status != "running" && bot.Status != "stopping" {
			continue
		}
		info := &BotInfo{
			BotID:           bot.ID,
			Pair:            firstPair(bot.Config.Pairs),
			Pairs:           append([]string(nil), bot.Config.Pairs...),
			Exchange:        bot.Config.Exchange,
			StartedAt:       bot.StartTime,
			Status:          bot.Status,
			PaperMode:       bot.Config.PaperMode,
			TradesToday:     bot.TradesToday,
			PnLToday:        bot.PnLToday,
			TotalTrades:     bot.TotalTrades,
			WinningTrades:   bot.WinningTrades,
			ActivePositions: bot.ActivePositions,
		}
		if len(bot.LastSignals) > 0 {
			info.LastSignals = append([]Signal(nil), bot.LastSignals...)
		}
		if bot.paperPositions != nil && len(bot.paperPositions) > 0 {
			dpmList := make([]DPMPositionInfo, 0, len(bot.paperPositions))
			for _, pos := range bot.paperPositions {
				dpmList = append(dpmList, DPMPositionInfo{
					Pair:              pos.Pair,
					Direction:         pos.Direction,
					EntryPrice:        pos.EntryPrice,
					CurrentQty:        pos.Quantity,
					OriginalQty:       pos.Quantity + pos.PartialQtyClosed,
					OpenedAt:          pos.OpenedAt,
					TakeProfit:        pos.TakeProfit,
					StopLoss:          pos.StopLoss,
					OriginalTP:        pos.OriginalTP,
					OriginalSL:        pos.OriginalSL,
					MaxFavorablePrice: pos.MaxFavorablePrice,
					BreakevenMoved:    pos.BreakevenMoved,
					Partial1Done:      pos.Partial1Done,
					Partial2Done:      pos.Partial2Done,
					PartialPnL:        pos.PartialPnL,
				})
			}
			info.DPMPositions = dpmList
		}
		infos = append(infos, info)
	}
	return infos
}

// firstPair returns the first pair from a slice, or "" if empty.
func firstPair(pairs []string) string {
	if len(pairs) == 0 {
		return ""
	}
	return pairs[0]
}

// GetBotTrades returns paginated trade history for a bot.
func (bm *BotManager) GetBotTrades(botID string, limit, offset int, status string) ([]BotTrade, int, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	bot, exists := bm.bots[botID]
	if !exists {
		return nil, 0, ErrBotNotFound
	}

	trades := bm.generateSampleTrades(bot, limit, offset, status)
	totalCount := bm.getTotalTradesCount(bot, status)

	return trades, totalCount, nil
}

func (bm *BotManager) generateSampleTrades(bot *BotInstance, limit, offset int, status string) []BotTrade {
	trades := make([]BotTrade, 0, limit)

	numTrades := bot.TotalTrades
	if numTrades == 0 {
		return trades
	}

	start := offset
	end := offset + limit
	if end > numTrades {
		end = numTrades
	}

	for i := start; i < end; i++ {
		isWinning := i < bot.WinningTrades
		direction := "long"
		if i%2 == 0 {
			direction = "short"
		}

		entryPrice := 100.0 + float64(i)*0.5
		exitPrice := entryPrice
		pnl := 0.0
		pnlPercent := 0.0
		tradeStatus := "completed"
		reason := "Take profit reached"

		if isWinning {
			exitPrice = entryPrice * 1.02
			pnl = (exitPrice - entryPrice) * 100
			pnlPercent = 2.0
		} else {
			exitPrice = entryPrice * 0.99
			pnl = (exitPrice - entryPrice) * 100
			pnlPercent = -1.0
			reason = "Stop loss hit"
		}

		if status != "" && tradeStatus != status {
			continue
		}

		trade := BotTrade{
			ID:              fmt.Sprintf("trade_%s_%d", bot.ID, i),
			Timestamp:       time.Now().Add(-time.Duration(numTrades-i) * time.Hour).Format(time.RFC3339),
			Symbol:          bot.Config.Pairs[0],
			Direction:       direction,
			EntryPrice:      entryPrice,
			ExitPrice:       exitPrice,
			Quantity:        100.0,
			PnL:             pnl,
			PnLPercent:      pnlPercent,
			DurationSeconds: 180 + (i * 30),
			Status:          tradeStatus,
			Reason:          reason,
			Strategy:        bot.Config.Strategies[0],
			EntryOrderID:    fmt.Sprintf("order_entry_%d", i),
			ExitOrderID:     fmt.Sprintf("order_exit_%d", i),
		}

		trades = append(trades, trade)
	}

	return trades
}

func (bm *BotManager) simulatePaperExecution(bot *BotInstance, signal *models.ScalpingSignal, amount float64) *executor.OrderExecution {
	now := time.Now()
	orderID := fmt.Sprintf("paper-%s-%d", bot.ID, now.UnixNano())
	price := signal.Price
	side := "buy"
	if signal.Direction == "short" || signal.Direction == "sell" {
		side = "sell"
	}

	bm.logger.Warnf("[PAPER] Bot %s: simulated %s %s amount=%.8f price=%.8f (no exchange call)",
		bot.ID, side, signal.Pair, amount, price)

	return &executor.OrderExecution{
		OrderID:     orderID,
		Signal:      signal,
		Exchange:    bot.Config.Exchange,
		Pair:        signal.Pair,
		Side:        side,
		Amount:      amount,
		Price:       price,
		OrderType:   "market",
		Status:      "filled",
		Latency:     0,
		ExecutedAt:  now,
		ConfirmedAt: now,
	}
}

// SetBotPaperMode toggles paper mode on a running bot identified by pair or bot ID.
func (bm *BotManager) SetBotPaperMode(identifier string, enabled bool) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	botID := ""
	if _, ok := bm.bots[identifier]; ok {
		botID = identifier
	} else {
		for id, bot := range bm.bots {
			if bot.Status == "running" || bot.Status == "stopping" {
				for _, p := range bot.Config.Pairs {
					if p == identifier {
						botID = id
						break
					}
				}
			}
			if botID != "" {
				break
			}
		}
	}
	if botID == "" {
		return ErrBotNotFound
	}

	bot := bm.bots[botID]
	bot.Config.PaperMode = enabled
	bm.logger.WithFields(logrus.Fields{
		"bot_id":     botID,
		"pair":       firstPair(bot.Config.Pairs),
		"paper_mode": enabled,
	}).Warn("bot_manager: paper mode toggled on running bot")
	return nil
}

func (bm *BotManager) getTotalTradesCount(bot *BotInstance, status string) int {
	if status == "active" {
		return bot.ActivePositions
	}
	if status == "completed" {
		return bot.TotalTrades - bot.ActivePositions
	}
	return bot.TotalTrades
}

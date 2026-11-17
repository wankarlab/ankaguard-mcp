package learning

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	btdomain "github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// BacktestRunner is the minimal interface the AutoTuner needs from the backtest engine.
// Using an interface keeps the learning package free of the backtest engine's dependencies
// (gRPC client, DecisionEngine) — the concrete *backtest.Engine satisfies it.
type BacktestRunner interface {
	Run(ctx context.Context, req btdomain.BacktestRequest) (*btdomain.BacktestResult, error)
}

// AutoTuner runs in the background, reads adjustments from the FeedbackLoop,
// and persists them to the dynamic config store so they survive restarts and
// are visible to all components that read dynamic config.
//
// All its control parameters (interval, min trades, scope) are themselves
// stored in the dynamic config store under the "autotuner.*" and "feedback.*"
// keys registered in the manifest.
type AutoTuner struct {
	mu             sync.Mutex
	log            *logrus.Logger
	feedbackLoop   *FeedbackLoop
	configStore    *dynamic.ConfigStore
	backtestEngine BacktestRunner
	cancel         context.CancelFunc
	done           chan struct{}
}

// SetBacktestEngine wires an optional backtest engine. When set and
// autotuner.backtest_validate=true, each candidate adjustment is validated
// against the last 7 days before being persisted to the config DB.
func (at *AutoTuner) SetBacktestEngine(e BacktestRunner) {
	at.mu.Lock()
	defer at.mu.Unlock()
	at.backtestEngine = e
}

// NewAutoTuner constructs an AutoTuner. Start() must be called separately.
func NewAutoTuner(log *logrus.Logger, fl *FeedbackLoop, store *dynamic.ConfigStore) *AutoTuner {
	if log == nil {
		log = logrus.New()
	}
	return &AutoTuner{
		log:          log,
		feedbackLoop: fl,
		configStore:  store,
	}
}

// Start launches the background persistence loop.
func (at *AutoTuner) Start(ctx context.Context) {
	at.mu.Lock()
	defer at.mu.Unlock()
	if at.cancel != nil {
		return // already running
	}
	loopCtx, cancel := context.WithCancel(ctx)
	at.cancel = cancel
	at.done = make(chan struct{})
	go at.loop(loopCtx)
	at.log.Info("🧠 AutoTuner started — feedback loop will persist to dynamic config DB")
}

// Stop shuts down the background loop gracefully.
func (at *AutoTuner) Stop() {
	at.mu.Lock()
	cancel := at.cancel
	done := at.done
	at.mu.Unlock()
	if cancel != nil {
		cancel()
		if done != nil {
			<-done
		}
	}
}

func (at *AutoTuner) loop(ctx context.Context) {
	defer close(at.done)

	interval := at.runInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	at.log.WithField("interval", interval).Info("🧠 AutoTuner loop running")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Re-read interval in case it changed in dynamic config.
			newInterval := at.runInterval()
			if newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
				at.log.WithField("interval", interval).Info("🧠 AutoTuner interval updated from dynamic config")
			}

			if !at.isEnabled() {
				continue
			}
			at.persistAdjustments(ctx)
		}
	}
}

// paramKeyMap maps FeedbackLoop internal parameter names to dynamic config manifest keys.
// Keys MUST match the FeedbackLoop param constants exactly, otherwise the
// lookup in params[internalKey] misses and that adjustment is never persisted.
// (paramMinConfidenceThresh = "min_confidence_threshold" — the old literal
// "min_confidence_thresh" silently dropped every confidence adjustment.)
var paramKeyMap = map[string]string{
	paramStopLossATRMult:     "feedback.stop_loss_atr_mult",
	paramTakeProfitATRMult:   "feedback.take_profit_atr_mult",
	paramMinConfidenceThresh: "feedback.min_confidence_threshold",
	paramSizeMultiplier:      "feedback.size_multiplier",
}

func (at *AutoTuner) persistAdjustments(ctx context.Context) {
	pairs := at.feedbackLoop.GetAllPairs()
	if len(pairs) == 0 {
		return
	}

	minTrades := at.minTradesToTune()
	persistScope := at.persistScope()
	validateWithBT := at.backtestValidate()

	var vals []*domain.ParameterValue
	var descriptions []string

	for _, pair := range pairs {
		adjustments := at.feedbackLoop.GetAllAdjustments(pair)
		if len(adjustments) < minTrades {
			continue
		}
		params := at.feedbackLoop.GetCurrentParams(pair)
		if params == nil {
			continue
		}

		// Build overrides map for this pair (internal key → config key → string value).
		overrides := make(map[string]string, len(paramKeyMap))
		for internalKey, configKey := range paramKeyMap {
			if v, ok := params[internalKey]; ok {
				overrides[configKey] = fmt.Sprintf("%.6f", v)
			}
		}

		// Optional backtest validation: only persist when adjusted params improve Sharpe.
		if validateWithBT && at.backtestEngine != nil && len(overrides) > 0 {
			if !at.validateWithBacktest(ctx, pair, overrides) {
				at.log.WithField("pair", pair).Info("🧠 AutoTuner: backtest validation failed — skipping param update for pair")
				continue
			}
		}

		scopeType := domain.ScopePair
		scopeID := pair
		if persistScope == "global" {
			scopeType = domain.ScopeGlobal
			scopeID = "*"
		}

		for internalKey, configKey := range paramKeyMap {
			v, ok := params[internalKey]
			if !ok {
				continue
			}
			vals = append(vals, &domain.ParameterValue{
				Key:       configKey,
				ScopeType: scopeType,
				ScopeID:   scopeID,
				Value:     fmt.Sprintf("%.6f", v),
				UpdatedBy: "autotuner",
				Source:    domain.SourceFeedbackLoop,
			})
			descriptions = append(descriptions, fmt.Sprintf("%s/%s=%.4f", pair, internalKey, v))
		}
	}

	if len(vals) == 0 {
		return
	}

	if err := at.configStore.BulkSet(ctx, vals, "autotuner", "feedback loop parameter adjustment"); err != nil {
		at.log.WithError(err).Warn("🧠 AutoTuner: failed to persist adjustments to config DB")
		return
	}

	at.log.WithFields(logrus.Fields{
		"params_written": len(vals),
		"pairs":          len(pairs),
		"details":        descriptions,
	}).Info("🧠 AutoTuner: persisted feedback adjustments to dynamic config DB")
}

// validateWithBacktest runs a 7-day baseline vs adjusted backtest for the pair.
// Returns true when adjusted Sharpe is >= baseline Sharpe * minImprovementRatio.
func (at *AutoTuner) validateWithBacktest(ctx context.Context, pair string, overrides map[string]string) bool {
	horizon := at.backtestHorizonDays()
	to := time.Now()
	from := to.Add(-time.Duration(horizon) * 24 * time.Hour)

	btCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	baseline, err := at.backtestEngine.Run(btCtx, btdomain.BacktestRequest{
		Pair:        pair,
		Exchange:    "kucoin",
		From:        from,
		To:          to,
		TriggeredBy: "autotuner/baseline",
	})
	if err != nil {
		at.log.WithError(err).WithField("pair", pair).Warn("🧠 AutoTuner: baseline backtest failed — accepting adjustment anyway")
		return true // fail open
	}

	adjusted, err := at.backtestEngine.Run(btCtx, btdomain.BacktestRequest{
		Pair:        pair,
		Exchange:    "kucoin",
		From:        from,
		To:          to,
		Overrides:   overrides,
		TriggeredBy: "autotuner/adjusted",
	})
	if err != nil {
		at.log.WithError(err).WithField("pair", pair).Warn("🧠 AutoTuner: adjusted backtest failed — accepting adjustment anyway")
		return true // fail open
	}

	// Require at least minTrades trades in adjusted run to make the Sharpe meaningful.
	if adjusted.Metrics.NTrades < at.minTradesToTune() {
		at.log.WithFields(logrus.Fields{
			"pair":   pair,
			"trades": adjusted.Metrics.NTrades,
		}).Debug("🧠 AutoTuner: adjusted backtest has too few trades — accepting adjustment")
		return true
	}

	minImprovement := at.backtestMinImprovement()
	improved := adjusted.Metrics.Sharpe >= baseline.Metrics.Sharpe*minImprovement

	at.log.WithFields(logrus.Fields{
		"pair":             pair,
		"baseline_sharpe":  baseline.Metrics.Sharpe,
		"adjusted_sharpe":  adjusted.Metrics.Sharpe,
		"min_improvement":  minImprovement,
		"baseline_winrate": baseline.Metrics.WinRate,
		"adjusted_winrate": adjusted.Metrics.WinRate,
		"passed":           improved,
	}).Info("🧠 AutoTuner: backtest validation result")

	return improved
}

func (at *AutoTuner) backtestValidate() bool {
	if at.configStore == nil {
		return false
	}
	snap := at.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return false
	}
	if v, ok := snap.BoolOk("autotuner.backtest_validate"); ok {
		return v
	}
	return false // opt-in
}

func (at *AutoTuner) backtestHorizonDays() int {
	if at.configStore == nil {
		return 7
	}
	snap := at.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return 7
	}
	if v, ok := snap.IntOk("autotuner.backtest_horizon_days"); ok && v > 0 {
		return v
	}
	return 7
}

func (at *AutoTuner) backtestMinImprovement() float64 {
	if at.configStore == nil {
		return 1.05
	}
	snap := at.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return 1.05
	}
	if v, ok := snap.FloatOk("autotuner.backtest_min_improvement"); ok && v > 0 {
		return v
	}
	return 1.05 // require 5% Sharpe improvement
}

func (at *AutoTuner) runInterval() time.Duration {
	if at.configStore == nil {
		return 15 * time.Minute
	}
	snap := at.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return 15 * time.Minute
	}
	if d := snap.Duration("autotuner.run_interval"); d > 0 {
		return d
	}
	return 15 * time.Minute
}

func (at *AutoTuner) isEnabled() bool {
	if at.configStore == nil {
		return true
	}
	snap := at.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return true
	}
	enabled, ok := snap.BoolOk("autotuner.enabled")
	if !ok {
		return true
	}
	return enabled
}

func (at *AutoTuner) minTradesToTune() int {
	if at.configStore == nil {
		return 5
	}
	snap := at.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return 5
	}
	if v, ok := snap.IntOk("autotuner.min_trades_to_tune"); ok {
		return v
	}
	return 5
}

func (at *AutoTuner) persistScope() string {
	if at.configStore == nil {
		return "pair"
	}
	snap := at.configStore.SnapshotForScope([]dynamic.ScopeRef{{Type: domain.ScopeGlobal, ID: "*"}})
	if snap == nil {
		return "pair"
	}
	if v := snap.String("autotuner.persist_scope"); v != "" {
		return v
	}
	return "pair"
}

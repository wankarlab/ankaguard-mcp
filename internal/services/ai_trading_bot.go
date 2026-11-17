package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/featureeng"
	"github.com/rantipay/trading-mcp/internal/ai/learning"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/ai/manipulation"
	"github.com/rantipay/trading-mcp/internal/ai/patterns"
	"github.com/rantipay/trading-mcp/internal/ai/regime"
	aivalidation "github.com/rantipay/trading-mcp/internal/ai/validation"
	"github.com/rantipay/trading-mcp/internal/config"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	dyndomain "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/exchange"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/notifications"
	"github.com/rantipay/trading-mcp/internal/orderflow"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
	"github.com/rantipay/trading-mcp/internal/scalping/coordinator"
	"github.com/rantipay/trading-mcp/internal/trading/mode"
	"github.com/rantipay/trading-mcp/internal/volatility"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
)

// SLTPExecutor interface for setting stop loss and take profit
type SLTPExecutor interface {
	UpdateSLTP(ctx context.Context, positionID string, sl, tp float64) error
}

// bestVenueRouter is the minimal smart-order-routing surface the bot needs.
// *exchange.Router satisfies this. Kept local so internal/exchange never needs
// to import internal/services (no import cycle).
type bestVenueRouter interface {
	BestVenue(ctx context.Context, pair, side string, size float64) (exchange.Venue, error)
}

// NotificationManager interface for sending notifications
type NotificationManager interface {
	NotifyTradeWithAI(ctx context.Context, trade interface{}, llmCallFunc func(ctx context.Context, prompt string) (string, error)) error
	NotifyPositionClosedWithAI(ctx context.Context, position interface{}, llmCallFunc func(ctx context.Context, prompt string) (string, error)) error
}

// AutoConfigurator interface for automatic configuration
type AutoConfigurator interface {
	SelectOptimalStrategyWithAI(ctx context.Context, conditions *MarketConditions) (string, *StrategyRecommendation, error)
	AdjustRiskParameters(ctx context.Context, conditions *MarketConditions) (*RiskParams, error)
	ApplyOptimizedParameters(ctx context.Context, params *OptimizedParameters) error
	ValidateParameters(params *OptimizedParameters) error
	GetCurrentConfiguration() *BotConfiguration
}

// StrategyRecommendation contains LLM-generated strategy recommendation
type StrategyRecommendation struct {
	Strategy    string    `json:"strategy"`
	Confidence  float64   `json:"confidence"`
	Reasoning   string    `json:"reasoning"`
	KeyFactors  []string  `json:"key_factors"`
	Warnings    []string  `json:"warnings"`
	TimeHorizon string    `json:"time_horizon"`
	Timestamp   time.Time `json:"timestamp"`
}

// RiskParams holds risk management parameters (from optimization package)
type RiskParams struct {
	StopLossPercent     float64
	TakeProfitPercent   float64
	MaxPositionSize     float64
	MaxOpenPositions    int
	ConfidenceThreshold float64
}

// OptimizedParameters contains all optimized parameters
type OptimizedParameters struct {
	Strategy         string                  `json:"strategy"`
	RiskParams       *RiskParams             `json:"risk_params"`
	MarketConditions *MarketConditions       `json:"market_conditions"`
	Recommendation   *StrategyRecommendation `json:"recommendation"`
	Timestamp        time.Time               `json:"timestamp"`
}

// BotConfiguration represents current bot configuration
type BotConfiguration struct {
	Strategy    string      `json:"strategy"`
	RiskParams  *RiskParams `json:"risk_params"`
	LastUpdated time.Time   `json:"last_updated"`
	UpdatedBy   string      `json:"updated_by"`
}

// calendarGate is a minimal interface so services/ doesn't import master/adapters/.
type calendarGate interface {
	IsTradeAllowed(ctx context.Context) bool
}

// AITradingBot manages automated AI trading with safety controls
type AITradingBot struct {
	// Core components
	contextBuilder  *ai.ContextBuilder
	decisionEngine  *ai.DecisionEngine
	positionManager *ai.PositionManager
	riskCalculator  *ai.RiskCalculator
	sltpExecutor    SLTPExecutor
	gctClient       gct.GCTClient
	log             *logrus.Logger

	// AI Enhancement components (NEW)
	llmAnalyzer         *llm.Analyzer
	fcAnalyzer          *llm.FunctionCallingAnalyzer // NEW: Function calling support
	costManager         llm.CostManager
	notificationManager NotificationManager
	learningSystem      *ai.SLTPLearningSystem
	autoConfigurator    AutoConfigurator // NEW: Auto-configuration component

	// Hybrid AI modules (optional - feature-flagged, default disabled)
	consensusEngine    *llm.ConsensusEngine      // multi-LLM consensus voting
	regimeDetector     *regime.Detector          // market regime classifier
	btcMonitor         *ai.BTCCorrelationMonitor // BTC-aware decision adjustment
	tradeRepo          TradeStatsReader          // historical stats source for Kelly sizing
	tradeHistoryWriter TradeHistoryWriter        // persists closed trades to trade_history (optional)
	blacklistStore     BlacklistStore            // persists toxic-pair blacklist across restarts (optional)
	validationGate     ValidationGate            // certifies a pair has a validated edge before live entry (optional)
	// riskSentinel is the central RiskSentinel (Ring0 kill switch / Ring1 spec
	// freeze / Ring2 tenant isolation). BOT-02 fix: without this the
	// `activate_kill_switch` MCP tool had zero effect on autonomous bots —
	// only the bot's own internal dailyLoss/emergencyStop stopped it. Wired
	// via SetRiskSentinel from main.go using the same instance registered
	// with risk.RegisterAll(). Optional (nil-safe) so bots constructed before
	// the sentinel exists (or in tests) keep working, but every real
	// deployment MUST wire it.
	riskSentinel internalRisk.RiskSentinel

	// validationSvc runs PreTradeValidator + rule engine + circuit breaker
	// (AI-02 fix) against every real order this bot submits. Previously only
	// reachable from the test endpoint POST /api/v1/validation/test — real
	// trading used a separate, ad-hoc set of checks and never called this.
	// Optional (nil-safe): a bot constructed without it (e.g. in tests) keeps
	// working, but every real deployment should wire it via
	// SetValidationService.
	validationSvc aivalidation.Service
	// accountCapitalFn returns the account's total and available USDT so the
	// pre-trade validator judges risk and margin against real capital. Set to
	// the GCT portfolio by the constructor; tests inject a fake. nil = no
	// source, and validation then fails closed.
	accountCapitalFn func(ctx context.Context) (total, available float64, err error)
	capitalMu        sync.Mutex
	capitalTotal     float64
	capitalAvailable float64
	capitalAt        time.Time
	// xsecBookReader reads the xsec_live_book ownership registry (optional).
	// Wired via SetXSECBookReader. When nil, xsecOwnedPairs always returns nil
	// (no exclusion). Kept as an interface (not a direct internal/xsec import)
	// to avoid an import cycle: internal/xsec -> internal/storage ->
	// internal/master -> internal/services. The cmd binary adapts
	// internal/xsec.ActiveLiveBookPairs to this interface.
	xsecBookReader   XSECBookReader
	gateLastOn       atomic.Bool // last gate-enabled value actually read from a dynamic snapshot
	gateSeen         atomic.Bool // whether any snapshot has ever been read (fail-closed when false)
	consensusEnabled bool        // feature flag for multi-LLM consensus
	useRegimeFilter  bool        // feature flag for regime-based filtering
	kellyEnabled     bool        // feature flag for Kelly position sizing

	// mlPredictor is the optional ML model voter (P2 — cmd/ml-server).
	// When nil or IsReady()=false, the ML vote is silently skipped and the bot
	// operates with technical+LLM analysis unchanged (safe degradation).
	mlPredictor mlPredictor

	// feedbackLabeler labels closed trades in feature_store with real outcomes (P3.1).
	// Optional — nil disables post-trade labeling but the bot keeps running normally.
	feedbackLabeler tradeLabeler

	// mlWeightStore reads the Bayesian-calibrated weight for the ML voter (P3.2/P3.4).
	// When nil, applyMLVote falls back to the static ml.voter_weight config key.
	mlWeightStore mlWeightStore

	// lastMLProb is an ephemeral carrier for the ML predictor's P(TP_first)
	// computed during Kelly sizing (calculateKellyOrderSize). It is written there
	// and read back when the position is created so the value can be stamped onto
	// ManagedPosition.MLProb for the feedback loop — without querying the ML twice.
	// Guarded by its own mutex (not bot.mu) to avoid lock-ordering coupling with
	// the broad state lock. Reset to 0 at the start of each entry attempt so a
	// stale value from a prior trade can never leak into a new position.
	lastMLProb   float64
	lastMLProbMu sync.Mutex

	// lastRegimeLabel is the most recent market regime label classified by
	// checkRegime. Stamped onto ManagedPosition.EntryRegime at open so the
	// post-trade labeler can record per-regime calibration observations (instead
	// of the constant "UNKNOWN" that left the calibrator inert). Guarded by its
	// own mutex to avoid coupling with the broad state lock.
	lastRegimeLabel   string
	lastRegimeLabelMu sync.Mutex

	// Post-trade learning hooks
	feedbackLoop   *learning.FeedbackLoop
	patternLibrary *patterns.Library
	calendarGate   calendarGate // optional calendar check before entries

	// Dynamic tuning store (optional — nil reverts to hardcoded fallbacks)
	tuning ai.TuningInterface

	// Dynamic configuration store (MU-6: optional — nil disables per-user scope chain).
	// When non-nil, the bot constructs BotScopeChain(effectiveUserID(), pair, exchange)
	// so dynamic overrides (e.g., breakeven.trigger_pct, dynamic_sizing.tiers) resolve
	// per user above the persisted YAML config.
	configStore *dynamic.ConfigStore

	// modeResolver derives the effective market-type and leverage for each order (Phase 1).
	modeResolver *mode.Resolver

	// Order flow cache — updated asynchronously by the FlowSubscriber callback.
	orderFlowCache   map[string]*orderflow.FlowAnalysis
	orderFlowCacheMu sync.RWMutex

	// Human-like exit indicator cache. Populated from each analysis cycle so
	// checkPosition can run condition-based exits (RSI/MACD reversal, profit
	// lock) without re-fetching candles per monitor tick.
	indicatorCache   map[string]*positionIndicatorSnapshot
	indicatorCacheMu sync.RWMutex

	// wickLearner (optional) tracks wick-exit outcomes and auto-tunes the
	// exit.wick_body_ratio_threshold config parameter via a 15-min deferred check.
	wickLearner *learning.WickLearner

	// volClassifier (optional) provides dynamic per-pair volatility tier lookups.
	// When wired, applyVolatilityProtection uses the live tier from this classifier
	// instead of the legacy hardcoded volatile-pair list.
	volClassifier *volatility.Classifier

	// pairCooldowns tracks per-pair re-entry cooldown after a stop-loss hit.
	// Map value is the time at which trading on that pair becomes allowed again.
	// Prevents "death by 1000 cuts" loops where the bot reopens immediately
	// after every SL hit (e.g. RAVE-USDT 17 entries in 9 min on 2026-05-28).
	pairCooldowns  map[string]time.Time
	pairCooldownMu sync.Mutex

	// staleSignalCounts tracks consecutive stale-signal blocks per pair.
	// After staleSignalMaxConsecutive consecutive blocks, the pair enters a
	// stale-signal cooldown (defaultStaleCooldown) to avoid burning LLM quota.
	staleSignalCounts  map[string]int
	staleSignalCountMu sync.Mutex

	// trendBlockCounts tracks consecutive macro-trend-filter blocks per pair.
	// When the bot keeps generating counter-trend signals on a pair (e.g. trying
	// to short a pair that pumped +20% above its EMA50), every entry is vetoed and
	// the bot would spin forever on the master's top pick. After a threshold it
	// blacklists the pair transiently so maybeSwitchPair rotates to a tradeable
	// opportunity instead of getting stuck.
	trendBlockCounts  map[string]int
	trendBlockCountMu sync.Mutex

	// macroTrendCache caches the per-pair 1h-EMA50 distance% so the trend filter
	// and the directional-bias path (runAnalysis) share ONE candle fetch per pair
	// per cache TTL. Keyed strictly by pair, mutex-guarded — no cross-pair leak.
	macroTrendCache   map[string]macroTrendEntry
	macroTrendCacheMu sync.Mutex

	// exitLadderCache caches the finer exit-confirmation-TF candles per pair+TF
	// (Active Profit Engine F2) so the soft-exit confirmation gate makes at most
	// one REST fetch per pair/TF per cache TTL. Keyed "pair|tf", mutex-guarded.
	exitLadderCache   map[string]exitLadderCacheEntry
	exitLadderCacheMu sync.Mutex

	// reentryHolds marks pairs paused under bot.reentry.mode="manual" after a
	// close. The hold is released automatically when the operator switches that
	// pair's mode back to auto/cooldown (checked at entry time), so no manual
	// cooldown-clearing is needed.
	reentryHolds  map[string]bool
	reentryHoldMu sync.Mutex

	// entryStreaks tracks consecutive same-direction high-confidence signals per
	// pair for the anti-whipsaw entry gate (see MinConsecutiveSignals).
	entryStreaks  map[string]*signalStreak
	entryStreakMu sync.Mutex

	// Configuration
	config *TradingBotConfig

	// State management
	mu                     sync.RWMutex
	running                bool
	paused                 bool
	startedAt              time.Time
	lastAnalysisAt         time.Time
	lastRiskAdjustAt       time.Time
	lastErrorAt            time.Time
	lastPositionSyncAt     time.Time
	lastPairSwitchAt       time.Time
	lastLLMCallAt          time.Time // throttle: don't call LLM on every analysis tick
	lastBlacklistRefreshAt time.Time // periodic reload of DB blacklist into memory
	pairProvider           PairProvider
	// pairBlacklist keeps pairs that the exchange refused (e.g. margin-mode
	// mismatch) for a cooldown. Cleared when the timestamp expires.
	pairBlacklist map[string]time.Time
	// pairSessionPnL tracks net PnL per pair since last reset (daily or restart).
	// Used to auto-pause pairs with persistent negative edge regardless of win/loss alternation.
	pairSessionPnL map[string]float64
	// ghostMissCount counts how many CONSECUTIVE sync cycles an internal position
	// was absent from the exchange snapshot. Used as an anti-flapping guard before
	// reconciling (closing) it: a single transient/partial fetch should not register
	// a phantom external close. Keyed by position ID. Lazily created.
	ghostMissCount map[string]int
	// openingPairs records, per normalized pair, the time the bot last STARTED
	// opening a position. syncOpenPositionsFromExchange uses it to avoid the
	// open-vs-adopt race: the exchange reflects a freshly placed order before the
	// open path calls AddPosition, so a sync firing in that window would adopt the
	// bot's own position as an orphan — duplicating it. Skipping adoption for a
	// pair opened within the guard window closes that race. Lazily created.
	openingPairs map[string]time.Time
	// externalCloses records, per "SYMBOL|side", when an external close was last
	// REGISTERED for a managed position. The adoption path refuses to re-adopt the
	// same symbol+side within externalCloseReadoptGuard, regardless of profit sign.
	// The post-loss cooldown alone was not enough: a PROFITABLE orphan whose close
	// was phantom (flaky fetch) re-adopted every ~40s, recording fake PnL each
	// cycle and stacking real stop orders on the exchange (observed 2026-06-10:
	// 474 cycles on TAO, KuCoin's 50-stop-order cap reached). Lazily created.
	externalCloses map[string]time.Time
	// syncRunMu serialises syncOpenPositionsFromExchange so the async startup sync
	// and the monitor/analysis loop syncs never run concurrently — concurrent
	// adoption could pass the alreadyTracked check on both and duplicate a position.
	syncRunMu sync.Mutex
	// syncStateMu guards ghostMissCount and openingPairs. It is a DEDICATED mutex,
	// deliberately NOT bot.mu: syncOpenPositionsFromExchange runs while the caller
	// (StartWithContext) holds bot.mu.Lock(), so touching bot.mu from inside the
	// sync — e.g. an RLock in the race guard — self-deadlocks (Go RWMutex is not
	// reentrant). Keeping this state on its own lock makes the guard helpers safe
	// to call from any context, locked or not.
	syncStateMu       sync.Mutex
	analysisCount     int64
	executionCount    int64
	errorCount        int64
	consecutiveErrs   int
	consecutiveLosses int
	// consecutiveLossesByPair tracks consecutive losses PER PAIR. The global
	// consecutiveLosses above is zeroed by a win on ANY pair, so it can't catch
	// one pair bleeding while others win (WLD lost 5x in 45min but never tripped
	// the global=3 gate because other pairs' wins kept resetting it). The
	// per-pair counter drives auto-pause trigger 1 so a pair that loses N times
	// in a row is paused regardless of what the other pairs do.
	consecutiveLossesByPair map[string]int

	// Safety controls
	dailyLoss     float64
	dailyTrades   int
	lastResetDate time.Time
	emergencyStop bool

	// Post-close state
	postCloseState *PostCloseState

	// Channels
	stopChan   chan struct{}
	stopOnce   sync.Once // guards close(stopChan) — prevents panic on double-stop
	pauseChan  chan bool
	resultChan chan *ExecutionResult
	manual     manualWaiters // operator manual entries waiting for their result
	closePnL   closePnLMemo  // exchange PnL seen by executeCloseOrder, keyed by position id
	sizing     sizingState   // capital-relative sizing state (day-start capital, drawdown factor, tier conversions)

	// Cross-component pair coordinator (optional — nil disables the guard).
	pairCoord *coordinator.PairCoordinator

	// observeOnlyFn, when non-nil, is consulted before switching to a candidate
	// pair. If it returns true the pair is scored/ranked normally but never
	// traded — the bot skips it and continues walking the ranking.
	observeOnlyFn func(pair string) bool

	// autoPauseMaxConsecutiveLosses is the consecutive-loss count that triggers
	// an automatic pair blacklist. 0 or negative = disabled.
	autoPauseMaxConsecutiveLosses int

	// exitSignalWindows is a per-pair sliding window of recent AI signal
	// observations used by the signal-driven exit and confidence-decay exit
	// strategies inside checkPosition.
	exitSignalWindows   map[string][]exitSignalRecord
	exitSignalWindowsMu sync.Mutex
	// slTightenedBySignal tracks the last time we tightened the SL for a pair
	// due to a reversal signal, so we don't spam tighten every tick.
	slTightenedBySignal map[string]time.Time

	// Smart Order Routing (DM-6.1) — GATED, default OFF. When sorEnabled is true
	// and sorRouter is non-nil, the bot consults the router for the best venue
	// before executing and routes to it. When disabled/nil it is a strict no-op
	// and execution behavior is unchanged.
	sorRouter  bestVenueRouter
	sorEnabled bool

	// MU-2: Per-user isolation. When userID is non-empty the bot belongs to a
	// specific multi-user account; session carries its isolated credentials so
	// account-touching operations use the *ForUser gctclient methods instead of
	// the global singleton. Empty userID → legacy single-bot path (unchanged).
	userID  string
	session *gct.ExchangeSession

	// onErrorFn is an optional callback invoked by incrementErrorCount when the bot
	// records a runtime failure. BotManager wires this to BotManager.RecordFailure
	// so the per-user circuit breaker is incremented automatically. It is always
	// called with the effective user ID (never empty — defaults to "default" for
	// the legacy single-bot path).
	onErrorFn func(userID string)
}

// SetSORRouter installs the optional smart-order-routing surface. Has no effect
// unless SOR is also enabled via SetSOREnabled.
func (bot *AITradingBot) SetSORRouter(r bestVenueRouter) {
	bot.sorRouter = r
}

// SetSOREnabled toggles smart-order routing. Default is false (no behavior
// change). Even when enabled it only redirects the target exchange when the
// router returns a venue without error.
func (bot *AITradingBot) SetSOREnabled(enabled bool) {
	bot.sorEnabled = enabled
}

// WithUserSession attaches per-user identity and isolated credentials to the
// bot. Call this immediately after NewAITradingBot when constructing a user-
// specific bot via BotManager. Safe to call with empty userID to retain the
// legacy single-bot (global credential) path.
func (bot *AITradingBot) WithUserSession(userID string, session *gct.ExchangeSession) {
	bot.userID = userID
	bot.session = session
}

// effectiveUserID returns the user identifier for coordinator scoping.
// Defaults to "default" for the legacy single-bot path.
func (bot *AITradingBot) effectiveUserID() string {
	if bot.userID != "" {
		return bot.userID
	}
	return "default"
}

// TradingBotConfig holds bot configuration
type TradingBotConfig struct {
	// Trading parameters
	Pair                string   `json:"pair"`
	Exchange            string   `json:"exchange"`
	MarketType          string   `json:"market_type,omitempty"`    // spot, futures, margin, options
	ModeSelection       string   `json:"mode_selection,omitempty"` // auto | manual | hybrid
	Timeframes          []string `json:"timeframes"`
	ConfidenceThreshold float64  `json:"confidence_threshold"`
	// MinConsecutiveSignals is the anti-whipsaw entry gate: the same-direction,
	// above-threshold signal must persist for this many consecutive analysis
	// ticks before a position is opened. 0 → default of 2; 1 → gate disabled.
	// In ranging markets the multi-timeframe trend alignment flips tick-to-tick
	// (observed: TON SELL→WAIT→BUY in ~90s), so a single-tick spike is noise.
	MinConsecutiveSignals int     `json:"min_consecutive_signals,omitempty"`
	TradeSize             float64 `json:"trade_size_usd"`
	Leverage              int     `json:"leverage,omitempty"` // Optional, only for futures/margin

	// Timing
	AnalysisInterval time.Duration `json:"analysis_interval"`
	MonitorInterval  time.Duration `json:"monitor_interval"`

	// Safety limits
	MaxDailyLoss            float64 `json:"max_daily_loss_usd"`
	MaxDailyLossPercent     float64 `json:"max_daily_loss_percent"`     // Max daily loss as % of portfolio
	MaxPortfolioRiskPercent float64 `json:"max_portfolio_risk_percent"` // Max portfolio risk as %
	MaxDailyTrades          int     `json:"max_daily_trades"`
	MaxConsecutiveErrors    int     `json:"max_consecutive_errors"`
	MaxOpenPositions        int     `json:"max_open_positions"`

	// Circuit breaker
	EnableCircuitBreaker bool          `json:"enable_circuit_breaker"`
	CircuitBreakerWindow time.Duration `json:"circuit_breaker_window"`
	CircuitBreakerLimit  int           `json:"circuit_breaker_limit"`

	// Auto-execution
	AutoExecute bool `json:"auto_execute"`
	DryRun      bool `json:"dry_run"` // Test mode without real orders

	// Autonomous mode
	AutonomousMode       bool    `json:"autonomous_mode"`         // Enable full autonomous trading with function calling
	AllowToolExecution   bool    `json:"allow_tool_execution"`    // Allow bot to execute MCP tools
	MaxToolIterations    int     `json:"max_tool_iterations"`     // Max iterations for function calling
	AutoSetSLTP          bool    `json:"auto_set_sltp"`           // Automatically set SL/TP on positions
	DefaultStopLossPct   float64 `json:"default_stop_loss_pct"`   // Default stop loss percentage
	DefaultTakeProfitPct float64 `json:"default_take_profit_pct"` // Default take profit percentage

	// Advanced TP/SL features (DYNAMIC)
	TPLevels        []config.TPLevel        `json:"tp_levels,omitempty"`        // Multi-level take profit
	BreakevenConfig *config.BreakevenConfig `json:"breakeven_config,omitempty"` // Breakeven automation
	TrailingConfig  *config.TrailingConfig  `json:"trailing_config,omitempty"`  // Trailing stop

	// Post-Close Behavior (DYNAMIC)
	PostCloseReanalysis    bool `json:"post_close_reanalysis"`
	ReanalysisDelaySeconds int  `json:"reanalysis_delay_seconds"`
	MinConfluenceFactors   int  `json:"min_confluence_factors"`
	QuickReentryEnabled    bool `json:"quick_reentry_enabled"`
	MaxReentrySeconds      int  `json:"max_reentry_seconds"`

	// Direction Switching (DYNAMIC)
	DirectionSwitchingEnabled bool `json:"direction_switching_enabled"`
	MinSwitchIntervalMinutes  int  `json:"min_switch_interval_minutes"`
	AutoCloseOpposite         bool `json:"auto_close_opposite"`
	SwitchCooldownMinutes     int  `json:"switch_cooldown_minutes"`

	// Short selling & macro filter (DYNAMIC)
	AllowShortSelling             bool    `json:"allow_short_selling"`              // Enable SELL/SHORT entries
	MacroFilterEnabled            bool    `json:"macro_filter_enabled"`             // Toggle macro trend filter
	MacroBearishStrengthThreshold float64 `json:"macro_bearish_strength_threshold"` // 0–1, default 0.6
	MacroBullishStrengthThreshold float64 `json:"macro_bullish_strength_threshold"` // 0–1, default 0.6
	CounterTrendFilterEnabled     bool    `json:"counter_trend_filter_enabled"`     // Block trades against macro

	// Market condition pausing
	PauseOnAdverseConditions bool     `json:"pause_on_adverse_conditions"`
	AdverseConditions        []string `json:"adverse_conditions"` // e.g., ["high_volatility", "btc_dump"]
	AvoidWeekends            bool     `json:"avoid_weekends"`     // Skip trading on weekends (lower liquidity)

	// Time-based exit
	MaxPositionDurationHours float64 `json:"max_position_duration_hours" yaml:"max_position_duration_hours"`

	// Confidence-based dynamic sizing (DYNAMIC, hot-reloadable)
	// When DynamicSizing.Enabled is true and Tiers is non-empty, executeTrade
	// picks the matching tier based on decision.Confidence and overrides
	// TradeSize / DefaultStopLossPct / DefaultTakeProfitPct for that trade only.
	DynamicSizing DynamicSizingConfig `json:"dynamic_sizing,omitempty"`

	// Partial take-profit scale-out ladder. When enabled, checkPosition fires
	// reduce-only market orders at each level before the final TP.
	PartialTP PartialTPConfig `json:"partial_tp,omitempty"`

	// PairSelection: route the bot to the highest-scored pair from the master
	// agent on each cycle (only when no position is open).
	PairSelection PairSelectionConfig `json:"pair_selection,omitempty"`
}

// SizingTier defines per-tier sizing/SL/TP triggered by signal confidence.
type SizingTier struct {
	Name          string  `json:"name"`
	MinConfidence float64 `json:"min_confidence"` // 0.0 - 1.0
	MaxConfidence float64 `json:"max_confidence"` // 0.0 - 1.0
	SizeUSD       float64 `json:"size_usd"`
	// SizePct is the tier notional as % of the account capital, used when
	// bot.sizing.mode=capital_pct. A tier without it is converted once from
	// SizeUSD against the capital at the time of the first use.
	SizePct       float64 `json:"size_pct"`
	TakeProfitPct float64 `json:"take_profit_pct"`
	StopLossPct   float64 `json:"stop_loss_pct"`
}

// DynamicSizingConfig toggles confidence-based sizing and holds the tier ladder.
type DynamicSizingConfig struct {
	Enabled bool         `json:"enabled"`
	Tiers   []SizingTier `json:"tiers"`
}

// PartialTPLevel defines one scale-out step. When the position reaches TPPct
// (price-move × leverage), the bot closes ClosePct% of the current remaining
// size via a reduce-only market order. MoveSLToBreakeven shifts the SL to
// entry once this level fires.
type PartialTPLevel struct {
	TPPct             float64 `json:"tp_pct"`
	ClosePct          float64 `json:"close_pct"`
	MoveSLToBreakeven bool    `json:"move_sl_to_breakeven"`
}

// PartialTPConfig holds the scale-out ladder. Disabled or empty levels → no-op.
type PartialTPConfig struct {
	Enabled bool             `json:"enabled"`
	Levels  []PartialTPLevel `json:"levels"`
}

// PairSelectionConfig enables smart pair switching driven by the master agent.
type PairSelectionConfig struct {
	Enabled               bool    `json:"enabled"`
	MinScore              float64 `json:"min_score"`
	SwitchCooldownSeconds int     `json:"switch_cooldown_seconds"`
	FallbackPair          string  `json:"fallback_pair"`
}

// RankedPair is the minimum surface the bot needs from the master agent
// to pick the next pair: a symbol and its score. Implementations live in main.go.
type RankedPair struct {
	Pair  string
	Score float64
	// Exchange labels which exchange surfaced this opportunity. Additive: empty
	// for single-exchange setups; populated when scanning a multi-exchange matrix.
	Exchange string
}

// PairProvider abstracts the master agent so the bot can stay decoupled from
// the master package. Implementations should return opportunities sorted by
// score descending (highest first).
type PairProvider interface {
	TopOpportunities() []RankedPair
}

// DefaultTradingBotConfig returns safe default configuration
func DefaultTradingBotConfig() *TradingBotConfig {
	return &TradingBotConfig{
		Pair:                    "DOGE-USDT",
		Exchange:                "kucoin",
		Timeframes:              []string{"5m", "15m"},
		ConfidenceThreshold:     0.70, // 70% minimum confidence
		MinConsecutiveSignals:   2,    // anti-whipsaw: require 2 consecutive same-direction signals
		TradeSize:               3.0,  // $3 USD per trade
		Leverage:                5,    // 5x leverage
		AnalysisInterval:        30 * time.Second,
		MonitorInterval:         10 * time.Second,
		MaxDailyLoss:            100.0, // dev mode default — override in config.yaml
		MaxDailyLossPercent:     5.0,   // 5% max daily loss as % of portfolio
		MaxPortfolioRiskPercent: 20.0,  // 20% max portfolio risk
		MaxDailyTrades:          20,    // Max 20 trades per day
		MaxConsecutiveErrors:    3,     // Stop after 3 consecutive errors
		MaxOpenPositions:        2,     // Max 2 positions open
		EnableCircuitBreaker:    true,
		CircuitBreakerWindow:    5 * time.Minute,
		CircuitBreakerLimit:     5,     // Max 5 errors in 5 minutes
		AutoExecute:             false, // Require explicit enable
		DryRun:                  true,  // Start in test mode

		// Autonomous mode defaults
		AutonomousMode:           false, // Disabled by default
		AutoSetSLTP:              true,  // Auto set SL/TP when enabled
		DefaultStopLossPct:       3.0,   // 3% default stop loss
		DefaultTakeProfitPct:     6.0,   // 6% default take profit (2:1 ratio)
		PauseOnAdverseConditions: true,  // Pause on adverse conditions
		AdverseConditions:        []string{"high_volatility", "btc_dump", "low_liquidity"},

		// Short selling / macro filter defaults
		AllowShortSelling:             true,
		MacroFilterEnabled:            true,
		MacroBearishStrengthThreshold: 0.6,
		MacroBullishStrengthThreshold: 0.6,
		CounterTrendFilterEnabled:     true,

		// Time-based exit default
		MaxPositionDurationHours: 24.0,
	}
}

// ExecutionResult holds result of trade execution
type ExecutionResult struct {
	Timestamp  time.Time
	Action     string
	Confidence float64
	OrderID    string
	Success    bool
	Error      error
	PnL        float64
	// Tag identifies the operator call waiting for this result (manual entries).
	Tag string
}

// NewAITradingBot creates a new AI trading bot
func NewAITradingBot(
	contextBuilder *ai.ContextBuilder,
	decisionEngine *ai.DecisionEngine,
	positionManager *ai.PositionManager,
	riskCalculator *ai.RiskCalculator,
	sltpExecutor SLTPExecutor,
	gctClient gct.GCTClient,
	llmAnalyzer *llm.Analyzer,
	notificationManager NotificationManager,
	learningSystem *ai.SLTPLearningSystem,
	autoConfigurator AutoConfigurator,
	config *TradingBotConfig,
	log *logrus.Logger,
) *AITradingBot {
	if config == nil {
		config = DefaultTradingBotConfig()
	}

	bot := &AITradingBot{
		contextBuilder:      contextBuilder,
		decisionEngine:      decisionEngine,
		positionManager:     positionManager,
		riskCalculator:      riskCalculator,
		sltpExecutor:        sltpExecutor,
		gctClient:           gctClient,
		llmAnalyzer:         llmAnalyzer,
		notificationManager: notificationManager,
		learningSystem:      learningSystem,
		autoConfigurator:    autoConfigurator,
		config:              config,
		log:                 log,
		modeResolver:        mode.NewResolver(log, nil), // Wave 2: pass TradingModeRepository via SetModeRepo if available
		entryStreaks:        make(map[string]*signalStreak),
		exitSignalWindows:   make(map[string][]exitSignalRecord),
		slTightenedBySignal: make(map[string]time.Time),
		pairCooldowns:       make(map[string]time.Time),
		staleSignalCounts:   make(map[string]int),
		trendBlockCounts:    make(map[string]int),
		macroTrendCache:     make(map[string]macroTrendEntry),
		exitLadderCache:     make(map[string]exitLadderCacheEntry),
		reentryHolds:        make(map[string]bool),
		stopChan:            make(chan struct{}),
		pauseChan:           make(chan bool, 1),
		resultChan:          make(chan *ExecutionResult, 100),
		lastResetDate:       time.Now(),
	}
	if gctClient != nil {
		bot.accountCapitalFn = bot.gctAccountCapital
	}
	return bot
}

// SetOnError wires an error callback into the bot. The callback is invoked with
// the bot's effective user ID on every call to incrementErrorCount. BotManager
// uses this to forward runtime failures to its per-user circuit breaker
// (RecordFailure). Safe to call with nil to remove the callback.
func (bot *AITradingBot) SetOnError(fn func(userID string)) {
	bot.onErrorFn = fn
}

// SetTuning wires a TuningInterface into the bot. Safe to call with nil to
// detach an existing store (reverts to hardcoded fallbacks).
func (bot *AITradingBot) SetTuning(t ai.TuningInterface) {
	bot.tuning = t
}

// SetConfigStore wires the dynamic ConfigStore into the bot (MU-6).
// When non-nil, the bot constructs per-user scope chains so dynamic overrides
// (breakeven.trigger_pct, dynamic_sizing.tiers, etc.) resolve above persisted YAML config.
// Safe to call with nil to disable per-user dynamic scoping.
func (bot *AITradingBot) SetConfigStore(store *dynamic.ConfigStore) {
	bot.configStore = store
}

// SetRiskSentinel wires the central RiskSentinel (Ring0 kill switch / Ring1
// spec freeze / Ring2 tenant isolation) into the bot. BOT-02 fix: pass the
// same *risk.Sentinel instance constructed in main.go and registered with
// risk.RegisterAll(), so `activate_kill_switch` actually stops autonomous
// bots, not just MCP-tool-driven order submission.
func (bot *AITradingBot) SetRiskSentinel(sentinel internalRisk.RiskSentinel) {
	bot.riskSentinel = sentinel
}

// SetValidationService wires the AI trade-validation service (PreTradeValidator
// + rule engine + circuit breaker) into the bot's real execution path
// (AI-02 fix).
func (bot *AITradingBot) SetValidationService(svc aivalidation.Service) {
	bot.validationSvc = svc
}

// confidenceBucket maps the bot's 0-1 confidence score to the validation
// package's HIGH/MEDIUM/LOW enum.
func confidenceBucket(confidence float64) string {
	switch {
	case confidence >= 0.75:
		return "HIGH"
	case confidence >= 0.5:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// runPreTradeValidation calls ValidationService.ValidateTrade (PreTradeValidator
// + rule engine + circuit breaker, AI-02 fix) against the proposed trade before
// it is submitted. Returns ok=false when validation fails or errors (fail-closed
// on an error — a validator that cannot run is not evidence a trade is safe).
// A nil validationSvc (not wired) is treated as ok=true so bots built without
// it (tests, or before main.go wires it) are unaffected; production wiring in
// main.go is what closes the real gap the audit identified.
func (bot *AITradingBot) runPreTradeValidation(decision *ai.Decision, marketCtx *ai.MarketContext, pair, exchange string, tradeSize float64, leverage int) (ok bool, reason string) {
	if bot.validationSvc == nil {
		return true, ""
	}
	// Hot-reload the validation.* limits before judging this trade.
	bot.applyValidationThresholds(context.Background())

	direction := "LONG"
	if strings.ToUpper(decision.Action) == "SELL" {
		direction = "SHORT"
	}

	// Real account capital: the validator sizes risk and margin against it.
	// Without it we cannot know the trade is safe, so block (fail-closed).
	totalCapital, availableCapital, err := bot.accountCapital(context.Background())
	if err != nil {
		bot.log.WithError(err).Warn("⚠️ AI-02: account capital unavailable — blocking trade (fail-closed)")
		return false, fmt.Sprintf("account capital unavailable: %v", err)
	}

	riskAmount := 0.0
	riskPercent := 0.0
	rrRatio := 0.0
	stopLossPercent := 0.0
	if decision.Entry > 0 && decision.StopLoss > 0 {
		stopLossPercent = math.Abs(decision.Entry-decision.StopLoss) / decision.Entry * 100
		perUnitRisk := decision.Entry - decision.StopLoss
		if direction == "SHORT" {
			perUnitRisk = decision.StopLoss - decision.Entry
		}
		if perUnitRisk > 0 {
			riskAmount = (perUnitRisk / decision.Entry) * tradeSize
			// Risk as % of account capital (the unit of MaxRiskPerTrade and
			// the MAX_RISK_PER_TRADE rule), not the stop distance in price.
			riskPercent = riskAmount / totalCapital * 100
		}
		if decision.TakeProfit > 0 {
			perUnitReward := decision.TakeProfit - decision.Entry
			if direction == "SHORT" {
				perUnitReward = decision.Entry - decision.TakeProfit
			}
			if perUnitRisk > 0 {
				rrRatio = perUnitReward / perUnitRisk
			}
		}
	}

	confluenceCount, confluenceFactors := bot.countConfluenceFactors(marketCtx, decision)
	if isManualEntry(decision) {
		// The operator's explicit order stands in for signal confluence; the
		// risk, capital, R:R, rule-engine and circuit-breaker checks still run.
		confluenceCount += manualEntryConfluenceCredit
		confluenceFactors = append(confluenceFactors, "operator_manual_entry")
	}

	trade := &aivalidation.TradingDecision{
		ID:                fmt.Sprintf("%s-%d", pair, time.Now().UnixNano()),
		Timestamp:         time.Now(),
		Pair:              pair,
		Direction:         direction,
		EntryPrice:        decision.Entry,
		StopLoss:          decision.StopLoss,
		TakeProfit:        decision.TakeProfit,
		PositionSize:      tradeSize,
		RiskAmount:        riskAmount,
		RiskPercent:       roundPct(riskPercent),
		RiskRewardRatio:   roundPct(rrRatio),
		Leverage:          float64(leverage),
		StopLossPercent:   roundPct(stopLossPercent),
		Confidence:        confidenceBucket(decision.Confidence),
		Reasoning:         decision.Reason,
		ConfluenceCount:   confluenceCount,
		ConfluenceFactors: confluenceFactors,
	}

	bot.mu.RLock()
	dailyLoss := bot.dailyLoss
	consecutiveLosses := bot.consecutiveLosses
	bot.mu.RUnlock()
	openPositions := len(bot.positionManager.GetAllPositions())

	portfolio := &aivalidation.PortfolioState{
		TotalCapital:      totalCapital,
		AvailableCapital:  availableCapital,
		PeakCapital:       totalCapital, // no drawdown tracking bot-side; keeps the drawdown ratio defined
		DailyLoss:         -dailyLoss,   // bot.dailyLoss is negative when losing; validator expects a positive loss magnitude
		ConsecutiveLosses: consecutiveLosses,
		OpenPositions:     openPositions,
		TotalRiskExposed:  riskAmount,
	}
	if portfolio.DailyLoss < 0 {
		portfolio.DailyLoss = 0
	}

	result, err := bot.validationSvc.ValidateTrade(context.Background(), trade, portfolio)
	if err != nil {
		bot.log.WithError(err).Warn("⚠️ AI-02: pre-trade validation errored — blocking trade (fail-closed)")
		return false, fmt.Sprintf("validation error: %v", err)
	}
	if !result.Passed {
		var msgs []string
		for _, f := range result.CriticalFailures {
			msgs = append(msgs, fmt.Sprintf("%s: %s", f.RuleID, f.Message))
		}
		bot.log.WithFields(logrus.Fields{
			"pair":       pair,
			"violations": msgs,
		}).Warn("🚫 AI-02: pre-trade validation failed — blocking trade")
		return false, strings.Join(msgs, "; ")
	}
	return true, ""
}

// accountCapitalCacheTTL bounds how often the pre-trade validation refreshes
// the account capital; accountCapitalStaleMax is how old a cached value may be
// when the refresh itself fails.
const (
	accountCapitalCacheTTL  = 60 * time.Second
	accountCapitalStaleMax  = 10 * time.Minute
	accountCapitalCallLimit = 5 * time.Second
)

// accountCapital returns the account's total and available USDT, refreshed at
// most every accountCapitalCacheTTL. When the source fails it falls back to a
// value no older than accountCapitalStaleMax, otherwise it returns the error.
func (bot *AITradingBot) accountCapital(ctx context.Context) (total, available float64, err error) {
	if bot.accountCapitalFn == nil {
		return 0, 0, fmt.Errorf("no account capital source wired")
	}
	bot.capitalMu.Lock()
	defer bot.capitalMu.Unlock()
	if !bot.capitalAt.IsZero() && time.Since(bot.capitalAt) < accountCapitalCacheTTL {
		return bot.capitalTotal, bot.capitalAvailable, nil
	}
	cctx, cancel := context.WithTimeout(ctx, accountCapitalCallLimit)
	defer cancel()
	total, available, err = bot.accountCapitalFn(cctx)
	if err == nil && total > 0 {
		bot.capitalTotal, bot.capitalAvailable, bot.capitalAt = total, available, time.Now()
		return total, available, nil
	}
	if err == nil {
		err = fmt.Errorf("account capital reported as %.4f USDT", total)
	}
	if !bot.capitalAt.IsZero() && time.Since(bot.capitalAt) < accountCapitalStaleMax {
		bot.log.WithError(err).Warn("AI-02: capital refresh failed, using last known value")
		return bot.capitalTotal, bot.capitalAvailable, nil
	}
	return 0, 0, err
}

// gctAccountCapital reads the account from the GCT portfolio. Total is the
// portfolio's USDT value (the futures margin on the KuCoin REST path) and
// available is the free USDT across the USDT holdings.
func (bot *AITradingBot) gctAccountCapital(ctx context.Context) (total, available float64, err error) {
	portfolio, err := bot.gctClient.GetPortfolio(ctx)
	if err != nil {
		return 0, 0, err
	}
	if portfolio == nil {
		return 0, 0, fmt.Errorf("empty portfolio")
	}
	total = portfolio.TotalValue
	usdtTotal := 0.0
	for key, h := range portfolio.Holdings {
		if h == nil {
			continue
		}
		if strings.EqualFold(h.Currency, "USDT") || strings.HasPrefix(strings.ToUpper(key), "USDT") {
			available += h.Available
			usdtTotal += h.Total
		}
	}
	if total <= 0 {
		total = usdtTotal
	}
	return total, available, nil
}

// killSwitchActive reports whether the central RiskSentinel's Ring0 kill
// switch is currently active. Returns false (fail-open at this specific
// check) when no sentinel has been wired, since callers of this bot without
// a sentinel predate BOT-02 and must not silently stop working; production
// wiring in main.go is what closes the real gap.
func (bot *AITradingBot) killSwitchActive(ctx context.Context) bool {
	if bot.riskSentinel == nil {
		return false
	}
	state, err := bot.riskSentinel.GetRiskState(ctx)
	if err != nil {
		bot.log.WithError(err).Warn("⚠️ RiskSentinel.GetRiskState failed — cannot confirm kill switch is clear, blocking cycle to be safe")
		return true
	}
	return state.KillSwitchActive
}

// SetWickLearner wires the Layer-3 wick feedback learner into the bot.
// When set, the bot passes wick-triggered exits to the learner for outcome
// verification and threshold auto-tuning.
func (bot *AITradingBot) SetWickLearner(wl *learning.WickLearner) {
	bot.wickLearner = wl
}

// wickThreshold returns the current wick/body ratio threshold. It reads from
// the WickLearner when available, or falls back to the default of 1.5.
func (bot *AITradingBot) wickThreshold() float64 {
	if bot.wickLearner != nil {
		return bot.wickLearner.GetThreshold()
	}
	return 1.5
}

// SetVolatilityClassifier wires the dynamic per-pair volatility classifier.
// When set, applyVolatilityProtection uses the live tier from this classifier
// instead of the legacy hardcoded volatile-pair list.
func (bot *AITradingBot) SetVolatilityClassifier(vc *volatility.Classifier) {
	bot.volClassifier = vc
}

// GetVolatilityClassifier returns the wired classifier (or nil). Used by the
// multi-pair orchestrator to propagate the same classifier instance to all
// spawned bots so volatility data is collected across the whole fleet.
func (bot *AITradingBot) GetVolatilityClassifier() *volatility.Classifier {
	return bot.volClassifier
}

// GetWickLearner returns the wired wick-exit learner (or nil). Used by the
// multi-pair orchestrator to propagate the learner to spawned bots.
func (bot *AITradingBot) GetWickLearner() *learning.WickLearner {
	return bot.wickLearner
}

// SampleATRPct implements volatility.ATRSampler so the classifier's background
// refresh loop can read the bot's latest indicator cache. Returns ok=false when
// no candle snapshot is available yet (early lifecycle or unanalysed pair).
func (bot *AITradingBot) SampleATRPct(pair string) (float64, bool) {
	bot.indicatorCacheMu.RLock()
	defer bot.indicatorCacheMu.RUnlock()
	snap, exists := bot.indicatorCache[pair]
	if !exists || snap == nil || snap.ATR <= 0 || snap.CurrentPrice <= 0 {
		return 0, false
	}
	return (snap.ATR / snap.CurrentPrice) * 100, true
}

// getTuning returns the value for key from the tuning store, or fallback when
// no store is configured.
func (bot *AITradingBot) getTuning(key string, fallback float64) float64 {
	if bot.tuning == nil {
		return fallback
	}
	return bot.tuning.Get(key, fallback)
}

// UpdateConfigFromStruct updates the bot's configuration at runtime from a full config struct (Hot-Reload).
// Used by the config file watcher. For API-level partial updates use UpdateConfig.
func (bot *AITradingBot) UpdateConfigFromStruct(newConfig *TradingBotConfig) {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	// Update specific dynamic fields while preserving state
	bot.config.DryRun = newConfig.DryRun
	bot.config.AutoExecute = newConfig.AutoExecute
	bot.config.AutonomousMode = newConfig.AutonomousMode
	bot.config.TradeSize = newConfig.TradeSize
	bot.config.Leverage = newConfig.Leverage
	bot.config.ConfidenceThreshold = newConfig.ConfidenceThreshold
	bot.config.MaxDailyLoss = newConfig.MaxDailyLoss
	bot.config.MaxOpenPositions = newConfig.MaxOpenPositions
	bot.config.DefaultStopLossPct = newConfig.DefaultStopLossPct
	bot.config.DefaultTakeProfitPct = newConfig.DefaultTakeProfitPct

	if newConfig.AnalysisInterval > 0 {
		bot.config.AnalysisInterval = newConfig.AnalysisInterval
	}
	if newConfig.MonitorInterval > 0 {
		bot.config.MonitorInterval = newConfig.MonitorInterval
	}

	// Propagate direction/macro fields to DecisionEngine
	if newConfig.MacroBearishStrengthThreshold > 0 {
		bot.config.MacroBearishStrengthThreshold = newConfig.MacroBearishStrengthThreshold
	}
	if newConfig.MacroBullishStrengthThreshold > 0 {
		bot.config.MacroBullishStrengthThreshold = newConfig.MacroBullishStrengthThreshold
	}
	bot.config.AllowShortSelling = newConfig.AllowShortSelling
	bot.config.MacroFilterEnabled = newConfig.MacroFilterEnabled
	bot.config.CounterTrendFilterEnabled = newConfig.CounterTrendFilterEnabled

	// Replace dynamic sizing tiers wholesale — operators edit the YAML to retune.
	bot.config.DynamicSizing = newConfig.DynamicSizing
	if bot.config.DynamicSizing.Enabled && len(bot.config.DynamicSizing.Tiers) > 0 {
		bot.log.WithField("tiers", len(bot.config.DynamicSizing.Tiers)).Info("📊 Dynamic sizing active")
	}

	bot.config.PartialTP = newConfig.PartialTP
	if bot.config.PartialTP.Enabled && len(bot.config.PartialTP.Levels) > 0 {
		bot.log.WithField("levels", len(bot.config.PartialTP.Levels)).Info("🪜 Partial take-profit active")
	}

	bot.config.PairSelection = newConfig.PairSelection
	if bot.config.PairSelection.Enabled {
		bot.log.WithFields(logrus.Fields{
			"min_score":        bot.config.PairSelection.MinScore,
			"cooldown_seconds": bot.config.PairSelection.SwitchCooldownSeconds,
		}).Info("🎯 Smart pair selection active")
	}

	// Breakeven and trailing have pointer types — copy them when present so
	// operators can retune trigger/buffer/distance without a service restart.
	// Existing in-memory positions keep their already-applied breakeven state.
	if newConfig.BreakevenConfig != nil {
		bot.config.BreakevenConfig = newConfig.BreakevenConfig
		bot.log.WithFields(logrus.Fields{
			"trigger_pct": newConfig.BreakevenConfig.TriggerPct,
			"buffer_pct":  newConfig.BreakevenConfig.BufferPct,
		}).Info("🛡️ Breakeven config updated")
	}
	if newConfig.TrailingConfig != nil {
		bot.config.TrailingConfig = newConfig.TrailingConfig
		bot.log.WithFields(logrus.Fields{
			"distance_pct": newConfig.TrailingConfig.DistancePct,
		}).Info("📐 Trailing config updated")
	}

	bot.syncDecisionEngineConfig()

	bot.log.WithFields(logrus.Fields{
		"dry_run":              bot.config.DryRun,
		"auto_execute":         bot.config.AutoExecute,
		"trade_size":           bot.config.TradeSize,
		"leverage":             bot.config.Leverage,
		"sl_pct":               bot.config.DefaultStopLossPct,
		"allow_short_selling":  bot.config.AllowShortSelling,
		"macro_filter_enabled": bot.config.MacroFilterEnabled,
		"macro_bearish_thresh": bot.config.MacroBearishStrengthThreshold,
	}).Info("Bot configuration updated (Hot-Reload)")
}

// syncDecisionEngineConfig pushes direction/macro settings from bot config into the DecisionEngine.
// Must be called while holding bot.mu (write lock).
func (bot *AITradingBot) syncDecisionEngineConfig() {
	if bot.decisionEngine == nil {
		return
	}
	bot.decisionEngine.UpdateDecisionConfig(&ai.DecisionConfig{
		ConfidenceThreshold:           bot.config.ConfidenceThreshold,
		RequireAlignment:              true,
		MinTimeframesAligned:          2,
		TradeSize:                     bot.config.TradeSize,
		Leverage:                      bot.config.Leverage,
		AllowShortSelling:             bot.config.AllowShortSelling,
		MacroFilterEnabled:            bot.config.MacroFilterEnabled,
		MacroBearishStrengthThreshold: bot.config.MacroBearishStrengthThreshold,
		MacroBullishStrengthThreshold: bot.config.MacroBullishStrengthThreshold,
		CounterTrendFilterEnabled:     bot.config.CounterTrendFilterEnabled,
	})
}

// Start starts the trading bot (convenience method without context)
func (bot *AITradingBot) Start() error {
	return bot.StartWithContext(context.Background())
}

// StartWithContext starts the trading bot with context
func (bot *AITradingBot) StartWithContext(ctx context.Context) error {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if bot.running {
		return fmt.Errorf("bot is already running")
	}

	// Safety checks
	if !bot.config.DryRun && !bot.config.AutoExecute {
		return fmt.Errorf("auto_execute must be true for live trading (or use dry_run mode)")
	}

	if bot.config.TradeSize <= 0 {
		return fmt.Errorf("trade_size must be positive")
	}

	if bot.config.ConfidenceThreshold < 0.0 || bot.config.ConfidenceThreshold > 1.0 {
		return fmt.Errorf("confidence_threshold must be between 0.0 and 1.0")
	}

	// Verify GCT connection
	if !bot.gctClient.IsConnected() {
		return fmt.Errorf("GoCryptoTrader is not connected")
	}

	// Claim pair in cross-component coordinator to prevent scalping bots from
	// starting on the same pair while this bot is active.
	// MU-2: use per-user claim so concurrent users don't block each other.
	if bot.pairCoord != nil && bot.config.Pair != "" {
		uid := bot.effectiveUserID()
		if !bot.pairCoord.TryClaimForUser(uid, bot.config.Pair, "ai_bot") {
			owner := bot.pairCoord.OwnedByForUser(uid, bot.config.Pair)
			return fmt.Errorf("pair %s is already active in another component (%s)", bot.config.Pair, owner)
		}
	}

	bot.running = true
	bot.paused = false
	bot.startedAt = time.Now()
	bot.emergencyStop = false
	bot.stopChan = make(chan struct{}) // Re-allocate channel for multiple start/stop cycles
	bot.stopOnce = sync.Once{}         // Reset so the new stopChan can be closed exactly once

	bot.log.WithFields(logrus.Fields{
		"pair":                 bot.config.Pair,
		"exchange":             bot.config.Exchange,
		"confidence_threshold": bot.config.ConfidenceThreshold,
		"trade_size":           bot.config.TradeSize,
		"leverage":             bot.config.Leverage,
		"dry_run":              bot.config.DryRun,
		"auto_execute":         bot.config.AutoExecute,
	}).Info("🤖 AI Trading Bot starting...")

	// Load existing positions from exchange before starting loops (sync, not goroutine)
	if !bot.config.DryRun {
		bot.log.Info("🔄 Synchronizing open positions from exchange...")
		// The REST client backing GetFuturesPositions here is a different (lazily
		// created) instance from the credentialed monitoring session, and it can
		// transiently return 0 for the first seconds after start. Running the sync
		// once at that moment left real open positions unmanaged (no trailing /
		// breakeven / exit fusion) until the next periodic re-sync ~minutes later.
		// Briefly poll until the exchange reports positions (or a short timeout) so
		// adoption happens promptly on restart; an account that is genuinely flat
		// just falls through after the timeout.
		for attempt := 0; attempt < 5; attempt++ {
			if ps, err := bot.gctClient.GetFuturesPositions(context.Background(), bot.config.Exchange); err == nil && len(ps) > 0 {
				break
			}
			if attempt < 4 {
				time.Sleep(6 * time.Second)
			}
		}
		// Run the initial sync in a goroutine: StartWithContext holds bot.mu for its
		// whole body (defer Unlock), but syncOpenPositionsFromExchange and its callees
		// (enableTrailingStop, withPairScope, the race guard) take bot.mu.RLock — a
		// non-reentrant RWMutex self-deadlock that froze startup and left the bot a
		// zombie (loops below never launched). Running it async lets Start return and
		// release bot.mu first; syncRunMu serialises it against the monitor loop's sync.
		go bot.syncOpenPositionsFromExchange(context.Background())
	}

	// Start analysis loop
	go bot.analysisLoop(ctx)

	// Start position monitoring loop
	go bot.monitoringLoop(ctx)

	// Start result processor
	go bot.resultProcessor(ctx)

	// Start daily reset timer
	go bot.dailyResetTimer(ctx)

	// Start the exchange-truth position reconciliation loop (audit P0b). The
	// exchange is the source of truth (VISION §8.8): a position can be closed
	// externally (exchange SL / liquidation / manual) or left behind by a prior
	// process without the local manager being told. The monitor loop already
	// piggybacks a sync, but that is coupled to MonitorInterval and only runs while
	// positions exist; this DEDICATED loop guarantees reconciliation against the
	// exchange on a fixed cadence regardless of monitor activity. It only corrects
	// local state toward exchange truth (records external closes, drops phantoms,
	// adopts orphans on managed pairs via the existing scoped sync) so it is safe to
	// run ON by default; the interval and an off-switch are read from dynamic config.
	// Skipped in DryRun (no real exchange positions to reconcile against).
	if !bot.config.DryRun {
		go bot.positionReconciliationLoop(ctx)
	}

	// Start auto-configuration loop if in autonomous mode
	if bot.config.AutonomousMode {
		go bot.autoConfigurationLoop(ctx)
		bot.log.Info("🤖 Auto-configuration loop started (runs every 6 hours)")
	}

	// Portfolio-level exit engine: monitors aggregate unrealized PnL across all
	// open positions and triggers basket exits when a sharp move reverses.
	// Controlled via portfolio.exit.enabled (default false, hot-reloadable).
	go bot.portfolioExitLoop(ctx)

	bot.log.Info("✅ AI Trading Bot started successfully")

	return nil
}

// syncOpenPositionsFromExchange fetches open positions from KuCoin, reconciles
// the internal position manager (removes ghosts), and adds any missing ones.
// normalizeSymbolForMatch reduces a trading symbol to a comparable canonical form
// so that exchange-format futures symbols ("DOTUSDTM"), dash pairs ("DOT-USDT"),
// and slash pairs ("DOT/USDT") all collapse to the same key ("DOTUSDT"). The
// trailing futures "M" marker is stripped only when it follows the quote asset,
// so a base asset that legitimately ends in M (e.g. "XMR") is left intact.
func normalizeSymbolForMatch(s string) string {
	u := strings.ToUpper(strings.TrimSpace(s))
	u = strings.ReplaceAll(u, "-", "")
	u = strings.ReplaceAll(u, "/", "")
	if strings.HasSuffix(u, "USDTM") {
		u = strings.TrimSuffix(u, "M")
	}
	return u
}

// openPositionsForPair counts internal managed positions whose symbol matches the
// given pair, comparing in normalized form so "DOT-USDT" matches an adopted
// "DOTUSDTM". Used by the pair-switch gate so positions on OTHER pairs (now
// adopted and independently managed with their own exchange SL) do not block
// rotating to a fresh opportunity on the current pair.
func (bot *AITradingBot) openPositionsForPair(pair string) int {
	if bot.positionManager == nil {
		return 0
	}
	target := normalizeSymbolForMatch(pair)
	if target == "" {
		return 0
	}
	n := 0
	for _, p := range bot.positionManager.GetAllPositions() {
		if normalizeSymbolForMatch(p.Symbol) == target {
			n++
		}
	}
	return n
}

// adoptOpenGuard is how long after the bot starts opening a position on a pair
// that syncOpenPositionsFromExchange must NOT adopt that pair's exchange position
// as an orphan. The exchange reflects a market order before the open path finishes
// calling AddPosition; without this window a concurrent sync adopts the bot's own
// fresh position, duplicating it (two managed positions + two exchange stops + two
// trade_history rows for one physical position). 90s comfortably covers order
// submission, fill polling, and local registration.
const adoptOpenGuard = 90 * time.Second

// ghostMissThreshold is how many consecutive successful syncs a position must be
// absent from the exchange before it is reconciled as an external close.
const ghostMissThreshold = 2

// ── Cross-instance position ownership ────────────────────────────────────────
// The multi-pair orchestrator (ai_bot.multi_pair.enabled) runs SEVERAL AITradingBot
// instances in one process, all sharing one exchange account. Each has its own
// positionManager and openingPairs, so the per-instance race guard and
// alreadyTracked check cannot stop instance B from adopting a position instance A
// opened — that is how one physical position became 4-5 managed positions (TAO
// 2026-06-02), duplicating exchange stops and double-counting closes. This
// PROCESS-WIDE registry gives each physical position (symbol+side, which nets to
// one on futures) a single owner across all instances: the opener claims it, and
// any other instance's adoption is refused. The owner refreshes its claim each
// monitor cycle; a claim unrefreshed for positionOwnershipTTL is treated as stale
// and reclaimable, so a missed release (crash, untracked close path) self-heals
// instead of permanently blocking the pair.
const positionOwnershipTTL = 10 * time.Minute

type positionOwnerEntry struct {
	ownerID string
	at      time.Time
}

var (
	positionOwnerMu sync.Mutex
	positionOwner   = make(map[string]positionOwnerEntry)
)

func positionOwnerKey(symbol, side string) string {
	s := "short"
	if sideIsLong(side) {
		s = "long"
	}
	return normalizeSymbolForMatch(symbol) + "|" + s
}

// claimPositionOwnership tries to make posID the sole owner of the physical
// position for symbol+side across all instances. It succeeds when the slot is
// free, already held by posID, the current owner's claim is stale, or force is set
// (used by the opener, which creates the authoritative real position). Returns
// false only when another instance holds a fresh claim — in which case the caller
// must NOT adopt/track the position (it is a cross-instance duplicate).
func claimPositionOwnership(symbol, side, posID string, force bool) bool {
	key := positionOwnerKey(symbol, side)
	positionOwnerMu.Lock()
	defer positionOwnerMu.Unlock()
	e, ok := positionOwner[key]
	if force || !ok || e.ownerID == posID || time.Since(e.at) > positionOwnershipTTL {
		positionOwner[key] = positionOwnerEntry{ownerID: posID, at: time.Now()}
		return true
	}
	return false
}

// refreshPositionOwnership extends the owner's claim so a long-lived position is
// never mistaken for stale. No-op if posID is not the current owner.
func refreshPositionOwnership(symbol, side, posID string) {
	key := positionOwnerKey(symbol, side)
	positionOwnerMu.Lock()
	if e, ok := positionOwner[key]; ok && e.ownerID == posID {
		e.at = time.Now()
		positionOwner[key] = e
	}
	positionOwnerMu.Unlock()
}

// releasePositionOwnership frees the slot when the owning position closes, so the
// pair can be opened/adopted again. No-op if posID is not the current owner (a
// duplicate's close must not free the real owner's slot).
func releasePositionOwnership(symbol, side, posID string) {
	key := positionOwnerKey(symbol, side)
	positionOwnerMu.Lock()
	if e, ok := positionOwner[key]; ok && e.ownerID == posID {
		delete(positionOwner, key)
	}
	positionOwnerMu.Unlock()
}

// recordGhostMiss increments the consecutive-absence counter for an internal
// position not seen on the exchange and reports whether it has now been absent
// long enough to reconcile. When reconcile is true the counter is cleared (the
// caller will close it). Guarded by syncStateMu, NOT bot.mu — see syncStateMu.
func (bot *AITradingBot) recordGhostMiss(id string) (misses int, reconcile bool) {
	bot.syncStateMu.Lock()
	defer bot.syncStateMu.Unlock()
	if bot.ghostMissCount == nil {
		bot.ghostMissCount = make(map[string]int)
	}
	bot.ghostMissCount[id]++
	misses = bot.ghostMissCount[id]
	if misses >= ghostMissThreshold {
		delete(bot.ghostMissCount, id)
		return misses, true
	}
	return misses, false
}

// clearGhostMiss resets the absence streak for a position seen on the exchange.
func (bot *AITradingBot) clearGhostMiss(id string) {
	bot.syncStateMu.Lock()
	if bot.ghostMissCount != nil {
		delete(bot.ghostMissCount, id)
	}
	bot.syncStateMu.Unlock()
}

// externalCloseReadoptGuard is how long after registering an external close the
// adoption path refuses to re-adopt the same symbol+side. Long enough to outlive
// a flaky-fetch window; short enough that a genuinely re-opened position is
// adopted on the next sync after the guard.
const externalCloseReadoptGuard = 5 * time.Minute

// markExternalClose stamps the time an external close was registered for
// symbol+side. Guarded by syncStateMu (same domain as the other sync state).
func (bot *AITradingBot) markExternalClose(symbol, side string) {
	key := normalizeSymbolForMatch(symbol) + "|" + strings.ToLower(side)
	bot.syncStateMu.Lock()
	if bot.externalCloses == nil {
		bot.externalCloses = make(map[string]time.Time)
	}
	bot.externalCloses[key] = time.Now()
	bot.syncStateMu.Unlock()
}

// recentExternalCloseRemaining reports how much of the re-adopt guard window is
// left for symbol+side (0 when clear).
func (bot *AITradingBot) recentExternalCloseRemaining(symbol, side string) time.Duration {
	key := normalizeSymbolForMatch(symbol) + "|" + strings.ToLower(side)
	bot.syncStateMu.Lock()
	defer bot.syncStateMu.Unlock()
	t, ok := bot.externalCloses[key]
	if !ok {
		return 0
	}
	if remaining := externalCloseReadoptGuard - time.Since(t); remaining > 0 {
		return remaining
	}
	delete(bot.externalCloses, key)
	return 0
}

// markPairOpening stamps the current time for a pair the bot is about to open, so
// the adoption path can skip it during the open-vs-adopt race window.
func (bot *AITradingBot) markPairOpening(pair string) {
	key := normalizeSymbolForMatch(pair)
	if key == "" {
		return
	}
	bot.syncStateMu.Lock()
	if bot.openingPairs == nil {
		bot.openingPairs = make(map[string]time.Time)
	}
	bot.openingPairs[key] = time.Now()
	bot.syncStateMu.Unlock()
}

// pairRecentlyOpened reports whether the bot started opening a position on the
// given pair within adoptOpenGuard. Used to suppress duplicate self-adoption.
// Uses syncStateMu (NOT bot.mu) so it is safe to call from syncOpenPositionsFrom-
// Exchange, which runs while StartWithContext holds bot.mu.
func (bot *AITradingBot) pairRecentlyOpened(symbol string) bool {
	key := normalizeSymbolForMatch(symbol)
	if key == "" {
		return false
	}
	bot.syncStateMu.Lock()
	t, ok := bot.openingPairs[key]
	bot.syncStateMu.Unlock()
	return ok && time.Since(t) < adoptOpenGuard
}

// sideIsLong maps the bot's various side spellings (buy/long, sell/short) to a
// boolean so positions can be compared regardless of which vocabulary set them.
func sideIsLong(side string) bool {
	s := strings.ToUpper(strings.TrimSpace(side))
	return s == "BUY" || s == "LONG"
}

// dropDuplicateManagedPositions removes any managed position that shares the given
// normalized symbol and side but is not keepID. Used to enforce the
// single-position-per-(symbol,side) invariant when the bot opens a position that a
// prior sync may have already adopted, so one physical exchange position never maps
// to two managed positions (which would duplicate exchange stops and double-count
// the close in trade_history).
func (bot *AITradingBot) dropDuplicateManagedPositions(symbol, side, keepID string) {
	if bot.positionManager == nil {
		return
	}
	target := normalizeSymbolForMatch(symbol)
	wantLong := sideIsLong(side)
	for _, p := range bot.positionManager.GetAllPositions() {
		if p.ID == keepID {
			continue
		}
		if normalizeSymbolForMatch(p.Symbol) != target || sideIsLong(p.Side) != wantLong {
			continue
		}
		bot.log.WithFields(logrus.Fields{
			"dropped_id": p.ID,
			"kept_id":    keepID,
			"symbol":     p.Symbol,
			"side":       p.Side,
		}).Warn("Dropping duplicate managed position (single-per-symbol+side invariant) — fresh open supersedes adopted/stale copy")
		bot.positionManager.RemovePosition(p.ID)
	}
}

// adoptAllPairsEnabled reports whether cross-pair adoption is active. When true,
// the bot adopts EVERY open exchange position (not just config.Pair's) and the
// pair-switch gate only blocks on the current pair. Gated default-OFF behind the
// dynamic key bot.adopt.all_pairs.enabled so it can be A/B tested and reverted on
// a live money bot without a redeploy.
func (bot *AITradingBot) adoptAllPairsEnabled() bool {
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		return snap.Bool("bot.adopt.all_pairs.enabled")
	}
	return false
}

func (bot *AITradingBot) syncOpenPositionsFromExchange(ctx context.Context) {
	// BOT-07: this is launched directly via `go bot.syncOpenPositionsFromExchange(...)`
	// at startup, so it needs its own recover — an unrecovered panic here would
	// crash the whole process (ALL users' bots), not just this one.
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in syncOpenPositionsFromExchange - recovered")
		}
	}()

	// Serialise syncs: the async startup sync and the monitor/analysis loop syncs
	// must not run concurrently (concurrent adoption could duplicate a position).
	// If one is already running, skip this cycle — the in-flight one covers it.
	if !bot.syncRunMu.TryLock() {
		return
	}
	defer bot.syncRunMu.Unlock()

	bot.log.Info("Starting position synchronization...")

	// Use Futures Manager or GCT Client to get positions
	positions, err := bot.gctClient.GetFuturesPositions(ctx, bot.config.Exchange)
	if err != nil {
		bot.log.WithError(err).Warn("Could not sync open positions from exchange - make sure KuCoin Futures is enabled")
		return
	}

	// In dynamic pair mode (bot.config.Pair == "") all exchange positions qualify.
	// Previously this function returned early when the pair was empty to avoid a
	// matchesPair("") bug that matched everything and blocked maybeSwitchPair.
	// That early return broke position adoption after restart — orphaned positions
	// lost trailing stop, exit fusion, and MFE protection.  The fix belongs in the
	// pair-switch gate (don't count adopted multi-pair positions against the
	// single-pair max), not here.  The adopt.max_notional_usd / leverage guards
	// below already filter out foreign / oversized leftovers.
	dynamicMode := strings.TrimSpace(bot.config.Pair) == ""

	// Cross-pair adoption (Phase 1): when enabled, adopt EVERY open exchange
	// position regardless of config.Pair so positions on pairs the bot has since
	// rotated away from stay actively managed (trailing, breakeven, exit fusion)
	// instead of orphaned until the bot happens to rotate back. Safe now because
	// the monitor loop iterates GetAllPositions() and every adopted position gets
	// its own exchange SL. Gated default-OFF; falls back to single-pair matching.
	adoptAll := dynamicMode || bot.adoptAllPairsEnabled()

	// Normalize pair for comparison: "DOGE-USDT" → "DOGEUSDT" to match futures symbols like "DOGEUSDTM"
	pairNorm := strings.ReplaceAll(strings.ReplaceAll(bot.config.Pair, "-", ""), "/", "")

	matchesPair := func(symbol string) bool {
		if adoptAll {
			return true // adopt all open positions (dynamic pair mode or cross-pair adoption)
		}
		symNorm := strings.ToUpper(symbol)
		return symNorm == strings.ToUpper(bot.config.Pair) ||
			strings.HasPrefix(symNorm, strings.ToUpper(pairNorm))
	}

	// Count real positions for our pair on the exchange
	realCount := 0
	for _, pos := range positions {
		if matchesPair(pos.Symbol) {
			realCount++
		}
	}

	// Build a set of symbols currently open on the exchange for quick lookup.
	exchangeSymbols := make(map[string]bool) // key: upper(symbol)+"|"+upper(side)
	for _, pos := range positions {
		if matchesPair(pos.Symbol) {
			k := strings.ToUpper(pos.Symbol) + "|" + strings.ToUpper(pos.Side)
			exchangeSymbols[k] = true
		}
	}

	// Reconcile: any internal position not present on the exchange was silently
	// closed (exchange stop-loss fired, manual close, liquidation). Emit a proper
	// close event so daily_loss, consecutive tracking, and feedback loop stay accurate.
	internal := bot.positionManager.GetAllPositions()
	for _, p := range internal {
		// Build lookup key normalizing USDT vs USDTM symbols.
		symNorm := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(p.Symbol, "-", ""), "/", ""))
		side := strings.ToUpper(p.Side)
		found := false
		for k := range exchangeSymbols {
			kSym := strings.Split(k, "|")[0]
			kSide := strings.Split(k, "|")[1]
			if (strings.HasPrefix(kSym, symNorm) || strings.HasPrefix(symNorm, kSym)) &&
				(kSide == side || (kSide == "SHORT" && side == "SELL") || (kSide == "LONG" && side == "BUY")) {
				found = true
				break
			}
		}
		if found {
			bot.clearGhostMiss(p.ID) // present again — clear any pending miss streak
			continue
		}

		// Anti-flapping (Phase 3): require the position to be absent for two
		// CONSECUTIVE successful syncs before registering an external close. A single
		// transient or partial fetch must not invent a phantom close — that would
		// fabricate a PnL row and reset cooldowns on a position that is still open.
		// (We already returned early above on a fetch error, so reaching here means
		// the snapshot was retrieved successfully.)
		if misses, reconcile := bot.recordGhostMiss(p.ID); !reconcile {
			bot.log.WithFields(logrus.Fields{
				"position_id": p.ID,
				"symbol":      p.Symbol,
				"misses":      misses,
				"threshold":   ghostMissThreshold,
			}).Debug("Position absent from exchange — deferring reconciliation (anti-flapping)")
			continue
		}

		// AUTHORITATIVE CONFIRM (2026-06-10): the bulk positions fetch is flaky —
		// it intermittently returns an empty list with no error (observed: TAO
		// alternating 0/1 positions across syncs), which sailed past the miss
		// counter and fabricated 474 phantom "external closes" (+$1,688 fake PnL)
		// in one evening, each followed by a re-adoption and a REAL stop order
		// (KuCoin's 50-stop-order cap was hit). Before registering a close, ask
		// the exchange for THIS symbol directly; if it is still open, it was a
		// fetch flake — clear the streak and move on. If the targeted check
		// errors, defer: never fabricate a close from ambiguous data.
		if bot.gctClient != nil {
			kuSym := dashPairToKuCoinFutures(p.Symbol)
			live, perr := bot.gctClient.GetFuturesPosition(ctx, bot.config.Exchange, kuSym)
			if perr == nil && live != nil && live.Size > 0 {
				bot.clearGhostMiss(p.ID)
				bot.log.WithFields(logrus.Fields{
					"position_id": p.ID,
					"symbol":      p.Symbol,
					"size":        live.Size,
				}).Warn("Bulk position fetch missed an OPEN position — flaky snapshot, close suppressed")
				continue
			}
			if perr != nil && !errors.Is(perr, gct.ErrPositionNotFound) {
				bot.log.WithError(perr).WithFields(logrus.Fields{
					"position_id": p.ID,
					"symbol":      p.Symbol,
				}).Warn("Targeted position check failed — deferring external-close reconciliation")
				continue
			}
		}

		// Position not on exchange — it was closed externally. That can be the
		// exchange stop-loss, a liquidation, OR the operator closing it MANUALLY
		// on the exchange UI. Previously this path blindly assumed exchange_sl
		// and recorded exitPrice := p.StopLoss — a synthetic price that falsified
		// the trade record whenever the close was manual (observed 2026-06-10:
		// operator's manual close logged as exchange_sl at exactly entry×1.02).
		// Resolve the REAL exit from order history and classify the reason.
		exitPrice, exitReason := bot.resolveExternalExit(ctx, p)
		if exitPrice <= 0 {
			exitPrice = p.StopLoss
			exitReason = "exchange_sl"
		}
		if exitPrice <= 0 {
			exitPrice = p.CurrentPrice
		}
		// Realized PnL = fractional price move × USD notional. Using p.Size here
		// (a USD target for bot-opened positions) as a base-asset quantity inflated
		// PnL by price × contract-multiplier; NotionalUSD makes it dimensionally
		// correct. Falls back to the legacy formula only for pre-NotionalUSD positions.
		var moveFrac float64
		if p.EntryPrice > 0 {
			if side == "BUY" || side == "LONG" {
				moveFrac = (exitPrice - p.EntryPrice) / p.EntryPrice
			} else {
				moveFrac = (p.EntryPrice - exitPrice) / p.EntryPrice
			}
		}
		var pnl float64
		if p.NotionalUSD > 0 {
			pnl = moveFrac * p.NotionalUSD
		} else if side == "BUY" || side == "LONG" {
			pnl = (exitPrice - p.EntryPrice) * p.Size
		} else {
			pnl = (p.EntryPrice - exitPrice) * p.Size
		}

		bot.log.WithFields(logrus.Fields{
			"position_id": p.ID,
			"symbol":      p.Symbol,
			"side":        p.Side,
			"entry":       p.EntryPrice,
			"exit":        exitPrice,
			"reason":      exitReason,
			"pnl":         fmt.Sprintf("$%.4f", pnl),
		}).Warn("⚠️ Position closed externally — registering close with resolved reason")

		// Stamp the re-adopt guard BEFORE anything else: if this close turns out
		// to be phantom (position still on the exchange), the adoption path must
		// not pick it up again for the guard window — that loop is what fabricated
		// 474 TAO trades in one evening.
		bot.markExternalClose(p.Symbol, p.Side)

		// Set re-entry cooldown to prevent the "death by 1000 cuts" loop where
		// the bot reopens immediately on every SL hit (RAVE-USDT 17 entries in
		// 9 min on 2026-05-28 cost ~$1.20 in cumulative fees and slippage).
		// Also apply the same loss_cooldown_minutes that internal stop_loss closes
		// get (line ~6320) — previously exchange_sl only got the short post_sl_cooldown
		// (5m default) and skipped the longer loss cooldown (15m default). Observed
		// 2026-06-17: UNI exchange_sl re-entered 5 times in 2h because only a 5-min
		// gate applied after each external SL. setPairCooldownFor is extend-only so the
		// longer of the two always wins.
		if pnl < 0 {
			bot.setPairCooldown(ctx, p.Symbol)
			lossCooldownMin := 15
			if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
				if n, ok := snap.IntOk("bot.loss_cooldown_minutes"); ok {
					lossCooldownMin = n
				}
			}
			if lossCooldownMin > 0 {
				bot.setPairCooldownFor(p.Symbol, time.Duration(lossCooldownMin)*time.Minute, "exchange_sl loss cooldown")
			}
		}
		// Apply the operator's re-entry policy. This fires on ANY external close,
		// including a manual take-profit, so "manual"/"cooldown" modes are honored
		// even when the close was in profit (pnl >= 0, which skips the SL cooldown).
		bot.applyReentryPolicy(ctx, p.Symbol)

		if pnl < 0 {
			bot.mu.Lock()
			bot.dailyLoss += pnl
			bot.mu.Unlock()
		}

		go bot.RecordTrade(pnl)
		go bot.UpdatePositionMetrics()

		if bot.feedbackLoop != nil {
			won := pnl > 0
			outcomeSide := side
			go func(pos *ai.ManagedPosition, closePnl float64, w bool, s, reason string) {
				outcome := &learning.TradeOutcome{
					TradeID:     pos.ID,
					Pair:        pos.Symbol,
					Side:        s,
					EntryPrice:  pos.EntryPrice,
					ExitPrice:   exitPrice,
					Size:        pos.Size,
					EntryTime:   pos.OpenedAt,
					ExitTime:    time.Now(),
					PnLAbsolute: closePnl,
					PnLPercent:  closePnl / (pos.EntryPrice * pos.Size) * 100,
					Won:         w,
					ExitReason:  reason,
				}
				bot.feedbackLoop.RegisterOutcome(outcome)
			}(p, pnl, won, outcomeSide, exitReason)
		}

		bot.sendPositionClosedNotificationWithAI(ctx, p, exitReason, pnl)

		// Persist the external close to trade_history. Without this, exchange-side
		// SL/liquidation closes were silently dropped from the durable record —
		// only bot-initiated closes were saved — so analytics, the feedback loop,
		// Kelly sizing and daily-loss all learned from an OPTIMISTICALLY BIASED
		// dataset (wins recorded, external losses lost). Observed 2026-05-30: a
		// TAO -$6.12 SL close never hit trade_history, making +$4.13 of recorded
		// PnL really ≈ -$2. Align CurrentPrice with the estimated exit so the
		// recorded exit price and pnl_pct match the pnl computed above.
		p.CurrentPrice = exitPrice
		go bot.persistClosedTrade(p, exitReason, pnl)

		// Auto-pause: mirror of handlePositionClose trigger logic for exchange-side
		// SL exits. Previously, exchange_sl bypassed pairSessionPnL tracking so the
		// -$0.15 threshold and consecutive-loss pause never fired for externally-closed
		// positions. Observed 2026-06-06: AAVE ran 66 exchange_sl trades (-$73.62)
		// without a single auto-pause activating.
		slPair := p.Symbol
		bot.mu.Lock()
		if bot.pairSessionPnL == nil {
			bot.pairSessionPnL = make(map[string]float64)
		}
		bot.pairSessionPnL[slPair] += pnl
		slNetPnL := bot.pairSessionPnL[slPair]
		if bot.consecutiveLossesByPair == nil {
			bot.consecutiveLossesByPair = make(map[string]int)
		}
		if pnl < 0 {
			bot.consecutiveLossesByPair[slPair]++
		} else {
			delete(bot.consecutiveLossesByPair, slPair)
		}
		slConsecutive := bot.consecutiveLossesByPair[slPair]
		bot.mu.Unlock()

		// Resolve the consecutive-loss threshold from dynamic config, mirroring the
		// closePosition route (~6499). Previously this route used only the static
		// bot.autoPauseMaxConsecutiveLosses (fallback 3) and ignored the dynamic
		// pair.auto_pause.max_consecutive_losses (default 2), so exchange_sl exits
		// paused a pair at a different (higher) count than bot-initiated closes —
		// the counter is shared, but the trigger threshold was not.
		slThreshold := bot.autoPauseMaxConsecutiveLosses
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			if n := snap.Int("pair.auto_pause.max_consecutive_losses"); n > 0 {
				slThreshold = n
			}
		}
		if slThreshold <= 0 {
			slThreshold = 3
		}

		// Relative to the day-start capital (bot.pair_pause.net_loss_pct);
		// -0.15 USD only in absolute mode or without a capital reading.
		slNetLossThreshold := bot.pairPauseThreshold(context.Background())
		if slNetPnL <= slNetLossThreshold && slPair != "" {
			pauseReason := fmt.Sprintf("exchange_sl auto-pause: net session PnL $%.4f for %s", slNetPnL, slPair)
			bot.log.WithFields(logrus.Fields{
				"pair":        slPair,
				"net_session": slNetPnL,
				"threshold":   slNetLossThreshold,
			}).Warn("🚫 Exchange SL: auto-pausing pair — negative session PnL threshold crossed")
			bot.blacklistPairToxic(slPair, pauseReason)
			bot.mu.Lock()
			bot.pairSessionPnL[slPair] = 0
			// Also clear the consecutive-loss streak: the pair is being paused, so
			// leaving a stale counter would immediately re-trigger the consecutive
			// pause on the first loss after the blacklist TTL expires. The session
			// branch is `else if`, so this reset never ran before.
			delete(bot.consecutiveLossesByPair, slPair)
			bot.mu.Unlock()
		} else if pnl < 0 && slPair != "" && slConsecutive >= slThreshold {
			pauseReason := fmt.Sprintf("exchange_sl auto-pause: %d consecutive losses on %s", slConsecutive, slPair)
			bot.log.WithFields(logrus.Fields{
				"pair":        slPair,
				"consecutive": slConsecutive,
			}).Warn("🚫 Exchange SL: auto-pausing pair — consecutive loss threshold reached")
			bot.blacklistPairToxic(slPair, pauseReason)
			bot.mu.Lock()
			delete(bot.consecutiveLossesByPair, slPair)
			bot.mu.Unlock()
		}

		bot.positionManager.RemovePosition(p.ID)
	}

	// Adopt orphan positions: the exchange has open positions for our pair that
	// the internal manager does not know about (e.g. after a server restart).
	// Register them so trailing stop, partial TP, MFE guard, and breakeven all
	// apply from this point forward.
	//
	// We only adopt positions whose side (long/short or buy/sell) is not already
	// covered by a tracked internal position for the same pair, to avoid
	// duplicating entries from scalping bots on the same pair.
	internal = bot.positionManager.GetAllPositions() // refresh after ghost removal
	adopted := 0
	// Computed ONCE for the whole adoption pass — the xsec_live_book registry
	// does not change mid-loop, and re-querying it per exchange position would
	// be wasteful. See xsecOwnedPairs for the exclusion contract.
	xsecOwned := bot.xsecOwnedPairs(ctx)
	for _, exchPos := range positions {
		if !matchesPair(exchPos.Symbol) {
			continue
		}

		// Blacklist guard: do not adopt positions on pairs the auto-pause has
		// flagged as toxic. Without this, the sync loop keeps inheriting exchange
		// contracts (e.g. multiple futures lots from one size=2 order) even after
		// the pair is blacklisted for 1 hour, allowing churn to continue through
		// the adoption path. Observed 2026-06-07: FET-USDT blacklisted at 08:50:42
		// but adopted-and-stopped-out 2 more times at 08:51 and 08:53.
		if bot.isPairBlacklisted(exchPos.Symbol) {
			bot.log.WithField("symbol", exchPos.Symbol).
				Warn("⛔ Skipping adoption: pair is blacklisted — not inheriting exchange position")
			continue
		}

		// XSEC-ownership guard: this exchange position is currently owned by the
		// xsec-live basket trader (per xsec_live_book). Adopting it here would let
		// the scalper manage (and potentially close/resize) a position it does
		// not own, and — on KuCoin one-way mode — any subsequent scalper trade on
		// this pair would net against it. See xsecOwnedPairs.
		if xsecOwned != nil {
			if _, isXSEC := xsecOwned[normalizeSymbolForMatch(exchPos.Symbol)]; isXSEC {
				bot.log.WithField("symbol", exchPos.Symbol).
					Warn("⛔ Skipping XSEC-owned position — managed by xsec-live, not adopting")
				continue
			}
		}

		// Open-vs-adopt race guard: the bot just placed an order on this pair and
		// the exchange already reflects the position, but the open path has not yet
		// (or only just) called AddPosition. Adopting now would duplicate the bot's
		// own fresh position. Skip — the open path owns it.
		if bot.pairRecentlyOpened(exchPos.Symbol) {
			bot.log.WithField("symbol", exchPos.Symbol).
				Debug("Skipping adoption: pair opened within race-guard window (bot's own position)")
			continue
		}

		// Normalise exchange side to uppercase for comparison.
		exchSideUp := strings.ToUpper(exchPos.Side) // "LONG" or "SHORT"

		// Check whether the internal manager already tracks this pair+side.
		// CRITICAL for cross-pair adoption: compare the SYMBOL too, not just the
		// side. Without the symbol guard, a SOL long already tracked would mark a
		// DOT long as "already tracked" and silently skip adopting it, leaving the
		// DOT position orphaned — the exact bug this fix targets. Symbols are
		// compared in normalized form so "DOT-USDT" matches an exchange "DOTUSDTM".
		exchSymNorm := normalizeSymbolForMatch(exchPos.Symbol)
		alreadyTracked := false
		for _, managed := range internal {
			if normalizeSymbolForMatch(managed.Symbol) != exchSymNorm {
				continue // different pair — not a duplicate
			}
			managedSideUp := strings.ToUpper(managed.Side)
			// Equate long↔buy and short↔sell.
			exchIsLong := exchSideUp == "LONG" || exchSideUp == "BUY"
			managedIsLong := managedSideUp == "LONG" || managedSideUp == "BUY"
			if exchIsLong == managedIsLong {
				alreadyTracked = true
				break
			}
		}
		if alreadyTracked {
			continue
		}

		// Reject oversized orphans — positions left by a prior process may be
		// contract-denominated and far exceed the bot's risk limits. Notional
		// (size×entry_price) gives USD exposure; if it exceeds the configured cap
		// we skip adoption to prevent inherited risk blowups on a small account.
		notional := exchPos.Size * exchPos.EntryPrice
		maxAdoptNotional := 50.0 // safe fallback
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			if v := snap.Float("adopt.max_notional_usd"); v > 0 {
				maxAdoptNotional = v
			}
		}
		if notional > maxAdoptNotional {
			bot.log.WithFields(logrus.Fields{
				"symbol":           exchPos.Symbol,
				"side":             exchSideUp,
				"size":             exchPos.Size,
				"entry":            exchPos.EntryPrice,
				"notional_usd":     notional,
				"max_notional_usd": maxAdoptNotional,
			}).Warn("⛔ Skipping oversized orphan: notional exceeds adopt.max_notional_usd — close position manually on exchange")
			continue
		}

		// Reject large leverage=1 orphans — the bot always opens at leverage≥5.
		// A position with leverage=1 and large size is almost certainly a leftover
		// from a prior manual open or a miscalculated legacy session; its SL/TP
		// won't align with the active strategy and it drags performance.
		if exchPos.Leverage <= 1 {
			maxSizeAt1x := 20.0 // safe fallback
			if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
				if v := snap.Float("adopt.max_size_at_leverage1"); v > 0 {
					maxSizeAt1x = v
				}
			}
			if exchPos.Size > maxSizeAt1x {
				bot.log.WithFields(logrus.Fields{
					"symbol":   exchPos.Symbol,
					"side":     exchSideUp,
					"size":     exchPos.Size,
					"leverage": exchPos.Leverage,
					"max_size": maxSizeAt1x,
				}).Warn("⛔ Skipping leverage=1 orphan: size exceeds adopt.max_size_at_leverage1 — likely a legacy/manual position, close manually on exchange")
				continue
			}
		}

		// Compute SL/TP from the entry price using bot defaults.
		slPct := bot.config.DefaultStopLossPct
		if slPct <= 0 {
			slPct = 3.0 // safe fallback
		}
		tpPct := bot.config.DefaultTakeProfitPct
		if tpPct <= 0 {
			tpPct = 6.0 // safe fallback (2:1)
		}

		// Apply the SAME dynamic SL floor used for fresh entries. Adopted orphans
		// otherwise inherit DefaultStopLossPct, which can be far tighter than the
		// noise floor and get wicked out in minutes — exactly the sub-floor stops
		// the fresh-entry clamp eliminated. Floored here, before the base stop is
		// computed; the profit-aware block below may still tighten it to lock a
		// gain (correct). Configurable via scoring.sl_floor_pct (default 0.8%, 0=off).
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			if v, ok := snap.FloatOk("scoring.sl_floor_pct"); ok && v > 0 && slPct < v {
				bot.log.WithFields(logrus.Fields{
					"symbol":    exchPos.Symbol,
					"side":      exchSideUp,
					"sl_before": fmt.Sprintf("%.2f%%", slPct),
					"sl_floor":  fmt.Sprintf("%.2f%%", v),
				}).Info("🛡️ Adopt: SL floor applied — widening orphan stop to minimum distance")
				slPct = v
			}
		}

		var stopLoss, takeProfit float64
		entryPrice := exchPos.EntryPrice
		isLong := exchSideUp == "LONG" || exchSideUp == "BUY"
		if isLong {
			stopLoss = entryPrice * (1 - slPct/100)
			takeProfit = entryPrice * (1 + tpPct/100)
		} else {
			stopLoss = entryPrice * (1 + slPct/100)
			takeProfit = entryPrice * (1 - tpPct/100)
		}

		// Profit-aware SL on adoption. A bot restart re-adopts the position fresh
		// and, without this, would reset the stop to entry±slPct — THROWING AWAY
		// any profit that breakeven/trailing had already locked on the exchange
		// (INJ rode +16% with a breakeven stop, then a restart would have rearmed
		// it at -2%). When the orphan is already in profit, pull the SL up to the
		// trailed level (currentPrice ∓ trailing distance), never below breakeven.
		// This makes the locked gain survive restarts even without a positions DB.
		adoptCur := exchPos.CurrentPrice
		breakevenApplied := false
		if adoptCur > 0 {
			var profitPct float64
			if isLong {
				profitPct = (adoptCur - entryPrice) / entryPrice * 100
			} else {
				profitPct = (entryPrice - adoptCur) / entryPrice * 100
			}
			beTrigger := 0.5
			trailDist := 0.4
			if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
				if v := snap.Float("breakeven.trigger_pct"); v > 0 {
					beTrigger = v
				}
				if v := snap.Float("trailing.distance_pct"); v > 0 {
					trailDist = v
				}
			}
			if profitPct >= beTrigger {
				// Trailed stop level; clamp to at least breakeven (entry).
				if isLong {
					trailed := adoptCur * (1 - trailDist/100)
					if trailed < entryPrice {
						trailed = entryPrice
					}
					if trailed > stopLoss {
						stopLoss = trailed
					}
				} else {
					trailed := adoptCur * (1 + trailDist/100)
					if trailed > entryPrice {
						trailed = entryPrice
					}
					if trailed < stopLoss {
						stopLoss = trailed
					}
				}
				breakevenApplied = true
				bot.log.WithFields(logrus.Fields{
					"symbol":     bot.config.Pair,
					"side":       exchSideUp,
					"entry":      entryPrice,
					"current":    adoptCur,
					"profit_pct": fmt.Sprintf("%.2f%%", profitPct),
					"adopted_sl": stopLoss,
				}).Info("🔒 Adopt: orphan in profit — SL pulled to trailed level (gain preserved across restart)")
			}
		}

		// Safety check: skip adoption if current price is already past the SL.
		// Adopting such a position guarantees an instant SL hit on the next tick.
		currentPrice := exchPos.CurrentPrice
		if currentPrice > 0 {
			pastSL := false
			if isLong && currentPrice <= stopLoss {
				pastSL = true
			} else if !isLong && currentPrice >= stopLoss {
				pastSL = true
			}
			if pastSL {
				bot.log.WithFields(logrus.Fields{
					"symbol":        bot.config.Pair,
					"side":          exchSideUp,
					"entry":         entryPrice,
					"current_price": currentPrice,
					"stop_loss":     stopLoss,
				}).Warn("⛔ Skipping orphan adoption: current price already past SL — would trigger instantly")
				continue
			}
		}

		// Canonical side: normalise to "buy"/"sell" to match the rest of the bot.
		canonicalSide := "buy"
		if !isLong {
			canonicalSide = "sell"
		}

		leverage := exchPos.Leverage
		if leverage <= 0 {
			leverage = bot.config.Leverage
		}
		if leverage <= 0 {
			leverage = 1
		}

		// Use the position's REAL exchange symbol, not bot.config.Pair: in dynamic
		// pair mode config.Pair is "" (and after a pair switch it points at a
		// different pair), which left adopted positions with an empty Symbol —
		// breaking the exchange stop-loss order ("pair is required") and any path
		// keyed on the symbol. Fall back to config.Pair only if it is missing.
		posSymbol := exchPos.Symbol
		if strings.TrimSpace(posSymbol) == "" {
			posSymbol = bot.config.Pair
		}

		// Honor the post-loss cooldown in the ADOPTION path too. Without this,
		// the adopt→external-close→re-adopt loop churned the same orphan every
		// sync cycle (observed 2026-06-09: ADOPTED-SOL-USDT re-adopted 3× after
		// identical external closes, recording the same -$1.38 loss each time).
		// The position (if it truly still exists) stays unmanaged for the
		// cooldown window — strictly better than mechanically re-bleeding.
		if remaining := bot.pairCooldownRemaining(posSymbol); remaining > 0 {
			bot.log.WithFields(logrus.Fields{
				"symbol":             posSymbol,
				"side":               canonicalSide,
				"cooldown_remaining": remaining.Round(time.Second).String(),
			}).Warn("🧊 Skipping orphan adoption — pair on post-loss cooldown (anti-churn)")
			continue
		}
		// Refuse to re-adopt a symbol+side we JUST registered an external close
		// for — regardless of profit sign. The post-loss cooldown above does not
		// fire for profitable closes, which is exactly how the phantom-close →
		// re-adopt loop churned a winning TAO orphan every ~40s on 2026-06-10.
		if remaining := bot.recentExternalCloseRemaining(posSymbol, canonicalSide); remaining > 0 {
			bot.log.WithFields(logrus.Fields{
				"symbol":          posSymbol,
				"side":            canonicalSide,
				"guard_remaining": remaining.Round(time.Second).String(),
			}).Warn("🧊 Skipping orphan adoption — external close registered moments ago (anti-churn)")
			continue
		}

		adoptedID := fmt.Sprintf("ADOPTED-%s-%s-%d", posSymbol, canonicalSide, time.Now().UnixNano())
		pos := &ai.ManagedPosition{
			ID:           adoptedID,
			Symbol:       posSymbol,
			Side:         canonicalSide,
			EntryPrice:   entryPrice,
			CurrentPrice: exchPos.CurrentPrice,
			Size:         exchPos.Size,
			// Real USD notional from the exchange, multiplier-independent. Used so
			// realized-PnL on this adopted orphan is computed in USD notional rather
			// than from the ambiguous contract Size.
			//
			// exchPos.Margin holds KuCoin's `currentCost`, which is ALREADY the
			// position's USD notional (qty × multiplier × price) — not the margin —
			// and it is NEGATIVE for shorts. The old `Margin × leverage` formula
			// therefore (a) double-counted leverage and (b) went negative on shorts,
			// failing the `NotionalUSD > 0` check downstream and falling back to the
			// size-based legacy PnL formula — which ignores the contract multiplier
			// and inflated adopted-TAO PnL 100× (mult 0.01: real ±$0.07 recorded as
			// ±$6.8 per close on 2026-06-10).
			NotionalUSD: math.Abs(exchPos.Margin),
			Leverage:    leverage,
			StopLoss:    stopLoss,
			TakeProfit:  takeProfit,
			OpenedAt:    exchPos.UpdatedAt,
			Exchange:    bot.config.Exchange,
			Status:      "open",
			// If the orphan was adopted in profit we already pulled the SL to the
			// trailed level above; mark breakeven as applied so checkAndApplyBreakeven
			// doesn't drag the stop back down to a fixed breakeven on the next tick.
			BreakevenApplied: breakevenApplied,
		}
		if pos.OpenedAt.IsZero() {
			pos.OpenedAt = time.Now()
		}

		// Cross-instance ownership claim: refuse to adopt a position another
		// orchestrator instance already owns (it opened it, or adopted it first).
		// This is the guard the per-instance race/alreadyTracked checks cannot
		// provide — it is what stops one physical position becoming N managed copies
		// across the bot fleet.
		if !claimPositionOwnership(posSymbol, canonicalSide, adoptedID, false) {
			bot.log.WithFields(logrus.Fields{
				"symbol": posSymbol,
				"side":   canonicalSide,
			}).Debug("Skipping adoption: position owned by another bot instance")
			continue
		}

		bot.positionManager.AddPosition(pos)
		adopted++

		// Give adopted orphans the SAME profit-protection machinery as bot-opened
		// positions. Without this a winning orphan carries only a static SL and
		// rides a reversal all the way back to the stop — observed: WLD short
		// +3.79% with its SL parked at -3% and no trailing, so the bot "did
		// nothing" as the move turned. Enabling trailing + partial-TP lets the
		// monitor (checkPosition) ratchet the stop and lock gains on a reversal.
		if bot.config.PartialTP.Enabled && len(bot.config.PartialTP.Levels) > 0 {
			pos.PartialTPTriggered = make([]bool, len(bot.config.PartialTP.Levels))
		}
		if terr := bot.enableTrailingStop(ctx, pos); terr != nil {
			bot.log.WithError(terr).WithField("position_id", adoptedID).Debug("adopt: enableTrailingStop failed")
		}

		// Place a durable exchange stop at the (possibly profit-aware) SL right
		// away. The pre-restart stop order's ID is lost on adoption, so without
		// this the locked-in level would live only in memory until the next
		// trailing tick. A duplicate stop from before the restart is harmless —
		// whichever fires first closes the position; the other no-ops.
		if !bot.config.DryRun {
			bot.placeExchangeStopLoss(ctx, pos)
		}

		bot.log.WithFields(logrus.Fields{
			"adopted_id":  adoptedID,
			"symbol":      posSymbol, // real exchange symbol, not config.Pair (empty in dynamic mode)
			"side":        canonicalSide,
			"entry":       entryPrice,
			"stop_loss":   stopLoss,
			"take_profit": takeProfit,
			"size":        exchPos.Size,
			"leverage":    leverage,
		}).Info("Adopted orphan position from exchange")
	}

	count := 0
	for _, pos := range positions {
		if matchesPair(pos.Symbol) {
			count++
		}
	}
	logKey := "exchange_positions_for_pair"
	if adoptAll {
		logKey = "exchange_positions_total"
	}
	bot.log.WithFields(logrus.Fields{
		logKey:      count,
		"adopted":   adopted,
		"adopt_all": adoptAll,
	}).Info("Position synchronization completed")
}

// Stop stops the trading bot gracefully (convenience method)
func (bot *AITradingBot) Stop() error {
	return bot.StopWithContext(context.Background())
}

// StopWithContext stops the trading bot gracefully with context
func (bot *AITradingBot) StopWithContext(ctx context.Context) error {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if !bot.running {
		// Loops may still be alive if they were launched through a path that did
		// not set running=true. Signal stopChan anyway (idempotent via stopOnce;
		// StartWithContext re-allocates it) so a stop request is never a no-op.
		bot.stopOnce.Do(func() {
			close(bot.stopChan)
		})
		return fmt.Errorf("bot is not running")
	}

	bot.log.Info("🛑 Stopping AI Trading Bot...")

	// Use sync.Once to guarantee close(stopChan) is called at most once,
	// even if Stop is called concurrently from multiple HTTP requests.
	bot.stopOnce.Do(func() {
		close(bot.stopChan)
	})

	// Wait a bit for goroutines to finish
	time.Sleep(2 * time.Second)

	bot.running = false

	if bot.pairCoord != nil && bot.config.Pair != "" {
		// MU-2: release per-user claim so other users are not blocked.
		bot.pairCoord.ReleaseForUser(bot.effectiveUserID(), bot.config.Pair, "ai_bot")
	}

	bot.log.WithFields(logrus.Fields{
		"uptime":          time.Since(bot.startedAt),
		"analysis_count":  bot.analysisCount,
		"execution_count": bot.executionCount,
		"error_count":     bot.errorCount,
	}).Info("✅ AI Trading Bot stopped")

	return nil
}

// Pause pauses the bot (stops new trades but continues monitoring)
func (bot *AITradingBot) Pause() {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if !bot.running || bot.paused {
		return
	}

	bot.paused = true
	bot.pauseChan <- true
	bot.log.Warn("⏸️  AI Trading Bot paused")
}

// Resume resumes the bot
func (bot *AITradingBot) Resume() {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if !bot.running || !bot.paused {
		return
	}

	bot.paused = false
	bot.pauseChan <- false
	bot.log.Info("▶️  AI Trading Bot resumed")
}

// ResetState clears runtime counters and unblocks the bot without stopping it.
// Resets: daily loss/trade counters, consecutive losses/errors, paused flag,
// emergency stop flag, pair blacklist, and stale internal positions.
func (bot *AITradingBot) ResetState() map[string]interface{} {
	bot.mu.Lock()
	wasPaused := bot.paused
	wasEmergency := bot.emergencyStop
	prevDailyLoss := bot.dailyLoss
	prevDailyTrades := bot.dailyTrades
	prevConsecutiveLosses := bot.consecutiveLosses
	prevBlacklisted := len(bot.pairBlacklist)

	bot.consecutiveLosses = 0
	bot.consecutiveLossesByPair = make(map[string]int)
	bot.consecutiveErrs = 0
	bot.dailyLoss = 0
	bot.dailyTrades = 0
	bot.emergencyStop = false
	bot.pairBlacklist = make(map[string]time.Time)
	bot.pairSessionPnL = make(map[string]float64)
	bot.resetSizingDay()

	// Unblock analysis loop if paused.
	if bot.paused {
		bot.paused = false
	}
	bot.mu.Unlock()

	// Send unpause signal outside the lock to avoid deadlock.
	if wasPaused {
		select {
		case bot.pauseChan <- false:
		default:
		}
	}

	bot.log.WithFields(map[string]interface{}{
		"was_paused":               wasPaused,
		"was_emergency_stop":       wasEmergency,
		"cleared_daily_loss":       prevDailyLoss,
		"cleared_daily_trades":     prevDailyTrades,
		"cleared_consecutive_loss": prevConsecutiveLosses,
		"cleared_blacklisted":      prevBlacklisted,
	}).Warn("🔄 Bot state reset via API")

	return map[string]interface{}{
		"message":                   "Bot state reset successfully",
		"was_paused":                wasPaused,
		"was_emergency_stop":        wasEmergency,
		"cleared_daily_loss":        prevDailyLoss,
		"cleared_daily_trades":      prevDailyTrades,
		"cleared_consecutive_loss":  prevConsecutiveLosses,
		"cleared_blacklisted_pairs": prevBlacklisted,
		"status":                    bot.GetStatus(),
	}
}

// EmergencyStop immediately stops all trading and closes positions (convenience method)
func (bot *AITradingBot) EmergencyStop() error {
	return bot.EmergencyStopWithContext(context.Background())
}

// EmergencyStopWithContext immediately stops all trading and closes positions with context
func (bot *AITradingBot) EmergencyStopWithContext(ctx context.Context) error {
	bot.mu.Lock()
	bot.emergencyStop = true
	bot.mu.Unlock()

	bot.log.Error("EMERGENCY STOP ACTIVATED")

	var closeErrors []error

	// Close all open positions with actual market orders
	positions := bot.positionManager.GetAllPositions()
	for _, pos := range positions {
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"symbol":      pos.Symbol,
			"side":        pos.Side,
			"size":        pos.Size,
		}).Warn("Closing position due to emergency stop")

		// Execute close order via GCT client
		if bot.gctClient != nil {
			closeErr := bot.closePositionViaGCT(ctx, pos)
			if closeErr != nil {
				bot.log.WithError(closeErr).WithField("position_id", pos.ID).Error("Failed to close position via GCT")
				closeErrors = append(closeErrors, closeErr)
				// Continue trying to close other positions
			} else {
				bot.log.WithField("position_id", pos.ID).Info("Position closed successfully via GCT")
			}
		} else {
			bot.log.WithField("position_id", pos.ID).Warn("GCT client not available - position marked for manual closure")
		}

		// Remove from position manager regardless (mark as closed)
		bot.positionManager.RemovePosition(pos.ID)
	}

	// Log summary of emergency stop
	bot.log.WithFields(logrus.Fields{
		"total_positions": len(positions),
		"close_errors":    len(closeErrors),
	}).Warn("Emergency stop completed")

	// Stop the bot
	stopErr := bot.StopWithContext(ctx)

	// Return aggregated error if any positions failed to close
	if len(closeErrors) > 0 {
		return fmt.Errorf("emergency stop completed with %d position close errors; stop error: %v", len(closeErrors), stopErr)
	}

	return stopErr
}

// dashPairToKuCoinFutures converts a dash-separated pair to KuCoin Futures format.
// Examples: "SUI-USDT" → "SUIUSDTM", "BTC-USDT" → "BTCUSDTM".
// If the symbol already ends with "M" it is returned unchanged.
func dashPairToKuCoinFutures(pair string) string {
	if pair == "" {
		return ""
	}
	if strings.HasSuffix(pair, "M") {
		return pair
	}
	return strings.ReplaceAll(pair, "-", "") + "M"
}

// closePositionViaGCT closes a futures position using CloseFuturesPosition,
// which sends a proper reduce-only close-position order to the exchange.
func (bot *AITradingBot) closePositionViaGCT(ctx context.Context, pos *ai.ManagedPosition) error {
	exchange := pos.Exchange
	if exchange == "" {
		exchange = bot.config.Exchange
	}
	if exchange == "" {
		exchange = "kucoin"
	}

	// KuCoin Futures API requires the BTCUSDTM format; the internal position
	// manager stores symbols in BTC-USDT format, so normalize before sending.
	symbol := dashPairToKuCoinFutures(pos.Symbol)

	// Multi-user path: when this bot owns an isolated ExchangeSession, the close
	// order MUST be signed with the user's own credentials — never the global
	// client. Routing through the session's futures manager keeps emergency
	// closes correctly attributed to the right account.
	if bot.session != nil {
		if err := bot.session.FuturesManager().ClosePosition(ctx, symbol); err != nil {
			return fmt.Errorf("failed to close futures position %s (user session): %w", pos.Symbol, err)
		}
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"symbol":      pos.Symbol,
			"exchange":    exchange,
			"user_id":     bot.userID,
		}).Info("Futures position close order submitted (user session)")
		return nil
	}

	// Legacy single-bot path: use the shared global client.
	if bot.gctClient == nil {
		return fmt.Errorf("GCT client not available")
	}

	// Use the dedicated futures-close call which sends closeOrder=true to the exchange.
	// This works for any position size and does not require knowing the number of contracts.
	err := bot.gctClient.CloseFuturesPosition(ctx, exchange, symbol)
	if err != nil {
		return fmt.Errorf("failed to close futures position %s: %w", pos.Symbol, err)
	}

	bot.log.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"exchange":    exchange,
	}).Info("Futures position close order submitted")

	return nil
}

// analysisLoop runs market analysis periodically
// withPairScope attaches the per-pair dynamic-config snapshot to ctx so config
// reads inside runAnalysis resolve the scope chain bot(userID)→pair→exchange→
// global. analysisLoop previously left this unattached, so SnapshotFromContext
// fell back to the GLOBAL-only snapshot and per-pair overrides (manifest
// globalAndPair scope, e.g. bot.directional.mode per pair) silently never took
// effect. Falls back to global cleanly when no per-pair override exists, so it's
// a no-op for pairs without overrides. Mirrors analysisLoopDynamic.
func (bot *AITradingBot) withPairScope(ctx context.Context) context.Context {
	cs := bot.configStore
	if cs == nil {
		return ctx
	}
	bot.mu.RLock()
	pair := bot.config.Pair
	exchange := bot.config.Exchange
	bot.mu.RUnlock()
	chain := dynamic.BotScopeChain(bot.effectiveUserID(), pair, exchange)
	return dynamic.WithSnapshot(ctx, cs.SnapshotForScope(chain))
}

func (bot *AITradingBot) analysisLoop(ctx context.Context) {
	ticker := time.NewTicker(bot.config.AnalysisInterval)
	defer ticker.Stop()

	bot.log.Info("📊 Analysis loop started")

	// Run an initial analysis immediately so the UI gets instant feedback
	bot.log.Info("Running initial analysis before waiting for interval...")
	bot.mu.RLock()
	paused := bot.paused
	bot.mu.RUnlock()
	if !paused {
		analysisTimeout := 90 * time.Second
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			analysisTimeout = snap.Duration("bot.exec.analysis_timeout")
		}
		initialCtx, cancelInitial := context.WithTimeout(ctx, analysisTimeout)
		bot.runAnalysis(bot.withPairScope(initialCtx))
		cancelInitial()
	}

	for {
		select {
		case <-ctx.Done():
			bot.log.Info("Analysis loop stopped (context cancelled)")
			return

		case <-bot.stopChan:
			bot.log.Info("Analysis loop stopped (stop signal)")
			return

		case paused := <-bot.pauseChan:
			if paused {
				bot.log.Info("Analysis loop paused")
				// Wait for resume
				<-bot.pauseChan
				bot.log.Info("Analysis loop resumed")
			}

		case <-ticker.C:
			bot.mu.RLock()
			if bot.emergencyStop {
				bot.mu.RUnlock()
				continue
			}
			paused := bot.paused
			bot.mu.RUnlock()

			if paused {
				continue
			}

			// Run analysis with timeout — must exceed LLM consensus timeout (30s) plus data fetching
			analysisTimeout := 90 * time.Second
			if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
				analysisTimeout = snap.Duration("bot.exec.analysis_timeout")
			}
			analysisCtx, cancel := context.WithTimeout(ctx, analysisTimeout)
			bot.runAnalysis(bot.withPairScope(analysisCtx))
			cancel()
		}
	}
}

// runAnalysis performs market analysis and executes trades if conditions are met
func (bot *AITradingBot) runAnalysis(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in runAnalysis - recovered")
			bot.incrementErrorCount()
		}
	}()

	// Log the effective sizing on the first cycle and whenever the dynamic
	// sizing keys change (hot reload), so the operator sees what is traded.
	bot.logSizingIfChanged(ctx)

	// Ring 0: skip the whole analysis cycle (no decisions, no data fetching).
	if killswitch.Ring0Active() {
		bot.log.Debug("runAnalysis skipped: ring 0 global kill switch active")
		return
	}

	bot.mu.Lock()
	bot.analysisCount++
	bot.lastAnalysisAt = time.Now()
	bot.mu.Unlock()

	// Check enhanced safety limits (includes win rate, drawdown, and cost budget)
	if !bot.checkEnhancedSafetyLimits(ctx) {
		return
	}

	// Check portfolio risk before opening new positions
	if !bot.checkPortfolioRisk(ctx) {
		return
	}

	// Early blacklist and cooldown check — before any LLM call so we don't
	// waste API quota on pairs that are already gated. The postCloseState field
	// was previously used here but was never populated (dead code). Replaced
	// with the live pairCooldownRemaining / isPairBlacklisted checks that
	// executeTrade already performs, hoisted here so the skip happens BEFORE
	// the 30-90s LLM consensus round-trip. See bug note 2026-06-17.
	bot.mu.RLock()
	currentPair := bot.config.Pair
	bot.mu.RUnlock()
	if currentPair != "" {
		if remaining := bot.pairCooldownRemaining(currentPair); remaining > 0 {
			bot.log.WithFields(logrus.Fields{
				"pair":               currentPair,
				"cooldown_remaining": remaining.Round(time.Second).String(),
			}).Debug("⏳ Pair on loss/SL cooldown — skipping analysis")
			return
		}
		if bot.isPairBlacklisted(currentPair) {
			bot.log.WithField("pair", currentPair).Debug("🚫 Pair is blacklisted — skipping analysis")
			return
		}
	}

	// Check if autonomous mode is active (only proceed if autonomous or manual execution is enabled)
	if bot.config.AutonomousMode && !bot.isAutonomousModeActive() {
		bot.log.Debug("🤖 Autonomous mode inactive - skipping analysis")
		return
	}

	// Check for adverse market conditions in autonomous mode
	if bot.config.AutonomousMode && bot.checkAdverseConditions(ctx) {
		bot.log.Warn("⚠️ Adverse market conditions detected - pausing autonomous trading")
		return
	}

	bot.log.Debug("🔍 Running market analysis...")

	// Route to the best-scored pair if smart pair selection is enabled and we
	// have no open position. This must happen before the read below so the
	// rest of the cycle operates on the freshly-selected pair.
	bot.maybeSwitchPair()

	// Get dynamic timeframes from config
	bot.mu.RLock()
	timeframes := bot.config.Timeframes
	pair := bot.config.Pair
	bot.mu.RUnlock()

	// Hard guard: no pair → no analysis. This happens when pair_selection is
	// enabled with an empty fallback and the master agent hasn't yet surfaced
	// any opportunity that beats min_score. We pause cleanly instead of
	// analyzing a stale or empty symbol.
	if strings.TrimSpace(pair) == "" {
		bot.log.Debug("⏸️  No tradable pair — waiting for master agent opportunities")
		return
	}

	// Build market context with dynamic timeframes
	var marketCtx *ai.MarketContext
	var err error

	if len(timeframes) > 0 {
		// Create dynamic strategy with configured timeframes
		strategy := bot.createDynamicStrategy(timeframes)
		marketCtx, err = bot.contextBuilder.BuildContext(ctx, pair, strategy)

		bot.log.WithFields(logrus.Fields{
			"pair":       pair,
			"timeframes": timeframes,
		}).Debug("Building context with dynamic timeframes")
	} else {
		// Fallback to default if no timeframes configured
		marketCtx, err = bot.contextBuilder.BuildContext(ctx, pair)
	}

	if err != nil {
		bot.log.WithError(err).Error("Failed to build market context")
		bot.incrementErrorCount()
		return
	}

	// Cache indicators so checkPosition can evaluate human-like exits
	// (RSI/MACD reversal, profit lock) without re-fetching candles.
	bot.cacheIndicatorsForExit(pair, marketCtx)

	// Apply learned parameter adjustments for this pair.
	if bot.feedbackLoop != nil {
		bot.mu.RLock()
		flPair := bot.config.Pair
		bot.mu.RUnlock()
		// NOTE: must match the key the FeedbackLoop writes (paramMinConfidenceThresh
		// = "min_confidence_threshold"). The old "confidence_threshold" string never
		// matched, so the learned adjustment was silently discarded and the bot
		// kept entering low-quality trades after a losing streak.
		adjustedThreshold := bot.feedbackLoop.GetParameter(flPair, "min_confidence_threshold", bot.config.ConfidenceThreshold)
		bot.mu.Lock()
		bot.config.ConfidenceThreshold = adjustedThreshold
		bot.mu.Unlock()
	}

	// Adjust risk parameters (MaxOpenPositions, SL%, TP%, confidence threshold) based on
	// current market volatility — independent of autonomous_mode.
	// TC-4.6: bot.risk_adjust.interval (default 15m)
	riskAdjustInterval := 15 * time.Minute
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if d := snap.Duration("bot.risk_adjust.interval"); d > 0 {
			riskAdjustInterval = d
		}
	}
	bot.mu.RLock()
	needsRiskAdjust := time.Since(bot.lastRiskAdjustAt) >= riskAdjustInterval
	bot.mu.RUnlock()
	if needsRiskAdjust {
		conditions := bot.analyzeMarketConditions(marketCtx)
		riskParams := bot.adjustRiskParameters(conditions.Volatility)
		bot.applyAutoConfiguration(riskParams)
		bot.mu.Lock()
		bot.lastRiskAdjustAt = time.Now()
		bot.mu.Unlock()
		// Real-time AI regulation of max_open_positions, persisted to the config DB.
		bot.regulateMaxOpenPositions(ctx, conditions.Volatility)
		bot.log.WithFields(logrus.Fields{
			"volatility":       fmt.Sprintf("%.2f%%", conditions.Volatility*100),
			"max_positions":    riskParams.MaxOpenPositions,
			"max_daily_trades": bot.config.MaxDailyTrades,
		}).Info("📐 Risk parameters adjusted dynamically")
	}

	// Reload DB blacklist every 5 minutes so manual entries (or entries written
	// by other bot instances) take effect without a service restart.
	bot.mu.RLock()
	needsBlacklistRefresh := time.Since(bot.lastBlacklistRefreshAt) >= 5*time.Minute
	bot.mu.RUnlock()
	if needsBlacklistRefresh {
		bot.mu.Lock()
		bot.lastBlacklistRefreshAt = time.Now()
		store := bot.blacklistStore
		bot.mu.Unlock()
		if store != nil {
			if active, err := store.LoadActive(bot.effectiveUserID()); err == nil {
				bot.mu.Lock()
				if bot.pairBlacklist == nil {
					bot.pairBlacklist = make(map[string]time.Time)
				}
				for pair, until := range active {
					existing, ok := bot.pairBlacklist[pair]
					// Only add/extend — never remove entries that are still valid in memory
					if !ok || until.After(existing) {
						bot.pairBlacklist[pair] = until
						if bot.config.Pair == pair {
							bot.config.Pair = "" // force pair switch
						}
					}
				}
				bot.mu.Unlock()
			}
		}
	}

	// Per-pair directional bias (futures bidirectional trading). Opt-in via
	// bot.directional.mode; futures-only. Computes THIS pair's macro trend from its
	// own 1h EMA50 and stamps it (+ the mode) onto marketCtx so the decision engine
	// takes the trend-ALIGNED side per pair — long on uptrend pairs, short on
	// downtrend pairs — instead of mean-reversion shorts that the trend filter
	// blocks. State is per-call on marketCtx, never bot-level, so pair directions
	// can't cross. Default "off" → byte-identical legacy behavior.
	if marketCtx != nil {
		directionalMode := "off"
		futuresOnly := true
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			if v := snap.String("bot.directional.mode"); v != "" {
				directionalMode = v
			}
			if v, ok := snap.BoolOk("bot.directional.futures_only"); ok {
				futuresOnly = v
			}
		}
		if directionalMode != "off" {
			bot.mu.RLock()
			exchange := bot.config.Exchange
			lev := bot.config.Leverage
			bot.mu.RUnlock()
			isFutures := true
			marketType := "futures"
			if bot.modeResolver != nil {
				if dec := bot.modeResolver.Resolve(ctx, pair, float64(lev)); string(dec.MarketType) != "" {
					marketType = string(dec.MarketType)
					isFutures = marketType == "futures" || marketType == "margin"
				}
			}
			trend := ""
			if !futuresOnly || isFutures {
				trend = bot.macroTrendLabel(ctx, exchange, pair)
				if trend != "" {
					marketCtx.MacroTrend = trend
					marketCtx.DirectionalMode = directionalMode
				}
			}
			bot.log.WithFields(logrus.Fields{
				"pair":        pair,
				"macro_trend": trend,
				"mode":        directionalMode,
				"market_type": marketType,
				"is_futures":  isFutures,
				"applied":     trend != "",
			}).Info("🧭 Directional bias evaluated")
		}
	}

	// Evaluate opportunity with technical analysis
	decision, err := bot.decisionEngine.EvaluateOpportunity(ctx, marketCtx)
	if err != nil {
		bot.log.WithError(err).Error("Failed to evaluate opportunity")
		bot.incrementErrorCount()
		return
	}

	// Enhance decision with LLM analysis if available.
	// Skip when already at max positions (no new entry possible — LLM adds no value for
	// pure monitoring) and enforce a minimum call interval to avoid exhausting free-tier
	// quotas (e.g. Gemini free tier: 20 req/day at 30s analysis interval = gone in 10min).
	atMaxPositions := len(bot.positionManager.GetAllPositions()) >= bot.effectiveMaxOpenPositions(ctx)
	// llm.call_cooldown_seconds (default 300) — hot-reloadable via dynamic config
	llmCooldownSecs := 300
	if cooldownSnap := dynamic.SnapshotFromContext(ctx); cooldownSnap != nil {
		if v := cooldownSnap.Int("llm.call_cooldown_seconds"); v >= 0 {
			llmCooldownSecs = v
		}
	}
	llmCooldown := time.Duration(llmCooldownSecs) * time.Second
	bot.mu.RLock()
	timeSinceLLM := time.Since(bot.lastLLMCallAt)
	bot.mu.RUnlock()
	llmReady := !atMaxPositions && timeSinceLLM >= llmCooldown

	if bot.llmAnalyzer != nil && llmReady {
		bot.log.Debug("🤖 Enhancing decision with LLM analysis...")

		bot.mu.Lock()
		bot.lastLLMCallAt = time.Now()
		bot.mu.Unlock()

		llmStartTime := time.Now()
		llmAnalysis, err := bot.callLLMAnalysis(ctx, marketCtx)
		llmDuration := time.Since(llmStartTime)

		if err != nil {
			bot.log.WithFields(logrus.Fields{
				"error":    err.Error(),
				"duration": llmDuration,
			}).Warn("⚠️ LLM analysis failed, using technical analysis only")
		} else {
			// Log LLM analysis results
			logFields := logrus.Fields{
				"llm_action":           llmAnalysis.Action,
				"llm_confidence":       fmt.Sprintf("%.2f%%", llmAnalysis.Confidence*100),
				"llm_reasoning":        llmAnalysis.Reasoning,
				"technical_action":     decision.Action,
				"technical_confidence": fmt.Sprintf("%.2f%%", decision.Confidence*100),
				"duration":             llmDuration,
				"consensus":            bot.consensusEnabled,
			}

			// Add provider info if available
			if llmAnalysis.Response != nil {
				logFields["provider"] = llmAnalysis.Response.Provider
				logFields["cached"] = llmAnalysis.Response.Cached
				logFields["cost"] = fmt.Sprintf("$%.6f", llmAnalysis.Response.Cost)
			}

			bot.log.WithFields(logFields).Info("🧠 LLM analysis received")

			// Combine LLM and technical analysis intelligently
			decision = bot.combineLLMAndTechnicalAnalysis(decision, llmAnalysis, marketCtx)
		}
	}

	// Optional: Apply regime-based filter (only if regime detector is wired).
	if regimeAnalysis := bot.checkRegime(marketCtx); regimeAnalysis != nil {
		bot.applyRegimeFilter(decision, regimeAnalysis)
	}

	// In ranging mode, restrict entries to the pairs with a demonstrated RANGING edge.
	bot.enforceRangingAllowlist(decision, marketCtx.Pair)

	// Optional: Apply BTC context adjustment (only if monitor is wired).
	// Note: DecisionEngine.EvaluateOpportunity already applies this internally
	// when its own btcMonitor is set, so this is a no-op fallback when the
	// monitor lives only on the bot.
	if bot.btcMonitor != nil && bot.decisionEngine != nil {
		adjusted, adjustment := bot.decisionEngine.AdjustDecisionWithBTC(decision)
		if adjustment.Applied {
			decision = adjusted
			bot.log.WithFields(logrus.Fields{
				"btc_direction": adjustment.BTCDirection,
				"old_conf":      adjustment.OriginalConfidence,
				"new_conf":      adjustment.AdjustedConfidence,
			}).Debug("Decision adjusted by BTC context (post-LLM)")
		}
	}

	// Reset consecutive errors on success
	bot.mu.Lock()
	bot.consecutiveErrs = 0
	bot.mu.Unlock()

	bot.log.WithFields(logrus.Fields{
		"action":     decision.Action,
		"confidence": fmt.Sprintf("%.2f%%", decision.Confidence*100),
		"reason":     decision.Reason,
		"price":      marketCtx.CurrentPrice,
	}).Info("📈 Analysis complete")

	// Broadcast the signal dynamically to the UI (WebSocket). Pair comes from
	// the live bot config so smart-pair-switching is reflected in each signal.
	if bot.notificationManager != nil {
		bot.mu.RLock()
		symbol := bot.config.Pair
		bot.mu.RUnlock()
		if broadcaster, ok := bot.notificationManager.(interface {
			BroadcastSignal(symbol string, action string, confidence float64, price float64, reason string) error
		}); ok {
			bErr := broadcaster.BroadcastSignal(
				symbol,
				decision.Action,
				decision.Confidence,
				marketCtx.CurrentPrice,
				decision.Reason,
			)
			if bErr != nil {
				bot.log.WithError(bErr).Error("Failed to broadcast AI signal via WebSocket")
			}
		} else {
			bot.log.Errorf("NotificationManager type assertion failed! Actual type: %T", bot.notificationManager)
		}
	} else {
		bot.log.Warn("NotificationManager is nil during analysis loop, cannot broadcast signal")
	}

	// Feed the exit signal buffer so checkPosition can apply signal-driven exits.
	bot.mu.RLock()
	exitPair := bot.config.Pair
	bot.mu.RUnlock()
	bot.recordExitSignal(exitPair, decision.Action, decision.Confidence)

	// Check if we should execute
	if decision.Action == "WAIT" {
		return
	}

	// Pattern library veto: if a high-confidence pattern contradicts the decision, skip.
	if bot.patternLibrary != nil && marketCtx != nil {
		bot.mu.RLock()
		plPair := bot.config.Pair
		bot.mu.RUnlock()
		var candles []ai.Candle
		if primaryCtx := marketCtx.GetPrimaryContext(); primaryCtx != nil && len(primaryCtx.Candles) > 0 {
			candles = primaryCtx.Candles
		}
		if len(candles) >= 10 {
			pattern := bot.patternLibrary.ExtractPattern(plPair, "5min", candles)
			patAction, patConf, patReason, patOK := bot.patternLibrary.GetSignal(pattern)
			if patOK && patConf > 0.6 && patAction != "WAIT" && patAction != decision.Action {
				bot.log.WithFields(logrus.Fields{
					"decision_action": decision.Action,
					"pattern_action":  patAction,
					"pattern_conf":    patConf,
					"pattern_reason":  patReason,
				}).Info("Pattern library veto: conflicting signal")
				return
			}
		}
	}

	// Confidence gate — read the live threshold from dynamic config so it can be
	// tuned in real time (data showed <70%-confidence trades lose money at 45%
	// win rate, while 90%+ trades win; raising this cuts the losing low-conf
	// bucket and reduces fee-bleeding over-trading). Falls back to static config.
	confThreshold := bot.config.ConfidenceThreshold
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.confidence_threshold"); v > 0 {
			confThreshold = v
		}
	}
	if decision.Confidence < confThreshold {
		bot.log.WithFields(logrus.Fields{
			"confidence": decision.Confidence,
			"threshold":  confThreshold,
		}).Debug("Confidence below threshold - skipping")
		return
	}

	// Validation gate (opt-in via bot.validation_gate.enabled, default off):
	// refuse live entries on pairs whose strategy has not passed out-of-sample
	// validation. This is the "only trade validated edges" discipline — a pair
	// with no demonstrable edge does not risk capital. The background validator
	// keeps certifications fresh; expired/failed pairs are blocked here.
	//
	// FAIL-CLOSED (2026-06-10): a nil dynamic snapshot (config-DB hiccup,
	// context without snapshot) previously read as gateOn=false, silently
	// disabling the gate and letting the bot enter real trades while the
	// operator believed it was flat (observed: entry on 2026-06-09 21:54 with
	// the gate set true in the DB). Now: remember the last value actually seen;
	// if none was ever seen, assume ON (block) — never fail open.
	if bot.validationGate != nil {
		var gateOn bool
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			gateOn = snap.Bool("bot.validation_gate.enabled")
			bot.gateLastOn.Store(gateOn)
			bot.gateSeen.Store(true)
		} else if bot.gateSeen.Load() {
			gateOn = bot.gateLastOn.Load()
			bot.log.Warn("validation gate: dynamic snapshot missing — using last-known gate state (fail-closed policy)")
		} else {
			gateOn = true
			bot.log.Warn("validation gate: dynamic snapshot never seen — assuming gate ON (fail-closed)")
		}
		if gateOn {
			gatePair := bot.config.Pair
			if !bot.validationGate.IsCertified(gatePair, bot.config.Exchange) {
				bot.log.WithFields(logrus.Fields{
					"pair":   gatePair,
					"action": decision.Action,
				}).Info("🔒 Validation gate: pair not certified (no validated edge) — skipping live entry")
				return
			}
		}
	}

	// Anti-whipsaw entry gate: require the same-direction, above-threshold
	// signal to persist for N consecutive analysis ticks before opening. In
	// ranging markets the multi-timeframe trend alignment flips tick-to-tick,
	// producing transient high-confidence spikes that reverse within ~90s
	// (observed: TON SELL→WAIT→BUY). MinConsecutiveSignals: 0 → default 2; 1 → off.
	required := bot.config.MinConsecutiveSignals
	if required == 0 {
		required = 2
	}
	if required > 1 {
		bot.mu.RLock()
		gatePair := bot.config.Pair
		bot.mu.RUnlock()
		if !bot.passesSignalPersistence(gatePair, decision.Action, required) {
			bot.log.WithFields(logrus.Fields{
				"pair":       gatePair,
				"action":     decision.Action,
				"confidence": decision.Confidence,
				"required":   required,
			}).Info("⏳ Entry gate: awaiting signal confirmation (anti-whipsaw) — skipping")
			return
		}
	}

	// Reconcile local state with exchange before deciding. The 2-min periodic
	// sync isn't enough — between syncs the exchange can close a position via
	// its own SL/TP and the local manager keeps a ghost. Forcing a sync here
	// guarantees local matches exchange before we evaluate the open-count.
	bot.syncOpenPositionsFromExchange(ctx)

	// Check max open positions using the internal manager — which only contains
	// positions the AI bot itself opened. Exchange positions from scalping bots on
	// the same pair must NOT block AI bot entries; they are handled at execution
	// time (executeTrade checks for conflicting same-direction exchange positions).
	openPositions := len(bot.positionManager.GetAllPositions())
	maxOpen := bot.effectiveMaxOpenPositions(ctx)
	if openPositions >= maxOpen {
		bot.log.WithFields(logrus.Fields{
			"open_positions": openPositions,
			"max_open":       maxOpen,
		}).Warn("🚫 Max open positions reached — skipping")
		return
	}

	// Per-pair guard: block a second entry on the same pair regardless of the
	// global max. Prevents inherited/adopted positions from racing with new
	// entries (observed 2026-06-06: 7 SOL positions × same entry $61.774 closed
	// sequentially by SL while the bot kept entering). openPositionsForPair uses
	// normalized symbol matching so "SOLUSDT" == "SOL-USDT".
	if bot.openPositionsForPair(pair) > 0 {
		bot.log.WithField("pair", pair).Debug("🚫 Already holding this pair — skipping new entry")
		return
	}

	if !bot.config.AutoExecute {
		bot.log.WithFields(logrus.Fields{
			"auto_execute":    bot.config.AutoExecute,
			"autonomous_mode": bot.config.AutonomousMode,
			"action":          decision.Action,
		}).Info("auto_execute=false — skipping trade execution")
		return
	}

	// ML entry gate: only take the trade if the GBM predictor agrees with its
	// direction. The model (PF≈2.6 / AUC≈0.82 OOS) was previously consulted only for
	// sizing — the bot entered even when the model pointed the other way, which is a
	// big source of the confidently-wrong shorts that bled into stops. When enabled
	// (default ON) a disagreeing or no-conviction (WAIT) model vote vetoes the entry.
	// Fails OPEN if the model is unavailable so a down ml-server never blocks trading.
	mlGateEnabled := true
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.BoolOk("bot.entry.ml_gate.enabled"); ok {
			mlGateEnabled = v
		}
	}
	if mlGateEnabled {
		// In a ranging (mean-reversion) regime the GBM — which is trend-following —
		// predicts continuation and would veto the fade entries that actually carry the
		// edge there. Skip the ML veto in RANGING/UNKNOWN when configured to.
		skipML := false
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			if v, ok := snap.BoolOk("bot.entry.ml_gate.skip_in_ranging"); ok && v {
				reg := bot.currentRegimeLabel()
				if reg == string(regime.RegimeRanging) || reg == string(regime.RegimeUnknown) || reg == "" {
					skipML = true
				}
			}
		}
		if skipML {
			bot.log.WithField("regime", bot.currentRegimeLabel()).
				Info("ML entry gate skipped — non-trending regime (GBM is trend-following; would veto reversion entries)")
		} else if agrees, mlProb, mlAction, evaluated := bot.mlEntryVote(marketCtx, decision.Action); evaluated && !agrees {
			bot.log.WithFields(logrus.Fields{
				"action":    decision.Action,
				"ml_action": mlAction,
				"ml_prob":   fmt.Sprintf("%.3f", mlProb),
			}).Warn("🚫 ML entry gate — model disagrees with direction, skipping trade")
			return
		}
	}

	bot.log.WithFields(logrus.Fields{
		"auto_execute":    bot.config.AutoExecute,
		"autonomous_mode": bot.config.AutonomousMode,
		"action":          decision.Action,
	}).Info("Executing trade")

	bot.executeTrade(ctx, decision, marketCtx)
}

// exitSignalRecord stores one AI signal observation for position exit analysis.
type exitSignalRecord struct {
	action     string // "BUY", "SELL", "WAIT"
	confidence float64
	recordedAt time.Time
}

// exitSignalWindowSize is the max number of observations kept per pair.
const exitSignalWindowSize = 8

// recordExitSignal appends a signal to the per-pair exit window and trims it
// to the last exitSignalWindowSize entries. Called from runAnalysis after every
// decision so checkPosition always has a fresh view of recent signal history.
func (bot *AITradingBot) recordExitSignal(pair, action string, confidence float64) {
	bot.exitSignalWindowsMu.Lock()
	defer bot.exitSignalWindowsMu.Unlock()

	rec := exitSignalRecord{action: action, confidence: confidence, recordedAt: time.Now()}
	window := append(bot.exitSignalWindows[pair], rec)
	if len(window) > exitSignalWindowSize {
		window = window[len(window)-exitSignalWindowSize:]
	}
	bot.exitSignalWindows[pair] = window
}

// checkSignalDrivenExit applies three intelligent exit strategies that use the
// rolling signal window to close positions before SL is hit:
//
//  1. Signal-driven exit: if N consecutive opposite-direction signals with
//     confidence ≥ threshold appear, close immediately.
//  2. Dynamic SL tightening: on the first opposite signal ≥ minTightenConf,
//     move SL to currentPrice minus a small buffer to protect capital.
//  3. Confidence-decay exit: if the rolling average confidence in the current
//     position direction drops below decayThreshold AND the position is in
//     profit, close to lock gains before momentum fully dies.
//
// Returns true + reason when the position should be closed.
// Returns false, "" when no exit is warranted (caller should continue to SL/TP check).
func (bot *AITradingBot) checkSignalDrivenExit(
	ctx context.Context,
	pos *ai.ManagedPosition,
	currentPrice, pnlPct float64,
) (shouldClose bool, reason string) {
	snap := dynamic.SnapshotFromContext(ctx)

	// ── config (all hot-reloadable) ──────────────────────────────────────────
	requiredConfirmations := 3
	exitConfThreshold := 0.72
	minTightenConf := 0.75
	// slTightenBuffer is expressed in % (e.g. 0.60 = 0.60%) and divided by 100
	// before use so the code works with price fractions throughout.
	slTightenBufferPct := 0.60
	decayThreshold := 0.35
	signalMaxAge := 5 * time.Minute
	tightenMinAge := 120 * time.Second // grace period: no SL tighten within first N sec

	if snap != nil {
		if v := snap.Float("exit.signal_driven.conf_threshold"); v > 0 {
			exitConfThreshold = v
		}
		if v := snap.Float("exit.signal_driven.tighten_conf"); v > 0 {
			minTightenConf = v
		}
		if v := snap.Float("exit.signal_driven.sl_tighten_buffer"); v > 0 {
			slTightenBufferPct = v
		}
		if v := snap.Float("exit.signal_driven.decay_threshold"); v > 0 {
			decayThreshold = v
		}
		if n := snap.Int("exit.signal_driven.required_confirmations"); n > 0 {
			requiredConfirmations = n
		}
		if n := snap.Int("exit.signal_driven.tighten_min_age_sec"); n >= 0 {
			tightenMinAge = time.Duration(n) * time.Second
		}
	}
	slTightenBuffer := slTightenBufferPct / 100

	side := strings.ToUpper(pos.Side)
	isLong := side == "BUY" || side == "LONG"
	oppositeAction := "SELL"
	if !isLong {
		oppositeAction = "BUY"
	}

	bot.exitSignalWindowsMu.Lock()
	window := bot.exitSignalWindows[pos.Symbol]
	bot.exitSignalWindowsMu.Unlock()

	if len(window) == 0 {
		return false, ""
	}

	now := time.Now()

	// Drop stale records from analysis (but keep them in the map as-is).
	fresh := make([]exitSignalRecord, 0, len(window))
	for _, r := range window {
		if now.Sub(r.recordedAt) <= signalMaxAge {
			fresh = append(fresh, r)
		}
	}
	if len(fresh) == 0 {
		return false, ""
	}

	// ── Strategy 1: signal-driven exit ───────────────────────────────────────
	// Count trailing consecutive opposite-direction signals above threshold.
	consecutiveOpposite := 0
	for i := len(fresh) - 1; i >= 0; i-- {
		r := fresh[i]
		if r.action == oppositeAction && r.confidence >= exitConfThreshold {
			consecutiveOpposite++
		} else {
			break
		}
	}
	if consecutiveOpposite >= requiredConfirmations {
		return true, fmt.Sprintf(
			"signal_reversal:%d_consecutive_%s_conf>=%.0f%%",
			consecutiveOpposite, oppositeAction, exitConfThreshold*100,
		)
	}

	// ── Strategy 2: dynamic SL tightening ────────────────────────────────────
	// On first opposite signal ≥ minTightenConf, snap SL to current price
	// minus a buffer so any further sustained adverse move closes the position.
	// Grace period: skip tightening for tightenMinAge seconds after entry so
	// the trade can breathe through normal oscillation before reacting.
	latestOpposite := fresh[len(fresh)-1]
	tradeAge := now.Sub(pos.OpenedAt)
	if latestOpposite.action == oppositeAction && latestOpposite.confidence >= minTightenConf &&
		tradeAge >= tightenMinAge {
		bot.exitSignalWindowsMu.Lock()
		lastTighten := bot.slTightenedBySignal[pos.Symbol]
		bot.exitSignalWindowsMu.Unlock()

		// Only tighten once per analysis cycle (not every tick).
		if now.Sub(lastTighten) > 30*time.Second {
			var newSL float64
			if isLong {
				newSL = currentPrice * (1 - slTightenBuffer)
			} else {
				newSL = currentPrice * (1 + slTightenBuffer)
			}
			// Only tighten, never loosen.
			tightenApplied := false
			if isLong && newSL > pos.StopLoss {
				pos.StopLoss = newSL
				tightenApplied = true
			} else if !isLong && newSL < pos.StopLoss {
				pos.StopLoss = newSL
				tightenApplied = true
			}
			if tightenApplied {
				bot.exitSignalWindowsMu.Lock()
				bot.slTightenedBySignal[pos.Symbol] = now
				bot.exitSignalWindowsMu.Unlock()
				bot.log.WithFields(logrus.Fields{
					"position_id":     pos.ID,
					"symbol":          pos.Symbol,
					"opposite_signal": oppositeAction,
					"conf":            fmt.Sprintf("%.0f%%", latestOpposite.confidence*100),
					"new_sl":          fmt.Sprintf("%.5f", newSL),
					"buffer_pct":      fmt.Sprintf("%.2f%%", slTightenBuffer*100),
				}).Info("🔧 SL tightened by reversal signal — protecting capital")
			}
		}
	}

	// ── Strategy 3: confidence decay exit ────────────────────────────────────
	// Compute rolling average of the last N signals' confidence FOR the current
	// position direction. A sustained drop means the AI is losing conviction.
	//
	// Three profit bands (price-% move, NOT leveraged) make this a winner-friendly
	// guard instead of the old "close at any +0.01%" behavior that flat-closed
	// trades at +0.19% — below round-trip fees — and capped the upside:
	//   • below minDecayProfitPct: noise, never decay-exit (let TP/SL/trailing run)
	//   • [minDecayProfitPct, holdProfitPct): modest gain, close to protect it
	//   • at/above holdProfitPct: strong winner — lock the gain via SL tighten and
	//     let it run rather than flat-closing, so winners can reach the wider TP.
	minDecayProfitPct := 0.8 // price-% floor below which decay never fires
	holdProfitPct := 1.5     // above this, lock-in and let it run
	if snap != nil {
		if v := snap.Float("exit.signal_driven.min_profit_pct"); v > 0 {
			minDecayProfitPct = v
		}
		if v := snap.Float("exit.signal_driven.hold_profit_pct"); v > 0 {
			holdProfitPct = v
		}
	}
	if pnlPct >= minDecayProfitPct {
		currentDirAction := "BUY"
		if !isLong {
			currentDirAction = "SELL"
		}
		var sumConf float64
		var n int
		for _, r := range fresh {
			if r.action == currentDirAction {
				sumConf += r.confidence
				n++
			} else if r.action == "WAIT" {
				// WAIT with low confidence penalizes the average
				sumConf += 0.25
				n++
			}
		}
		if n > 0 {
			avgConf := sumConf / float64(n)
			if avgConf < decayThreshold {
				// Strong winner: lock the gain via SL tighten and let it run.
				if pnlPct >= holdProfitPct {
					var newSL float64
					if isLong {
						newSL = currentPrice * (1 - slTightenBuffer)
					} else {
						newSL = currentPrice * (1 + slTightenBuffer)
					}
					tightened := false
					if isLong && newSL > pos.StopLoss {
						pos.StopLoss = newSL
						tightened = true
					} else if !isLong && newSL < pos.StopLoss {
						pos.StopLoss = newSL
						tightened = true
					}
					if tightened {
						bot.log.WithFields(logrus.Fields{
							"position_id": pos.ID,
							"symbol":      pos.Symbol,
							"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
							"avg_conf":    fmt.Sprintf("%.0f%%", avgConf*100),
							"new_sl":      fmt.Sprintf("%.5f", newSL),
						}).Info("🔒 Confidence decay on strong winner — gain locked via SL, letting it run")
					}
					return false, ""
				}
				return true, fmt.Sprintf(
					"confidence_decay:avg_conf=%.0f%%<%.0f%%_while_profitable",
					avgConf*100, decayThreshold*100,
				)
			}
		}
	}

	return false, ""
}

// signalStreak tracks consecutive same-direction, above-threshold signals for
// a pair, used by the anti-whipsaw entry gate.
type signalStreak struct {
	action string
	count  int
	last   time.Time
}

// passesSignalPersistence reports whether the current above-threshold signal
// for pair has persisted in the same direction for `required` consecutive
// analysis ticks. A direction flip, or a gap longer than ~1.5× the analysis
// interval (meaning an intervening WAIT / low-confidence tick broke the run),
// restarts the streak at 1. This filters the transient confidence spikes that
// reverse within a tick or two in ranging markets (observed: TON SELL→WAIT→BUY
// in ~90s), which is what made the bot churn on noise.
func (bot *AITradingBot) passesSignalPersistence(pair, action string, required int) bool {
	bot.entryStreakMu.Lock()
	defer bot.entryStreakMu.Unlock()

	now := time.Now()
	maxGap := 3 * bot.config.AnalysisInterval / 2 // 1.5× the analysis interval
	if maxGap <= 0 {
		maxGap = 45 * time.Second
	}

	st := bot.entryStreaks[pair]
	if st == nil || st.action != action || now.Sub(st.last) > maxGap {
		bot.entryStreaks[pair] = &signalStreak{action: action, count: 1, last: now}
		return required <= 1
	}
	st.count++
	st.last = now
	return st.count >= required
}

// executeTrade executes a trade based on AI decision
func (bot *AITradingBot) executeTrade(ctx context.Context, decision *ai.Decision, marketCtx *ai.MarketContext) {
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in executeTrade - recovered")
		}
	}()

	result := &ExecutionResult{
		Timestamp:  time.Now(),
		Action:     decision.Action,
		Confidence: decision.Confidence,
		Tag:        manualTag(decision),
	}

	// Ring 0 (global kill switch): never open positions, regardless of whether
	// the loops are still alive or who called executeTrade.
	if killswitch.Ring0Active() {
		bot.log.Warn("🛑 Ring 0 global kill switch active — aborting trade execution")
		result.Success = false
		result.Error = killswitch.ErrActive
		select {
		case bot.resultChan <- result:
		default:
		}
		return
	}

	// BOT-02 fix: defense-in-depth — re-check the kill switch here too, since
	// executeTrade can run some time after runAnalysis scheduled it and is
	// also the entry point other callers may use directly.
	if bot.killSwitchActive(ctx) {
		bot.log.Warn("🛑 RiskSentinel kill switch active — aborting trade execution")
		result.Success = false
		result.Error = fmt.Errorf("blocked by RiskSentinel kill switch")
		bot.resultChan <- result
		return
	}

	// Calendar gate: block entries during high-impact macro events.
	if bot.calendarGate != nil && !bot.calendarGate.IsTradeAllowed(ctx) {
		bot.log.Info("📅 Bot: trade blocked by macro calendar (high-impact event imminent)")
		result := &ExecutionResult{
			Timestamp: time.Now(),
			Action:    decision.Action,
			Success:   false,
			Error:     fmt.Errorf("blocked by macro calendar"),
			Tag:       manualTag(decision),
		}
		bot.resultChan <- result
		return
	}

	// Dry run mode
	if bot.config.DryRun {
		bot.log.WithFields(logrus.Fields{
			"action":     decision.Action,
			"confidence": decision.Confidence,
			"entry":      decision.Entry,
			"sl":         decision.StopLoss,
			"tp":         decision.TakeProfit,
		}).Info("🧪 DRY RUN - Would execute trade")
		result.Success = true
		result.OrderID = fmt.Sprintf("DRY_RUN_%d", time.Now().Unix())
		bot.resultChan <- result

		// Send AI-enhanced notification even in dry run mode
		bot.sendTradeNotificationWithAI(ctx, decision, marketCtx, result.OrderID)

		return
	}

	// Get dynamic execution parameters
	bot.mu.RLock()
	tradeSize := bot.config.TradeSize
	leverage := bot.config.Leverage
	exchange := bot.config.Exchange
	pair := bot.config.Pair
	effectiveSLPct := bot.config.DefaultStopLossPct
	effectiveTPPct := bot.config.DefaultTakeProfitPct
	// TODO(TC-3.1): wire ctx into selectSizingTier for dynamic config
	tier := bot.selectSizingTier(decision.Confidence)
	bot.mu.RUnlock()

	// Capital-relative sizing: the base notional comes from the resolver (a %
	// of the real account capital in capital_pct mode, the YAML size in
	// absolute mode). Without a capital reading the entry is skipped instead of
	// trading a guessed size (fail-closed).
	if resolved, err := bot.resolveNotional(ctx, nil); err != nil {
		bot.log.WithError(err).WithField("pair", pair).Warn("📏 Skipping entry — sizing could not be resolved")
		result.Success = false
		result.Error = fmt.Errorf("sizing: %w", err)
		bot.resultChan <- result
		return
	} else {
		tradeSize = resolved
	}

	// Operator manual entry: the stated size replaces the confidence-tier
	// ladder (confidence is nominal); without a stated size it trades the
	// resolved base notional. Every safety gate below still applies.
	if isManualEntry(decision) {
		tier = nil
		if decision.Size > 0 {
			tradeSize = decision.Size
		}
	}

	// AI-02 fix: run PreTradeValidator/rule-engine/circuit-breaker (previously
	// only reachable from the test endpoint) before this real order is
	// submitted. Independent of BOT-02's RiskSentinel kill switch check above —
	// the sentinel gates account-level order submission (size/exposure caps,
	// Ring0/1/2), this validates decision quality (R:R ratio, leverage sanity,
	// configured rule set) and its own circuit breaker keyed off this bot's
	// dailyLoss/consecutiveLosses. Neither supersedes the other; both run.
	//
	// Validate the levels the order will actually carry: normalize SL/TP (fix
	// inverted levels, apply the SL floor) and widen the TP to the minimum R:R
	// first. Before this the validator judged the raw technical levels — whose
	// TP is clamped to the nearest S/R — and blocked every real order on
	// POOR_RISK_REWARD / INVALID_TP_PLACEMENT (2026-11-02) while the fixes
	// below only ran after it.
	bot.normalizeDecisionLevels(ctx, decision, marketCtx)
	if ok, reason := bot.runPreTradeValidation(decision, marketCtx, pair, exchange, tradeSize, leverage); !ok {
		result.Success = false
		result.Error = fmt.Errorf("blocked by pre-trade validation: %s", reason)
		bot.resultChan <- result
		return
	}

	// Manual re-entry hold: the operator closed this pair under
	// bot.reentry.mode="manual" and hasn't re-enabled it. Honor that — don't
	// re-open the pair the operator deliberately exited (e.g. took profit ahead
	// of an expected pullback).
	if bot.reentryHeld(ctx, pair) {
		bot.log.WithField("pair", pair).Info("✋ Skipping entry — pair on manual re-entry hold (set bot.reentry.mode=auto to re-enable)")
		result.Success = false
		result.Error = fmt.Errorf("pair %s on manual re-entry hold", pair)
		bot.resultChan <- result
		return
	}

	// Blacklist gate: auto-pause (toxic pair) and exchange-refusal blacklists are
	// checked in maybeSwitchPair when picking a NEW pair, but a pair that was
	// blacklisted while it was still bot.config.Pair could otherwise enter here
	// before the next maybeSwitchPair cycle replaces it. Re-check at the entry
	// gate so a just-paused pair can't slip one more losing trade through.
	if bot.isPairBlacklisted(pair) {
		bot.log.WithField("pair", pair).Info("🚫 Skipping entry — pair is blacklisted (auto-pause/refusal)")
		result.Success = false
		result.Error = fmt.Errorf("pair %s is blacklisted", pair)
		bot.resultChan <- result
		return
	}

	// XSEC-ownership gate: refuse the entry outright when this pair is
	// currently owned by the xsec-live basket trader (per xsec_live_book).
	// This is the last line of defense and blocks BOTH buy and sell — on
	// KuCoin one-way mode either direction would net against xsec-live's
	// position, silently shrinking or flipping it. See xsecOwnedPairs.
	if xsecOwned := bot.xsecOwnedPairs(ctx); xsecOwned != nil {
		if _, isXSEC := xsecOwned[normalizeSymbolForMatch(pair)]; isXSEC {
			bot.log.WithField("pair", pair).Warn("🚫 Skipping entry — pair is owned by xsec-live basket trader")
			result.Success = false
			result.Error = fmt.Errorf("pair %s is owned by xsec-live basket", pair)
			bot.resultChan <- result
			return
		}
	}

	// Global loss-streak throttle: after K consecutive losing closes ACROSS
	// ALL PAIRS, every new entry pauses for a cooldown. Losses cluster in
	// account-wide time windows (choppy market hits every pair at once), so
	// the per-pair auto-pause above cannot catch this. See GlobalLossStreakReader.
	if paused, streak, resumeAt := bot.globalStreakPaused(ctx); paused {
		bot.log.WithFields(logrus.Fields{
			"pair":      pair,
			"streak":    streak,
			"resume_at": resumeAt.Format(time.RFC3339),
		}).Info("⏸️ Skipping entry — global loss-streak pause active")
		result.Success = false
		result.Error = fmt.Errorf("global loss-streak pause: %d consecutive losses account-wide, entries resume %s", streak, resumeAt.Format(time.RFC3339))
		bot.resultChan <- result
		return
	}

	// Post-SL cooldown: skip re-entry if this pair was stopped out recently.
	// Prevents "death by 1000 cuts" — entering, getting SL'd, re-entering.
	// (RAVE-USDT 17 entries in 9 min on 2026-05-28 cost ~$1.20 cumulative.)
	if remaining := bot.pairCooldownRemaining(pair); remaining > 0 {
		bot.log.WithFields(logrus.Fields{
			"pair":               pair,
			"cooldown_remaining": remaining.Round(time.Second).String(),
		}).Info("🧊 Skipping entry — pair on post-SL cooldown")
		result.Success = false
		result.Error = fmt.Errorf("pair %s on post-SL cooldown for %s", pair, remaining.Round(time.Second))
		bot.resultChan <- result
		return
	}

	// Override sizing tiers from dynamic config when available, falling back to
	// the YAML-populated bot.config.DynamicSizing.Tiers.
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil && !isManualEntry(decision) {
		var dynTiers []SizingTier
		if err := snap.JSON("dynamic_sizing.tiers", &dynTiers); err == nil && len(dynTiers) > 0 {
			// Re-select tier from the live config values.
			tier = nil
			for i := range dynTiers {
				t := &dynTiers[i]
				if decision.Confidence >= t.MinConfidence && decision.Confidence <= t.MaxConfidence {
					tier = t
					break
				}
			}
		}
	}

	// Apply confidence-tier overrides. The tier wins over the flat defaults
	// but Kelly (below) can still override the size if enabled.
	if tier != nil {
		if tier.SizeUSD > 0 || tier.SizePct > 0 {
			// Same resolver as the base size: tier % of capital (or the
			// tier's USD in absolute mode), clamped to the notional floor/ceiling.
			if sized, err := bot.resolveNotional(ctx, tier); err == nil {
				tradeSize = sized
			}
		}
		if tier.StopLossPct > 0 {
			effectiveSLPct = tier.StopLossPct
		}
		if tier.TakeProfitPct > 0 {
			effectiveTPPct = tier.TakeProfitPct
		}
		bot.log.WithFields(logrus.Fields{
			"tier":       tier.Name,
			"confidence": fmt.Sprintf("%.2f%%", decision.Confidence*100),
			"size":       fmt.Sprintf("$%.2f", tradeSize),
			"tp_pct":     fmt.Sprintf("%.2f%%", effectiveTPPct),
			"sl_pct":     fmt.Sprintf("%.2f%%", effectiveSLPct),
		}).Info("📊 Sizing tier selected")
	}

	// Macro trend alignment filter (EMA of a higher timeframe).
	// Prevents the bot from entering LONG in a bearish macro trend or SHORT in a
	// bullish one — the main cause of consecutive stop-outs when 1m/5m signals
	// disagree with the dominant trend (e.g. STG shorted during a 10% uptrend).
	// The 1h-EMA50 distance is computed by macroTrendDistance (per-pair cached,
	// shared with the directional-bias path). All knobs are dynamic config
	// (bot.trend_filter.*); fails open if the trend can't be computed.
	trendFilterEnabled := true
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.BoolOk("bot.trend_filter.enabled"); ok {
			trendFilterEnabled = v
		}
	}
	if isManualEntry(decision) {
		trendFilterEnabled = false
		bot.log.WithFields(logrus.Fields{"pair": pair, "action": decision.Action}).
			Warn("🧑‍✈️ Manual entry: macro trend filter and order-flow gate bypassed by operator")
	}
	if trendFilterEnabled && bot.gctClient != nil {
		distancePct, _, ema50, currentClose, ok := bot.macroTrendDistance(ctx, exchange, pair)
		if !ok {
			// Fail open, but make it VISIBLE — a silent fail-open let HYPE get
			// shorted against a +4% macro uptrend on 2026-06-01.
			bot.log.WithFields(logrus.Fields{
				"pair":   pair,
				"action": decision.Action,
			}).Warn("⚠️ Trend filter: macro trend unavailable — allowing trade (fail-open)")
		} else {
			// Only block when price is meaningfully on the wrong side (default ≥0.3%).
			minDistancePct := 0.003
			if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
				if v := snap.Float("bot.trend_filter.min_distance_pct"); v > 0 {
					minDistancePct = v / 100
				}
			}
			if decision.Action == "BUY" && distancePct < -minDistancePct {
				bot.log.WithFields(logrus.Fields{
					"pair":         pair,
					"action":       "BUY",
					"close":        currentClose,
					"ema50_1h":     ema50,
					"distance_pct": fmt.Sprintf("%.2f%%", distancePct*100),
				}).Warn("🚫 Trend filter: LONG blocked — price below 1h EMA50 (bearish macro)")
				bot.recordTrendBlock(ctx, pair)
				result.Success = false
				result.Error = fmt.Errorf("trend filter: BUY blocked, price %.4f is %.2f%% below 1h EMA50 %.4f", currentClose, -distancePct*100, ema50)
				bot.resultChan <- result
				return
			}
			if decision.Action == "SELL" && distancePct > minDistancePct {
				bot.log.WithFields(logrus.Fields{
					"pair":         pair,
					"action":       "SELL",
					"close":        currentClose,
					"ema50_1h":     ema50,
					"distance_pct": fmt.Sprintf("%.2f%%", distancePct*100),
				}).Warn("🚫 Trend filter: SHORT blocked — price above 1h EMA50 (bullish macro)")
				bot.recordTrendBlock(ctx, pair)
				result.Success = false
				result.Error = fmt.Errorf("trend filter: SELL blocked, price %.4f is %.2f%% above 1h EMA50 %.4f", currentClose, distancePct*100, ema50)
				bot.resultChan <- result
				return
			}
			// Aligned with the macro trend — clear any accumulated block count.
			bot.resetTrendBlockCount(pair)
			bot.log.WithFields(logrus.Fields{
				"pair":         pair,
				"action":       decision.Action,
				"close":        currentClose,
				"ema50_1h":     fmt.Sprintf("%.4f", ema50),
				"distance_pct": fmt.Sprintf("%.2f%%", distancePct*100),
			}).Info("✅ Trend filter: aligned with 1h EMA50")
		}
	}

	// Check order flow imbalance — strong sell pressure overrides BUY signals.
	bot.orderFlowCacheMu.RLock()
	flowAnalysis := bot.orderFlowCache[pair]
	bot.orderFlowCacheMu.RUnlock()
	// TC-4.6: bot.order_flow.imbalance_block (default -0.35)
	imbalanceBlock := -0.35
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.order_flow.imbalance_block"); v != 0 {
			imbalanceBlock = v
		}
	}
	if flowAnalysis != nil && decision.Action == "BUY" && flowAnalysis.Imbalance < imbalanceBlock && !isManualEntry(decision) {
		bot.log.WithFields(logrus.Fields{
			"imbalance": flowAnalysis.Imbalance,
			"signal":    flowAnalysis.Signal,
		}).Info("Order flow: strong sell pressure — skipping BUY")
		result.Success = false
		result.Error = fmt.Errorf("blocked by order flow: imbalance=%.2f", flowAnalysis.Imbalance)
		bot.resultChan <- result
		return
	}

	// Reset the ephemeral ML-prob carrier so a value from a previous entry attempt
	// can never leak into this position if the ML predictor is not queried below.
	bot.setLastMLProb(0)

	// Optional: Override trade size with Kelly sizing if enabled.
	if bot.kellyEnabled && bot.riskCalculator != nil && !isManualEntry(decision) {
		if kellySize := bot.calculateKellyOrderSize(decision, marketCtx, tradeSize); kellySize > 0 {
			bot.log.WithFields(logrus.Fields{
				"original_size": tradeSize,
				"kelly_size":    kellySize,
			}).Info("💎 Kelly sizing override applied")
			tradeSize = kellySize
		}
	}

	// Volatility protection: scale size, widen SL, or block entries based on ATR%.
	// Applied AFTER all other sizing (tier, Kelly) so it acts as a final safety filter.
	// Returns block=true if volatility is extreme — abort the trade entirely.
	//
	// We must compute ATR locally here because (1) marketCtx.Indicators.ATR is
	// often 0 (not populated by every analyzer path) and (2) the indicatorCache
	// is only filled when a position is already open, leading to a chicken-and-egg
	// problem on first trade for a pair. Computing from the candles we already
	// have in marketCtx avoids both issues.
	atrFromCtx := 0.0
	if primary := marketCtx.GetPrimaryContext(); primary != nil {
		if primary.Indicators != nil && primary.Indicators.ATR > 0 {
			atrFromCtx = primary.Indicators.ATR
		} else if len(primary.Candles) > 0 {
			atrFromCtx = computeATRFromCandles(primary.Candles, 14)
		}
	}
	if adj := bot.applyVolatilityProtection(ctx, pair, marketCtx.CurrentPrice, atrFromCtx, tradeSize, effectiveSLPct); adj.Block {
		bot.log.WithFields(logrus.Fields{
			"pair":       pair,
			"volatility": fmt.Sprintf("%.2f%%", adj.VolatilityPct),
			"reason":     adj.BlockReason,
		}).Warn("🛑 Trade BLOCKED by volatility circuit breaker")
		result.Success = false
		result.Error = fmt.Errorf("volatility circuit breaker: %s", adj.BlockReason)
		bot.resultChan <- result
		return
	} else if adj.Applied {
		bot.log.WithFields(logrus.Fields{
			"pair":        pair,
			"volatility":  fmt.Sprintf("%.2f%%", adj.VolatilityPct),
			"tier":        adj.Tier,
			"size_before": fmt.Sprintf("$%.2f", tradeSize),
			"size_after":  fmt.Sprintf("$%.2f", adj.AdjustedSize),
			"sl_before":   fmt.Sprintf("%.2f%%", effectiveSLPct),
			"sl_after":    fmt.Sprintf("%.2f%%", adj.AdjustedSLPct),
		}).Warn("⚡ Volatility protection applied — size reduced, SL widened")
		tradeSize = adj.AdjustedSize
		effectiveSLPct = adj.AdjustedSLPct
	}

	// === Stop-loss floor ===
	// The tier ladder and volatility protection can drive the effective stop
	// below the noise floor (recent data: 85% of losers died on <0.5% stops,
	// avg -0.4% in ~2min, while every stop >=0.8% was net positive). Enforce a
	// configurable minimum HERE — before buffers, the post-fill recompute, and
	// the R:R widening — so the floor survives all downstream clamping instead
	// of being shrunk back by maxSLBuffer (=1.5x effectiveSLPct). Configurable
	// via scoring.sl_floor_pct (default 0.8%); set 0 to disable.
	slFloorPct := 0.8
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.FloatOk("scoring.sl_floor_pct"); ok {
			slFloorPct = v
		}
	}
	if slFloorPct > 0 && effectiveSLPct < slFloorPct {
		bot.log.WithFields(logrus.Fields{
			"pair":      pair,
			"sl_before": fmt.Sprintf("%.2f%%", effectiveSLPct),
			"sl_floor":  fmt.Sprintf("%.2f%%", slFloorPct),
		}).Info("🛡️ SL floor applied — raising effective stop to minimum distance")
		effectiveSLPct = slFloorPct
	}

	// === Dynamic leverage by volatility tier ===
	// Leverage multiplies absolute $ PnL but NOT the price-% stop distance, so
	// cutting leverage on volatile pairs directly shrinks the worst-case loss
	// without widening the SL. Stable pairs keep full leverage for upside.
	leverage = bot.resolveDynamicLeverage(ctx, pair, leverage)

	// === Risk:Reward enforcement ===
	// Root cause of the negative edge: the bot cut winners early (avg +0.53%
	// price move) but let losers run to a wide SL (avg -1.32%), an inverted
	// ~1:2.5 R:R. Force TP to be at least minRR× the SL distance so every trade
	// carries positive expectancy headroom. We widen TP rather than tighten SL —
	// tightening SL would just trade more stop-outs for the same problem.
	minRR := 2.0
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.risk_reward.min_ratio"); v > 0 {
			minRR = v
		}
	}
	if effectiveSLPct > 0 && effectiveTPPct < minRR*effectiveSLPct {
		oldTP := effectiveTPPct
		effectiveTPPct = minRR * effectiveSLPct
		bot.log.WithFields(logrus.Fields{
			"pair":      pair,
			"sl_pct":    fmt.Sprintf("%.2f%%", effectiveSLPct),
			"tp_before": fmt.Sprintf("%.2f%%", oldTP),
			"tp_after":  fmt.Sprintf("%.2f%%", effectiveTPPct),
			"min_rr":    minRR,
		}).Info("⚖️ R:R enforced — TP widened to meet minimum ratio")
	}

	// Guard: verify on the exchange that no conflicting position exists.
	// Two cases to handle:
	//   1. Same-direction → reject to prevent pyramiding into a loss.
	//   2. Opposite-direction → take profit by closing the winning position
	//      and let post-close reanalysis decide the next entry. If the
	//      existing position is at a loss, block the new trade and let
	//      SL/TP manage the exit (no reversal into drawdown).
	newSide := strings.ToLower(decision.Action)
	if exchangePositions, exchErr := bot.gctClient.GetFuturesPositions(ctx, exchange); exchErr == nil {
		pairNormCheck := strings.ReplaceAll(strings.ReplaceAll(pair, "-", ""), "/", "")
		for _, ep := range exchangePositions {
			symNorm := strings.ToUpper(ep.Symbol)
			if !strings.HasPrefix(symNorm, strings.ToUpper(pairNormCheck)) {
				continue
			}
			epSide := strings.ToLower(ep.Side)
			sameDirection := (newSide == "sell" && (epSide == "short" || epSide == "sell")) ||
				(newSide == "buy" && (epSide == "long" || epSide == "buy"))

			if sameDirection {
				bot.log.WithFields(logrus.Fields{
					"existing_side": ep.Side,
					"existing_size": ep.Size,
					"new_signal":    newSide,
				}).Warn("🚫 Exchange already has same-direction position — skipping to prevent pyramiding")
				result.Success = false
				result.Error = fmt.Errorf("same-direction position already open on exchange")
				bot.resultChan <- result
				return
			}

			// Opposite-direction: decide based on PnL of the existing position.
			bot.log.WithFields(logrus.Fields{
				"existing_side":  ep.Side,
				"existing_size":  ep.Size,
				"existing_entry": ep.EntryPrice,
				"existing_pnl":   ep.UnrealizedPnL,
				"new_signal":     newSide,
			}).Info("🔄 Opposite-direction signal detected — evaluating PnL")

			if ep.UnrealizedPnL > 0 {
				bot.log.WithFields(logrus.Fields{
					"symbol": ep.Symbol,
					"side":   ep.Side,
					"pnl":    fmt.Sprintf("$%.4f", ep.UnrealizedPnL),
				}).Info("💰 Existing position in profit — closing to take profit on contrarian signal")

				closeTimeout := 10 * time.Second
				if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
					closeTimeout = snap.Duration("bot.exec.close_timeout")
				}
				closeCtx, closeCancel := context.WithTimeout(ctx, closeTimeout)
				closeErr := bot.gctClient.CloseFuturesPosition(closeCtx, exchange, ep.Symbol)
				closeCancel()
				if closeErr != nil {
					bot.log.WithError(closeErr).Error("❌ Failed to close opposite position — aborting new trade")
					result.Success = false
					result.Error = fmt.Errorf("failed to close opposite position: %w", closeErr)
					bot.resultChan <- result
					return
				}

				bot.log.WithField("pnl", ep.UnrealizedPnL).Info("✅ Opposite position closed — triggering post-close reanalysis")

				closedPos := &ai.ManagedPosition{
					ID:         fmt.Sprintf("CLOSED-OPPOSITE-%d", time.Now().Unix()),
					Symbol:     pair,
					Side:       ep.Side,
					EntryPrice: ep.EntryPrice,
					Size:       ep.Size,
					Leverage:   ep.Leverage,
					OpenedAt:   ep.UpdatedAt,
				}
				go bot.onPositionClosed(ctx, closedPos, "opposite_signal_take_profit", ep.UnrealizedPnL)

				result.Success = false
				result.Error = fmt.Errorf("closed opposite-direction position for profit-taking; reanalysis pending")
				bot.resultChan <- result
				return
			}

			// High-confidence override: a very strong opposite signal can bail out of a losing
			// position before SL is hit. Bailing at -0.5% beats waiting for SL at -2% when the
			// trend has clearly flipped. Below the threshold we keep the conservative behavior.
			// TC-4.6: bot.high_confidence_reversal_threshold (default 0.70)
			highConfidenceReversalThreshold := 0.70
			if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
				if v := snap.Float("bot.high_confidence_reversal_threshold"); v != 0 {
					highConfidenceReversalThreshold = v
				}
			}
			if decision.Confidence >= highConfidenceReversalThreshold {
				bot.log.WithFields(logrus.Fields{
					"symbol":     ep.Symbol,
					"side":       ep.Side,
					"pnl":        fmt.Sprintf("$%.4f", ep.UnrealizedPnL),
					"confidence": fmt.Sprintf("%.2f%%", decision.Confidence*100),
					"new_signal": newSide,
				}).Info("🎯 High-confidence opposite signal — closing losing position to allow reversal")

				closeTimeout2 := 10 * time.Second
				if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
					closeTimeout2 = snap.Duration("bot.exec.close_timeout")
				}
				closeCtx, closeCancel := context.WithTimeout(ctx, closeTimeout2)
				closeErr := bot.gctClient.CloseFuturesPosition(closeCtx, exchange, ep.Symbol)
				closeCancel()
				if closeErr != nil {
					bot.log.WithError(closeErr).Error("❌ Failed to close losing position for high-confidence reversal")
					result.Success = false
					result.Error = fmt.Errorf("failed to close losing position for reversal: %w", closeErr)
					bot.resultChan <- result
					return
				}

				bot.log.WithField("pnl", ep.UnrealizedPnL).Info("✅ Losing position closed — triggering post-close reanalysis")

				closedPos := &ai.ManagedPosition{
					ID:         fmt.Sprintf("CLOSED-LOSS-OVERRIDE-%d", time.Now().Unix()),
					Symbol:     pair,
					Side:       ep.Side,
					EntryPrice: ep.EntryPrice,
					Size:       ep.Size,
					Leverage:   ep.Leverage,
					OpenedAt:   ep.UpdatedAt,
				}
				go bot.onPositionClosed(ctx, closedPos, "high_confidence_reversal", ep.UnrealizedPnL)

				result.Success = false
				result.Error = fmt.Errorf("closed losing opposite-direction position for high-confidence reversal; reanalysis pending")
				bot.resultChan <- result
				return
			}

			// PnL <= 0 AND confidence below threshold → block. Don't reverse into a losing position
			// on a weak signal — let SL/TP manage the exit.
			bot.log.WithFields(logrus.Fields{
				"existing_side": ep.Side,
				"existing_pnl":  ep.UnrealizedPnL,
				"new_signal":    newSide,
				"confidence":    fmt.Sprintf("%.2f%%", decision.Confidence*100),
				"threshold":     fmt.Sprintf("%.0f%%", highConfidenceReversalThreshold*100),
			}).Warn("🚫 Opposite signal but existing position in loss and confidence below override threshold — letting SL/TP manage exit (no reversal)")
			result.Success = false
			result.Error = fmt.Errorf("opposite-direction position in loss; blocked to avoid reversal into drawdown")
			bot.resultChan <- result
			return
		}
	}

	// SL/TP were normalized (validateAndFixSLTP + minimum R:R) before the AI-02
	// pre-trade validation above, so the validated levels are the ones placed.

	bot.log.WithFields(logrus.Fields{
		"action":     decision.Action,
		"confidence": fmt.Sprintf("%.2f%%", decision.Confidence*100),
		"size":       tradeSize,
		"leverage":   leverage,
		"exchange":   exchange,
		"pair":       pair,
	}).Info("💰 Executing trade...")

	// Prepare order request
	// Convert action to lowercase for GCT compatibility
	side := strings.ToLower(decision.Action)

	// Smart Order Routing (DM-6.1) — GATED, default OFF.
	// When enabled and a router is wired, pick the best venue (fee+slippage
	// aware) and route there. Strict no-op when disabled/nil or on any error:
	// the configured `exchange` is kept and default behavior is unchanged.
	if bot.sorEnabled && bot.sorRouter != nil {
		if venue, verr := bot.sorRouter.BestVenue(ctx, pair, side, tradeSize); verr == nil && venue.Exchange != "" {
			bot.log.WithFields(logrus.Fields{
				"from_exchange":   exchange,
				"chosen_exchange": venue.Exchange,
				"raw_price":       venue.Price,
				"effective_price": venue.EffectivePrice,
				"reason":          venue.Reason,
			}).Info("🧭 SOR: routing order to best venue")
			exchange = venue.Exchange
		} else if verr != nil {
			bot.log.WithError(verr).Debug("SOR: best venue lookup failed; keeping configured exchange")
		}
	}

	// Resolve effective market-type and leverage via mode.Resolver.
	// AUTO / HYBRID (default, bot.auto_mode_intelligence==false): dec.MarketType==""
	//   → legacy leverage-driven routing preserved (byte-identical to pre-Wave-3).
	// AUTO with bot.auto_mode_intelligence==true: regime-driven mode + adaptive leverage.
	// MANUAL: dec.MarketType is set explicitly from bot.market_type config.
	//
	// Wave 3-A: supply confidence and ATR% (cheaply available here) to enable
	// the A1+A3 adaptive leverage path. Regime label is not available at this
	// call site without invasive plumbing — it is passed as empty string so that
	// when the flag is ON but regime is unknown the logic falls back to legacy.
	//
	// TODO(Wave 3-B): thread regimeAnalysis from runAnalysis into executeTrade
	// (or cache on bot struct) so the full regime → market-type mapping fires.
	modeCtx := mode.MarketContext{Confidence: decision.Confidence}
	if marketCtx != nil && marketCtx.CurrentPrice > 0 {
		if primary := marketCtx.GetPrimaryContext(); primary != nil && primary.Indicators != nil && primary.Indicators.ATR > 0 {
			modeCtx.ATRPct = primary.Indicators.ATR / marketCtx.CurrentPrice
		}
	}
	dec := bot.modeResolver.ResolveWithContext(ctx, pair, float64(leverage), modeCtx)

	// Fetch live price BEFORE submitting the order.
	// - Checks drift against analysis price (stale signal guard)
	// - Anchors SL/TP to actual market price, not the analysis snapshot
	// This must happen before SubmitOrder so we never place an order and then
	// discover the signal is stale — that would leave an untracked orphan position.
	fillPrice := decision.Entry
	maxDriftPct := 5.0
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("execution.stale_signal.max_drift_pct"); v > 0 {
			maxDriftPct = v
		}
	}
	// Fetch the price from the SAME market the order will fill on. For futures
	// trades the analysis price comes from the futures contract, so the drift
	// check must compare against the FUTURES ticker — not spot. GetTicker infers
	// asset type from the symbol format, so passing a spot-format pair
	// ("ICNT-USDT") fetches the spot price and produces a phantom drift (observed
	// 9-15% on ICNT, which has a distinct perp price and a gRPC "currency not
	// enabled" fallback). This kept every futures entry blocked as a "stale
	// signal" while the bot scanned and signalled correctly.
	priceSymbol := pair
	if dec.Leverage > 0 || dec.MarketType == gct.MarketTypeFutures {
		priceSymbol = toFuturesSymbol(pair)
	}
	if tickerResp, terr := bot.gctClient.GetTicker(ctx, exchange, priceSymbol); terr == nil && tickerResp.Last > 0 {
		priceDriftPct := math.Abs((tickerResp.Last - decision.Entry) / decision.Entry * 100)
		bot.log.WithFields(logrus.Fields{
			"analysis_price": decision.Entry,
			"live_price":     tickerResp.Last,
			"drift_pct":      fmt.Sprintf("%.4f%%", priceDriftPct),
		}).Debug("Pre-order price check")

		if priceDriftPct > maxDriftPct {
			bot.log.WithFields(logrus.Fields{
				"pair":           pair,
				"analysis_price": decision.Entry,
				"live_price":     tickerResp.Last,
				"drift_pct":      fmt.Sprintf("%.2f%%", priceDriftPct),
				"max_drift_pct":  maxDriftPct,
			}).Warn("🚫 Trade aborted before submission: price drifted too far from analysis — stale signal")
			bot.recordStaleSignal(pair)
			result.Success = false
			result.Error = fmt.Errorf("stale signal: price drifted %.2f%% (max %.0f%%)", priceDriftPct, maxDriftPct)
			bot.resultChan <- result
			return
		}
		bot.resetStaleSignalCount(pair)
		fillPrice = tickerResp.Last
	}

	// Notional safety factor from dynamic config (risk.max_notional_factor):
	// rejects an order whose single-contract notional exceeds this multiple of the
	// USD target (guards against a defaulted contract multiplier). 0 → gct default.
	maxNotionalFactor := 0.0
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("risk.max_notional_factor"); v > 0 {
			maxNotionalFactor = v
		}
	}

	req := &gct.SubmitOrderRequest{
		Exchange:          exchange,
		Pair:              pair,
		Side:              side,
		Type:              "market", // Always use market orders for now (can be made dynamic later)
		Amount:            tradeSize,
		Leverage:          dec.Leverage,
		MarketType:        dec.MarketType,
		ClientID:          deterministicOrderID(bot.effectiveUserID(), pair, side, tradeSize),
		MaxNotionalFactor: maxNotionalFactor,
	}

	// Execute order via GCT with dynamic timeout
	execTimeout := 10 * time.Second // Default timeout
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		execTimeout = snap.Duration("bot.exec.order_timeout")
	}
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	// Open-vs-adopt race guard: stamp the pair BEFORE the order hits the exchange.
	// The exchange shows the resulting position before the code below calls
	// AddPosition, so a concurrent syncOpenPositionsFromExchange would otherwise
	// adopt this very position as an orphan and duplicate it. The stamp tells the
	// adoption path to leave this pair alone for adoptOpenGuard.
	bot.markPairOpening(pair)

	order, err := bot.gctClient.SubmitOrder(execCtx, req)
	if err != nil {
		// One contract of this pair costs more than the guard allows for the
		// resolved notional: the pair cannot be traded at this account size.
		// Rotate away from it for a while instead of retrying every cycle.
		var minErr *gct.ErrMinContractTooLarge
		if errors.As(err, &minErr) {
			if isManualEntry(decision) {
				// The operator chose the pair: report the minimum instead of
				// blacklisting it (the handler answers 422 with min_size_usd).
				bot.log.WithFields(logrus.Fields{
					"pair":             pair,
					"min_contract_usd": fmt.Sprintf("%.2f", minErr.MinContractNotional),
					"min_size_usd":     fmt.Sprintf("%.2f", minErr.MinSizeUSD()),
				}).Warn("🧑‍✈️ Manual entry refused — minimum contract exceeds the requested size")
			} else {
				bot.handleMinContractTooLarge(pair, minErr)
			}
			result.Success = false
			result.Error = err
			bot.resultChan <- result
			return
		}
		bot.log.WithError(err).Error("❌ Failed to execute trade")
		// Detect deterministic exchange refusals (margin/position mode mismatch)
		// and blacklist the pair so the bot stops retrying every cycle and
		// rotates to the next-best opportunity from the master agent.
		errStr := err.Error()
		if strings.Contains(errStr, "MARGIN_MODE_MISMATCH") ||
			strings.Contains(errStr, "330005") ||
			strings.Contains(errStr, "position mode") {
			bot.blacklistPair(pair, "exchange refused: margin/position mode mismatch")
		}
		result.Success = false
		result.Error = err
		bot.resultChan <- result
		bot.incrementErrorCount()
		return
	}

	bot.log.WithFields(logrus.Fields{
		"order_id": order.ID,
		"status":   order.Status,
	}).Info("✅ Trade executed successfully")

	// Recompute SL/TP from actual fill price. Keep AI values when they already
	// have at least half the configured buffer; otherwise replace with
	// percentage-based levels anchored to the real fill to prevent instant SL hits.
	// effectiveSLPct/effectiveTPPct come from the matched tier (or flat defaults).
	isLongSide := strings.EqualFold(side, "buy") || strings.EqualFold(side, "long")
	minSLBuffer := (effectiveSLPct / 100) / 2
	// Cap SL at 1.5× the configured % to prevent stale-analysis-price SL drift.
	// When analysis price diverges from fill price (e.g. after a spike), the
	// AI's suggested SL can be 3-5× wider than intended — this clamps it back.
	maxSLBuffer := (effectiveSLPct / 100) * 1.5
	stopLoss := decision.StopLoss
	takeProfit := decision.TakeProfit

	// In trend_follow mode the setup's TP can be clamped to a nearby
	// resistance/support (the pullback entry sits just under the recent high),
	// leaving the TP a hair from entry — the position then closes in seconds at a
	// fee-eating "take_profit" (~$0), which is the death-by-a-thousand-cuts the
	// operator observed. Enforce a minimum TP distance (the R:R-widened
	// effectiveTPPct) for trend_follow so every entry has a real target. Legacy
	// (mean-reversion) keeps targeting resistance/support unchanged.
	trendFollowMode := marketCtx != nil && marketCtx.DirectionalMode == "trend_follow"

	if isLongSide {
		slDist := (fillPrice - stopLoss) / fillPrice
		if fillPrice > 0 && (stopLoss <= 0 || slDist < minSLBuffer || slDist > maxSLBuffer) {
			newSL := fillPrice * (1 - effectiveSLPct/100)
			bot.log.WithFields(logrus.Fields{
				"old_sl": stopLoss, "new_sl": newSL, "fill": fillPrice, "sl_dist_pct": fmt.Sprintf("%.2f%%", slDist*100),
			}).Warn("⚠️ SL out of range — recomputed from actual entry")
			stopLoss = newSL
		}
		minTP := fillPrice * (1 + effectiveTPPct/100)
		if takeProfit <= 0 || takeProfit <= fillPrice || (trendFollowMode && takeProfit < minTP) {
			if trendFollowMode && takeProfit > fillPrice && takeProfit < minTP {
				bot.log.WithFields(logrus.Fields{
					"old_tp": takeProfit, "new_tp": minTP, "fill": fillPrice,
				}).Warn("⚠️ TP too tight for trend-follow — widened to R:R minimum")
			}
			takeProfit = minTP
		}
	} else {
		slDist := (stopLoss - fillPrice) / fillPrice
		if fillPrice > 0 && (stopLoss <= 0 || slDist < minSLBuffer || slDist > maxSLBuffer) {
			newSL := fillPrice * (1 + effectiveSLPct/100)
			bot.log.WithFields(logrus.Fields{
				"old_sl": stopLoss, "new_sl": newSL, "fill": fillPrice, "sl_dist_pct": fmt.Sprintf("%.2f%%", slDist*100),
			}).Warn("⚠️ SL out of range — recomputed from actual entry")
			stopLoss = newSL
		}
		minTP := fillPrice * (1 - effectiveTPPct/100)
		if takeProfit <= 0 || takeProfit >= fillPrice || (trendFollowMode && takeProfit > minTP) {
			if trendFollowMode && takeProfit < fillPrice && takeProfit > minTP {
				bot.log.WithFields(logrus.Fields{
					"old_tp": takeProfit, "new_tp": minTP, "fill": fillPrice,
				}).Warn("⚠️ TP too tight for trend-follow — widened to R:R minimum")
			}
			takeProfit = minTP
		}
	}

	// HARD stop-loss floor on the FINAL stop, anchored to the actual fill.
	// The earlier effectiveSLPct floor is not enough: the post-fill recompute
	// keeps any decision stop landing inside [effectiveSLPct/2, effectiveSLPct*1.5],
	// so a 0.5-0.7% stop survives even when the floor is 1.2% (minSLBuffer is half
	// the floor). This clamp is the last word before the stop is placed on the
	// exchange — it guarantees |stop - fill|/fill >= slFloorPct. Configurable via
	// scoring.sl_floor_pct (default 0.8%, 0 disables).
	if fillPrice > 0 && slFloorPct > 0 {
		minDist := fillPrice * (slFloorPct / 100)
		var floorSL float64
		if isLongSide {
			floorSL = fillPrice - minDist
			if stopLoss > floorSL {
				stopLoss = floorSL
			}
		} else {
			floorSL = fillPrice + minDist
			if stopLoss < floorSL {
				stopLoss = floorSL
			}
		}
		if stopLoss == floorSL {
			bot.log.WithFields(logrus.Fields{
				"pair":     pair,
				"fill":     fillPrice,
				"final_sl": stopLoss,
				"sl_floor": fmt.Sprintf("%.2f%%", slFloorPct),
			}).Info("🛡️ Hard SL floor clamped final stop to minimum distance from fill")
		}
	}

	// Register position with actual fill price and validated SL/TP
	position := &ai.ManagedPosition{
		ID:           order.ID,
		Symbol:       pair,
		Side:         side,
		EntryPrice:   fillPrice,
		Size:         tradeSize,
		OriginalSize: tradeSize,
		// tradeSize is the USD notional target; the futures order layer converts it
		// to the correct contract count. Store it so realized-PnL uses USD notional.
		NotionalUSD:    tradeSize,
		Leverage:       leverage,
		StopLoss:       stopLoss,
		TakeProfit:     takeProfit,
		OpenedAt:       time.Now(),
		Confidence:     decision.Confidence,
		DecisionReason: decision.Reason,
		// P(TP_first) from the ML predictor, captured during Kelly sizing. Carried
		// through to the feedback labeler at close so the ML's vote is credited
		// (otherwise MLProb=0 makes the calibrator treat the ML as always abstaining).
		MLProb: bot.consumeLastMLProb(),
		// Entry signal attribution for the post-trade per-source calibration
		// (Active Profit Engine F0). EntryRegime unblocks per-regime weights;
		// the technical/llm votes break their collinearity in signal_calibration.
		EntryRegime:   bot.currentRegimeLabel(),
		TechnicalVote: decision.TechnicalAction,
		LLMVote:       decision.LLMAction,
		TechnicalConf: decision.TechnicalConfidence,
		LLMConf:       decision.LLMConfidence,
	}
	partialTPLevels := bot.config.PartialTP.Levels
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		var dynLevels []PartialTPLevel
		if err := snap.JSON("partial_tp.levels", &dynLevels); err == nil && len(dynLevels) > 0 {
			partialTPLevels = dynLevels
		}
	}
	if bot.config.PartialTP.Enabled && len(partialTPLevels) > 0 {
		position.PartialTPTriggered = make([]bool, len(partialTPLevels))
	}

	// Single-position-per-(symbol,side) invariant. A futures account nets to ONE
	// physical position per symbol+side, so the bot must track exactly one managed
	// position for it. A prior sync may have already adopted this orphan (adopt-then-
	// open), or a duplicate may linger from before; drop any such managed position
	// now — this fresh open carries the authoritative SL/TP/notional/entry signal.
	// Without this, two managed positions map to one physical one, producing
	// duplicate exchange stops and double-counted closes in trade_history.
	bot.dropDuplicateManagedPositions(position.Symbol, position.Side, position.ID)

	// Claim cross-instance ownership: the opener creates the authoritative physical
	// position, so force-claim the slot. Other orchestrator instances will then
	// refuse to adopt this position as a duplicate.
	claimPositionOwnership(position.Symbol, position.Side, position.ID, true)

	bot.positionManager.AddPosition(position)

	// Place a real stop-loss order on the exchange immediately. This is the
	// primary protection layer — works even if the bot crashes or the software
	// monitoring loop loses track of this position.
	if !bot.config.DryRun {
		bot.placeExchangeStopLoss(ctx, position)
	}

	// Always attempt to set SL/TP on the exchange when the executor is available,
	// regardless of AutonomousMode — a stop loss that only lives in memory is no
	// protection if the bot crashes or fails to close the position via software.
	if bot.config.AutoSetSLTP && bot.sltpExecutor != nil {
		bot.mu.RLock()
		hasMultiLevelTP := len(bot.config.TPLevels) > 0
		bot.mu.RUnlock()

		if hasMultiLevelTP {
			if err := bot.setMultiLevelTPSL(ctx, position, decision); err != nil {
				bot.log.WithError(err).WithField("position_id", position.ID).Warn("Failed to set multi-level TP/SL")
			}
		} else {
			if err := bot.setAutonomousSLTP(ctx, position, decision); err != nil {
				bot.log.WithError(err).WithField("position_id", position.ID).Warn("Failed to set autonomous SL/TP")
			}
		}
	}

	// Trailing stop is the primary profit-locker — enable it for EVERY position
	// when trailing.enabled, NOT only in AutonomousMode and NOT only when an
	// sltpExecutor is wired. Gating it behind AutonomousMode (false in production)
	// meant bot-opened positions never trailed: INJ rode +16% with its SL parked
	// at breakeven and gave it all back on the pullback (2026-06-01). The trailing
	// config itself decides whether it runs; updateTrailingStop ratchets the
	// exchange stop order directly so it works even without the executor.
	if err := bot.enableTrailingStop(ctx, position); err != nil {
		bot.log.WithError(err).WithField("position_id", position.ID).Warn("Failed to enable trailing stop")
	}

	// Send AI-enhanced trade notification
	bot.sendTradeNotificationWithAI(ctx, decision, marketCtx, order.ID)

	result.Success = true
	result.OrderID = order.ID
	bot.resultChan <- result

	bot.mu.Lock()
	bot.executionCount++
	bot.dailyTrades++
	bot.mu.Unlock()
}

// validateAndFixSLTP ensures stop-loss and take-profit are on the correct side
// of entry relative to the final decision action. SL/TP can end up inverted
// when the action is flipped after the initial computation (e.g. LLM consensus
// overriding a technical SELL into a BUY) — left uncorrected, the SL triggers
// instantly on the next tick. When an inverted value is detected, recompute
// from config defaults using the final action as the source of truth.
// normalizeDecisionLevels brings the decision's SL/TP to the levels the order
// will carry: validateAndFixSLTP repairs inverted levels and applies the SL
// floor, then the TP is widened so reward/risk is at least
// bot.risk_reward.min_ratio (default 2.0) — the same policy executeTrade applies
// to the percentage targets ("widen TP, never tighten SL"). Running this before
// the AI-02 validation means the validator judges real levels instead of the
// raw technical TP clamped to the nearest support/resistance.
func (bot *AITradingBot) normalizeDecisionLevels(ctx context.Context, decision *ai.Decision, marketCtx *ai.MarketContext) {
	if decision == nil {
		return
	}
	bot.validateAndFixSLTP(decision, marketCtx)

	entry := decision.Entry
	if entry <= 0 || decision.StopLoss <= 0 {
		return
	}
	minRR := 2.0
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.risk_reward.min_ratio"); v > 0 {
			minRR = v
		}
	}
	var risk, reward, minTP float64
	switch strings.ToUpper(decision.Action) {
	case "BUY":
		risk = entry - decision.StopLoss
		reward = decision.TakeProfit - entry
		minTP = entry + minRR*risk
	case "SELL":
		risk = decision.StopLoss - entry
		reward = entry - decision.TakeProfit
		minTP = entry - minRR*risk
	default:
		return
	}
	if risk <= 0 || reward >= minRR*risk {
		return
	}
	bot.log.WithFields(logrus.Fields{
		"action": decision.Action,
		"entry":  entry,
		"sl":     decision.StopLoss,
		"old_tp": decision.TakeProfit,
		"new_tp": minTP,
		"min_rr": minRR,
		"rr_was": fmt.Sprintf("%.2f", reward/risk),
	}).Info("⚖️ R:R enforced on decision — TP widened to meet minimum ratio")
	decision.TakeProfit = minTP
}

func (bot *AITradingBot) validateAndFixSLTP(decision *ai.Decision, marketCtx *ai.MarketContext) {
	if decision == nil {
		return
	}

	// Anchor on a known-good reference price: prefer decision.Entry, fall back
	// to current market price. Without a reference we cannot validate.
	entry := decision.Entry
	if entry <= 0 && marketCtx != nil {
		entry = marketCtx.CurrentPrice
		decision.Entry = entry
	}
	if entry <= 0 {
		return
	}

	action := strings.ToUpper(decision.Action)
	if action != "BUY" && action != "SELL" {
		return
	}

	slPct := bot.config.DefaultStopLossPct
	tpPct := bot.config.DefaultTakeProfitPct
	if slPct <= 0 {
		slPct = 3.0
	}
	if tpPct <= 0 {
		tpPct = 6.0
	}

	slInverted := false
	tpInverted := false

	switch action {
	case "BUY":
		// LONG: SL must be below entry, TP must be above entry.
		if decision.StopLoss > 0 && decision.StopLoss >= entry {
			slInverted = true
		}
		if decision.TakeProfit > 0 && decision.TakeProfit <= entry {
			tpInverted = true
		}
		if slInverted || decision.StopLoss <= 0 {
			decision.StopLoss = entry * (1 - slPct/100)
		}
		if tpInverted || decision.TakeProfit <= 0 {
			decision.TakeProfit = entry * (1 + tpPct/100)
		}
	case "SELL":
		// SHORT: SL must be above entry, TP must be below entry.
		if decision.StopLoss > 0 && decision.StopLoss <= entry {
			slInverted = true
		}
		if decision.TakeProfit > 0 && decision.TakeProfit >= entry {
			tpInverted = true
		}
		if slInverted || decision.StopLoss <= 0 {
			decision.StopLoss = entry * (1 + slPct/100)
		}
		if tpInverted || decision.TakeProfit <= 0 {
			decision.TakeProfit = entry * (1 - tpPct/100)
		}
	}

	if slInverted || tpInverted {
		bot.log.WithFields(logrus.Fields{
			"action":          action,
			"entry":           entry,
			"sl_inverted":     slInverted,
			"tp_inverted":     tpInverted,
			"new_stop_loss":   decision.StopLoss,
			"new_take_profit": decision.TakeProfit,
			"sl_pct":          slPct,
			"tp_pct":          tpPct,
		}).Warn("⚠️ SL/TP on wrong side of entry — recomputed from defaults to match final action")
	}

	// Apply SL floor: ensures the stop is at least slFloorPct% away from entry.
	// Prevents ATR-computed stops too tight to survive normal price noise.
	// Configurable via scoring.sl_floor_pct (default 0.8%).
	slFloorPct := 0.8
	if bot.configStore != nil {
		if v, ok := bot.configStore.Snapshot().ToScopedSnapshot().FloatOk("scoring.sl_floor_pct"); ok && v > 0 {
			slFloorPct = v
		}
	}
	switch action {
	case "BUY":
		floor := entry * (1 - slFloorPct/100)
		if decision.StopLoss > floor {
			bot.log.WithFields(logrus.Fields{
				"original_sl":  fmt.Sprintf("%.4f", decision.StopLoss),
				"floor_sl":     fmt.Sprintf("%.4f", floor),
				"sl_floor_pct": slFloorPct,
			}).Info("🛡️ SL floor applied — widening stop to minimum distance")
			decision.StopLoss = floor
		}
	case "SELL":
		floor := entry * (1 + slFloorPct/100)
		if decision.StopLoss < floor {
			bot.log.WithFields(logrus.Fields{
				"original_sl":  fmt.Sprintf("%.4f", decision.StopLoss),
				"floor_sl":     fmt.Sprintf("%.4f", floor),
				"sl_floor_pct": slFloorPct,
			}).Info("🛡️ SL floor applied — widening stop to minimum distance")
			decision.StopLoss = floor
		}
	}
}

// selectSizingTier picks the SizingTier whose [MinConfidence, MaxConfidence] range
// contains `confidence`. Returns nil when dynamic sizing is disabled, the ladder
// is empty, or no tier matches (caller falls back to flat config defaults).
//
// Tiers may be authored in any order; matching is by range membership so gaps
// just produce a fallback rather than a silent mis-pick. Inputs are not mutated.
func (bot *AITradingBot) selectSizingTier(confidence float64) *SizingTier {
	if !bot.config.DynamicSizing.Enabled || len(bot.config.DynamicSizing.Tiers) == 0 {
		return nil
	}
	for i := range bot.config.DynamicSizing.Tiers {
		t := &bot.config.DynamicSizing.Tiers[i]
		if confidence >= t.MinConfidence && confidence <= t.MaxConfidence {
			return t
		}
	}
	return nil
}

// checkPartialTakeProfits fires any configured scale-out level that has been
// reached since the last tick. pnlPct is the raw price-move percentage from
// entry (already side-corrected, positive when in profit). Each level fires at
// most once. When a level closes part of the position, pos.Size is reduced
// in-place so the next SL/TP check still uses a valid remaining size.
//
// Behavior on failure: if the reduce-only order fails we leave the level
// flagged as triggered to avoid retry storms. The remaining position is still
// protected by the exchange stop order and the final TP, so partial-close
// failures degrade gracefully rather than locking the position.
func (bot *AITradingBot) checkPartialTakeProfits(ctx context.Context, pos *ai.ManagedPosition, currentPrice, pnlPct float64) {
	if !bot.config.PartialTP.Enabled || len(bot.config.PartialTP.Levels) == 0 {
		return
	}

	// Load partial TP levels from dynamic config; fall back to YAML struct values.
	levels := bot.config.PartialTP.Levels
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		var dynLevels []PartialTPLevel
		if err := snap.JSON("partial_tp.levels", &dynLevels); err == nil && len(dynLevels) > 0 {
			levels = dynLevels
		}
	}

	if pos.PartialTPTriggered == nil {
		pos.PartialTPTriggered = make([]bool, len(levels))
	}
	if len(pos.PartialTPTriggered) < len(levels) {
		grown := make([]bool, len(levels))
		copy(grown, pos.PartialTPTriggered)
		pos.PartialTPTriggered = grown
	}
	if pnlPct <= 0 {
		return // not profitable yet
	}

	for i, level := range levels {
		if pos.PartialTPTriggered[i] {
			continue
		}
		if level.TPPct <= 0 || level.ClosePct <= 0 {
			pos.PartialTPTriggered[i] = true
			continue
		}
		if pnlPct < level.TPPct {
			continue
		}
		// Mark before sending the order so a same-tick re-entry can't double-fire.
		pos.PartialTPTriggered[i] = true

		closeFraction := level.ClosePct / 100.0
		if closeFraction > 1.0 {
			closeFraction = 1.0
		}
		closeAmount := pos.Size * closeFraction
		if closeAmount <= 0 {
			continue
		}

		bot.log.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"level":         i + 1,
			"tp_pct":        level.TPPct,
			"close_pct":     level.ClosePct,
			"close_amount":  closeAmount,
			"remaining":     pos.Size - closeAmount,
			"current_price": currentPrice,
			"pnl_pct":       fmt.Sprintf("%.2f%%", pnlPct),
		}).Info("🪜 Partial TP triggered")

		if bot.config.DryRun {
			bot.log.Info("🔵 DRY-RUN: would close partial size")
		} else if err := bot.executePartialClose(ctx, pos, closeAmount); err != nil {
			bot.log.WithError(err).Warn("⚠️ Partial close failed — exchange stop and final TP remain in force")
			continue
		}

		pos.Size -= closeAmount
		if pos.Size < 0 {
			pos.Size = 0
		}

		if level.MoveSLToBreakeven && !pos.BreakevenApplied {
			pos.Lock()
			pos.StopLoss = pos.EntryPrice
			pos.BreakevenApplied = true
			pos.Unlock()
			bot.log.WithFields(logrus.Fields{
				"position_id": pos.ID,
				"new_sl":      pos.StopLoss,
			}).Info("🛡️ Stop loss moved to breakeven after partial TP")
			// Reissue the exchange stop order at the new price. Best-effort:
			// failures here just leave software monitoring as the SL guard.
			// BOT-09: sequential reissue (cancel then place), not two
			// unordered goroutines.
			go bot.reissueExchangeStopLoss(context.Background(), pos)
		}
	}
}

// positionIndicatorSnapshot holds the indicator values needed to evaluate
// condition-based exits, plus the previous-cycle values for delta detection.
type positionIndicatorSnapshot struct {
	CurrentPrice float64
	RSI          float64
	PrevRSI      float64
	MACD         float64
	MACDSignal   float64
	PrevMACD     float64
	Volume       float64
	AvgVolume    float64
	ATR          float64

	// Trend-strength / momentum-acceleration fields (Active Profit Engine F0).
	// Populated in cacheIndicatorsForExit from the primary context. Used by the
	// continuation/reversal scoring and the exit-decision fusion. Zero means the
	// factor is absent (e.g. not enough candles for ADX) and downstream treats it
	// as neutral, never as a negative signal.
	Histogram      float64 // MACD histogram (MACD - signal)
	HistogramDelta float64 // Histogram - prev.Histogram (momentum acceleration)
	ADX            float64 // ADX trend strength (0 if insufficient data)
	DIPlus         float64 // +DI directional indicator
	DIMinus        float64 // -DI directional indicator

	UpdatedAt time.Time

	// Wick pattern fields — derived from the last 5 closed candles.
	// Used by detectWickReversal (Layer 1) and enriched LLM prompt (Layer 2).
	LastLowerWicks []float64 // lower wick per candle, oldest→newest
	LastUpperWicks []float64 // upper wick per candle
	LastBodies     []float64 // |close-open| per candle
	LastVolumes    []float64 // volume per candle
	WickPattern    string    // "buyer_absorption" | "seller_absorption" | "doji_cluster" | "none"
}

// cacheIndicatorsForExit stores the latest indicators from the analysis cycle
// so checkPosition can evaluate exit conditions without re-fetching candles.
// Keeps the previous RSI/MACD so the condition checker can detect direction
// reversals (RSI overbought reverting, MACD bearish cross).
// Also computes wick geometry for the last 5 candles (Layer 1 + LLM context).
func (bot *AITradingBot) cacheIndicatorsForExit(symbol string, marketCtx *ai.MarketContext) {
	if marketCtx == nil || symbol == "" {
		return
	}
	primary := marketCtx.GetPrimaryContext()
	if primary == nil || primary.Indicators == nil {
		return
	}
	ind := primary.Indicators

	avgVol := ind.Volume
	if len(primary.Candles) >= 20 {
		var sum float64
		recent := primary.Candles[len(primary.Candles)-20:]
		for _, c := range recent {
			sum += c.Volume
		}
		avgVol = sum / float64(len(recent))
	}

	var macd, signal, histogram float64
	if ind.MACD != nil {
		macd = ind.MACD.Value
		signal = ind.MACD.Signal
		histogram = ind.MACD.Histogram
	}

	// ADX / +DI / -DI from the primary context candles — trend strength used by
	// the continuation/reversal scoring (Active Profit Engine F0). Needs at least
	// 2*period+1 candles; if there aren't enough, leave ADX/DI at zero so the
	// scorer treats the trend-strength factor as absent (never negative).
	const exitADXPeriod = 14
	var adx, diPlus, diMinus float64
	if len(primary.Candles) >= exitADXPeriod*2+1 {
		highs := make([]float64, len(primary.Candles))
		lows := make([]float64, len(primary.Candles))
		closes := make([]float64, len(primary.Candles))
		for i, c := range primary.Candles {
			highs[i], lows[i], closes[i] = c.High, c.Low, c.Close
		}
		if a, dp, dm, errADX := regime.ComputeADX(highs, lows, closes, exitADXPeriod); errADX == nil {
			adx, diPlus, diMinus = a, dp, dm
		}
	}

	// Compute wick data from the last 5 closed candles.
	const wickWindow = 5
	var lowerWicks, upperWicks, bodies, volumes []float64
	if len(primary.Candles) >= wickWindow {
		recent := primary.Candles[len(primary.Candles)-wickWindow:]
		for _, c := range recent {
			body := math.Abs(c.Close - c.Open)
			upper := c.High - math.Max(c.Open, c.Close)
			lower := math.Min(c.Open, c.Close) - c.Low
			if upper < 0 {
				upper = 0
			}
			if lower < 0 {
				lower = 0
			}
			bodies = append(bodies, body)
			upperWicks = append(upperWicks, upper)
			lowerWicks = append(lowerWicks, lower)
			volumes = append(volumes, c.Volume)
		}
	}

	bot.indicatorCacheMu.Lock()
	defer bot.indicatorCacheMu.Unlock()
	if bot.indicatorCache == nil {
		bot.indicatorCache = make(map[string]*positionIndicatorSnapshot)
	}
	prev := bot.indicatorCache[symbol]
	snap := &positionIndicatorSnapshot{
		CurrentPrice:   marketCtx.CurrentPrice,
		RSI:            ind.RSI,
		MACD:           macd,
		MACDSignal:     signal,
		Volume:         ind.Volume,
		AvgVolume:      avgVol,
		ATR:            ind.ATR,
		Histogram:      histogram,
		ADX:            adx,
		DIPlus:         diPlus,
		DIMinus:        diMinus,
		UpdatedAt:      time.Now(),
		LastLowerWicks: lowerWicks,
		LastUpperWicks: upperWicks,
		LastBodies:     bodies,
		LastVolumes:    volumes,
		WickPattern:    classifyWickPattern(lowerWicks, upperWicks, bodies, marketCtx.CurrentPrice, bot.wickThreshold()),
	}
	if prev != nil {
		snap.PrevRSI = prev.RSI
		snap.PrevMACD = prev.MACD
		snap.HistogramDelta = snap.Histogram - prev.Histogram
	} else {
		snap.PrevRSI = ind.RSI
		snap.PrevMACD = macd
	}
	bot.indicatorCache[symbol] = snap
}

// classifyWickPattern examines the last N candles' wick geometry and returns
// a named pattern that signals an imminent reversal:
//   - "buyer_absorption": growing lower wicks on SHORT — buyers absorbing the drop
//   - "seller_absorption": growing upper wicks on LONG — sellers absorbing the rally
//   - "doji_cluster": consecutive tiny bodies — momentum exhaustion
//   - "none": no actionable pattern detected
//
// threshold is the minimum wick/body ratio required (auto-tuned by WickLearner;
// default 1.5). Pass 0 to use the default.
func classifyWickPattern(lower, upper, bodies []float64, price, threshold float64) string {
	n := len(lower)
	if n < 3 {
		return "none"
	}
	if threshold <= 0 {
		threshold = 1.5
	}
	// Minimum meaningful wick: 0.15% of current price (avoids noise on tiny ticks).
	minWick := price * 0.0015
	if price == 0 {
		minWick = 0.001
	}

	// Buyer absorption: last 2 lower wicks both grow AND last wick is significant.
	lowerGrowing := lower[n-1] > lower[n-2] && lower[n-2] > lower[n-3]
	lastLWRatio := 0.0
	if bodies[n-1] > 0.0001 {
		lastLWRatio = lower[n-1] / bodies[n-1]
	}
	if lowerGrowing && lower[n-1] > minWick && lastLWRatio >= threshold {
		return "buyer_absorption"
	}

	// Seller absorption: last 2 upper wicks both grow AND last wick is significant.
	upperGrowing := upper[n-1] > upper[n-2] && upper[n-2] > upper[n-3]
	lastUWRatio := 0.0
	if bodies[n-1] > 0.0001 {
		lastUWRatio = upper[n-1] / bodies[n-1]
	}
	if upperGrowing && upper[n-1] > minWick && lastUWRatio >= threshold {
		return "seller_absorption"
	}

	// Doji cluster: 2+ of last 3 candles have body < 10% of their total range.
	dojiCount := 0
	for i := n - 3; i < n; i++ {
		rng := upper[i] + bodies[i] + lower[i]
		if rng > 0.0001 && bodies[i]/rng < 0.10 {
			dojiCount++
		}
	}
	if dojiCount >= 2 {
		return "doji_cluster"
	}

	return "none"
}

// detectWickReversal is Layer 1 of the enhanced exit system. It runs
// deterministic wick pattern checks (sub-millisecond, no LLM) and returns
// (true, reason) when the pattern strongly indicates an imminent reversal
// against the open position's direction. Only fires when in profit.
func (bot *AITradingBot) detectWickReversal(pos *ai.ManagedPosition, gainPct float64) (bool, string) {
	if gainPct < 0.5 {
		return false, "" // only protect meaningful profits
	}

	bot.indicatorCacheMu.RLock()
	snap := bot.indicatorCache[pos.Symbol]
	bot.indicatorCacheMu.RUnlock()
	if snap == nil || len(snap.LastLowerWicks) < 3 {
		return false, ""
	}
	if time.Since(snap.UpdatedAt) > 2*bot.config.AnalysisInterval {
		return false, "" // stale data — don't act
	}

	side := strings.ToLower(pos.Side)
	isShort := side == "sell" || side == "short"
	isLong := side == "buy" || side == "long"
	n := len(snap.LastLowerWicks)

	switch snap.WickPattern {
	case "buyer_absorption":
		if isShort {
			lw := snap.LastLowerWicks
			return true, fmt.Sprintf(
				"buyer_absorption: lower wicks %.3f→%.3f→%.3f (buyers rejecting the drop)",
				lw[n-3], lw[n-2], lw[n-1],
			)
		}
	case "seller_absorption":
		if isLong {
			uw := snap.LastUpperWicks
			return true, fmt.Sprintf(
				"seller_absorption: upper wicks %.3f→%.3f→%.3f (sellers rejecting the rally)",
				uw[n-3], uw[n-2], uw[n-1],
			)
		}
	case "doji_cluster":
		// Doji cluster signals exhaustion for both directions, but only exit
		// when sufficiently in profit (>= 1.5%) to avoid false exits on noise.
		if gainPct >= 1.5 && (isShort || isLong) {
			return true, fmt.Sprintf("doji_cluster: momentum exhaustion after %.2f%% gain", gainPct)
		}
	}
	return false, ""
}

// evaluateConditionExit runs the human-like exit logic (RSI/MACD reversal,
// volume death, profit lock) using cached indicators. Returns (true, reason)
// when the position should be closed before SL/TP is hit.
//
// Profit lock thresholds are read from dynamic config at call time so they
// can be tuned without restarting the bot.
func (bot *AITradingBot) evaluateConditionExit(ctx context.Context, pos *ai.ManagedPosition) (bool, string) {
	if pos == nil || bot.positionManager == nil {
		return false, ""
	}
	bot.indicatorCacheMu.RLock()
	snap := bot.indicatorCache[pos.Symbol]
	bot.indicatorCacheMu.RUnlock()
	if snap == nil {
		return false, ""
	}
	// Skip if the cached data is older than 2 analysis cycles — stale signals
	// are worse than no signal.
	if time.Since(snap.UpdatedAt) > 2*bot.config.AnalysisInterval {
		return false, ""
	}

	// Read configurable profit-lock thresholds from dynamic config.
	profitLockMinPct := 0.8   // default: 0.8% (was hardcoded 1.5%)
	profitLockRSILong := 72.0 // default RSI for long profit-lock (was 75)
	profitLockRSIShort := 28.0
	if plSnap := dynamic.SnapshotFromContext(ctx); plSnap != nil {
		if v := plSnap.Float("exit.profit_lock_min_pct"); v > 0 {
			profitLockMinPct = v
		}
		if v := plSnap.Float("exit.profit_lock_rsi_long"); v > 0 {
			profitLockRSILong = v
		}
		if v := plSnap.Float("exit.profit_lock_rsi_short"); v > 0 {
			profitLockRSIShort = v
		}
	}

	market := &ai.MarketSnapshot{
		CurrentPrice:       snap.CurrentPrice,
		RSI:                snap.RSI,
		PrevRSI:            snap.PrevRSI,
		MACD:               snap.MACD,
		MACDSignal:         snap.MACDSignal,
		PrevMACD:           snap.PrevMACD,
		Volume:             snap.Volume,
		AvgVolume:          snap.AvgVolume,
		ATR:                snap.ATR,
		EntryPrice:         pos.EntryPrice,
		Side:               strings.ToUpper(pos.Side),
		ProfitLockMinPct:   profitLockMinPct / 100.0, // convert % to ratio
		ProfitLockRSILong:  profitLockRSILong,
		ProfitLockRSIShort: profitLockRSIShort,
	}
	analysis := bot.positionManager.EvaluateExitConditions(market)
	if analysis == nil || !analysis.ShouldExit {
		return false, ""
	}
	return true, analysis.PrimaryReason
}

// aiExitAction describes the LLM-recommended action on an open position.
type aiExitAction struct {
	Action     string // "hold" | "tighten_stop" | "close_partial" | "close_all"
	Confidence float64
	Reasoning  string
}

// consultAIForExit asks the LLM whether to exit/tighten/hold the position.
// Rate-limited to AICheckInterval (default 5 min). Returns (nil, false) when
// the call should be skipped (interval not elapsed, no analyzer, no profit,
// position too young) or when the LLM fails. On success returns the parsed
// action and updates pos.LastAICheck.
//
// Action semantics:
//   - "close_all":     close immediately
//   - "close_partial": close 50% of remaining size
//   - "tighten_stop":  move SL halfway between current SL and current price
//   - "hold":          do nothing
//
// Confidence guard: actions other than "hold" require confidence >= 0.65 so
// low-conviction LLM calls don't surprise-close a position.
func (bot *AITradingBot) consultAIForExit(ctx context.Context, pos *ai.ManagedPosition, currentPrice, gainPct float64) (*aiExitAction, bool) {
	if bot.llmAnalyzer == nil || pos == nil {
		return nil, false
	}
	// Only consult when meaningfully in profit — micro-gains should be managed by
	// breakeven/trailing, not LLM tighten-stop calls.
	// llm.exit_advisor.min_gain_pct (default 0.5) — hot-reloadable via dynamic config
	minGainPct := 0.5
	// llm.exit_advisor.min_age (default 60s) — hot-reloadable via dynamic config
	minAge := time.Minute
	// llm.exit_advisor.interval (default 5m) — hot-reloadable via dynamic config
	exitInterval := 5 * time.Minute
	if exitSnap := dynamic.SnapshotFromContext(ctx); exitSnap != nil {
		if v := exitSnap.Float("llm.exit_advisor.min_gain_pct"); v > 0 {
			minGainPct = v
		}
		if v := exitSnap.Duration("llm.exit_advisor.min_age"); v > 0 {
			minAge = v
		}
		if v := exitSnap.Duration("llm.exit_advisor.interval"); v > 0 {
			exitInterval = v
		}
	}
	if gainPct < minGainPct {
		return nil, false
	}
	// Avoid LLM calls in the first moments of a trade — too noisy.
	if time.Since(pos.OpenedAt) < minAge {
		return nil, false
	}
	// Rate-limit between LLM calls per position.
	if !pos.LastAICheck.IsZero() && time.Since(pos.LastAICheck) < exitInterval {
		return nil, false
	}

	// Pull latest indicators from the cache for richer LLM context.
	bot.indicatorCacheMu.RLock()
	snap := bot.indicatorCache[pos.Symbol]
	bot.indicatorCacheMu.RUnlock()
	var rsi, macd, signal float64
	var wickPattern string
	var lowerWicks, upperWicks, bodies, volumes []float64
	if snap != nil {
		rsi = snap.RSI
		macd = snap.MACD
		signal = snap.MACDSignal
		wickPattern = snap.WickPattern
		lowerWicks = snap.LastLowerWicks
		upperWicks = snap.LastUpperWicks
		bodies = snap.LastBodies
		volumes = snap.LastVolumes
	}

	side := strings.ToLower(pos.Side)
	mfe := pos.HighestPrice
	if side == "sell" || side == "short" {
		mfe = pos.LowestPrice
	}

	input := &llm.ExitDecisionInput{
		Pair:              pos.Symbol,
		Direction:         side,
		EntryPrice:        pos.EntryPrice,
		CurrentPrice:      currentPrice,
		GainPct:           gainPct,
		TakeProfit:        pos.TakeProfit,
		StopLoss:          pos.StopLoss,
		MaxFavorablePrice: mfe,
		AgeSeconds:        int64(time.Since(pos.OpenedAt).Seconds()),
		RSI:               rsi,
		MACD:              macd,
		MACDSignal:        signal,
		WickPattern:       wickPattern,
		LowerWicks:        lowerWicks,
		UpperWicks:        upperWicks,
		Bodies:            bodies,
		Volumes:           volumes,
	}

	// llm.exit_advisor.timeout (default 30s) — hot-reloadable via dynamic config
	exitTimeout := 30 * time.Second
	// llm.exit_advisor.confidence_floor (default 0.65) — hot-reloadable via dynamic config
	confidenceFloor := 0.65
	if exitSnap2 := dynamic.SnapshotFromContext(ctx); exitSnap2 != nil {
		if v := exitSnap2.Duration("llm.exit_advisor.timeout"); v > 0 {
			exitTimeout = v
		}
		if v := exitSnap2.Float("llm.exit_advisor.confidence_floor"); v > 0 {
			confidenceFloor = v
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, exitTimeout)
	defer cancel()

	out, err := bot.llmAnalyzer.AnalyzeExitDecision(callCtx, input)
	pos.LastAICheck = time.Now()
	if err != nil || out == nil {
		bot.log.WithError(err).WithField("symbol", pos.Symbol).Debug("AI exit consult failed — holding")
		return nil, false
	}

	bot.log.WithFields(logrus.Fields{
		"symbol":     pos.Symbol,
		"action":     out.Action,
		"confidence": fmt.Sprintf("%.2f", out.Confidence),
		"gain_pct":   fmt.Sprintf("%.2f%%", gainPct),
		"reasoning":  out.Reasoning,
	}).Info("🤖 AI exit consultation")

	action := strings.ToLower(out.Action)
	// Conviction floor for any destructive action.
	if action != "hold" && out.Confidence < confidenceFloor {
		return &aiExitAction{Action: "hold", Confidence: out.Confidence, Reasoning: out.Reasoning}, true
	}

	switch action {
	case "close_all":
		return &aiExitAction{Action: "close_all", Confidence: out.Confidence, Reasoning: out.Reasoning}, true
	case "close_partial":
		return &aiExitAction{Action: "close_partial", Confidence: out.Confidence, Reasoning: out.Reasoning}, true
	case "tighten_stop":
		return &aiExitAction{Action: "tighten_stop", Confidence: out.Confidence, Reasoning: out.Reasoning}, true
	default:
		return &aiExitAction{Action: "hold", Confidence: out.Confidence, Reasoning: out.Reasoning}, true
	}
}

// tightenStopLoss moves the position's stop-loss toward currentPrice by the configured
// midpoint fraction. Never widens the stop (long: only moves up; short: only moves down).
// Called when the LLM says TIGHTEN_STOP.
func (bot *AITradingBot) tightenStopLoss(pos *ai.ManagedPosition, currentPrice float64) {
	if pos == nil {
		return
	}
	// llm.exit_advisor.tighten_midpoint (default 0.5) — fraction toward currentPrice; hot-reloadable
	tightenMidpoint := 0.5
	if tightenSnap := dynamic.SnapshotFromContext(context.Background()); tightenSnap != nil {
		if v := tightenSnap.Float("llm.exit_advisor.tighten_midpoint"); v > 0 {
			tightenMidpoint = v
		}
	}
	// BOT-08: serialize this read-compute-write against monitoringLoop/
	// portfolioExitLoop mutating the same *ManagedPosition's StopLoss.
	pos.Lock()
	side := strings.ToUpper(pos.Side)
	isLong := side == "BUY" || side == "LONG"
	old := pos.StopLoss
	newSL := pos.StopLoss + (currentPrice-pos.StopLoss)*tightenMidpoint
	if isLong {
		if newSL > pos.StopLoss {
			pos.StopLoss = newSL
		}
	} else {
		if newSL < pos.StopLoss {
			pos.StopLoss = newSL
		}
	}
	changed := pos.StopLoss != old
	pos.Unlock()
	if changed {
		bot.log.WithFields(logrus.Fields{
			"symbol":  pos.Symbol,
			"old_sl":  fmt.Sprintf("%.8f", old),
			"new_sl":  fmt.Sprintf("%.8f", pos.StopLoss),
			"current": fmt.Sprintf("%.8f", currentPrice),
		}).Info("🔒 AI tightened stop loss")

		// Cancel the existing exchange stop-order and place a new one at the
		// tightened price. Best-effort — failures are only logged. BOT-09:
		// sequential reissue, not two unordered goroutines.
		go bot.reissueExchangeStopLoss(context.Background(), pos)
	}
}

// executePartialClose issues a reduce-only market order opposite to the
// position's side to close `amount` units. Reduce-only guarantees we never
// flip the position by accident, and a market order ensures we capture the
// scale-out at the current price even if the level is grazed quickly.
func (bot *AITradingBot) executePartialClose(ctx context.Context, pos *ai.ManagedPosition, amount float64) error {
	side := strings.ToUpper(pos.Side)
	isLong := side == "BUY" || side == "LONG"
	closeSide := "sell"
	if !isLong {
		closeSide = "buy"
	}
	req := &gct.SubmitOrderRequest{
		Exchange:   bot.config.Exchange,
		Pair:       pos.Symbol,
		Side:       closeSide,
		Type:       "market",
		Amount:     amount,
		Leverage:   float64(pos.Leverage),
		ReduceOnly: true,
		ClientID:   fmt.Sprintf("PTP%d", time.Now().UnixMilli()%1e13), // max 16 chars, within KuCoin 40-char limit
	}
	closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	order, err := bot.gctClient.SubmitOrder(closeCtx, req)
	if err != nil {
		return fmt.Errorf("partial close order failed: %w", err)
	}
	bot.log.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"order_id":    order.ID,
		"amount":      amount,
	}).Info("✅ Partial close order placed")
	return nil
}

// reissueExchangeStopLoss cancels the position's existing exchange stop order
// (if any) and places a new one, SEQUENTIALLY in one goroutine (BOT-09).
// Previously every call site launched `go cancelExchangeStopLoss(...)` and
// `go placeExchangeStopLoss(...)` as two UNORDERED goroutines racing on
// pos.ExchangeStopOrderID: if place (which retries up to 3x) won and wrote
// the new order ID before cancel read it, cancel would cancel the NEW order
// instead of the old one — leaving either two live stop orders or zero,
// on a leveraged real-money position. Callers should invoke this with `go
// bot.reissueExchangeStopLoss(...)` instead of the two separate goroutines.
func (bot *AITradingBot) reissueExchangeStopLoss(ctx context.Context, pos *ai.ManagedPosition) {
	pos.Lock()
	defer pos.Unlock()

	if pos.ExchangeStopOrderID != "" {
		bot.cancelExchangeStopLoss(ctx, pos)
	}
	bot.placeExchangeStopLoss(ctx, pos)
}

// placeExchangeStopLoss places a real stop-market order on the exchange so the
// position is closed even if the bot crashes or loses track of the position.
// This is the primary SL protection layer; software monitoring is secondary.
//
// Callers: use reissueExchangeStopLoss (which holds pos.Lock for the whole
// cancel+place sequence) rather than calling this directly in its own
// goroutine — see BOT-09.
func (bot *AITradingBot) placeExchangeStopLoss(ctx context.Context, pos *ai.ManagedPosition) {
	if pos.StopLoss <= 0 {
		return
	}

	side := strings.ToUpper(pos.Side)
	isLong := side == "BUY" || side == "LONG"

	closeSide := "sell"
	if !isLong {
		closeSide = "buy"
	}

	req := &gct.SubmitOrderRequest{
		Exchange:      bot.config.Exchange,
		Pair:          pos.Symbol,
		Side:          closeSide,
		Type:          "market",
		Amount:        pos.Size,
		Leverage:      float64(pos.Leverage),
		StopPrice:     pos.StopLoss,
		StopPriceType: "MP", // Mark price
		CloseOrder:    true,
		ClientID:      fmt.Sprintf("SL%d", time.Now().UnixMilli()%1e13), // max 15 chars, within KuCoin 40-char limit
	}

	// The exchange stop is the position's primary crash-safe protection: a
	// single silent failure here used to leave the position on software
	// monitoring only. Retry with a backoff before giving up.
	retryAttempts := 3
	retryDelay := 2 * time.Second
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.IntOk("exit.exchange_sl.retry_attempts"); ok && v > 0 {
			retryAttempts = v
		}
		if v, ok := snap.IntOk("exit.exchange_sl.retry_delay_ms"); ok && v > 0 {
			retryDelay = time.Duration(v) * time.Millisecond
		}
	}

	var lastErr error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		order, err := bot.gctClient.SubmitOrder(stopCtx, req)
		cancel()

		if err == nil {
			pos.ExchangeStopOrderID = order.ID
			bot.log.WithFields(logrus.Fields{
				"position_id":   pos.ID,
				"stop_order_id": order.ID,
				"stop_price":    pos.StopLoss,
				"side":          closeSide,
				"attempt":       attempt,
			}).Info("🛡️ Exchange stop-loss order placed successfully")
			return
		}

		lastErr = err
		bot.log.WithError(err).WithFields(logrus.Fields{
			"position_id": pos.ID,
			"stop_price":  pos.StopLoss,
			"attempt":     attempt,
			"max":         retryAttempts,
		}).Warn("⚠️ Exchange stop-loss placement failed — retrying")

		if attempt < retryAttempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
		}
	}

	bot.log.WithError(lastErr).WithFields(logrus.Fields{
		"position_id": pos.ID,
		"stop_price":  pos.StopLoss,
		"attempts":    retryAttempts,
	}).Error("🚨 Failed to place exchange stop-loss after all retries — software monitoring only")
}

// cancelExchangeStopLoss cancels the exchange-side stop order when the position
// closes via TP or other means, to avoid a dangling stop order.
func (bot *AITradingBot) cancelExchangeStopLoss(ctx context.Context, pos *ai.ManagedPosition) {
	if pos.ExchangeStopOrderID == "" {
		return
	}

	cancelCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// The stop was placed through the direct KuCoin REST path, which bypasses
	// GCT's order manager, so the plain gRPC CancelOrder cannot find it and
	// fails with "currency pair unset" (seen live 2026-11-03 on the first real
	// close since July). Prefer the direct cancel when the client offers it.
	var err error
	if fc, ok := bot.gctClient.(interface {
		CancelFuturesOrder(ctx context.Context, exchange, orderID string) error
	}); ok {
		err = fc.CancelFuturesOrder(cancelCtx, bot.config.Exchange, pos.ExchangeStopOrderID)
	} else {
		err = bot.gctClient.CancelOrder(cancelCtx, bot.config.Exchange, pos.ExchangeStopOrderID)
	}
	if err != nil {
		bot.log.WithError(err).WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"stop_order_id": pos.ExchangeStopOrderID,
		}).Warn("⚠️ Failed to cancel exchange stop-loss order")
		return
	}

	bot.log.WithField("stop_order_id", pos.ExchangeStopOrderID).Info("✅ Exchange stop-loss order cancelled")
	pos.ExchangeStopOrderID = ""
}

// setAutonomousSLTP automatically sets stop loss and take profit for a position
func (bot *AITradingBot) setAutonomousSLTP(ctx context.Context, position *ai.ManagedPosition, decision *ai.Decision) error {
	var stopLoss, takeProfit float64

	// Prefer the position's authoritative SL/TP. executeTrade recomputes these
	// from the actual fill price (widening a too-tight clamp-to-support stop to
	// the tier's real distance) and stores them on the position. The decision is
	// the PRE-recompute snapshot — its StopLoss can still hold the ~0.3% clamped
	// value that gets wicked out by noise in minutes. Using decision here placed
	// that tight stop on the exchange, overriding the healthy one. Fall back to
	// decision, then to percentage defaults, only when the position lacks a value.
	if position.StopLoss > 0 {
		stopLoss = position.StopLoss
	} else if decision.StopLoss > 0 {
		stopLoss = decision.StopLoss
	} else {
		// Calculate default stop loss based on entry price and percentage
		if position.Side == "buy" || position.Side == "long" {
			stopLoss = position.EntryPrice * (1 - bot.config.DefaultStopLossPct/100)
		} else {
			stopLoss = position.EntryPrice * (1 + bot.config.DefaultStopLossPct/100)
		}
	}

	if position.TakeProfit > 0 {
		takeProfit = position.TakeProfit
	} else if decision.TakeProfit > 0 {
		takeProfit = decision.TakeProfit
	} else {
		// Calculate default take profit based on entry price and percentage
		if position.Side == "buy" || position.Side == "long" {
			takeProfit = position.EntryPrice * (1 + bot.config.DefaultTakeProfitPct/100)
		} else {
			takeProfit = position.EntryPrice * (1 - bot.config.DefaultTakeProfitPct/100)
		}
	}

	// Set SL/TP via executor
	if err := bot.sltpExecutor.UpdateSLTP(ctx, position.ID, stopLoss, takeProfit); err != nil {
		return fmt.Errorf("failed to set SL/TP: %w", err)
	}

	bot.log.WithFields(logrus.Fields{
		"position_id": position.ID,
		"symbol":      position.Symbol,
		"side":        position.Side,
		"entry_price": position.EntryPrice,
		"stop_loss":   stopLoss,
		"take_profit": takeProfit,
		"sl_pct":      fmt.Sprintf("%.2f%%", bot.config.DefaultStopLossPct),
		"tp_pct":      fmt.Sprintf("%.2f%%", bot.config.DefaultTakeProfitPct),
	}).Info("🛡️ Autonomous SL/TP set successfully")

	return nil
}

// checkAdverseConditions checks if current market conditions are adverse
func (bot *AITradingBot) checkAdverseConditions(ctx context.Context) bool {
	if !bot.config.PauseOnAdverseConditions {
		return false
	}

	// TC-4.6: read adverse condition thresholds from dynamic config
	btcDumpThreshold := -5.0 // default
	btcSpreadPct := 10.0     // default
	consecLossesBlock := 3   // default
	consecErrorsBlock := 5   // default
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.adverse.btc_dump_threshold_pct"); v != 0 {
			btcDumpThreshold = v
		}
		if v := snap.Float("bot.adverse.btc_spread_pct"); v != 0 {
			btcSpreadPct = v
		}
		if v := snap.Int("bot.adverse.consec_losses_block"); v != 0 {
			consecLossesBlock = v
		}
		if v := snap.Int("bot.adverse.consec_errors_block"); v != 0 {
			consecErrorsBlock = v
		}
	}

	// Check if GCT client is available
	if bot.gctClient == nil {
		bot.log.Debug("GCT client not available, skipping adverse condition check")
		return false
	}

	adverse := false
	reasons := []string{}

	// 1. Check BTC price movement (proxy for overall market health)
	btcTicker, err := bot.gctClient.GetTicker(ctx, "kucoin", "BTC-USDT")
	if err == nil && btcTicker != nil {
		// Calculate 1-hour price change percentage
		if btcTicker.High > 0 && btcTicker.Last > 0 {
			priceChange := ((btcTicker.Last - btcTicker.High) / btcTicker.High) * 100
			if priceChange < btcDumpThreshold {
				adverse = true
				reasons = append(reasons, fmt.Sprintf("BTC dump detected: %.2f%% drop", priceChange))
			}
		}
	}

	// 2. Check volatility through price spread
	if btcTicker != nil && btcTicker.High > 0 && btcTicker.Low > 0 {
		spread := ((btcTicker.High - btcTicker.Low) / btcTicker.Low) * 100
		if spread > btcSpreadPct {
			adverse = true
			reasons = append(reasons, fmt.Sprintf("High volatility: %.2f%% range", spread))
		}
	}

	// 3. Check our own recent performance (consecutive losses indicate adverse conditions for our strategy)
	bot.mu.RLock()
	recentLosses := bot.consecutiveLosses
	recentErrors := bot.consecutiveErrs
	bot.mu.RUnlock()

	if recentLosses >= consecLossesBlock {
		adverse = true
		reasons = append(reasons, fmt.Sprintf("Consecutive losses: %d", recentLosses))
	}

	if recentErrors >= consecErrorsBlock {
		adverse = true
		reasons = append(reasons, fmt.Sprintf("High error rate: %d consecutive errors", recentErrors))
	}

	// 4. Check market hours (optional - crypto trades 24/7 but weekends may have lower liquidity)
	now := time.Now().UTC()
	isWeekend := now.Weekday() == time.Saturday || now.Weekday() == time.Sunday
	if isWeekend && bot.config.AvoidWeekends {
		adverse = true
		reasons = append(reasons, "Weekend trading disabled")
	}

	if adverse && len(reasons) > 0 {
		bot.log.WithFields(logrus.Fields{
			"reasons": reasons,
		}).Warn("Adverse market conditions detected - pausing trading")
	}

	return adverse
}

// isAutonomousModeActive checks if autonomous mode is active and safe
func (bot *AITradingBot) isAutonomousModeActive() bool {
	// Resolved before taking bot.mu: it may read the (cached) account capital.
	dailyLossLimit := bot.effectiveDailyLossLimit(context.Background())

	bot.mu.RLock()
	defer bot.mu.RUnlock()

	// Check if autonomous mode is enabled
	if !bot.config.AutonomousMode {
		return false
	}

	// Check if bot is running and not paused
	if !bot.running || bot.paused || bot.emergencyStop {
		return false
	}

	// Check safety limits — dailyLoss is negative when losing; cap triggers when |loss| >= limit.
	if dailyLossLimit > 0 && bot.dailyLoss <= -dailyLossLimit {
		bot.log.WithFields(logrus.Fields{"daily_loss": bot.dailyLoss, "limit": dailyLossLimit}).
			Warn("🚫 Autonomous mode paused - daily loss limit reached")
		return false
	}

	maxDailyTradesAuton := bot.config.MaxDailyTrades
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if v := snap.Int("bot.max_daily_trades"); v > 0 {
			maxDailyTradesAuton = v
		}
	}
	if bot.dailyTrades >= maxDailyTradesAuton {
		bot.log.Warn("🚫 Autonomous mode paused - daily trade limit reached")
		return false
	}

	// TC-4.6: bot.autonomous.consec_error_window (default 5m)
	consecErrorWindow := 5 * time.Minute
	// Note: no ctx available here — use default store via context.Background()
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if d := snap.Duration("bot.autonomous.consec_error_window"); d > 0 {
			consecErrorWindow = d
		}
	}
	if bot.consecutiveErrs >= bot.config.MaxConsecutiveErrors {
		if time.Since(bot.lastErrorAt) <= consecErrorWindow {
			bot.log.Warn("🚫 Autonomous mode paused - consecutive error limit reached")
			return false
		}
	}

	return true
}

// Default cadence for the dedicated reconciliation loop when the dynamic key
// bot.position.reconcile_interval is unset. 60s is a robustness floor: it is
// independent of MonitorInterval / bot.position.sync_interval (which gate the
// monitor-loop piggyback sync) so reconciliation against exchange truth keeps
// happening even when no position is open or the monitor cadence is slow.
const defaultReconcileInterval = 60 * time.Second

// reconcileLoopEnabled reports whether the dedicated reconciliation loop should
// run. It defaults to ON (this is a robustness fix that only corrects local state
// toward exchange truth): the loop runs unless bot.position.reconcile_loop.enabled
// is explicitly set to false in dynamic config. Using BoolOk (not Bool) means an
// unconfigured key keeps the loop ON rather than depending on a registry default.
func (bot *AITradingBot) reconcileLoopEnabled(ctx context.Context) bool {
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.BoolOk("bot.position.reconcile_loop.enabled"); ok {
			return v
		}
	}
	return true
}

// reconcileInterval returns the dedicated reconciliation cadence, read from
// dynamic config (bot.position.reconcile_interval) with a sane default.
func (bot *AITradingBot) reconcileInterval(ctx context.Context) time.Duration {
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if d, ok := snap.DurationOk("bot.position.reconcile_interval"); ok && d > 0 {
			return d
		}
	}
	return defaultReconcileInterval
}

// positionReconciliationLoop periodically reconciles the local position view
// against the exchange (the source of truth — VISION §8.8). It is a thin,
// dedicated scheduler: the divergence detection and corrective actions live in
// syncOpenPositionsFromExchange, which is already scoped to the bot's managed
// pair(s) and is idempotent (anti-flapping ghost streak + synchronous
// RemovePosition after a recorded external close, so a close is never
// double-recorded). The loop:
//   - records external closes (exchange SL / liquidation / manual) to
//     trade_history and drops the resulting phantom local position;
//   - adopts orphan exchange positions on managed pairs (respecting blacklist,
//     race-guard and notional caps);
//   - NEVER imports unrelated pairs indiscriminately — matchesPair inside the
//     sync scopes adoption to config.Pair (or, only when explicitly enabled,
//     cross-pair / dynamic mode). This avoids the historical pair="" bug that
//     imported ALL exchange positions and blocked pair switching.
//
// The off-switch and interval are dynamic so a live money bot can tune or disable
// the cadence without a redeploy. Default ON, default 60s.
func (bot *AITradingBot) positionReconciliationLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in positionReconciliationLoop - recovered")
		}
	}()

	interval := bot.reconcileInterval(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	bot.log.WithField("interval", interval.String()).
		Info("🔁 Position reconciliation loop started (exchange = source of truth)")

	for {
		select {
		case <-ctx.Done():
			return
		case <-bot.stopChan:
			return
		case <-ticker.C:
			bot.mu.RLock()
			paused := bot.paused
			bot.mu.RUnlock()
			if paused {
				// Honour pause: a paused bot should not be mutating local state. The
				// startup sync and monitor-loop sync still cover the active path.
				continue
			}
			if !bot.reconcileLoopEnabled(ctx) {
				continue
			}
			// Re-read the interval each tick so an operator's dynamic change takes
			// effect without a restart.
			if d := bot.reconcileInterval(ctx); d > 0 && d != interval {
				interval = d
				ticker.Reset(interval)
				bot.log.WithField("interval", interval.String()).
					Info("🔁 Position reconciliation interval updated")
			}
			// syncOpenPositionsFromExchange self-serialises (syncRunMu.TryLock) so a
			// reconcile tick that overlaps the monitor-loop sync or the per-entry sync
			// is skipped harmlessly rather than racing.
			bot.syncOpenPositionsFromExchange(ctx)
		}
	}
}

// monitoringLoop monitors open positions
func (bot *AITradingBot) monitoringLoop(ctx context.Context) {
	// BOT-07: the loop itself had no recover — monitorPositions (called below)
	// does, but a panic in the select/ticker plumbing itself (or anything added
	// here later) would still crash the whole process. Blast radius: ALL bots
	// for ALL users share one process, so a single unrecovered panic here kills
	// every user's trading, not just this bot's.
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in monitoringLoop - recovered")
		}
	}()

	ticker := time.NewTicker(bot.config.MonitorInterval)
	defer ticker.Stop()

	bot.log.Info("👁️  Position monitoring loop started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-bot.stopChan:
			return
		case <-ticker.C:
			bot.monitorPositions(ctx)
		}
	}
}

// monitorPositions checks all open positions and broadcasts real-time updates.
func (bot *AITradingBot) monitorPositions(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in monitorPositions - recovered")
		}
	}()

	positions := bot.positionManager.GetAllPositions()
	// Re-sync from exchange periodically to drop ghost positions (exchange-side
	// SL fires don't notify the local manager) and recover missed entries.
	// Done unconditionally — not only when local is empty — because the
	// usual bug is exchange having fewer positions than local.
	// TC-4.6: bot.position.sync_interval (default 30s)
	positionSyncInterval := 30 * time.Second
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if d := snap.Duration("bot.position.sync_interval"); d > 0 {
			positionSyncInterval = d
		}
	}
	if time.Since(bot.lastPositionSyncAt) > positionSyncInterval {
		bot.syncOpenPositionsFromExchange(ctx)
		bot.lastPositionSyncAt = time.Now()
		positions = bot.positionManager.GetAllPositions()
	}
	if len(positions) == 0 {
		bot.broadcastExchangePositions(ctx)
		return
	}

	bot.log.WithField("count", len(positions)).Debug("Monitoring positions")

	for _, pos := range positions {
		// Keep this instance's cross-instance ownership claim fresh so a long-lived
		// position is never seen as stale and re-adopted by a sibling instance.
		refreshPositionOwnership(pos.Symbol, pos.Side, pos.ID)
		// Attach the per-pair dynamic-config scope chain so per-pair overrides of
		// exit/continuation keys resolve inside the monitor (the loop previously
		// passed the raw ctx, which fell back to the GLOBAL snapshot and silently
		// ignored per-pair overrides). bot.config.Pair is this instance's single pair.
		bot.checkPosition(bot.withPairScope(ctx), pos)
	}

	// Broadcast real-time updates to Flutter via WebSocket after all checks.
	bot.broadcastExchangePositions(ctx)
}

// broadcastExchangePositions fetches all open positions from the exchange and
// pushes each as a position_update WebSocket message so Flutter updates live.
func (bot *AITradingBot) broadcastExchangePositions(ctx context.Context) {
	if bot.notificationManager == nil {
		return
	}

	broadcaster, ok := bot.notificationManager.(interface {
		BroadcastPositions(positions []map[string]interface{}) error
	})
	if !ok {
		return
	}

	exchPositions, err := bot.gctClient.GetFuturesPositions(ctx, bot.config.Exchange)
	if err != nil {
		bot.log.WithError(err).Debug("broadcastExchangePositions: exchange fetch failed, skipping broadcast")
		return
	}

	// Build SL/TP lookup from internal manager.
	managed := bot.positionManager.GetAllPositions()
	sltp := make(map[string]map[string]float64)
	for _, p := range managed {
		key := strings.ToUpper(p.Symbol) + "|" + strings.ToUpper(p.Side)
		sltp[key] = map[string]float64{"stop_loss": p.StopLoss, "take_profit": p.TakeProfit}
	}

	out := make([]map[string]interface{}, 0, len(exchPositions))
	for _, p := range exchPositions {
		pnl := p.UnrealizedPnL
		key := strings.ToUpper(p.Symbol) + "|" + strings.ToUpper(p.Side)
		entry := map[string]interface{}{
			"id":             fmt.Sprintf("EX-%s-%s", p.Symbol, p.Side),
			"symbol":         p.Symbol,
			"side":           p.Side,
			"entry_price":    p.EntryPrice,
			"current_price":  p.CurrentPrice,
			"size":           p.Size,
			"leverage":       p.Leverage,
			"unrealized_pnl": pnl,
			"open_time":      p.UpdatedAt,
			"strategy":       "ai_bot",
			"status":         "open",
		}
		if st, found := sltp[key]; found {
			entry["stop_loss"] = st["stop_loss"]
			entry["take_profit"] = st["take_profit"]
		}
		out = append(out, entry)
	}

	if err := broadcaster.BroadcastPositions(out); err != nil {
		bot.log.WithError(err).Debug("broadcastExchangePositions: broadcast failed")
	}
}

// exchangeUnrealizedPnL returns the UnrealizedPnL KuCoin reports for the open
// position matching pos (by normalized symbol + direction). KuCoin computes PnL
// with the correct per-pair contract multiplier and denomination, so it is the
// source of truth on a market close — it sidesteps the USD-notional-vs-contracts
// ambiguity in our own Size field. Returns ok=false if the exchange call fails or
// no matching open position is found (e.g. already closed), so callers fall back
// to the local estimate.
func (bot *AITradingBot) exchangeUnrealizedPnL(ctx context.Context, pos *ai.ManagedPosition) (float64, bool) {
	if pos == nil {
		return 0, false
	}
	positions, err := bot.gctClient.GetFuturesPositions(ctx, bot.config.Exchange)
	if err != nil {
		return 0, false
	}
	want := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(pos.Symbol), "-", ""), "/", "")
	posLong := strings.ToUpper(pos.Side) == "BUY" || strings.ToUpper(pos.Side) == "LONG"
	for _, p := range positions {
		if !strings.HasPrefix(strings.ToUpper(p.Symbol), want) {
			continue
		}
		pLong := strings.ToUpper(p.Side) == "LONG" || strings.ToUpper(p.Side) == "BUY"
		if pLong != posLong {
			continue
		}
		return p.UnrealizedPnL, true
	}
	return 0, false
}

// checkLiquidationGuard evaluates BOT-03's liquidation-distance guard for
// leveraged (futures) positions and acts on the recommended GuardAction. It
// returns true when the position was closed by the guard (caller should stop
// processing it further this tick).
//
// GuardReduceLeverage/GuardConvertToSpot are logged as warnings — actually
// reducing leverage or converting to spot requires exchange margin-adjustment
// APIs this bot does not currently call, so the safest available reaction
// short of closing is tightening the stop-loss toward the current price to
// shrink further downside while the position remains open. GuardClose forces
// an immediate close via the existing closePosition path, ahead of the
// regular SL/TP/trailing checks below.
func (bot *AITradingBot) checkLiquidationGuard(ctx context.Context, pos *ai.ManagedPosition, currentPrice float64) bool {
	if pos == nil || pos.Leverage <= 1 {
		// Spot / unleveraged positions have no liquidation price to guard.
		return false
	}

	liqPrice, ok := bot.exchangeLiquidationPrice(ctx, pos)
	if !ok || liqPrice <= 0 {
		// No exchange-reported liquidation price available this tick — nothing
		// to evaluate against, fail open on this specific check (the regular
		// SL/TP logic still runs) rather than block monitoring entirely.
		return false
	}

	minBufferPct := 10.0 // sane default: warn/act well ahead of the SL
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("risk.liquidation_min_buffer_pct"); v > 0 {
			minBufferPct = v
		}
	}

	action := internalRisk.EvaluateLiquidationRisk(pos.EntryPrice, currentPrice, liqPrice, minBufferPct, pos.Side)
	if action == internalRisk.GuardOK {
		return false
	}

	logFields := logrus.Fields{
		"position_id":       pos.ID,
		"symbol":            pos.Symbol,
		"side":              pos.Side,
		"leverage":          pos.Leverage,
		"current_price":     currentPrice,
		"liquidation_price": liqPrice,
		"min_buffer_pct":    minBufferPct,
		"guard_action":      action.String(),
	}

	switch action {
	case internalRisk.GuardClose:
		bot.log.WithFields(logFields).Warn("🚨 Liquidation guard: position critically close to liquidation — closing now")
		pnl, _ := bot.exchangeUnrealizedPnL(ctx, pos)
		bot.closePosition(ctx, pos, "liquidation_guard_close", pnl)
		return true
	case internalRisk.GuardConvertToSpot, internalRisk.GuardReduceLeverage:
		bot.log.WithFields(logFields).Warn("⚠️ Liquidation guard: thin buffer — tightening stop-loss toward mark price")
		bot.tightenStopTowardPrice(pos, currentPrice, action)
	}
	return false
}

// tightenStopTowardPrice pulls the position's in-memory stop-loss halfway
// toward the current price when the liquidation guard reports a thin buffer,
// shrinking further downside without an outright close. GuardConvertToSpot
// (the more severe case) tightens harder (75% of the remaining distance) than
// GuardReduceLeverage (50%). Only tightens — never loosens an existing
// closer stop.
func (bot *AITradingBot) tightenStopTowardPrice(pos *ai.ManagedPosition, currentPrice float64, action internalRisk.GuardAction) {
	if pos.StopLoss <= 0 {
		return
	}
	pull := 0.5
	if action == internalRisk.GuardConvertToSpot {
		pull = 0.75
	}
	candidate := pos.StopLoss + (currentPrice-pos.StopLoss)*pull
	side := strings.ToUpper(pos.Side)
	if side == "BUY" {
		if candidate > pos.StopLoss {
			pos.StopLoss = candidate
		}
	} else {
		if candidate < pos.StopLoss {
			pos.StopLoss = candidate
		}
	}
}

// exchangeLiquidationPrice returns the exchange-reported liquidation price for
// the open position matching pos (by normalized symbol + direction), mirroring
// the matching logic in exchangeUnrealizedPnL. Returns ok=false if the
// exchange call fails or no matching open position is found.
func (bot *AITradingBot) exchangeLiquidationPrice(ctx context.Context, pos *ai.ManagedPosition) (float64, bool) {
	if pos == nil {
		return 0, false
	}
	positions, err := bot.gctClient.GetFuturesPositions(ctx, bot.config.Exchange)
	if err != nil {
		return 0, false
	}
	want := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(pos.Symbol), "-", ""), "/", "")
	posLong := strings.ToUpper(pos.Side) == "BUY" || strings.ToUpper(pos.Side) == "LONG"
	for _, p := range positions {
		if !strings.HasPrefix(strings.ToUpper(p.Symbol), want) {
			continue
		}
		pLong := strings.ToUpper(p.Side) == "LONG" || strings.ToUpper(p.Side) == "BUY"
		if pLong != posLong {
			continue
		}
		if p.LiquidationPrice <= 0 {
			return 0, false
		}
		return p.LiquidationPrice, true
	}
	return 0, false
}

// livePriceForPosition returns the current market price from the SAME market the
// position trades on. Futures positions (Leverage > 0) MUST be priced via the
// futures ticker — pricing a futures position with the spot ticker produces a
// phantom PnL equal to the spot/perp basis. On newly-listed tokens (LIT, ICNT)
// that basis is large enough (10%+) to trigger false stop-losses and book
// phantom losses (observed 2026-05-29: LIT short "stopped" at +12.28% / -$0.705
// in 7 seconds while the real futures price was flat). Returns the ticker error
// unchanged so callers keep their fallbacks.
func (bot *AITradingBot) livePriceForPosition(ctx context.Context, pos *ai.ManagedPosition) (float64, error) {
	symbol := pos.Symbol
	if pos.Leverage > 0 {
		symbol = toFuturesSymbol(pos.Symbol)
	}
	ticker, err := bot.gctClient.GetTicker(ctx, bot.config.Exchange, symbol)
	if err != nil {
		return 0, err
	}
	// Guard against a stale/empty ticker reporting Last <= 0. A zero price made
	// checkPosition compute pnlPct = (entry-0)/entry*100 = 100% for SHORTs,
	// firing phantom take-profits that recorded +size as fake wins with an empty
	// exit_price (e.g. 127 fake BCH +100% trades inflating trade_history by
	// ~$440). Returning an error makes every caller skip the tick instead.
	if ticker.Last <= 0 {
		return 0, fmt.Errorf("invalid ticker price %.8f for %s (stale/empty market data)", ticker.Last, symbol)
	}
	return ticker.Last, nil
}

// resolveExternalExit determines the REAL exit price and reason for a position
// that disappeared from the exchange without a bot-initiated close. Resolution
// order:
//  1. Order history since the position opened: the most recent filled order on
//     the opposite side is the closing fill. If its price sits within 0.3% of
//     the position's stop-loss → "exchange_sl"; otherwise → "manual_close"
//     (the operator closed it on the exchange UI — must be recorded as such,
//     at the REAL fill price, not a synthetic SL price).
//  2. Live ticker → "external_close" (price is approximate but honest).
//  3. Zero (caller falls back to the legacy StopLoss estimate).
//
// Added 2026-06-10 after an operator's manual close was recorded as exchange_sl
// at exactly entry×1.02 (the synthetic SL price), falsifying trade_history and
// the feedback loop's training data.
func (bot *AITradingBot) resolveExternalExit(ctx context.Context, p *ai.ManagedPosition) (float64, string) {
	// 1) Closing fill from order history.
	if bot.gctClient != nil && p.OpenedAt.Unix() > 0 {
		start := p.OpenedAt
		end := time.Now()
		orders, err := bot.gctClient.GetOrders(ctx, bot.config.Exchange, p.Symbol, "", "futures", &start, &end)
		if err == nil {
			posIsLong := strings.EqualFold(p.Side, "buy") || strings.EqualFold(p.Side, "long")
			var best *gct.Order
			for _, o := range orders {
				if o == nil || o.Filled <= 0 {
					continue
				}
				oIsBuy := strings.EqualFold(o.Side, "buy") || strings.EqualFold(o.Side, "long")
				if oIsBuy == posIsLong {
					continue // same side as the position = the opening order, not the close
				}
				if best == nil || o.UpdatedAt.After(best.UpdatedAt) {
					best = o
				}
			}
			if best != nil && best.Price > 0 {
				reason := "manual_close"
				if p.StopLoss > 0 && math.Abs(best.Price-p.StopLoss)/p.StopLoss <= 0.003 {
					reason = "exchange_sl"
				}
				if strings.Contains(strings.ToLower(best.Type), "stop") {
					reason = "exchange_sl"
				}
				return best.Price, reason
			}
		} else {
			bot.log.WithError(err).WithField("symbol", p.Symbol).
				Debug("resolveExternalExit: order history unavailable; falling back to ticker")
		}
	}
	// 2) Live ticker — honest approximation when history is unavailable.
	if price, err := bot.livePriceForPosition(ctx, p); err == nil && price > 0 {
		return price, "external_close"
	}
	// 3) Caller applies the legacy StopLoss fallback.
	return 0, ""
}

// toFuturesSymbol converts a standard pair ("WLD-USDT") to KuCoin's futures
// ticker format ("WLDUSDTM") — no separator, "M" suffix. This matches the order
// path (gct.convertPairToFuturesSymbol) and KuCoin's futures REST endpoints.
// IMPORTANT: do NOT use gct.ConvertSymbol here — it emits the underscore form
// ("WLD_USDTM") which KuCoin's futures ticker endpoint rejects with error
// 200003, silently breaking the price fetch.
func toFuturesSymbol(pair string) string {
	s := strings.ReplaceAll(strings.ReplaceAll(pair, "-", ""), "_", "")
	if !strings.HasSuffix(s, "M") {
		s += "M"
	}
	return s
}

// checkPosition checks a single position for SL/TP
// ──────────────────────────────────────────────────────────────────────────
// Active Profit Engine — continuation gate (Phase 1)
//
// While a position is in profit, the continuation gate measures whether the
// move is still healthy enough to RUN rather than take profit. When the score
// clears the threshold it (a) loosens the trailing stop, (b) defers partial
// take-profits, (c) lets extendTakeProfit push the TP further out, and (d)
// vetoes the soft profit-taking exits (MFE ratchet, wick, condition, LLM,
// signal-driven) so a pullback-in-trend is held instead of closed. The HARD
// protectors (breakeven, trailing, SL/TP) always remain the floor.
//
// Opt-in (bot.continuation.enabled, default false) and shadow-first
// (bot.continuation.shadow_mode, default true): in shadow the score and the
// action it WOULD take are logged but nothing is vetoed and the TP is not moved,
// so we can measure on live data whether holding improves expectancy before
// giving it teeth. With the feature disabled, behaviour is byte-identical.
// ──────────────────────────────────────────────────────────────────────────

type continuationGate struct {
	enabled       bool
	shadow        bool
	confirmed     bool // live authority to hold/extend (false while shadow)
	wouldHold     bool // score >= threshold regardless of shadow (for logging)
	score         float64
	threshold     float64
	deferPartials bool
	widenFactor   float64 // trailing widen multiplier when holding (>= 1)
	reasons       []string
}

// trailingWiden returns the factor to loosen the trailing stop by when the gate
// has LIVE authority to hold; 1.0 (no change) otherwise.
func (g continuationGate) trailingWiden() float64 {
	if g.confirmed && g.widenFactor > 1 {
		return g.widenFactor
	}
	return 1.0
}

// assessContinuation measures whether an in-profit position is still trending
// strongly enough to RUN rather than take profit. Opt-in + shadow-first.
func (bot *AITradingBot) assessContinuation(ctx context.Context, pos *ai.ManagedPosition, pnlPct float64) continuationGate {
	g := continuationGate{widenFactor: 1.0}
	cfg := dynamic.SnapshotFromContext(ctx)
	if cfg == nil || !cfg.Bool("bot.continuation.enabled") {
		return g
	}
	g.enabled = true
	g.shadow = true
	if v, ok := cfg.BoolOk("bot.continuation.shadow_mode"); ok {
		g.shadow = v
	}
	g.threshold = 0.65
	if v := cfg.Float("bot.continuation.threshold"); v > 0 {
		g.threshold = v
	}
	g.deferPartials = cfg.Bool("bot.continuation.defer_partials_once")
	if v := cfg.Float("trailing.continuation_widen_factor"); v >= 1 {
		g.widenFactor = v
	}
	// Never hold a loser past its stop; only assess positions in profit.
	if pnlPct <= 0 {
		return g
	}
	bot.indicatorCacheMu.RLock()
	snap := bot.indicatorCache[pos.Symbol]
	bot.indicatorCacheMu.RUnlock()
	if snap == nil {
		return g
	}
	// Don't hold on stale signals.
	if time.Since(snap.UpdatedAt) > 2*bot.config.AnalysisInterval {
		return g
	}
	g.score, g.reasons = bot.continuationScore(ctx, pos, snap, cfg)
	g.wouldHold = g.score >= g.threshold
	g.confirmed = g.wouldHold && !g.shadow
	if g.wouldHold {
		tag := ""
		if g.shadow {
			tag = " [SHADOW]"
		}
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"symbol":      pos.Symbol,
			"score":       fmt.Sprintf("%.2f", g.score),
			"threshold":   fmt.Sprintf("%.2f", g.threshold),
			"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
			"reasons":     strings.Join(g.reasons, ","),
		}).Info("🏃 Continuation gate: holding the runner" + tag)
	}
	return g
}

// continuationScore rates [0..1] how strongly the move favours HOLDING an
// in-profit position. Weighted factors, all from already-cached indicators so
// the gate adds no per-tick fetch beyond the (cached) macro-1h label:
//
//	ADX trend strength (0.30), DI alignment (0.15), macro-1h agreement (0.20),
//	volume expansion (0.10), MACD-histogram acceleration (0.15),
//	price at a favourable extreme (0.10).
func (bot *AITradingBot) continuationScore(ctx context.Context, pos *ai.ManagedPosition, snap *positionIndicatorSnapshot, cfg *dynamic.ScopedSnapshot) (float64, []string) {
	isLong := pos.Side == "buy" || pos.Side == "long"
	adxMin := 25.0
	if v := cfg.Float("bot.continuation.adx_min"); v > 0 {
		adxMin = v
	}
	volRatio := 1.2
	if v := cfg.Float("bot.continuation.volume_expansion_ratio"); v > 0 {
		volRatio = v
	}
	exch := pos.Exchange
	if exch == "" {
		exch = bot.config.Exchange
	}
	macroLabel := bot.macroTrendLabel(ctx, exch, pos.Symbol)
	return continuationScoreFromInputs(isLong, snap, macroLabel, adxMin, volRatio, pos.HighestPrice, pos.LowestPrice)
}

// continuationScoreFromInputs is the pure scoring core (no bot / network /
// config dependencies) so it can be unit-tested directly. macroLabel is
// "bullish" | "bearish" | "" from the (cached) macro-1h trend.
func continuationScoreFromInputs(isLong bool, snap *positionIndicatorSnapshot, macroLabel string, adxMin, volRatio, highestPrice, lowestPrice float64) (float64, []string) {
	var score float64
	var reasons []string
	if snap == nil {
		return 0, reasons
	}

	// 1) ADX trend strength.
	if snap.ADX >= adxMin {
		score += 0.30
		reasons = append(reasons, fmt.Sprintf("adx=%.0f", snap.ADX))
	}
	// 2) Directional indicator aligned with the position side.
	if (isLong && snap.DIPlus > snap.DIMinus) || (!isLong && snap.DIMinus > snap.DIPlus) {
		score += 0.15
		reasons = append(reasons, "di_aligned")
	}
	// 3) Macro 1h trend agrees with the position side.
	switch macroLabel {
	case "bullish":
		if isLong {
			score += 0.20
			reasons = append(reasons, "macro_bull")
		}
	case "bearish":
		if !isLong {
			score += 0.20
			reasons = append(reasons, "macro_bear")
		}
	}
	// 4) Volume expansion (momentum still being fed).
	if snap.AvgVolume > 0 && snap.Volume >= volRatio*snap.AvgVolume {
		score += 0.10
		reasons = append(reasons, "vol_expansion")
	}
	// 5) MACD histogram accelerating in the position direction.
	if (isLong && snap.Histogram > 0 && snap.HistogramDelta > 0) ||
		(!isLong && snap.Histogram < 0 && snap.HistogramDelta < 0) {
		score += 0.15
		reasons = append(reasons, "macd_accel")
	}
	// 6) Price at a favourable extreme (new high for long / new low for short).
	if isLong && highestPrice > 0 && snap.CurrentPrice >= highestPrice*0.999 {
		score += 0.10
		reasons = append(reasons, "new_high")
	} else if !isLong && lowestPrice > 0 && snap.CurrentPrice <= lowestPrice*1.001 {
		score += 0.10
		reasons = append(reasons, "new_low")
	}
	return score, reasons
}

// extendTakeProfit pushes the TP further out (by tp_step_atr × ATR) when price
// approaches the current TP and the continuation gate confirms a healthy trend.
// This is the ONLY place pos.TakeProfit is rewritten after entry — it converts
// the static TP into a trailing target so a strong move is not capped. Returns
// true when the TP was actually moved. In shadow mode it logs the target it
// WOULD set without moving the TP.
func (bot *AITradingBot) extendTakeProfit(ctx context.Context, pos *ai.ManagedPosition, currentPrice float64, g continuationGate) bool {
	if !g.enabled || !g.wouldHold || pos.TakeProfit <= 0 {
		return false
	}
	bot.indicatorCacheMu.RLock()
	snap := bot.indicatorCache[pos.Symbol]
	bot.indicatorCacheMu.RUnlock()
	if snap == nil || snap.ATR <= 0 {
		return false
	}
	atr := snap.ATR

	stepATR := 1.0
	approachPct := 0.20
	if cfg := dynamic.SnapshotFromContext(ctx); cfg != nil {
		if v := cfg.Float("bot.continuation.tp_step_atr"); v > 0 {
			stepATR = v
		}
		if v := cfg.Float("bot.continuation.tp_approach_pct"); v > 0 {
			approachPct = v
		}
	}

	// Only extend once price is within approachPct of the remaining distance to
	// the TP — answers "winning but not reaching the TP", and avoids ratcheting
	// the target every tick from far away.
	remaining := math.Abs(pos.TakeProfit - currentPrice)
	gap := math.Abs(pos.TakeProfit - pos.EntryPrice)
	if gap <= 0 || remaining > gap*approachPct {
		return false
	}

	isLong := pos.Side == "buy" || pos.Side == "long"
	var newTP float64
	if isLong {
		newTP = pos.TakeProfit + stepATR*atr
	} else {
		newTP = pos.TakeProfit - stepATR*atr
		if newTP <= 0 {
			return false
		}
	}

	if g.shadow || !g.confirmed {
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID, "symbol": pos.Symbol,
			"tp": pos.TakeProfit, "would_tp": newTP, "atr": atr,
		}).Info("🎯 extendTakeProfit [SHADOW] — would push TP further out")
		return false
	}
	old := pos.TakeProfit
	pos.TakeProfit = newTP
	bot.log.WithFields(logrus.Fields{
		"position_id": pos.ID, "symbol": pos.Symbol,
		"old_tp": old, "new_tp": newTP, "step_atr": stepATR, "atr": atr,
		"score": fmt.Sprintf("%.2f", g.score),
	}).Info("🎯 extendTakeProfit — TP re-targeted further out (continuation confirmed)")
	return true
}

// ──────────────────────────────────────────────────────────────────────────
// Active Profit Engine — exit-decision fusion (Phase 3)
//
// Fuse the independent exit signals (deterministic rule, market regime, and —
// when available — ML and LLM) into ONE signed decision in [-1,+1]:
//
//	score = Σ(weightᵢ · confᵢ · valueᵢ) / Σ(weightᵢ · confᵢ)
//
// Precedence is explicit and safe: the HARD protectors (breakeven, trailing,
// SL/TP, MFE) run OUTSIDE the fusion and are never overridden. A deterministic
// rule reversal is a HARD VETO (forces CLOSE). Soft votes alone never close —
// CLOSE needs score ≤ close_threshold AND ≥ min_sources non-abstaining sources,
// otherwise it degrades to TIGHTEN. Opt-in (bot.exit.fusion.enabled, default
// off) and shadow-first (bot.exit.fusion.shadow_mode, default true: log the
// decision without acting). Sources that can't opine (ml-server down, LLM
// rate-limited) ABSTAIN and drop out of the weighting — graceful degradation.
// ──────────────────────────────────────────────────────────────────────────

const (
	mgmtHold       = "HOLD"
	mgmtExtend     = "EXTEND"
	mgmtTakeProfit = "TAKE_PROFIT"
	mgmtTighten    = "TIGHTEN"
	mgmtClose      = "CLOSE"
)

// ManagementDecision is the fused exit decision for one tick.
type ManagementDecision struct {
	Action     string
	Score      float64
	Confidence float64
	Reason     string
	Votes      map[string]float64
	NonAbstain int
}

// exitVote is one source's signed opinion. value<0 = exit, value>0 = hold/extend.
type exitVote struct {
	source   string
	value    float64 // [-1,+1]
	conf     float64 // [0,1]
	weight   float64
	abstain  bool
	hardVeto bool // deterministic rule reversal — forces CLOSE
}

type fusionThresholds struct {
	closeScore   float64
	tightenScore float64
	extendScore  float64
	minSources   int
}

// fuseVotes is the pure scoring core (no bot/network deps) — unit-tested.
func fuseVotes(votes []exitVote, th fusionThresholds) ManagementDecision {
	d := ManagementDecision{Action: mgmtHold, Votes: map[string]float64{}}
	var num, den float64
	for _, v := range votes {
		if v.abstain || v.weight <= 0 {
			continue
		}
		if v.hardVeto {
			return ManagementDecision{
				Action: mgmtClose, Score: -1, Confidence: 1,
				Reason: "hard veto: " + v.source + " reversal",
				Votes:  map[string]float64{v.source: v.value}, NonAbstain: 1,
			}
		}
		w := v.weight * v.conf
		num += w * v.value
		den += w
		d.NonAbstain++
		d.Votes[v.source] = v.value
	}
	if den <= 0 {
		d.Reason = "no active sources — hold"
		return d
	}
	score := num / den
	d.Score = score
	d.Confidence = math.Abs(score)
	switch {
	case score <= th.closeScore && d.NonAbstain >= th.minSources:
		d.Action = mgmtClose
	case score <= th.tightenScore: // strong-ish negative (incl. close-but-too-few) → tighten
		d.Action = mgmtTighten
	case score >= th.extendScore:
		d.Action = mgmtExtend
	default:
		d.Action = mgmtHold
	}
	d.Reason = fmt.Sprintf("score=%.2f sources=%d", score, d.NonAbstain)
	return d
}

// regimeExitVote maps the market regime to a signed hold/exit vote for the
// position side. Aligned trend → hold; ranging/unknown → take profit; chaos → exit.
func regimeExitVote(regimeLabel string, isLong bool) (value, conf float64) {
	switch regimeLabel {
	case string(regime.RegimeTrendingUp), string(regime.RegimeQuietTrendUp):
		if isLong {
			return 0.4, 0.7
		}
		return -0.4, 0.7
	case string(regime.RegimeTrendingDown), string(regime.RegimeQuietTrendDown):
		if !isLong {
			return 0.4, 0.7
		}
		return -0.4, 0.7
	case string(regime.RegimeBreakout):
		return 0.3, 0.5
	case string(regime.RegimeRanging), string(regime.RegimeUnknown), "":
		return -0.3, 0.5
	case string(regime.RegimeChaos):
		return -1.0, 0.8
	}
	return 0, 0.3
}

// extractPositionFeatures builds an ML feature vector from the open position's
// current primary-timeframe candles (cached via the exit-ladder fetch so it adds
// no per-tick REST call). Returns nil when candles are unavailable/insufficient.
func (bot *AITradingBot) extractPositionFeatures(ctx context.Context, pos *ai.ManagedPosition) *featureeng.Features {
	tf := "5m"
	if len(bot.config.Timeframes) > 0 && bot.config.Timeframes[0] != "" {
		tf = bot.config.Timeframes[0]
	}
	candles, ok := bot.exitLadderCandles(ctx, pos, tf, 100, 5*time.Second, 30*time.Second)
	if !ok || len(candles) < 35 {
		return nil
	}
	fc := make([]featureeng.Candle, len(candles))
	for i, c := range candles {
		fc[i] = featureeng.Candle{
			Open: c.Open, High: c.High, Low: c.Low,
			Close: c.Close, Volume: c.Volume,
			HourUTC: c.Timestamp.UTC().Hour(),
		}
	}
	return featureeng.ExtractFeatures(fc)
}

// mlExitVote queries the ML predictor for a hold/exit opinion on the open
// position (Phase C). Gated by ml.exit.enabled (default off). Returns ok=false
// (ABSTAIN) when disabled, the predictor isn't ready, features can't be built,
// or the prediction confidence is below ml.exit.min_confidence.
//
// The probability is interpreted as P(bullish continuation): high prob favours
// HOLDING a long and EXITING a short (sign flips for shorts). This reuses the
// wired entry predictor as a defensible approximation; a dedicated exit model
// (train.py --target exit) can replace it without touching the fusion core.
func (bot *AITradingBot) mlExitVote(ctx context.Context, pos *ai.ManagedPosition) (value, conf float64, ok bool) {
	cfg := dynamic.SnapshotFromContext(ctx)
	if cfg == nil || !cfg.Bool("ml.exit.enabled") {
		return 0, 0, false
	}
	bot.mu.RLock()
	predictor := bot.mlPredictor
	bot.mu.RUnlock()
	if predictor == nil || !predictor.IsReady() {
		return 0, 0, false
	}
	feat := bot.extractPositionFeatures(ctx, pos)
	if feat == nil {
		return 0, 0, false
	}
	pctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	pred, err := predictor.Predict(pctx, feat, bot.currentRegimeLabel())
	cancel()
	if err != nil || pred == nil {
		return 0, 0, false
	}
	minConf := 0.2
	if v := cfg.Float("ml.exit.min_confidence"); v > 0 {
		minConf = v
	}
	conf = math.Abs(pred.Probability-0.5) * 2
	if conf < minConf {
		return 0, 0, false // not confident enough — abstain
	}
	// P>0.5 = bullish continuation. Long: hold (+). Short: exit (-).
	signed := (pred.Probability - 0.5) * 2
	if pos.Side == "sell" || pos.Side == "short" {
		signed = -signed
	}
	return signed, conf, true
}

// fuseExitDecision gathers the per-source votes and runs the pure fusion core.
func (bot *AITradingBot) fuseExitDecision(ctx context.Context, pos *ai.ManagedPosition, currentPrice, pnlPct float64, regimeLabel string) ManagementDecision {
	cfg := dynamic.SnapshotFromContext(ctx)
	wRule, wLLM, wML, wRegime, wMTF := 0.40, 0.25, 0.20, 0.15, 0.30
	th := fusionThresholds{closeScore: -0.6, tightenScore: -0.25, extendScore: 0.6, minSources: 2}
	if cfg != nil {
		if v := cfg.Float("bot.exit.fusion.weight_rule"); v > 0 {
			wRule = v
		}
		if v := cfg.Float("bot.exit.fusion.weight_llm"); v > 0 {
			wLLM = v
		}
		if v := cfg.Float("bot.exit.fusion.weight_ml"); v > 0 {
			wML = v
		}
		if v := cfg.Float("bot.exit.fusion.weight_regime"); v > 0 {
			wRegime = v
		}
		if v := cfg.Float("bot.exit.fusion.weight_mtf"); v > 0 {
			wMTF = v
		}
		if v, ok := cfg.FloatOk("bot.exit.fusion.close_score_threshold"); ok {
			th.closeScore = v
		}
		if v, ok := cfg.FloatOk("bot.exit.fusion.tighten_score_threshold"); ok {
			th.tightenScore = v
		}
		if v, ok := cfg.FloatOk("bot.exit.fusion.extend_score_threshold"); ok {
			th.extendScore = v
		}
		if v := cfg.Int("bot.exit.fusion.min_sources_for_close"); v > 0 {
			th.minSources = v
		}
	}

	// Phase 5 — close the loop: optionally replace the fixed fallback weights with
	// the Bayesian calibrator's per-regime, per-source weights (learned from real
	// exit outcomes). Default off — enable only once the calibrator is no longer
	// inert. "rule" maps to the technical source in signal_calibration.
	if cfg != nil && cfg.Bool("bot.exit.fusion.use_calibrator_weights") {
		if sw, ok := bot.mlWeightStore.(sourceWeightStore); ok && sw != nil {
			if w := sw.SourceWeightFor("technical", regimeLabel); w > 0 {
				wRule = w
			}
			if w := sw.SourceWeightFor("llm", regimeLabel); w > 0 {
				wLLM = w
			}
			if w := sw.SourceWeightFor("ml", regimeLabel); w > 0 {
				wML = w
			}
			if w := sw.SourceWeightFor("regime", regimeLabel); w > 0 {
				wRegime = w
			}
		}
	}

	isLong := pos.Side == "buy" || pos.Side == "long"
	votes := make([]exitVote, 0, 4)

	// vRule — deterministic wick/condition reversal (read-only detection here).
	ruleFire := false
	if wc, _ := bot.detectWickReversal(pos, pnlPct); wc {
		ruleFire = true
	} else if se, _ := bot.evaluateConditionExit(ctx, pos); se {
		ruleFire = true
	}
	if ruleFire {
		votes = append(votes, exitVote{source: "rule", value: -1, conf: 1.0, weight: wRule, hardVeto: true})
	} else {
		votes = append(votes, exitVote{source: "rule", value: 0.1, conf: 0.3, weight: wRule})
	}

	// vRegime.
	rv, rc := regimeExitVote(regimeLabel, isLong)
	votes = append(votes, exitVote{source: "regime", value: rv, conf: rc, weight: wRegime})

	// vML — abstains when the predictor is unavailable (graceful degradation).
	if mv, mc, ok := bot.mlExitVote(ctx, pos); ok {
		votes = append(votes, exitVote{source: "ml", value: mv, conf: mc, weight: wML})
	} else {
		votes = append(votes, exitVote{source: "ml", abstain: true, weight: wML})
	}

	// vLLM — abstains in the fusion path (the cascade's LLM advisor still runs
	// when fusion is in shadow; avoids a double LLM call when fusion is live).
	votes = append(votes, exitVote{source: "llm", abstain: true, weight: wLLM})

	// vMTF — AnkaPulse: higher-timeframe (15m/30m/1h) confluence. Banks an
	// in-profit position when the bigger picture turns against it (don't wait for
	// TP and let the gain dilute); leans EXTEND when the higher TFs confirm the
	// move. Only votes once meaningfully green — it manages gains, not losses —
	// and abstains (fail-open) when disabled or no TF could be read.
	mtfMinProfit := 0.2
	if cfg != nil {
		if v, ok := cfg.FloatOk("bot.exit.mtf.min_profit_pct"); ok {
			mtfMinProfit = v
		}
	}
	if pnlPct > mtfMinProfit {
		if mtv, mtc, ok := bot.mtfPulse(ctx, pos); ok {
			votes = append(votes, exitVote{source: "mtf", value: mtv, conf: mtc, weight: wMTF})
		} else {
			votes = append(votes, exitVote{source: "mtf", abstain: true, weight: wMTF})
		}
	} else {
		votes = append(votes, exitVote{source: "mtf", abstain: true, weight: wMTF})
	}

	return fuseVotes(votes, th)
}

// mtfPulse — AnkaPulse: the higher-timeframe confluence read for the fusion exit.
//
// It looks at recent momentum on each configured higher timeframe (default
// 15m/30m/1h) and returns a signed score for the POSITION SIDE:
//
//	value > 0 → the bigger picture still favours the move (let the winner run)
//	value < 0 → the higher TFs are turning against it (bank the gain now, before
//	            it dilutes — the human "I see the turn coming" reflex)
//
// conf scales with how many TFs agree and how strong the move is. ok=false
// (abstain) when disabled or no TF could be read — the caller fails open so a
// fetch hiccup never forces an exit. Candle fetches reuse the per-pair/TF exit
// ladder cache (≤1 REST call per pair/TF per cache window).
func (bot *AITradingBot) mtfPulse(ctx context.Context, pos *ai.ManagedPosition) (value, conf float64, ok bool) {
	cfg := dynamic.SnapshotFromContext(ctx)
	if cfg == nil || !cfg.Bool("bot.exit.mtf.enabled") {
		return 0, 0, false
	}

	tfs := []string{"15m", "30m", "1h"}
	if csv := cfg.String("bot.exit.mtf.timeframes"); csv != "" {
		parsed := make([]string, 0, 4)
		for _, raw := range strings.Split(csv, ",") {
			if tf := strings.TrimSpace(raw); tf != "" {
				parsed = append(parsed, tf)
			}
		}
		if len(parsed) > 0 {
			tfs = parsed
		}
	}

	momLookback := 6
	if v := cfg.Int("bot.exit.mtf.momentum_lookback"); v > 0 {
		momLookback = v
	}
	refMove := 0.01 // 1% per-TF move == full-strength signal
	if v := cfg.Float("bot.exit.mtf.reference_move_pct"); v > 0 {
		refMove = v / 100
	}
	cacheTTL := 30 * time.Second
	if v := cfg.Duration("bot.exit.mtf.cache_ttl"); v > 0 {
		cacheTTL = v
	}

	isLong := pos.Side == "buy" || pos.Side == "long"
	limit := momLookback + 5

	var sum, wsum float64
	agree, total := 0, 0
	for _, tf := range tfs {
		candles, cok := bot.exitLadderCandles(ctx, pos, tf, limit, 8*time.Second, cacheTTL)
		if !cok || len(candles) <= momLookback {
			continue
		}
		last := candles[len(candles)-1].Close
		past := candles[len(candles)-1-momLookback].Close
		if last <= 0 || past <= 0 {
			continue
		}
		changePct := (last - past) / past // signed price momentum on this TF
		// Align to the position side: + favours holding the move, - favours banking.
		aligned := changePct
		if !isLong {
			aligned = -aligned
		}
		// Per-TF signal normalized into [-1,1] by the reference move; weight by the
		// move magnitude (bigger moves carry more conviction, capped) so a flat TF
		// barely tilts the result.
		norm := math.Max(-1, math.Min(1, aligned/refMove))
		w := math.Min(math.Abs(changePct)/refMove, 3.0)
		if w < 0.2 {
			w = 0.2
		}
		sum += w * norm
		wsum += w
		total++
		if aligned >= 0 {
			agree++
		}
	}
	if total == 0 || wsum <= 0 {
		return 0, 0, false
	}

	value = sum / wsum // [-1,1]: + run, - bank
	agreeFrac := float64(agree) / float64(total)
	conf = math.Min(1.0, math.Abs(value)*0.6+agreeFrac*0.4)
	return value, conf, true
}

// ── Active Profit Engine — regime × volatility-tier exit profiles (Phase 4) ──
// "Win in all scenarios" = the right exit profile per scenario, not one fixed
// set. A small, honest model (3 regime buckets + a volatility multiplier) tunes
// the MFE keep-fraction and the trailing distance by market condition. Opt-in
// (bot.exit.profiles.enabled, default off). Deliberately 3 buckets, not a
// 6×4 grid, to avoid over-fitting profiles with no data per cell.

type exitProfile struct {
	keepFraction     float64 // MFE keep-fraction override (0 = none)
	trailingDistance float64 // trailing base distance % override (0 = none)
	trailingMult     float64 // volatility-tier trailing multiplier (default 1)
	active           bool
}

// regimeProfileBucket collapses a regime label to one of three profile buckets.
func regimeProfileBucket(regimeLabel string) string {
	switch regimeLabel {
	case string(regime.RegimeTrendingUp), string(regime.RegimeTrendingDown),
		string(regime.RegimeQuietTrendUp), string(regime.RegimeQuietTrendDown):
		return "trending"
	case string(regime.RegimeBreakout):
		return "breakout"
	default:
		return "ranging" // ranging / unknown / chaos / "" → conservative
	}
}

// voltierMult selects the trailing-distance multiplier for a volatility tier.
func voltierMult(tier volatility.Tier, low, normal, vol, extreme float64) float64 {
	switch tier {
	case volatility.TierLow:
		return low
	case volatility.TierVolatile:
		return vol
	case volatility.TierExtreme:
		return extreme
	default:
		return normal
	}
}

// resolveExitProfile selects the MFE keep-fraction and trailing parameters for
// the position's current regime and volatility tier. Returns an inactive profile
// (no overrides) when the feature is disabled.
func (bot *AITradingBot) resolveExitProfile(ctx context.Context, pos *ai.ManagedPosition) exitProfile {
	p := exitProfile{trailingMult: 1}
	cfg := dynamic.SnapshotFromContext(ctx)
	if cfg == nil || !cfg.Bool("bot.exit.profiles.enabled") {
		return p
	}
	p.active = true
	bucket := regimeProfileBucket(bot.currentRegimeLabel())
	p.keepFraction = cfg.Float("bot.exit.profiles." + bucket + ".keep_fraction")
	if bucket == "trending" || bucket == "ranging" {
		p.trailingDistance = cfg.Float("bot.exit.profiles." + bucket + ".trailing_distance_pct")
	}
	if bot.volClassifier != nil {
		m := voltierMult(bot.volClassifier.Classify(pos.Symbol),
			cfg.Float("bot.exit.voltier_distance_mult.low"),
			cfg.Float("bot.exit.voltier_distance_mult.normal"),
			cfg.Float("bot.exit.voltier_distance_mult.volatile"),
			cfg.Float("bot.exit.voltier_distance_mult.extreme"))
		if m > 0 {
			p.trailingMult = m
		}
	}
	return p
}

func (bot *AITradingBot) checkPosition(ctx context.Context, pos *ai.ManagedPosition) {
	// Get current price from the market the position actually trades on (futures
	// ticker for leveraged positions — see livePriceForPosition).
	currentPrice, err := bot.livePriceForPosition(ctx, pos)
	if err != nil {
		bot.log.WithError(err).Warn("Failed to get ticker for position monitoring")
		return
	}

	// Update current price in position
	pos.CurrentPrice = currentPrice

	// BOT-03 fix: liquidation-distance check for leveraged (futures) positions.
	// Previously EvaluateLiquidationRisk had zero real call sites, so a
	// position up to 20x leverage could ride all the way to forced exchange
	// liquidation with no guard acting ahead of (or in place of) the SL. Runs
	// before the SL/TP/trailing logic below so a critical proximity closes
	// the position immediately rather than waiting for the next SL check.
	if bot.checkLiquidationGuard(ctx, pos, currentPrice) {
		// Position was closed by the guard — nothing left to monitor.
		return
	}

	// Calculate PnL — normalize side to uppercase to handle both "buy" and "BUY"
	side := strings.ToUpper(pos.Side)
	var pnl float64
	var pnlPct float64

	// Realized PnL = price move × size. Leverage must NOT multiply realized PnL —
	// it only sets the margin requirement. Multiplying by leverage here inflated
	// every closed trade ×leverage (e.g. HYPE +$6.125 for a +0.28% move that was
	// really ~+$1.2), corrupting trade_history, daily-loss tracking, Kelly and the
	// feedback loop. This now matches the exchange-close handler (price move × size).
	if side == "BUY" {
		pnlPct = ((currentPrice - pos.EntryPrice) / pos.EntryPrice) * 100
	} else {
		pnlPct = ((pos.EntryPrice - currentPrice) / pos.EntryPrice) * 100
	}
	// Realized PnL = fractional move × USD notional. pos.Size is the USD target for
	// bot-opened positions (and contracts for adopted ones), so multiplying the raw
	// price move by it inflated PnL by price × contract-multiplier. NotionalUSD is
	// dimensionally correct; fall back to the legacy formula only when it's unset.
	if pos.NotionalUSD > 0 {
		pnl = (pnlPct / 100) * pos.NotionalUSD
	} else if side == "BUY" {
		pnl = (currentPrice - pos.EntryPrice) * pos.Size
	} else {
		pnl = (pos.EntryPrice - currentPrice) * pos.Size
	}

	// Source-of-truth PnL: prefer the exchange's own UnrealizedPnL, which KuCoin
	// computes with the correct per-pair contract multiplier. Our size-based
	// estimate above is ambiguous (Size is USD notional for bot-opened positions
	// but contracts for adopted ones), so it's only a fallback. pnlPct stays as the
	// pure price move — that's what SL/TP/partial/MFE triggers compare against.
	if exPnL, ok := bot.exchangeUnrealizedPnL(ctx, pos); ok {
		pnl = exPnL
	}

	// Active Profit Engine — continuation gate (Phase 1). Assessed once per tick
	// BEFORE the protectors so it can loosen the trailing stop, defer partials,
	// re-target the TP and veto soft profit-taking exits when the trend is healthy
	// enough to run. No effect when disabled or in shadow mode.
	contGate := bot.assessContinuation(ctx, pos, pnlPct)
	continuationConfirmed := contGate.confirmed

	// Regime × volatility-tier exit profile (Phase 4): tunes the trailing distance
	// and the MFE keep-fraction by market condition. Inactive (no overrides) when
	// bot.exit.profiles.enabled is false.
	exitProf := bot.resolveExitProfile(ctx, pos)

	// Check and apply breakeven if configured
	if err := bot.checkAndApplyBreakeven(ctx, pos); err != nil {
		bot.log.WithError(err).WithField("position_id", pos.ID).Debug("Breakeven check failed")
	}

	// Retroactively enable trailing for positions that were opened before the
	// config had it enabled (e.g. after a hot-reload reset TrailingConfig).
	if !pos.TrailingStopEnabled {
		if err := bot.enableTrailingStop(ctx, pos); err != nil {
			bot.log.WithError(err).WithField("position_id", pos.ID).Debug("enableTrailingStop (retro) failed")
		}
	}

	// Update trailing stop if enabled. The continuation gate and the volatility
	// tier may loosen the trail (widenFactor × tier multiplier); the regime
	// profile may override the base distance.
	if err := bot.updateTrailingStop(ctx, pos, currentPrice, contGate.trailingWiden()*exitProf.trailingMult, exitProf.trailingDistance); err != nil {
		bot.log.WithError(err).WithField("position_id", pos.ID).Debug("Trailing stop update failed")
	}

	// Re-target the TP further out when the trend is healthy and price nears the
	// TP (the only post-entry writer of pos.TakeProfit).
	bot.extendTakeProfit(ctx, pos, currentPrice, contGate)

	// Partial take-profits: fire any reached scale-out level before checking
	// the final SL/TP. checkPartialTakeProfits mutates pos.Size on success
	// and may move pos.StopLoss to breakeven. Deferred while continuation is
	// confirmed (hold full size in a healthy trend).
	if !(continuationConfirmed && contGate.deferPartials) {
		bot.checkPartialTakeProfits(ctx, pos, currentPrice, pnlPct)
	}

	// ── MFE (Maximum Favorable Excursion) tracking ────────────────────────────
	// Update peak profit seen so the drawdown guard can reference it.
	if pnlPct > pos.PeakPnlPct {
		pos.PeakPnlPct = pnlPct
	}

	// ── MFE profit ratchet ──────────────────────────────────────────────────────
	// Lock in a FRACTION of peak profit once the position is meaningfully green.
	// The old guard used one threshold for both arming and giveback, so a +2.6%
	// peak was allowed to retrace the full 1.5% and exit at +0.6% — "didn't take
	// profit". The ratchet instead keeps ≥ keepFraction of the peak: a +2.6% peak
	// exits at ~+1.7% (keep 65%), a +1.0% peak at ~+0.65%. Tunable via:
	//   exit.mfe.arm_pct       — peak profit % at which the ratchet engages
	//   exit.mfe.keep_fraction — fraction of peak profit to protect [0,1)
	// A small absolute floor (exit.mfe.min_giveback_pct) avoids exiting on noise
	// right at the arming level.
	mfeArmPct := 0.6
	mfeKeepFraction := 0.80 // keep 80% of peak (was 65%): a +3% peak exits ~+2.4%
	mfeMinGiveback := 0.20
	mfeHardFloorEnabled := false // un-vetoable ratchet once peak ≥ hard_floor_min_peak_pct
	mfeHardFloorMinPeak := 1.0
	if mfeSnap := dynamic.SnapshotFromContext(ctx); mfeSnap != nil {
		if v := mfeSnap.Float("exit.mfe.arm_pct"); v > 0 {
			mfeArmPct = v
		}
		if v := mfeSnap.Float("exit.mfe.keep_fraction"); v > 0 && v < 1 {
			mfeKeepFraction = v
		}
		if v := mfeSnap.Float("exit.mfe.min_giveback_pct"); v >= 0 {
			mfeMinGiveback = v
		}
		if v, ok := mfeSnap.BoolOk("exit.mfe.hard_floor_enabled"); ok {
			mfeHardFloorEnabled = v
		}
		if v := mfeSnap.Float("exit.mfe.hard_floor_min_peak_pct"); v > 0 {
			mfeHardFloorMinPeak = v
		}
	}
	// Regime profile overrides the MFE keep-fraction by market condition (Phase 4):
	// trending → keep more (let it run), ranging → keep less (bank it sooner).
	if exitProf.active && exitProf.keepFraction > 0 && exitProf.keepFraction < 1 {
		mfeKeepFraction = exitProf.keepFraction
	}
	if mfeArmPct > 0 && pos.PeakPnlPct >= mfeArmPct {
		lockFloor := pos.PeakPnlPct * mfeKeepFraction
		// Never exit on a giveback smaller than the noise floor.
		if pos.PeakPnlPct-lockFloor < mfeMinGiveback {
			lockFloor = pos.PeakPnlPct - mfeMinGiveback
		}
		if pnlPct <= lockFloor {
			// Hard floor: once the peak exceeded hard_floor_min_peak_pct, the
			// ratchet close ignores the continuation gate — a confirmed winner
			// must never round-trip into a loser because the gate voted "hold".
			hardFloor := mfeHardFloorEnabled && pos.PeakPnlPct >= mfeHardFloorMinPeak
			if !continuationConfirmed || hardFloor {
				reason := "mfe_ratchet"
				if hardFloor && continuationConfirmed {
					reason = "mfe_ratchet_hard_floor" // continuation gate overridden
				}
				bot.log.WithFields(logrus.Fields{
					"position_id":     pos.ID,
					"symbol":          pos.Symbol,
					"peak_pnl_pct":    fmt.Sprintf("%.2f%%", pos.PeakPnlPct),
					"current_pnl_pct": fmt.Sprintf("%.2f%%", pnlPct),
					"lock_floor_pct":  fmt.Sprintf("%.2f%%", lockFloor),
					"kept_fraction":   fmt.Sprintf("%.0f%%", mfeKeepFraction*100),
					"hard_floor":      hardFloor,
				}).Info("📉 MFE ratchet triggered — locking in peak profit")
				bot.closePosition(ctx, pos, fmt.Sprintf("%s peak=%.2f%% now=%.2f%% kept=%.0f%%", reason, pos.PeakPnlPct, pnlPct, mfeKeepFraction*100), pnl)
				return
			}
		}
	}

	// ── Active Profit Engine — exit-decision fusion (Phase 3) ─────────────────
	// When enabled, fuse rule+regime+(ml/llm) into one signed decision. Shadow by
	// default: logs the decision without acting. Live (!shadow) it GOVERNS the
	// soft-exit zone (the legacy cascade below is skipped). Hard protectors above
	// are untouched. Only evaluated in profit and when not already holding a runner.
	fusionGoverns := false
	if fcfg := dynamic.SnapshotFromContext(ctx); fcfg != nil && fcfg.Bool("bot.exit.fusion.enabled") && pnlPct > 0 && !continuationConfirmed {
		shadow := true
		if v, ok := fcfg.BoolOk("bot.exit.fusion.shadow_mode"); ok {
			shadow = v
		}
		dec := bot.fuseExitDecision(ctx, pos, currentPrice, pnlPct, bot.currentRegimeLabel())
		tag := ""
		if shadow {
			tag = " [SHADOW]"
		}
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"symbol":      pos.Symbol,
			"action":      dec.Action,
			"score":       fmt.Sprintf("%.2f", dec.Score),
			"sources":     dec.NonAbstain,
			"votes":       dec.Votes,
			"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
		}).Info("⚖️ Exit fusion decision" + tag)
		if !shadow {
			fusionGoverns = true // the fusion now governs; skip the legacy cascade
			switch dec.Action {
			case mgmtClose:
				bot.closePosition(ctx, pos, "fusion:close "+dec.Reason, pnl)
				return
			case mgmtTighten, mgmtTakeProfit:
				bot.tightenStopLoss(pos, currentPrice)
			}
		}
	}

	// Human-like opportunistic exit: close early on RSI/MACD reversal,
	// volume death, or profit-lock at overbought. Only fires when the
	// position is in profit — losing positions stay under SL management.
	// Vetoed while the continuation gate confirms a healthy trend (hold the
	// runner; the hard trailing/SL still protect the floor) and while the
	// exit-decision fusion governs (Phase 3, live mode).
	if pnlPct > 0 && !continuationConfirmed && !fusionGoverns {
		// Exit-confirmation ladder (Phase 2): require the finer TF to confirm the
		// reversal before acting on a deterministic soft exit (anti-whipsaw).
		// Returns true (no gating) when the ladder is disabled or unresolvable.
		ladderConfirms := bot.exitLadderConfirmsReversal(ctx, pos)

		// Layer 1 — wick reversal (deterministic, sub-ms, no LLM).
		// Exits immediately when candle wick geometry signals buyer/seller
		// absorption before the LLM rate-limit window expires.
		if wickClose, wickReason := bot.detectWickReversal(pos, pnlPct); wickClose && ladderConfirms {
			bot.log.WithFields(logrus.Fields{
				"position_id": pos.ID,
				"symbol":      pos.Symbol,
				"reason":      wickReason,
				"pnl":         fmt.Sprintf("$%.4f", pnl),
				"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
			}).Info("🕯️ Wick reversal exit — locking profit before reversal")
			bot.closePosition(ctx, pos, "wick:"+wickReason, pnl)
			return
		}

		if shouldExit, reason := bot.evaluateConditionExit(ctx, pos); shouldExit && ladderConfirms {
			bot.log.WithFields(logrus.Fields{
				"position_id": pos.ID,
				"symbol":      pos.Symbol,
				"reason":      reason,
				"pnl":         fmt.Sprintf("$%.2f", pnl),
				"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
			}).Info("🧠 Human-like exit triggered — locking in profit")
			bot.closePosition(ctx, pos, "condition:"+reason, pnl)
			return
		}

		// AI re-consultation (Layer 2): every 5 min while in profit, ask the
		// LLM whether to hold, tighten, partial-close, or exit. Rate-limited
		// and confidence-gated inside consultAIForExit.
		if aiAction, ok := bot.consultAIForExit(ctx, pos, currentPrice, pnlPct); ok && aiAction != nil {
			switch aiAction.Action {
			case "close_all":
				bot.log.WithField("reasoning", aiAction.Reasoning).Info("🤖 AI says CLOSE_ALL — exiting position")
				bot.closePosition(ctx, pos, fmt.Sprintf("ai:close_all conf=%.2f", aiAction.Confidence), pnl)
				return
			case "close_partial":
				// llm.exit_advisor.partial_close_fraction (default 0.5) — hot-reloadable
				partialFrac := 0.5
				if pcSnap := dynamic.SnapshotFromContext(ctx); pcSnap != nil {
					if v := pcSnap.Float("llm.exit_advisor.partial_close_fraction"); v > 0 {
						partialFrac = v
					}
				}
				half := pos.Size * partialFrac
				if half > 0 {
					bot.log.WithField("reasoning", aiAction.Reasoning).Info("🤖 AI says CLOSE_PARTIAL — closing partial position")
					if err := bot.executePartialClose(ctx, pos, half); err != nil {
						bot.log.WithError(err).Warn("AI partial close failed — falling through to SL/TP check")
					}
				}
			case "tighten_stop":
				bot.tightenStopLoss(pos, currentPrice)
			}
		}
	}

	// ── Signal-driven exit (Strategies 1–3) ──────────────────────────────────
	// Applies BEFORE the static SL/TP check so the AI can exit intelligently
	// rather than waiting for the exchange to hit stop loss.
	if exitNow, exitReason := bot.checkSignalDrivenExit(ctx, pos, currentPrice, pnlPct); exitNow {
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"symbol":      pos.Symbol,
			"reason":      exitReason,
			"pnl":         fmt.Sprintf("$%.4f", pnl),
			"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
		}).Info("🧠 Signal-driven exit triggered — closing before SL is hit")
		bot.closePosition(ctx, pos, exitReason, pnl)
		return
	}

	// Check Stop Loss (use normalized side — accepts BUY/LONG and SELL/SHORT).
	// Exchange-synced positions use long/short; bot-opened use buy/sell.
	isLong := side == "BUY" || side == "LONG"
	isShort := side == "SELL" || side == "SHORT"
	shouldClose := false
	closeReason := ""

	if isLong && currentPrice <= pos.StopLoss {
		shouldClose = true
		closeReason = "stop_loss"
		bot.log.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"current_price": currentPrice,
			"stop_loss":     pos.StopLoss,
		}).Warn("🛑 Stop Loss hit!")
	} else if isShort && currentPrice >= pos.StopLoss {
		shouldClose = true
		closeReason = "stop_loss"
		bot.log.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"current_price": currentPrice,
			"stop_loss":     pos.StopLoss,
		}).Warn("🛑 Stop Loss hit!")
	}

	// Check Take Profit (use normalized side)
	if isLong && currentPrice >= pos.TakeProfit {
		shouldClose = true
		closeReason = "take_profit"
		bot.log.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"current_price": currentPrice,
			"take_profit":   pos.TakeProfit,
			"pnl":           fmt.Sprintf("$%.2f", pnl),
		}).Info("🎯 Take Profit hit!")
	} else if isShort && currentPrice <= pos.TakeProfit {
		shouldClose = true
		closeReason = "take_profit"
		bot.log.WithFields(logrus.Fields{
			"position_id":   pos.ID,
			"current_price": currentPrice,
			"take_profit":   pos.TakeProfit,
			"pnl":           fmt.Sprintf("$%.2f", pnl),
		}).Info("🎯 Take Profit hit!")
	}

	if !shouldClose && bot.config.MaxPositionDurationHours > 0 {
		maxDuration := time.Duration(bot.config.MaxPositionDurationHours * float64(time.Hour))
		if time.Since(pos.OpenedAt) > maxDuration {
			shouldClose = true
			closeReason = "max_duration_exceeded"
			bot.log.WithFields(logrus.Fields{
				"pair":          pos.Symbol,
				"open_duration": time.Since(pos.OpenedAt).Round(time.Minute),
				"max_hours":     bot.config.MaxPositionDurationHours,
			}).Warn("⏰ Position exceeded max duration — closing")
		}
	}

	if shouldClose {
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID,
			"reason":      closeReason,
			"pnl":         fmt.Sprintf("$%.2f", pnl),
			"pnl_pct":     fmt.Sprintf("%.2f%%", pnlPct),
		}).Info("🎯 Closing position")

		// Apply post-SL cooldown so the next analysis loop doesn't immediately
		// reopen the same direction at almost the same entry. See pairCooldowns.
		if closeReason == "stop_loss" {
			bot.setPairCooldown(ctx, pos.Symbol)
		}

		bot.closePosition(ctx, pos, closeReason, pnl)
	}
}

// closePosition closes a position
func (bot *AITradingBot) closePosition(ctx context.Context, pos *ai.ManagedPosition, reason string, pnl float64) {
	// Atomic guard: prevent two monitoring ticks (or an exchange-side close racing
	// a bot-initiated close) from issuing duplicate close orders for the same
	// position. The first caller proceeds; concurrent callers bail out.
	if !pos.BeginClose() {
		bot.log.WithField("position_id", pos.ID).Debug("⏭️  Close already in progress — skipping duplicate")
		return
	}

	bot.log.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"symbol":      pos.Symbol,
		"side":        pos.Side,
		"reason":      reason,
		"pnl":         pnl,
	}).Info("🔴 Closing position...")

	// Execute close order via GCT
	if !bot.config.DryRun {
		err := bot.executeCloseOrder(ctx, pos)
		if err != nil {
			bot.log.WithError(err).Error("❌ Failed to execute close order — keeping position in manager for retry on next cycle")
			bot.incrementErrorCount()
			pos.EndClose() // release the guard so the next monitoring tick can retry
			return         // Keep the position in manager so the next monitoring tick retries
		}
		bot.log.Info("✅ Close order executed successfully")
		// Callers that could not read the exchange PnL pass 0 (e.g. the bulk
		// position fetch missed the position); use the figure the exchange
		// reported at close time so trade_history does not store NULL.
		if pnl == 0 {
			if realized, ok := bot.takeClosePnL(pos.ID); ok {
				pnl = realized
			}
		}
	} else {
		bot.log.Info("🔵 DRY-RUN: Would close position")
	}

	// Remove from manager only on successful close (or dry-run)
	bot.positionManager.RemovePosition(pos.ID)

	// Cancel the exchange stop-loss order if the position closed for a reason
	// other than stop_loss (e.g. TP or manual close). On a SL hit, KuCoin already
	// executed the stop order so there is nothing to cancel.
	if reason != "stop_loss" && !bot.config.DryRun {
		// BOT-08: serialize against a concurrent reissueExchangeStopLoss for
		// this same position (e.g. an in-flight breakeven/tightening call).
		go func() {
			pos.Lock()
			defer pos.Unlock()
			bot.cancelExchangeStopLoss(ctx, pos)
		}()
	}

	// Update daily loss — only track losses for bot-originated positions.
	// SYNC positions (opened before this session) use a reconstructed entry price
	// that may not match the real exchange entry, so their PnL is unreliable.
	isSyncPosition := strings.HasPrefix(pos.ID, "SYNC-")
	closedPair := pos.Symbol
	if closedPair == "" {
		closedPair = bot.config.Pair
	}

	if !isSyncPosition {
		bot.mu.Lock()
		if pnl < 0 {
			bot.dailyLoss += pnl
			bot.consecutiveLosses++
		} else {
			bot.consecutiveLosses = 0
		}
		threshold := bot.autoPauseMaxConsecutiveLosses

		// Per-pair consecutive-loss tracking (drives trigger 1). Increment the
		// pair that actually closed; a win on that pair clears its own streak.
		if bot.consecutiveLossesByPair == nil {
			bot.consecutiveLossesByPair = make(map[string]int)
		}
		if pnl < 0 {
			bot.consecutiveLossesByPair[closedPair]++
		} else {
			delete(bot.consecutiveLossesByPair, closedPair)
		}
		pairConsecutive := bot.consecutiveLossesByPair[closedPair]

		// Track net PnL per pair for session-level auto-pause.
		if bot.pairSessionPnL == nil {
			bot.pairSessionPnL = make(map[string]float64)
		}
		bot.pairSessionPnL[closedPair] += pnl
		pairNetPnL := bot.pairSessionPnL[closedPair]
		bot.mu.Unlock()

		// Resolve consecutive-loss threshold.
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			if n := snap.Int("pair.auto_pause.max_consecutive_losses"); n > 0 {
				threshold = n
			}
		}
		if threshold <= 0 {
			threshold = 3
		}

		// Auto-pause trigger 1: N consecutive losses ON THE SAME PAIR. Uses the
		// per-pair counter so a pair that loses N times in a row is paused even
		// when other pairs win in between, and blacklists the pair that actually
		// lost (closedPair) — not whatever bot.config.Pair happens to be now.
		if pnl < 0 && threshold > 0 && pairConsecutive >= threshold && closedPair != "" {
			pauseReason := fmt.Sprintf("auto-pause: %d consecutive losses", pairConsecutive)
			bot.blacklistPairToxic(closedPair, pauseReason)
			bot.mu.Lock()
			delete(bot.consecutiveLossesByPair, closedPair)
			bot.mu.Unlock()
			if bot.notificationManager != nil {
				msg := fmt.Sprintf("Pair %s auto-paused after %d consecutive losses", closedPair, pairConsecutive)
				go func(m string) {
					_ = bot.notificationManager.NotifyTradeWithAI(ctx, map[string]interface{}{
						"event":  "auto_pause",
						"pair":   closedPair,
						"losses": pairConsecutive,
					}, func(_ context.Context, _ string) (string, error) { return m, nil })
				}(msg)
			}
		}

		// Auto-pause trigger 2: session net PnL for this pair crosses the
		// relative threshold (bot.pair_pause.net_loss_pct of the day-start
		// capital; -0.15 USD in absolute mode). Catches pairs that alternate wins
		// and losses but remain net negative (NEAR pattern: wins reset the
		// consecutive counter but PnL stays red).
		pairNetLossThreshold := bot.pairPauseThreshold(ctx)
		if pairNetPnL <= pairNetLossThreshold && closedPair != "" {
			pauseReason := fmt.Sprintf("auto-pause: net session PnL $%.4f for %s", pairNetPnL, closedPair)
			bot.log.WithFields(logrus.Fields{
				"pair":        closedPair,
				"net_session": pairNetPnL,
				"threshold":   pairNetLossThreshold,
			}).Warn("🚫 Auto-pausing pair: negative session PnL threshold crossed")
			bot.blacklistPairToxic(closedPair, pauseReason)
			// Reset so the pair can re-enter next session if it recovers.
			bot.mu.Lock()
			bot.pairSessionPnL[closedPair] = 0
			bot.mu.Unlock()
		}
	}

	go bot.RecordTrade(pnl)
	go bot.UpdatePositionMetrics()
	go bot.persistClosedTrade(pos, reason, pnl)

	// Honor the operator's re-entry policy (auto/cooldown/manual) after a
	// bot-initiated close too, so a "manual" hold pauses the pair regardless of
	// which side closed it.
	bot.applyReentryPolicy(ctx, pos.Symbol)

	// Loss cooldown: any losing close gets a re-entry hold, independent of
	// reentry.mode. applyReentryPolicy only enforces the long cooldown in
	// mode="cooldown" (default is "auto"), so without this a loss in auto mode
	// only got the short post-SL window — letting the bot churn the same pair
	// (WLD: 5 losing re-entries ~6min apart). setPairCooldownFor is extend-only
	// so it never shortens an existing longer hold. Default 15min, 0 disables.
	if pnl < 0 && pos.Symbol != "" {
		lossCooldownMin := 15
		if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
			if n, ok := snap.IntOk("bot.loss_cooldown_minutes"); ok {
				lossCooldownMin = n
			}
		}
		if lossCooldownMin > 0 {
			bot.setPairCooldownFor(pos.Symbol, time.Duration(lossCooldownMin)*time.Minute, "loss cooldown")
		}
	}

	// Post-trade learning: register outcome with feedback loop and pattern library.
	if bot.feedbackLoop != nil || bot.patternLibrary != nil || bot.wickLearner != nil {
		won := pnl > 0
		side := strings.ToUpper(pos.Side)
		go func(p *ai.ManagedPosition, closePnl float64, closeReason string, won bool) {
			// Extract wick pattern from reason (e.g. "wick:buyer_absorption").
			wickPattern := ""
			if strings.HasPrefix(closeReason, "wick:") {
				wickPattern = strings.TrimPrefix(closeReason, "wick:")
			}

			if bot.feedbackLoop != nil {
				outcome := &learning.TradeOutcome{
					TradeID:     p.ID,
					Pair:        p.Symbol,
					Side:        side,
					EntryPrice:  p.EntryPrice,
					ExitPrice:   p.CurrentPrice,
					Size:        p.Size,
					EntryTime:   p.OpenedAt,
					ExitTime:    time.Now(),
					PnLAbsolute: closePnl,
					// Pure price move — not derived from closePnl (which may be the
					// exchange's contract-aware PnL). side is uppercased above.
					PnLPercent: func() float64 {
						if p.EntryPrice <= 0 {
							return 0
						}
						if side == "BUY" || side == "LONG" {
							return (p.CurrentPrice - p.EntryPrice) / p.EntryPrice * 100
						}
						return (p.EntryPrice - p.CurrentPrice) / p.EntryPrice * 100
					}(),
					Won:         won,
					ExitReason:  closeReason,
					WickPattern: wickPattern,
					StopLoss:    p.StopLoss,
					TakeProfit:  p.TakeProfit,
				}
				bot.feedbackLoop.RegisterOutcome(outcome)
			}
			if bot.patternLibrary != nil {
				patOutcome := &patterns.PatternOutcome{
					Won: won,
				}
				bot.patternLibrary.RecordOutcome(patOutcome)
			}
			// Layer 3: queue wick exits for deferred outcome verification.
			if bot.wickLearner != nil && wickPattern != "" {
				bot.wickLearner.RecordExit(p.Symbol, wickPattern, side, p.CurrentPrice)
			}
		}(pos, pnl, reason, won)
	}

	bot.log.WithFields(logrus.Fields{
		"position_id": pos.ID,
		"reason":      reason,
		"pnl":         pnl,
		"daily_loss":  bot.dailyLoss,
	}).Info("✅ Position closed")

	// Analyze with LLM-enhanced learning system if available
	if bot.learningSystem != nil && bot.llmAnalyzer != nil {
		go bot.analyzeClosedPositionWithLLM(ctx, pos, reason, pnl)
	}

	// Send AI-enhanced position closed notification
	bot.sendPositionClosedNotificationWithAI(ctx, pos, reason, pnl)

	// Trigger post-close analysis
	go bot.onPositionClosed(ctx, pos, reason, pnl)
}

// persistClosedTrade writes a closed position to the durable trade_history sink
// so win-rate / profit-factor analytics and Master Agent feedback have real
// data. No-op when no writer is wired. Runs in its own goroutine — failures are
// logged but never block the trading loop.
func (bot *AITradingBot) persistClosedTrade(pos *ai.ManagedPosition, reason string, pnl float64) {
	// Release the cross-instance ownership slot the moment this position closes so
	// the pair can be opened/adopted again. Done before the writer guard so the slot
	// frees even when trade history is not configured. No-op if pos doesn't own it.
	if pos != nil {
		releasePositionOwnership(pos.Symbol, pos.Side, pos.ID)
	}

	bot.mu.RLock()
	writer := bot.tradeHistoryWriter
	bot.mu.RUnlock()
	if writer == nil || pos == nil {
		return
	}

	// pnlPct is the pure price move (NOT derived from pnl, which may now come from
	// the exchange's contract-multiplier-aware UnrealizedPnL and wouldn't divide
	// cleanly by entry×size). This keeps pnl_pct comparable to SL/TP thresholds.
	pnlPct := 0.0
	if pos.EntryPrice > 0 {
		if strings.ToUpper(pos.Side) == "BUY" || strings.ToUpper(pos.Side) == "LONG" {
			pnlPct = (pos.CurrentPrice - pos.EntryPrice) / pos.EntryPrice * 100
		} else {
			pnlPct = (pos.EntryPrice - pos.CurrentPrice) / pos.EntryPrice * 100
		}
	}

	exchange := pos.Exchange
	if exchange == "" {
		exchange = bot.config.Exchange
	}

	// Fee recording: pnl is the exchange's real (contract-multiplier-aware) USD
	// PnL and pnlPct is the pure price move, so the real USD notional traded is
	// |pnl| / (|pnlPct|/100). This matched the ledger to the cent on audit. We
	// charge a round-trip taker fee (entry 0.06% + exit 0.06% = 0.12%) on that
	// notional — slightly conservative when an exit fills as maker. When the
	// move is sub-2bps the notional is indeterminate from pnl, so we record zero
	// fee for that trade (its real fee is negligible) rather than fabricate one.
	// Previously fees were recorded as 0 and net_pnl == gross on every row, making
	// all performance numbers optimistic by ~0.12-0.17% round-trip.
	const roundTripFeeRate = 0.0012
	fees := 0.0
	if math.Abs(pnlPct) >= 0.02 {
		notional := math.Abs(pnl) / (math.Abs(pnlPct) / 100.0)
		fees = notional * roundTripFeeRate
	}
	netPnL := pnl - fees

	trade := &ClosedTrade{
		ID:             pos.ID,
		Pair:           pos.Symbol,
		Exchange:       exchange,
		Side:           pos.Side,
		EntryPrice:     pos.EntryPrice,
		ExitPrice:      pos.CurrentPrice,
		Size:           pos.Size,
		Leverage:       pos.Leverage,
		StopLoss:       pos.StopLoss,
		TakeProfit:     pos.TakeProfit,
		PnL:            pnl,
		PnLPct:         pnlPct,
		Fees:           fees,
		NetPnL:         netPnL,
		Confidence:     pos.Confidence,
		DecisionReason: pos.DecisionReason,
		ExitReason:     reason,
		EntryTime:      pos.OpenedAt,
		ExitTime:       time.Now(),
	}

	if err := writer.SaveClosedTrade(bot.effectiveUserID(), trade); err != nil {
		bot.log.WithError(err).WithField("trade_id", pos.ID).
			Warn("Failed to persist closed trade to trade_history")
		return
	}
	bot.log.WithFields(logrus.Fields{
		"trade_id": pos.ID,
		"pair":     pos.Symbol,
		"pnl":      pnl,
	}).Debug("📝 Closed trade persisted to trade_history")

	// P3.1 — label the trade in feature_store with real outcome.
	bot.mu.RLock()
	labeler := bot.feedbackLabeler
	bot.mu.RUnlock()
	if labeler != nil {
		go labeler.Label(context.Background(), &feedbackOutcome{
			Pair:          pos.Symbol,
			Exchange:      exchange,
			Side:          pos.Side,
			EntryPrice:    pos.EntryPrice,
			ExitPrice:     pos.CurrentPrice,
			EntryTime:     pos.OpenedAt,
			ExitTime:      time.Now(),
			PnLPct:        pnlPct,
			ExitReason:    reason,
			Confidence:    pos.Confidence,
			Regime:        pos.EntryRegime,
			MLProb:        pos.MLProb,
			TechnicalVote: pos.TechnicalVote,
			LLMVote:       pos.LLMVote,
			TechnicalConf: pos.TechnicalConf,
			LLMConf:       pos.LLMConf,
		})
	}
}

// executeCloseOrder executes the actual close order via GCT
func (bot *AITradingBot) executeCloseOrder(ctx context.Context, pos *ai.ManagedPosition) error {
	if bot.gctClient == nil {
		return fmt.Errorf("GCT client not initialized")
	}

	// For futures, we need to close the position by placing an opposite order
	// If we have a LONG position, we SELL to close
	// If we have a SHORT position, we BUY to close

	// Normalize side to lowercase for comparison (handles "buy", "BUY", "long", "LONG")
	closeSide := "sell"
	normalizedSide := strings.ToLower(pos.Side)
	if normalizedSide == "short" || normalizedSide == "sell" {
		closeSide = "buy"
	}

	bot.log.WithFields(logrus.Fields{
		"symbol": pos.Symbol,
		"side":   closeSide,
		"size":   pos.Size,
		"entry":  pos.EntryPrice,
	}).Info("Executing close order...")

	// Get current positions to verify
	positions, err := bot.gctClient.GetFuturesPositions(ctx, bot.config.Exchange)
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// Find the position. Exchange positions use KuCoin Futures format (BTCUSDTM)
	// while pos.Symbol uses dash format (BTC-USDT); normalize for comparison.
	normalizedSymbol := dashPairToKuCoinFutures(pos.Symbol)
	var foundPosition *gct.Position
	for _, p := range positions {
		if p.Symbol == normalizedSymbol || p.Symbol == pos.Symbol {
			foundPosition = p
			break
		}
	}

	if foundPosition == nil {
		bot.log.Warn("Position not found in exchange, may already be closed")
		return nil
	}

	// Close the position using GCT. KuCoin Futures requires BTCUSDTM format.
	err = bot.gctClient.CloseFuturesPosition(ctx, bot.config.Exchange, normalizedSymbol)
	if err != nil {
		return fmt.Errorf("failed to close position: %w", err)
	}

	bot.log.WithFields(logrus.Fields{
		"symbol": pos.Symbol,
		"pnl":    foundPosition.UnrealizedPnL,
	}).Info("✅ Position closed on exchange")
	bot.rememberClosePnL(pos.ID, foundPosition.UnrealizedPnL)

	return nil
}

// resultProcessor processes execution results
func (bot *AITradingBot) resultProcessor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-bot.stopChan:
			return
		case result := <-bot.resultChan:
			bot.processResult(result)
		}
	}
}

// processResult processes a single execution result
func (bot *AITradingBot) processResult(result *ExecutionResult) {
	bot.manual.deliver(result)
	// BOT-07: resultProcessor's loop calls this once per message with no
	// recover of its own (mirrors analysisLoop delegating recovery to
	// runAnalysis) — a panic here must not propagate past this call, or it
	// kills resultProcessor's goroutine for good (and, unrecovered anywhere,
	// the whole process — every user's bot, not just this one).
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in processResult - recovered")
		}
	}()

	if result.Success {
		bot.log.WithFields(logrus.Fields{
			"action":   result.Action,
			"order_id": result.OrderID,
		}).Info("✅ Execution result: SUCCESS")
	} else {
		bot.log.WithFields(logrus.Fields{
			"action": result.Action,
			"error":  result.Error,
		}).Error("❌ Execution result: FAILED")

		// When another bot already holds a same-direction position on this pair,
		// blacklist the pair so the dynamic selector picks a different one next cycle.
		if result.Error != nil && strings.Contains(result.Error.Error(), "same-direction position already open") {
			bot.mu.RLock()
			pair := bot.config.Pair
			bot.mu.RUnlock()
			if pair != "" {
				bot.blacklistPair(pair, "scalping bot already holds same-direction position")
			}
		}
	}
}

// checkSafetyLimits checks if trading is allowed based on safety limits
func (bot *AITradingBot) checkSafetyLimits() bool {
	// Resolved before taking bot.mu: it may read the (cached) account capital.
	dailyLossLimit := bot.effectiveDailyLossLimit(context.Background())

	bot.mu.RLock()
	defer bot.mu.RUnlock()

	// Check emergency stop
	if bot.emergencyStop {
		return false
	}

	// Check daily loss limit
	if dailyLossLimit > 0 && bot.dailyLoss <= -dailyLossLimit {
		bot.log.WithFields(logrus.Fields{
			"daily_loss": bot.dailyLoss,
			"limit":      dailyLossLimit,
		}).Error("🚨 Daily loss limit reached - trading paused")
		return false
	}

	// Check daily trades limit — read from dynamic config so it can be tuned
	// in real time without a restart; falls back to the static config value.
	maxDailyTrades := bot.config.MaxDailyTrades
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if v := snap.Int("bot.max_daily_trades"); v > 0 {
			maxDailyTrades = v
		}
	}
	if bot.dailyTrades >= maxDailyTrades {
		bot.log.WithFields(logrus.Fields{
			"daily_trades": bot.dailyTrades,
			"limit":        maxDailyTrades,
		}).Warn("⚠️  Daily trades limit reached - trading paused")
		return false
	}

	// Check consecutive errors — auto-reset after configurable window (half-open circuit breaker).
	// Uses TryLock to avoid blocking the analysis loop if another goroutine holds the
	// write lock — we'll retry on the next cycle.
	// TC-4.6: bot.autonomous.consec_error_window (default 5m)
	cbResetWindow := 5 * time.Minute
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if d := snap.Duration("bot.autonomous.consec_error_window"); d > 0 {
			cbResetWindow = d
		}
	}
	if bot.consecutiveErrs >= bot.config.MaxConsecutiveErrors {
		if time.Since(bot.lastErrorAt) > cbResetWindow {
			if bot.mu.TryLock() {
				bot.consecutiveErrs = 0
				bot.mu.Unlock()
				bot.log.Infof("🔄 Circuit breaker auto-reset after %s cooldown — retrying analysis", cbResetWindow)
			}
			// If TryLock failed, skip this cycle — reset will retry next analysis tick.
		} else {
			bot.log.WithField("consecutive_errors", bot.consecutiveErrs).Error("🚨 Too many consecutive errors - trading paused")
			return false
		}
	}

	return true
}

// checkPortfolioRisk checks if portfolio risk is within acceptable limits
func (bot *AITradingBot) checkPortfolioRisk(ctx context.Context) bool {
	// Portfolio risk thresholds
	const (
		maxDrawdownPercent     = 10.0  // Max allowed drawdown percentage
		maxExposurePercent     = 100.0 // Max portfolio exposure percentage (increased for scalping)
		maxConcentrationPct    = 100.0 // Max concentration in single asset (increased for single pair trading)
		minAvailableCapitalPct = 20.0  // Minimum free capital percentage
	)

	positions := bot.positionManager.GetAllPositions()

	// If no positions, risk is acceptable
	if len(positions) == 0 {
		return true
	}

	// Classify positions: leveraged futures use a SEPARATE exchange wallet from
	// spot. Comparing futures notional against spot balance produces false alarms.
	// For futures positions the meaningful risk limits are drawdown and unrealized
	// loss — NOT exposure vs spot capital.
	//
	// Additionally, adopted orphan positions always have Leverage=1 (unknown at
	// adoption time), so we also flag futures mode when the bot's configured
	// market type is "futures" or "margin".
	bot.mu.RLock()
	configuredMarketType := bot.config.MarketType
	bot.mu.RUnlock()

	var totalExposure float64
	hasFuturesPositions := configuredMarketType == "futures" || configuredMarketType == "margin"
	exposureByAsset := make(map[string]float64)

	for _, pos := range positions {
		price := pos.CurrentPrice
		if price == 0 {
			price = pos.EntryPrice
		}
		lev := float64(pos.Leverage)
		if lev < 1 {
			lev = 1
		}
		if lev > 1 {
			hasFuturesPositions = true
		}
		positionValue := (pos.Size * price) / lev // margin used
		totalExposure += positionValue

		asset := strings.Split(pos.Symbol, "-")[0]
		if asset == "" {
			asset = pos.Symbol
		}
		exposureByAsset[asset] += positionValue
	}

	// Get available capital (spot wallet; used only for spot-position checks)
	var totalCapital float64 = 10000.0 // Default assumption
	if bot.gctClient != nil {
		portfolio, err := bot.gctClient.GetPortfolio(ctx)
		if err == nil && portfolio != nil {
			// Prefer total portfolio value so drawdown % is relative to the full
			// account, not just free USDT (which shrinks when margin is locked).
			if portfolio.TotalValue > 0 {
				totalCapital = portfolio.TotalValue
			} else {
				for currency, holding := range portfolio.Holdings {
					if currency == "USDT" || currency == "USD" {
						totalCapital = holding.Total
						break
					}
				}
			}
		}
	}

	riskViolations := []string{}

	// 1. Check total exposure percentage — skip for futures (separate wallet).
	if !hasFuturesPositions && totalCapital > 0 {
		exposurePercent := (totalExposure / totalCapital) * 100
		if exposurePercent > maxExposurePercent {
			riskViolations = append(riskViolations,
				fmt.Sprintf("Total exposure %.1f%% exceeds max %.1f%%", exposurePercent, maxExposurePercent))
		}

		// Check available capital
		availablePercent := ((totalCapital - totalExposure) / totalCapital) * 100
		if availablePercent < minAvailableCapitalPct {
			riskViolations = append(riskViolations,
				fmt.Sprintf("Available capital %.1f%% below minimum %.1f%%", availablePercent, minAvailableCapitalPct))
		}
	}

	// 2. Check concentration risk
	for asset, exposure := range exposureByAsset {
		if totalExposure > 0 {
			concentrationPct := (exposure / totalExposure) * 100
			if concentrationPct > maxConcentrationPct {
				riskViolations = append(riskViolations,
					fmt.Sprintf("Concentration in %s: %.1f%% exceeds max %.1f%%", asset, concentrationPct, maxConcentrationPct))
			}
		}
	}

	// 3. Check current drawdown
	bot.mu.RLock()
	currentDrawdown := bot.dailyLoss
	bot.mu.RUnlock()

	if totalCapital > 0 {
		drawdownPercent := (math.Abs(currentDrawdown) / totalCapital) * 100
		if drawdownPercent > maxDrawdownPercent {
			riskViolations = append(riskViolations,
				fmt.Sprintf("Current drawdown %.1f%% exceeds max %.1f%%", drawdownPercent, maxDrawdownPercent))
		}
	}

	// 4. Check unrealized P&L across positions — spot only.
	// For futures the exchange enforces stop-loss orders independently, and the
	// manual (price-entry)*size formula is wrong for contracts (size is in
	// contract units, not USD). Skip the check entirely for futures accounts to
	// avoid false alarms that block all new entries.
	if !hasFuturesPositions {
		var totalUnrealizedLoss float64
		for _, pos := range positions {
			if pos.CurrentPrice > 0 && pos.EntryPrice > 0 {
				unrealizedPnL := (pos.CurrentPrice - pos.EntryPrice) * pos.Size
				if pos.Side == "sell" || pos.Side == "short" {
					unrealizedPnL = -unrealizedPnL
				}
				if unrealizedPnL < 0 {
					totalUnrealizedLoss += math.Abs(unrealizedPnL)
				}
			}
		}

		if totalCapital > 0 {
			unrealizedLossPct := (totalUnrealizedLoss / totalCapital) * 100
			if unrealizedLossPct > maxDrawdownPercent {
				riskViolations = append(riskViolations,
					fmt.Sprintf("Unrealized loss %.1f%% exceeds max %.1f%%", unrealizedLossPct, maxDrawdownPercent))
			}
		}
	}

	// Log risk violations if any
	if len(riskViolations) > 0 {
		bot.log.WithFields(logrus.Fields{
			"violations":     riskViolations,
			"total_exposure": totalExposure,
			"position_count": len(positions),
		}).Warn("Portfolio risk limits violated - blocking new trades")
		return false
	}

	return true
}

// incrementErrorCount increments error counters and fires the optional onErrorFn
// callback so BotManager's circuit breaker stays in sync with runtime failures.
func (bot *AITradingBot) incrementErrorCount() {
	bot.mu.Lock()
	bot.errorCount++
	bot.consecutiveErrs++
	bot.lastErrorAt = time.Now()
	fn := bot.onErrorFn
	uid := bot.effectiveUserID()
	bot.mu.Unlock()

	// Fire outside the lock to avoid deadlocks (BotManager.RecordFailure takes
	// its own mutex and may attempt to stop this bot).
	if fn != nil {
		fn(uid)
	}
}

// dailyResetTimer resets daily counters at midnight
func (bot *AITradingBot) dailyResetTimer(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-bot.stopChan:
			return
		case <-ticker.C:
			now := time.Now()
			if now.Day() != bot.lastResetDate.Day() {
				bot.resetDailyCounters()
			}
		}
	}
}

// resetDailyCounters resets daily counters
func (bot *AITradingBot) resetDailyCounters() {
	// BOT-07: dailyResetTimer's loop calls this with no recover of its own —
	// a panic here must not propagate past this call (see processResult above
	// for the same reasoning: it would kill the goroutine, and if unrecovered
	// anywhere, the whole process).
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in resetDailyCounters - recovered")
		}
	}()

	bot.mu.Lock()
	defer bot.mu.Unlock()

	bot.log.WithFields(logrus.Fields{
		"daily_loss":   bot.dailyLoss,
		"daily_trades": bot.dailyTrades,
	}).Info("🔄 Resetting daily counters")

	bot.dailyLoss = 0
	bot.dailyTrades = 0
	bot.lastResetDate = time.Now()
	// The relative loss caps re-freeze the capital at the next resolution.
	bot.resetSizingDay()
}

// GetStatus returns current bot status
func (bot *AITradingBot) GetStatus() map[string]interface{} {
	// Effective sizing from cached values only (no exchange call), resolved
	// before bot.mu is taken because it reads the config under its own lock.
	sizing := bot.SizingStatus(context.Background())

	bot.mu.RLock()
	defer bot.mu.RUnlock()

	uptime := time.Duration(0)
	if bot.running {
		uptime = time.Since(bot.startedAt)
	}

	return map[string]interface{}{
		"running":            bot.running,
		"paused":             bot.paused,
		"emergency_stop":     bot.emergencyStop,
		"started_at":         bot.startedAt,
		"uptime_seconds":     int(uptime.Seconds()),
		"last_analysis_at":   bot.lastAnalysisAt,
		"last_error_at":      bot.lastErrorAt,
		"analysis_count":     bot.analysisCount,
		"execution_count":    bot.executionCount,
		"error_count":        bot.errorCount,
		"consecutive_errors": bot.consecutiveErrs,
		"daily_loss":         bot.dailyLoss,
		"daily_trades":       bot.dailyTrades,
		"open_positions":     len(bot.positionManager.GetAllPositions()),
		"config":             bot.config,
		"sizing":             sizing,
	}
}

// GetOpenPositions returns all positions currently tracked by the position manager.
func (bot *AITradingBot) GetOpenPositions() []*ai.ManagedPosition {
	return bot.positionManager.GetAllPositions()
}

// GetAllExchangePositions fetches ALL open futures positions directly from KuCoin,
// regardless of which pair the bot is configured for.
func (bot *AITradingBot) GetAllExchangePositions(ctx context.Context) ([]*gct.Position, error) {
	return bot.gctClient.GetFuturesPositions(ctx, bot.config.Exchange)
}

// GetExchange returns the exchange name the bot is configured for.
func (bot *AITradingBot) GetExchange() string {
	return bot.config.Exchange
}

// UpdateConfig updates bot configuration dynamically
func (bot *AITradingBot) UpdateConfig(updates map[string]interface{}) error {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	// Update dry_run
	if dryRun, ok := updates["dry_run"].(bool); ok {
		bot.config.DryRun = dryRun
		bot.log.WithField("dry_run", dryRun).Info("Updated dry_run mode")
	}

	// Update auto_execute
	if autoExecute, ok := updates["auto_execute"].(bool); ok {
		bot.config.AutoExecute = autoExecute
		bot.log.WithField("auto_execute", autoExecute).Info("Updated auto_execute mode")
	}

	// Update confidence_threshold
	if threshold, ok := updates["confidence_threshold"].(float64); ok {
		if threshold >= 0.5 && threshold <= 1.0 {
			bot.config.ConfidenceThreshold = threshold
			bot.log.WithField("threshold", threshold).Info("Updated confidence threshold")
		} else {
			return fmt.Errorf("confidence_threshold must be between 0.0 and 1.0")
		}
	}

	// Update trade_size
	if tradeSize, ok := updates["trade_size_usd"].(float64); ok {
		if tradeSize > 0 {
			bot.config.TradeSize = tradeSize
			bot.log.WithField("trade_size", tradeSize).Info("Updated trade size")
		} else {
			return fmt.Errorf("trade_size_usd must be positive")
		}
	}

	// Update leverage
	if leverage, ok := updates["leverage"].(float64); ok {
		if leverage >= 1 && leverage <= 100 {
			bot.config.Leverage = int(leverage)
			bot.log.WithField("leverage", leverage).Info("Updated leverage")
		} else {
			return fmt.Errorf("leverage must be between 1 and 100")
		}
	}

	// Update max_daily_loss
	if maxLoss, ok := updates["max_daily_loss_usd"].(float64); ok {
		if maxLoss > 0 {
			bot.config.MaxDailyLoss = maxLoss
			bot.log.WithField("max_daily_loss", maxLoss).Info("Updated max daily loss")
		}
	}

	// Update max_daily_trades
	if maxTrades, ok := updates["max_daily_trades"].(float64); ok {
		if maxTrades > 0 {
			bot.config.MaxDailyTrades = int(maxTrades)
			bot.log.WithField("max_daily_trades", maxTrades).Info("Updated max daily trades")
		}
	}

	// Update pair
	if pair, ok := updates["pair"].(string); ok {
		if pair != "" {
			bot.config.Pair = pair
			bot.log.WithField("pair", pair).Info("Updated trading pair")
		}
	}

	// Direction / macro filter settings
	if v, ok := updates["allow_short_selling"].(bool); ok {
		bot.config.AllowShortSelling = v
		bot.log.WithField("allow_short_selling", v).Info("Updated short selling flag")
	}
	if v, ok := updates["macro_filter_enabled"].(bool); ok {
		bot.config.MacroFilterEnabled = v
		bot.log.WithField("macro_filter_enabled", v).Info("Updated macro filter flag")
	}
	if v, ok := updates["counter_trend_filter_enabled"].(bool); ok {
		bot.config.CounterTrendFilterEnabled = v
		bot.log.WithField("counter_trend_filter_enabled", v).Info("Updated counter-trend filter flag")
	}
	if v, ok := updates["macro_bearish_strength_threshold"].(float64); ok {
		if v >= 0 && v <= 1.0 {
			bot.config.MacroBearishStrengthThreshold = v
			bot.log.WithField("macro_bearish_strength_threshold", v).Info("Updated bearish macro threshold")
		} else {
			return fmt.Errorf("macro_bearish_strength_threshold must be between 0 and 1")
		}
	}
	if v, ok := updates["macro_bullish_strength_threshold"].(float64); ok {
		if v >= 0 && v <= 1.0 {
			bot.config.MacroBullishStrengthThreshold = v
			bot.log.WithField("macro_bullish_strength_threshold", v).Info("Updated bullish macro threshold")
		} else {
			return fmt.Errorf("macro_bullish_strength_threshold must be between 0 and 1")
		}
	}

	// Sync all direction settings to DecisionEngine
	bot.syncDecisionEngineConfig()

	return nil
}

// GetConfig returns current configuration
func (bot *AITradingBot) GetConfig() *TradingBotConfig {
	bot.mu.RLock()
	defer bot.mu.RUnlock()
	return bot.config
}

// SetCostManager sets the cost manager for budget tracking
func (bot *AITradingBot) SetCostManager(costManager llm.CostManager) {
	bot.costManager = costManager

	// Also set it on the LLM analyzer if available
	if bot.llmAnalyzer != nil {
		bot.llmAnalyzer.SetCostManager(costManager)
		bot.log.Info("Cost manager configured for AI Trading Bot and LLM Analyzer")
	} else {
		bot.log.Info("Cost manager configured for AI Trading Bot")
	}
}

// SetLLMAnalyzer sets the LLM analyzer for AI-enhanced trading
// SetPairCoordinator attaches a cross-component pair coordinator.
// When set, StartWithContext claims the bot's configured pair so that
// BotManager scalping bots cannot start on the same pair simultaneously.
func (bot *AITradingBot) SetPairCoordinator(c *coordinator.PairCoordinator) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.pairCoord = c
}

func (bot *AITradingBot) SetLLMAnalyzer(analyzer *llm.Analyzer) {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	bot.llmAnalyzer = analyzer

	// If cost manager is already set, configure it on the analyzer
	if bot.costManager != nil && analyzer != nil {
		analyzer.SetCostManager(bot.costManager)
	}

	bot.log.Info("LLM Analyzer configured for AI Trading Bot")
}

// SetLearningSystem sets the learning system for continuous improvement
func (bot *AITradingBot) SetLearningSystem(learningSystem *ai.SLTPLearningSystem) {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	bot.learningSystem = learningSystem
	bot.log.Info("Learning System configured for AI Trading Bot")
}

// SetNotificationManager sets the notification manager for alerts
func (bot *AITradingBot) SetNotificationManager(notificationManager NotificationManager) {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	bot.notificationManager = notificationManager
	bot.log.Info("Notification Manager configured for AI Trading Bot")
}

// combineLLMAndTechnicalAnalysis intelligently combines LLM and technical analysis
func (bot *AITradingBot) combineLLMAndTechnicalAnalysis(
	technicalDecision *ai.Decision,
	llmAnalysis *llm.LLMAnalysis,
	marketCtx *ai.MarketContext,
) *ai.Decision {
	// TC-4.6: bot.exec.tp_min_distance_pct (default 0.5) — avoids hair-trigger TPs
	tpMinDistancePct := 0.5
	// llm.consensus.confidence_boost (default 1.15) — hot-reloadable via dynamic config
	consensusBoost := 1.15
	// llm.disagreement_penalty (default 0.85) — hot-reloadable via dynamic config
	disagreementPenalty := 0.85
	// llm.wait_penalty (default 0.90) — hot-reloadable via dynamic config
	waitPenalty := 0.90
	// llm.opportunity_min_confidence (default 0.80) — hot-reloadable via dynamic config
	opportunityMinConf := 0.80
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if v := snap.Float("bot.exec.tp_min_distance_pct"); v != 0 {
			tpMinDistancePct = v
		}
		if v := snap.Float("llm.consensus.confidence_boost"); v > 0 {
			consensusBoost = v
		}
		if v := snap.Float("llm.disagreement_penalty"); v > 0 {
			disagreementPenalty = v
		}
		if v := snap.Float("llm.wait_penalty"); v > 0 {
			waitPenalty = v
		}
		if v := snap.Float("llm.opportunity_min_confidence"); v > 0 {
			opportunityMinConf = v
		}
	}

	// Create a new decision based on the combination
	combinedDecision := &ai.Decision{
		Action:     technicalDecision.Action,
		Confidence: technicalDecision.Confidence,
		Reason:     technicalDecision.Reason,
		Entry:      technicalDecision.Entry,
		StopLoss:   technicalDecision.StopLoss,
		TakeProfit: technicalDecision.TakeProfit,
		// Per-source vote attribution captured from the inputs (before the
		// combine branches mutate confidence) so the post-trade labeler records
		// distinct technical vs llm calibration observations (Active Profit Engine F0).
		TechnicalAction:     technicalDecision.Action,
		TechnicalConfidence: technicalDecision.Confidence,
		LLMAction:           llmAnalysis.Action,
		LLMConfidence:       llmAnalysis.Confidence,
	}

	// Strategy 1: Both agree - boost confidence
	if technicalDecision.Action == llmAnalysis.Action {
		// Both agree on the action - increase confidence
		avgConfidence := (technicalDecision.Confidence + llmAnalysis.Confidence) / 2
		boostFactor := consensusBoost // llm.consensus.confidence_boost — dynamic config
		combinedDecision.Confidence = avgConfidence * boostFactor
		if combinedDecision.Confidence > 1.0 {
			combinedDecision.Confidence = 1.0
		}

		combinedDecision.Reason = fmt.Sprintf(
			"✅ CONSENSUS: Technical (%.0f%%) + LLM (%.0f%%) agree on %s. %s | %s",
			technicalDecision.Confidence*100,
			llmAnalysis.Confidence*100,
			llmAnalysis.Action,
			technicalDecision.Reason,
			llmAnalysis.Reasoning,
		)

		bot.log.WithFields(logrus.Fields{
			"action":              combinedDecision.Action,
			"combined_confidence": fmt.Sprintf("%.2f%%", combinedDecision.Confidence*100),
			"boost":               "15%",
		}).Info("✅ Technical + LLM CONSENSUS")

		// Use LLM prices only if distance from current price is >= 0.5% (avoids hair-trigger TPs)
		if llmAnalysis.StopLoss > 0 {
			combinedDecision.StopLoss = llmAnalysis.StopLoss
		}
		if llmAnalysis.TakeProfit > 0 {
			tpDist := math.Abs(llmAnalysis.TakeProfit-marketCtx.CurrentPrice) / marketCtx.CurrentPrice * 100
			if tpDist >= tpMinDistancePct {
				combinedDecision.TakeProfit = llmAnalysis.TakeProfit
			}
		}

		bot.applyMLVote(combinedDecision, marketCtx)
		return combinedDecision
	}

	// Strategy 2: Disagreement - use higher confidence but reduce it
	if technicalDecision.Action != llmAnalysis.Action && llmAnalysis.Action != "WAIT" {
		bot.log.WithFields(logrus.Fields{
			"technical_action": technicalDecision.Action,
			"technical_conf":   fmt.Sprintf("%.0f%%", technicalDecision.Confidence*100),
			"llm_action":       llmAnalysis.Action,
			"llm_conf":         fmt.Sprintf("%.0f%%", llmAnalysis.Confidence*100),
		}).Warn("⚠️ DISAGREEMENT between Technical and LLM")

		// Use the one with higher confidence but apply penalty
		if llmAnalysis.Confidence > technicalDecision.Confidence {
			// LLM has higher confidence
			combinedDecision.Action = llmAnalysis.Action
			combinedDecision.Confidence = llmAnalysis.Confidence * disagreementPenalty // llm.disagreement_penalty — dynamic config
			combinedDecision.Reason = fmt.Sprintf(
				"⚠️ DISAGREEMENT: Using LLM (%.0f%%) over Technical (%.0f%%). LLM: %s | Technical: %s",
				llmAnalysis.Confidence*100,
				technicalDecision.Confidence*100,
				llmAnalysis.Reasoning,
				technicalDecision.Reason,
			)

			// Use LLM prices
			if llmAnalysis.EntryPrice > 0 {
				combinedDecision.Entry = llmAnalysis.EntryPrice
			}
			if llmAnalysis.StopLoss > 0 {
				combinedDecision.StopLoss = llmAnalysis.StopLoss
			}
			if llmAnalysis.TakeProfit > 0 {
				tpDist := math.Abs(llmAnalysis.TakeProfit-marketCtx.CurrentPrice) / marketCtx.CurrentPrice * 100
				if tpDist >= tpMinDistancePct {
					combinedDecision.TakeProfit = llmAnalysis.TakeProfit
				}
			}

			bot.log.Info("Using LLM decision (higher confidence, with penalty)")
		} else {
			// Technical has higher confidence
			combinedDecision.Confidence = technicalDecision.Confidence * disagreementPenalty // llm.disagreement_penalty — dynamic config
			combinedDecision.Reason = fmt.Sprintf(
				"⚠️ DISAGREEMENT: Using Technical (%.0f%%) over LLM (%.0f%%). Technical: %s | LLM: %s",
				technicalDecision.Confidence*100,
				llmAnalysis.Confidence*100,
				technicalDecision.Reason,
				llmAnalysis.Reasoning,
			)

			bot.log.Info("Using Technical decision (higher confidence, with penalty)")
		}

		bot.applyMLVote(combinedDecision, marketCtx)
		return combinedDecision
	}

	// Strategy 3: LLM says WAIT but technical says trade
	if llmAnalysis.Action == "WAIT" && technicalDecision.Action != "WAIT" {
		// If LLM is unavailable (circuit breaker, quota, etc.) don't penalize technical analysis
		if llmAnalysis.Reasoning == "no consensus among LLM providers" {
			combinedDecision.Reason = fmt.Sprintf(
				"📊 TECHNICAL ONLY (LLM unavailable): %s | %s",
				technicalDecision.Action,
				technicalDecision.Reason,
			)
			bot.log.WithField("action", combinedDecision.Action).Info("📊 LLM unavailable — using technical analysis at full confidence")
			return combinedDecision
		}

		// LLM actively advises caution - reduce technical confidence by 10%
		combinedDecision.Confidence = technicalDecision.Confidence * waitPenalty // llm.wait_penalty — dynamic config
		combinedDecision.Reason = fmt.Sprintf(
			"⚠️ LLM CAUTION: Technical suggests %s (%.0f%%) but LLM advises WAIT. %s | LLM: %s",
			technicalDecision.Action,
			technicalDecision.Confidence*100,
			technicalDecision.Reason,
			llmAnalysis.Reasoning,
		)

		bot.log.WithFields(logrus.Fields{
			"action":             combinedDecision.Action,
			"reduced_confidence": fmt.Sprintf("%.2f%%", combinedDecision.Confidence*100),
			"penalty":            "10%",
		}).Warn("⚠️ LLM advises caution - reducing confidence")

		bot.applyMLVote(combinedDecision, marketCtx)
		return combinedDecision
	}

	// Strategy 4: Technical says WAIT, LLM says trade
	if technicalDecision.Action == "WAIT" && llmAnalysis.Action != "WAIT" {
		// Technical is cautious but LLM sees opportunity
		// Only take LLM suggestion if confidence is very high
		if llmAnalysis.Confidence >= opportunityMinConf { // llm.opportunity_min_confidence — dynamic config
			combinedDecision.Action = llmAnalysis.Action
			combinedDecision.Confidence = llmAnalysis.Confidence * waitPenalty // llm.wait_penalty — dynamic config
			combinedDecision.Reason = fmt.Sprintf(
				"🤖 LLM OPPORTUNITY: Technical says WAIT but LLM (%.0f%%) suggests %s. %s",
				llmAnalysis.Confidence*100,
				llmAnalysis.Action,
				llmAnalysis.Reasoning,
			)

			// Use LLM prices
			if llmAnalysis.EntryPrice > 0 {
				combinedDecision.Entry = llmAnalysis.EntryPrice
			}
			if llmAnalysis.StopLoss > 0 {
				combinedDecision.StopLoss = llmAnalysis.StopLoss
			}
			if llmAnalysis.TakeProfit > 0 {
				tpDist := math.Abs(llmAnalysis.TakeProfit-marketCtx.CurrentPrice) / marketCtx.CurrentPrice * 100
				if tpDist >= tpMinDistancePct {
					combinedDecision.TakeProfit = llmAnalysis.TakeProfit
				}
			}

			bot.log.WithFields(logrus.Fields{
				"action":     combinedDecision.Action,
				"confidence": fmt.Sprintf("%.2f%%", combinedDecision.Confidence*100),
			}).Info("🤖 Taking LLM opportunity (high confidence)")
		} else {
			// LLM confidence not high enough - stay with WAIT
			combinedDecision.Reason = fmt.Sprintf(
				"⏸️ WAIT: Technical says WAIT, LLM suggests %s but confidence (%.0f%%) too low. LLM: %s",
				llmAnalysis.Action,
				llmAnalysis.Confidence*100,
				llmAnalysis.Reasoning,
			)

			bot.log.WithField("llm_confidence", fmt.Sprintf("%.0f%%", llmAnalysis.Confidence*100)).
				Info("Staying with WAIT - LLM confidence too low")
		}

		bot.applyMLVote(combinedDecision, marketCtx)
		return combinedDecision
	}

	// Default: return technical decision with LLM context
	combinedDecision.Reason = fmt.Sprintf("%s | LLM: %s", technicalDecision.Reason, llmAnalysis.Reasoning)
	bot.applyMLVote(combinedDecision, marketCtx)
	return combinedDecision
}

// sendTradeNotificationWithAI sends an AI-enhanced trade notification
func (bot *AITradingBot) sendTradeNotificationWithAI(
	ctx context.Context,
	decision *ai.Decision,
	marketCtx *ai.MarketContext,
	orderID string,
) {
	// Skip if no notification manager configured
	if bot.notificationManager == nil {
		return
	}

	// Calculate expected P&L
	expectedPnL := 0.0
	if decision.TakeProfit > 0 && decision.Entry > 0 {
		// No leverage multiplier — realized PnL = price move × size (see checkPosition).
		if decision.Action == "BUY" {
			expectedPnL = (decision.TakeProfit - decision.Entry) * bot.config.TradeSize
		} else {
			expectedPnL = (decision.Entry - decision.TakeProfit) * bot.config.TradeSize
		}
	}

	// Build trade info
	// Get primary context for trend and RSI
	primaryCtx := marketCtx.GetPrimaryContext()
	trend := "neutral"
	rsi := 50.0
	if primaryCtx != nil {
		trend = primaryCtx.Trend
		if primaryCtx.Indicators != nil {
			rsi = primaryCtx.Indicators.RSI
		}
	}

	tradeInfo := &notifications.TradeInfo{
		Symbol:          bot.config.Pair,
		Action:          decision.Action,
		Price:           decision.Entry,
		Size:            bot.config.TradeSize,
		Confidence:      decision.Confidence,
		ExpectedPnL:     expectedPnL,
		ExpectedOutcome: fmt.Sprintf("TP: $%.4f, SL: $%.4f", decision.TakeProfit, decision.StopLoss),
		OrderID:         orderID,
		UserID:          "system",
		Trend:           trend,
		RSI:             rsi,
		VolumeStatus:    "normal", // Could be enhanced with actual volume analysis
	}

	// Create LLM callback function
	llmCallFunc := func(ctx context.Context, prompt string) (string, error) {
		if bot.llmAnalyzer == nil {
			return "", fmt.Errorf("LLM analyzer not configured")
		}

		// Use LLM to generate explanation
		explanation, err := bot.llmAnalyzer.ExplainTrade(ctx, tradeInfo, marketCtx)
		if err != nil {
			return "", err
		}

		return explanation.Analysis, nil
	}

	// Send notification with AI enhancement
	if err := bot.notificationManager.NotifyTradeWithAI(ctx, tradeInfo, llmCallFunc); err != nil {
		bot.log.WithError(err).Warn("Failed to send AI-enhanced trade notification")
	} else {
		bot.log.Info("📧 AI-enhanced trade notification sent")
	}
}

// sendPositionClosedNotificationWithAI sends an AI-enhanced position closed notification
func (bot *AITradingBot) sendPositionClosedNotificationWithAI(
	ctx context.Context,
	position *ai.ManagedPosition,
	closeReason string,
	pnl float64,
) {
	// Skip if no notification manager configured
	if bot.notificationManager == nil {
		return
	}

	// Calculate P&L percentage
	pnlPercent := 0.0
	if position.EntryPrice > 0 {
		if position.Side == "BUY" {
			pnlPercent = ((position.EntryPrice - position.EntryPrice) / position.EntryPrice) * 100
		} else {
			pnlPercent = ((position.EntryPrice - position.EntryPrice) / position.EntryPrice) * 100
		}
	}

	// Get current price for exit price (futures-aware — see livePriceForPosition)
	exitPrice := position.EntryPrice
	if px, perr := bot.livePriceForPosition(ctx, position); perr == nil {
		exitPrice = px
	}

	// Build position info
	positionInfo := &notifications.PositionInfo{
		PositionID:  position.ID,
		Symbol:      position.Symbol,
		EntryPrice:  position.EntryPrice,
		ExitPrice:   exitPrice,
		PnL:         pnl,
		PnLPercent:  pnlPercent,
		Size:        position.Size,
		Duration:    time.Since(position.OpenedAt),
		CloseReason: closeReason,
		UserID:      "system",
	}

	// Create LLM callback function
	llmCallFunc := func(ctx context.Context, prompt string) (string, error) {
		if bot.llmAnalyzer == nil {
			return "", fmt.Errorf("LLM analyzer not configured")
		}

		// Use LLM to analyze what went right/wrong
		explanation, err := bot.llmAnalyzer.ExplainTrade(ctx, positionInfo, nil)
		if err != nil {
			return "", err
		}

		return explanation.Analysis, nil
	}

	// Send notification with AI enhancement
	if err := bot.notificationManager.NotifyPositionClosedWithAI(ctx, positionInfo, llmCallFunc); err != nil {
		bot.log.WithError(err).Warn("Failed to send AI-enhanced position closed notification")
	} else {
		bot.log.Info("📧 AI-enhanced position closed notification sent")
	}
}

// analyzeClosedPositionWithLLM analyzes a closed position using LLM-enhanced learning
func (bot *AITradingBot) analyzeClosedPositionWithLLM(
	ctx context.Context,
	position *ai.ManagedPosition,
	closeReason string,
	pnl float64,
) {
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in analyzeClosedPositionWithLLM - recovered")
		}
	}()

	bot.log.WithField("position_id", position.ID).Debug("🧠 Analyzing closed position with LLM")

	// Create execution history record for analysis
	// Note: In a real implementation, this would be fetched from the database
	// For now, we create a mock execution history from the position data
	executionHistory := bot.createExecutionHistoryFromPosition(position, closeReason, pnl)

	// Create LLM callback function
	llmCallFunc := func(ctx context.Context, prompt string) (string, error) {
		if bot.llmAnalyzer == nil {
			return "", fmt.Errorf("LLM analyzer not configured")
		}

		// Use ExplainTrade to get LLM analysis
		explanation, err := bot.llmAnalyzer.ExplainTrade(ctx, executionHistory, nil)
		if err != nil {
			return "", err
		}

		return explanation.Analysis, nil
	}

	// Analyze with LLM
	analysis, err := bot.learningSystem.AnalyzeExecutionWithLLM(ctx, executionHistory, llmCallFunc)
	if err != nil {
		bot.log.WithError(err).Warn("Failed to analyze position with LLM")
		return
	}

	// Log insights
	if analysis.LLMInsights != nil {
		bot.log.WithFields(logrus.Fields{
			"position_id":       position.ID,
			"failure_reason":    analysis.LLMInsights.FailureReason,
			"should_adjust_sl":  analysis.LLMInsights.ShouldAdjustSL,
			"should_adjust_tp":  analysis.LLMInsights.ShouldAdjustTP,
			"learning_priority": analysis.LLMInsights.LearningPriority,
			"confidence":        analysis.LLMInsights.ConfidenceScore,
		}).Info("🎓 LLM learning insights generated")

		// If high priority learning, log recommendations
		if analysis.LLMInsights.LearningPriority == "high" && len(analysis.LLMInsights.Recommendations) > 0 {
			bot.log.WithFields(logrus.Fields{
				"position_id":     position.ID,
				"recommendations": analysis.LLMInsights.Recommendations,
			}).Info("💡 High priority learning recommendations")
		}
	}
}

// createExecutionHistoryFromPosition creates an execution history record from a position
func (bot *AITradingBot) createExecutionHistoryFromPosition(
	position *ai.ManagedPosition,
	closeReason string,
	pnl float64,
) *domain.ExecutionHistory {
	// Get current price for execution price (futures-aware — see livePriceForPosition)
	executionPrice := position.EntryPrice
	if px, perr := bot.livePriceForPosition(context.Background(), position); perr == nil {
		executionPrice = px
	}

	// Determine execution type
	execType := "stop_loss"
	if closeReason == "take_profit" {
		execType = "take_profit"
	}

	return &domain.ExecutionHistory{
		ID:             position.ID,
		Symbol:         position.Symbol,
		Exchange:       bot.config.Exchange,
		Type:           execType,
		TriggerPrice:   decimal.NewFromFloat(position.StopLoss),
		ExecutionPrice: decimal.NewFromFloat(executionPrice),
		Size:           decimal.NewFromFloat(position.Size),
		PnL:            decimal.NewFromFloat(pnl),
		Reason:         closeReason,
		ExecutedAt:     time.Now(),
	}
}

// autoConfigurationLoop runs auto-configuration every 6 hours
func (bot *AITradingBot) autoConfigurationLoop(ctx context.Context) {
	// Run immediately on start
	bot.runAutoConfiguration(ctx)

	// Then run every 6 hours
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	bot.log.Info("🔧 Auto-configuration loop started")

	for {
		select {
		case <-ctx.Done():
			bot.log.Info("Auto-configuration loop stopped (context cancelled)")
			return

		case <-bot.stopChan:
			bot.log.Info("Auto-configuration loop stopped (stop signal)")
			return

		case <-ticker.C:
			bot.mu.RLock()
			if bot.emergencyStop || bot.paused {
				bot.mu.RUnlock()
				continue
			}
			bot.mu.RUnlock()

			bot.runAutoConfiguration(ctx)
		}
	}
}

// runAutoConfiguration performs automatic configuration based on market conditions
func (bot *AITradingBot) runAutoConfiguration(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			bot.log.WithField("panic", r).Error("🚨 Panic in runAutoConfiguration - recovered")
		}
	}()

	bot.log.Info("🔧 Running auto-configuration...")

	// Skip if auto-configurator is not available
	if bot.autoConfigurator == nil {
		bot.log.Warn("Auto-configurator not available, using legacy configuration")
		bot.runLegacyAutoConfiguration(ctx)
		return
	}

	// Build market context to analyze current conditions
	marketCtx, err := bot.contextBuilder.BuildContext(ctx, bot.config.Pair)
	if err != nil {
		bot.log.WithError(err).Error("Failed to build market context for auto-configuration")
		return
	}

	// Convert to optimization package MarketConditions
	conditions := bot.convertToOptimizationMarketConditions(marketCtx)

	bot.log.WithFields(logrus.Fields{
		"trend":          conditions.Trend,
		"trend_strength": fmt.Sprintf("%.2f", conditions.TrendStrength),
		"volatility":     fmt.Sprintf("%.4f", conditions.Volatility),
		"is_range":       conditions.IsRange,
		"liquidity":      fmt.Sprintf("%.2f", conditions.Liquidity),
	}).Info("📊 Market conditions analyzed")

	// 1. Select optimal strategy using Auto-Configurator
	strategy, recommendation, err := bot.autoConfigurator.SelectOptimalStrategyWithAI(ctx, conditions)
	if err != nil {
		bot.log.WithError(err).Error("Failed to select optimal strategy")
		return
	}

	bot.log.WithFields(logrus.Fields{
		"recommended_strategy": strategy,
		"confidence":           fmt.Sprintf("%.2f", recommendation.Confidence),
		"reasoning":            recommendation.Reasoning,
	}).Info("🎯 Strategy selected with AI")

	// 2. Adjust risk parameters using Auto-Configurator
	riskParams, err := bot.autoConfigurator.AdjustRiskParameters(ctx, conditions)
	if err != nil {
		bot.log.WithError(err).Error("Failed to adjust risk parameters")
		return
	}

	bot.log.WithFields(logrus.Fields{
		"stop_loss_pct":        fmt.Sprintf("%.2f%%", riskParams.StopLossPercent),
		"take_profit_pct":      fmt.Sprintf("%.2f%%", riskParams.TakeProfitPercent),
		"max_position_size":    fmt.Sprintf("%.2f", riskParams.MaxPositionSize),
		"confidence_threshold": fmt.Sprintf("%.2f", riskParams.ConfidenceThreshold),
	}).Info("⚙️ Risk parameters adjusted")

	// 3. Create optimized parameters package
	optimizedParams := &OptimizedParameters{
		Strategy:         strategy,
		RiskParams:       riskParams,
		MarketConditions: conditions,
		Recommendation:   recommendation,
		Timestamp:        time.Now(),
	}

	// 4. Validate and apply optimized parameters
	err = bot.autoConfigurator.ApplyOptimizedParameters(ctx, optimizedParams)
	if err != nil {
		bot.log.WithError(err).Error("Failed to apply optimized parameters")
		return
	}

	// 5. Update bot configuration with new parameters
	bot.updateBotConfiguration(optimizedParams)

	// 6. Send notification about configuration change
	bot.notifyConfigurationChange(ctx, optimizedParams)

	bot.log.Info("✅ Auto-configuration complete")
}

// analyzeMarketConditions analyzes current market conditions
func (bot *AITradingBot) analyzeMarketConditions(marketCtx *ai.MarketContext) *MarketConditions {
	conditions := &MarketConditions{}

	// Get primary and secondary contexts
	primaryCtx := marketCtx.GetPrimaryContext()
	secondaryContexts := marketCtx.GetSecondaryContexts()

	// Determine trend using primary and first secondary timeframe
	var trendPrimary, trendSecondary string
	var rsiPrimary, rsiSecondary float64

	if primaryCtx != nil {
		trendPrimary = primaryCtx.Trend
		if primaryCtx.Indicators != nil {
			rsiPrimary = primaryCtx.Indicators.RSI
		}
	}

	if len(secondaryContexts) > 0 && secondaryContexts[0] != nil {
		trendSecondary = secondaryContexts[0].Trend
		if secondaryContexts[0].Indicators != nil {
			rsiSecondary = secondaryContexts[0].Indicators.RSI
		}
	}

	// Analyze trend alignment
	if trendPrimary == "bullish" && trendSecondary == "bullish" {
		conditions.Trend = "strong_bullish"
		conditions.TrendStrength = 0.9
	} else if trendPrimary == "bearish" && trendSecondary == "bearish" {
		conditions.Trend = "strong_bearish"
		conditions.TrendStrength = 0.9
	} else if trendPrimary == trendSecondary {
		conditions.Trend = trendPrimary
		conditions.TrendStrength = 0.7
	} else {
		conditions.Trend = "sideways"
		conditions.TrendStrength = 0.3
	}

	// Calculate volatility (simplified - using RSI range as proxy)
	rsiRange := rsiPrimary - rsiSecondary
	if rsiRange < 0 {
		rsiRange = -rsiRange
	}
	conditions.Volatility = rsiRange / 100.0 // Normalize to 0-1

	// Detect range-bound market
	conditions.IsRange = conditions.TrendStrength < 0.4

	// Set support and resistance (simplified)
	conditions.SupportLevel = marketCtx.CurrentPrice * 0.98
	conditions.ResistanceLevel = marketCtx.CurrentPrice * 1.02

	// Volume and liquidity (simplified - would need actual data)
	conditions.Volume = 1.0
	conditions.Liquidity = 1.0

	return conditions
}

// selectOptimalStrategy selects the best strategy for current conditions
func (bot *AITradingBot) selectOptimalStrategy(conditions *MarketConditions) string {
	// Strong trend → Trend Following
	if conditions.TrendStrength > 0.7 {
		return "trend_following"
	}

	// High volatility → Scalping
	if conditions.Volatility > 0.05 {
		return "scalping"
	}

	// Range bound → Grid Trading
	if conditions.IsRange {
		return "grid_trading"
	}

	// Moderate conditions → Swing Trading
	if conditions.Volatility > 0.02 && conditions.Volatility < 0.05 {
		return "swing_trading"
	}

	// Default → Multi-timeframe
	return "multi_timeframe"
}

// adjustRiskParameters adjusts risk parameters based on current market volatility.
// High volatility → fewer positions, larger stops, higher confidence bar, fewer trades.
// Low volatility  → more positions allowed, tighter stops, more trades.
func (bot *AITradingBot) adjustRiskParameters(volatility float64) *RiskParameters {
	params := &RiskParameters{}

	if volatility > 0.05 {
		// High volatility: reduce exposure
		params.StopLossPct = 5.0
		params.TakeProfitPct = 10.0
		params.MaxPositionSize = 2.0
		params.MaxOpenPositions = 1
		params.ConfidenceThreshold = 0.75
	} else if volatility > 0.03 {
		// Medium volatility: balanced
		params.StopLossPct = 3.0
		params.TakeProfitPct = 6.0
		params.MaxPositionSize = 3.0
		params.MaxOpenPositions = 1
		params.ConfidenceThreshold = 0.70
	} else {
		// Low volatility: more room to operate
		params.StopLossPct = 2.0
		params.TakeProfitPct = 4.0
		params.MaxPositionSize = 5.0
		params.MaxOpenPositions = 1
		params.ConfidenceThreshold = 0.65
	}
	// MaxDailyTrades is intentionally not set here — it is read at check-time
	// from the dynamic config (bot.max_daily_trades) so the DB value always wins.

	return params
}

// applyAutoConfiguration applies new configuration parameters
func (bot *AITradingBot) applyAutoConfiguration(params *RiskParameters) {
	// The volatility regimes can pick a stop the AI-02 validator would refuse
	// (high volatility = 5 %); never default to a stop beyond its limit.
	if limit := maxStopLossPct(context.Background()); params.StopLossPct > limit {
		bot.log.WithFields(logrus.Fields{
			"auto_stop_loss_pct": params.StopLossPct,
			"max_stop_loss_pct":  limit,
		}).Warn("📐 Auto-configured stop loss capped to validation.max_stop_loss_pct")
		params.StopLossPct = limit
	}

	bot.mu.Lock()
	defer bot.mu.Unlock()

	// Update configuration
	bot.config.DefaultStopLossPct = params.StopLossPct
	bot.config.DefaultTakeProfitPct = params.TakeProfitPct
	bot.config.TradeSize = params.MaxPositionSize
	bot.config.MaxOpenPositions = params.MaxOpenPositions
	bot.config.ConfidenceThreshold = params.ConfidenceThreshold
	if params.MaxDailyTrades > 0 {
		bot.config.MaxDailyTrades = params.MaxDailyTrades
	}

	bot.log.WithFields(logrus.Fields{
		"stop_loss":            fmt.Sprintf("%.2f%%", params.StopLossPct),
		"take_profit":          fmt.Sprintf("%.2f%%", params.TakeProfitPct),
		"position_size":        fmt.Sprintf("$%.2f", params.MaxPositionSize),
		"max_positions":        params.MaxOpenPositions,
		"max_daily_trades":     params.MaxDailyTrades,
		"confidence_threshold": fmt.Sprintf("%.2f%%", params.ConfidenceThreshold*100),
	}).Info("✅ Configuration applied")
}

// getLLMConfigurationInsights gets additional insights from LLM
func (bot *AITradingBot) getLLMConfigurationInsights(
	ctx context.Context,
	marketCtx *ai.MarketContext,
	conditions *MarketConditions,
	strategy string,
) {
	bot.log.Debug("🤖 Getting LLM configuration insights...")

	// Use LLM to analyze market and provide recommendations
	llmAnalysis, err := bot.llmAnalyzer.AnalyzeMarket(ctx, marketCtx)
	if err != nil {
		bot.log.WithError(err).Warn("Failed to get LLM configuration insights")
		return
	}

	bot.log.WithFields(logrus.Fields{
		"llm_action":       llmAnalysis.Action,
		"llm_confidence":   fmt.Sprintf("%.2f%%", llmAnalysis.Confidence*100),
		"market_condition": llmAnalysis.MarketCondition,
		"risk_assessment":  llmAnalysis.RiskAssessment,
	}).Info("🧠 LLM configuration insights received")

	// Log key factors
	if len(llmAnalysis.KeyFactors) > 0 {
		bot.log.WithField("key_factors", llmAnalysis.KeyFactors).Info("💡 Key market factors identified")
	}

	// Log warnings if any
	if len(llmAnalysis.Warnings) > 0 {
		bot.log.WithField("warnings", llmAnalysis.Warnings).Warn("⚠️ LLM warnings")
	}
}

// MarketConditions represents current market conditions
type MarketConditions struct {
	Trend           string
	TrendStrength   float64
	Volatility      float64
	Volume          float64
	Liquidity       float64
	IsRange         bool
	SupportLevel    float64
	ResistanceLevel float64
	Sentiment       float64
}

// RiskParameters represents risk management parameters
type RiskParameters struct {
	StopLossPct         float64
	TakeProfitPct       float64
	MaxPositionSize     float64
	MaxOpenPositions    int
	MaxDailyTrades      int
	ConfidenceThreshold float64
}

// checkEnhancedSafetyLimits performs enhanced safety checks including win rate and drawdown
func (bot *AITradingBot) checkEnhancedSafetyLimits(ctx context.Context) bool {
	// BOT-02 fix: the central RiskSentinel kill switch must halt autonomous
	// bots too, not only MCP-tool-driven order submission. Checked first so
	// an operator's activate_kill_switch takes effect on the very next cycle.
	if bot.killSwitchActive(ctx) {
		bot.log.Warn("🛑 RiskSentinel kill switch active — skipping analysis cycle")
		return false
	}

	// First check basic safety limits
	if !bot.checkSafetyLimits() {
		return false
	}

	// Check win rate if learning system is available
	if bot.learningSystem != nil {
		if !bot.checkWinRateSafety(ctx) {
			return false
		}
	}

	// Check drawdown
	if !bot.checkDrawdownSafety(ctx) {
		return false
	}

	// Check cost budget if cost manager is available
	if bot.costManager != nil {
		if !bot.checkCostBudget(ctx) {
			return false
		}
	}

	return true
}

// checkWinRateSafety checks if win rate is acceptable
func (bot *AITradingBot) checkWinRateSafety(ctx context.Context) bool {
	// Get recent performance from learning system
	// This would typically query the last 50 trades
	// For now, we'll use a simplified check

	bot.mu.RLock()
	executionCount := bot.executionCount
	bot.mu.RUnlock()

	// Need at least 50 trades to evaluate win rate
	if executionCount < 50 {
		return true // Not enough data yet
	}

	// In a real implementation, this would:
	// 1. Query execution history from database
	// 2. Calculate win rate from last 50 trades
	// 3. Pause if win rate < 35%

	// For now, we'll log that the check is being performed
	bot.log.WithField("execution_count", executionCount).Debug("🔍 Win rate safety check performed")

	return true
}

// checkDrawdownSafety checks the intraday drawdown (realized daily loss as % of
// the day-start account capital, or of bot.drawdown.initial_capital_proxy when
// bot.drawdown.use_account_capital is off or no capital reading exists). Above
// the reduce threshold new entries are scaled by the drawdown factor for the
// rest of the day (applied by the sizing resolver, never compounded); above the
// pause threshold trading is paused.
func (bot *AITradingBot) checkDrawdownSafety(ctx context.Context) bool {
	bot.mu.RLock()
	dailyLoss := bot.dailyLoss
	bot.mu.RUnlock()

	// Read thresholds from dynamic config; fall back to manifest defaults.
	sizeReduceThreshold := 15.0
	pauseThreshold := 25.0
	sizeReduceFactor := 0.5
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.drawdown.size_reduce_threshold_pct"); v > 0 {
			sizeReduceThreshold = v
		}
		if v := snap.Float("bot.drawdown.pause_threshold_pct"); v > 0 {
			pauseThreshold = v
		}
		if v := snap.Float("bot.drawdown.size_reduce_factor"); v > 0 {
			sizeReduceFactor = v
		}
	}

	// dailyLoss is negative when losing: the drawdown is the loss as a positive %.
	loss := -dailyLoss
	if loss <= 0 {
		return true
	}
	base := bot.drawdownBaseCapital(ctx)
	if base <= 0 {
		return true
	}
	drawdownPercent := loss / base * 100

	if drawdownPercent > sizeReduceThreshold {
		bot.setDrawdownSizeFactor(sizeReduceFactor)
		bot.log.WithFields(logrus.Fields{
			"drawdown":    fmt.Sprintf("%.2f%%", drawdownPercent),
			"daily_loss":  dailyLoss,
			"base_usd":    fmt.Sprintf("%.2f", base),
			"threshold":   fmt.Sprintf("%.1f%%", sizeReduceThreshold),
			"size_factor": bot.drawdownSizeFactor(),
		}).Warn("⚠️ High drawdown detected - new entries scaled by the drawdown factor")
	}

	if drawdownPercent > pauseThreshold {
		bot.log.WithFields(logrus.Fields{
			"drawdown":   fmt.Sprintf("%.2f%%", drawdownPercent),
			"daily_loss": dailyLoss,
			"base_usd":   fmt.Sprintf("%.2f", base),
			"threshold":  fmt.Sprintf("%.1f%%", pauseThreshold),
		}).Error("🚨 Critical drawdown - pausing trading")

		bot.Pause()
		return false
	}

	return true
}

// checkCostBudget checks if API cost budget is within limits
func (bot *AITradingBot) checkCostBudget(ctx context.Context) bool {
	if bot.costManager == nil {
		return true
	}

	// Check budget for LLM provider
	allowed, err := bot.costManager.CheckBudget(ctx, "llm")
	if err != nil {
		bot.log.WithError(err).Warn("Failed to check cost budget")
		return true // Don't block trading on budget check errors
	}

	if !allowed {
		bot.log.Warn("💰 LLM budget limit reached - will use technical analysis only")
		// Don't block trading, just disable LLM
		return true
	}

	return true
}

// GetPerformanceMetrics returns current performance metrics
func (bot *AITradingBot) GetPerformanceMetrics() map[string]interface{} {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	metrics := map[string]interface{}{
		"total_executions":   bot.executionCount,
		"total_analyses":     bot.analysisCount,
		"daily_trades":       bot.dailyTrades,
		"daily_loss":         bot.dailyLoss,
		"consecutive_errors": bot.consecutiveErrs,
		"error_count":        bot.errorCount,
		"uptime":             time.Since(bot.startedAt).String(),
		"last_analysis":      bot.lastAnalysisAt.Format(time.RFC3339),
	}

	// Calculate win rate if we have enough data
	if bot.executionCount > 0 {
		// This is simplified - in reality would query actual trade results
		metrics["estimated_win_rate"] = "N/A - needs trade history"
	}

	// Intraday drawdown on the day-start capital (no network call: bot.mu is
	// held), falling back to bot.drawdown.initial_capital_proxy.
	initialCapital := 1000.0
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if v := snap.Float("bot.drawdown.initial_capital_proxy"); v > 0 {
			initialCapital = v
		}
	}
	bot.sizing.mu.Lock()
	if bot.sizing.dayStartCapital > 0 {
		initialCapital = bot.sizing.dayStartCapital
	}
	bot.sizing.mu.Unlock()
	drawdownPercent := 0.0
	if bot.dailyLoss < 0 {
		drawdownPercent = -bot.dailyLoss / initialCapital * 100
	}
	metrics["drawdown_percent"] = fmt.Sprintf("%.2f%%", drawdownPercent)

	return metrics
}

// TradingPerformance holds aggregate performance data derived from closed trades.
type TradingPerformance struct {
	TotalTrades   int     `json:"total_trades"`
	WinningTrades int     `json:"winning_trades"`
	LosingTrades  int     `json:"losing_trades"`
	WinRatePct    float64 `json:"win_rate_pct"`
	AvgPnLPct     float64 `json:"avg_pnl_pct"`
	BestTradePct  float64 `json:"best_trade_pct"`
	WorstTradePct float64 `json:"worst_trade_pct"`
	TotalPnLUSD   float64 `json:"total_pnl_usd"`
	CurrentStreak int     `json:"current_streak"`
	StreakType    string  `json:"streak_type"`
}

// GetTradingPerformance builds aggregate performance data from closed trade history.
// Returns zeroed metrics when no trade repository is configured or no trades exist.
func (bot *AITradingBot) GetTradingPerformance() TradingPerformance {
	result := TradingPerformance{StreakType: "none"}

	bot.mu.RLock()
	repo := bot.tradeRepo
	pair := ""
	if bot.config != nil {
		pair = bot.config.Pair
	}
	bot.mu.RUnlock()

	if repo == nil || pair == "" {
		return result
	}

	trades, err := repo.GetTradeStatsByPair(pair, 1000)
	if err != nil || len(trades) == 0 {
		return result
	}

	var sumPct float64
	bestPct := trades[0].PnLPct
	worstPct := trades[0].PnLPct

	for _, t := range trades {
		result.TotalTrades++
		sumPct += t.PnLPct
		if t.PnLPct > 0 {
			result.WinningTrades++
		} else if t.PnLPct < 0 {
			result.LosingTrades++
		}
		if t.PnLPct > bestPct {
			bestPct = t.PnLPct
		}
		if t.PnLPct < worstPct {
			worstPct = t.PnLPct
		}
	}

	if result.TotalTrades > 0 {
		result.WinRatePct = float64(result.WinningTrades) / float64(result.TotalTrades) * 100
		result.AvgPnLPct = sumPct / float64(result.TotalTrades)
	}
	result.BestTradePct = bestPct
	result.WorstTradePct = worstPct

	// Compute current streak from most-recent trades (last = most recent).
	streak := 0
	streakType := "none"
	for i := len(trades) - 1; i >= 0; i-- {
		pct := trades[i].PnLPct
		if streak == 0 {
			if pct > 0 {
				streakType = "win"
				streak = 1
			} else if pct < 0 {
				streakType = "loss"
				streak = 1
			} else {
				break
			}
		} else {
			if streakType == "win" && pct > 0 {
				streak++
			} else if streakType == "loss" && pct < 0 {
				streak++
			} else {
				break
			}
		}
	}
	result.CurrentStreak = streak
	result.StreakType = streakType

	return result
}

// SetFunctionCallingAnalyzer sets the function calling analyzer for autonomous mode
func (bot *AITradingBot) SetFunctionCallingAnalyzer(fcAnalyzer *llm.FunctionCallingAnalyzer) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.fcAnalyzer = fcAnalyzer
	bot.log.Info("✅ Function calling analyzer configured - Autonomous mode available")
}

// EnableAutonomousMode enables full autonomous trading with function calling
func (bot *AITradingBot) EnableAutonomousMode(enable bool) error {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	if bot.running {
		return fmt.Errorf("cannot change autonomous mode while bot is running")
	}

	if enable && bot.fcAnalyzer == nil {
		return fmt.Errorf("function calling analyzer not configured")
	}

	bot.config.AutonomousMode = enable
	bot.config.AllowToolExecution = enable

	if enable {
		bot.log.Warn("⚠️  AUTONOMOUS MODE ENABLED - Bot will execute tools and trades automatically")
	} else {
		bot.log.Info("Autonomous mode disabled")
	}

	return nil
}

// IsAutonomousMode returns true if autonomous mode is enabled
func (bot *AITradingBot) IsAutonomousMode() bool {
	bot.mu.RLock()
	defer bot.mu.RUnlock()
	return bot.config.AutonomousMode
}

// GetLLMAnalyzer returns the LLM analyzer
func (bot *AITradingBot) GetLLMAnalyzer() *llm.Analyzer {
	bot.mu.RLock()
	defer bot.mu.RUnlock()
	return bot.llmAnalyzer
}

// GetFunctionCallingAnalyzer returns the function calling analyzer
func (bot *AITradingBot) GetFunctionCallingAnalyzer() *llm.FunctionCallingAnalyzer {
	bot.mu.RLock()
	defer bot.mu.RUnlock()
	return bot.fcAnalyzer
}

// convertToOptimizationMarketConditions converts AI MarketContext to optimization MarketConditions
func (bot *AITradingBot) convertToOptimizationMarketConditions(marketCtx *ai.MarketContext) *MarketConditions {
	conditions := &MarketConditions{
		Sentiment: 0.0, // Default neutral sentiment
	}

	// Get primary and secondary contexts
	primaryCtx := marketCtx.GetPrimaryContext()
	secondaryContexts := marketCtx.GetSecondaryContexts()

	if primaryCtx != nil {
		// Set trend from primary context
		conditions.Trend = primaryCtx.Trend

		// Calculate trend strength based on trend alignment
		conditions.TrendStrength = 0.5 // Default medium strength
		if len(secondaryContexts) > 0 && secondaryContexts[0] != nil {
			if primaryCtx.Trend == secondaryContexts[0].Trend {
				conditions.TrendStrength = 0.7 // Higher strength for aligned trends
			}
		}

		// Calculate volatility from price data (simplified)
		if len(primaryCtx.Candles) > 20 {
			prices := make([]float64, len(primaryCtx.Candles))
			volumes := make([]float64, len(primaryCtx.Candles))

			for i, candle := range primaryCtx.Candles {
				prices[i] = candle.Close
				volumes[i] = candle.Volume
			}

			// Calculate simple volatility (standard deviation of returns)
			if len(prices) > 1 {
				returns := make([]float64, len(prices)-1)
				for i := 1; i < len(prices); i++ {
					returns[i-1] = (prices[i] - prices[i-1]) / prices[i-1]
				}

				// Calculate standard deviation
				mean := 0.0
				for _, ret := range returns {
					mean += ret
				}
				mean /= float64(len(returns))

				variance := 0.0
				for _, ret := range returns {
					diff := ret - mean
					variance += diff * diff
				}
				variance /= float64(len(returns))

				conditions.Volatility = math.Sqrt(variance) * math.Sqrt(365) // Annualized
			}

			// Calculate 24h volume
			volumePeriods := len(volumes)
			if volumePeriods > 24 {
				volumePeriods = 24
			}

			volume24h := 0.0
			for i := len(volumes) - volumePeriods; i < len(volumes); i++ {
				volume24h += volumes[i]
			}
			conditions.Volume = volume24h

			// Calculate liquidity score (simplified)
			avgVolume := volume24h / float64(volumePeriods)
			if avgVolume > 0 {
				conditions.Liquidity = math.Min(1.0, volume24h/avgVolume/2.0)
			} else {
				conditions.Liquidity = 0.5
			}

			// Detect range-bound market (simplified)
			if len(prices) >= 20 {
				recentPrices := prices[len(prices)-20:]
				high := recentPrices[0]
				low := recentPrices[0]

				for _, price := range recentPrices {
					if price > high {
						high = price
					}
					if price < low {
						low = price
					}
				}

				rangePercent := (high - low) / low
				conditions.IsRange = rangePercent < 0.02 // 2% range threshold
				conditions.SupportLevel = low
				conditions.ResistanceLevel = high
			}
		}
	}

	return conditions
}

// runLegacyAutoConfiguration runs the old auto-configuration logic as fallback
func (bot *AITradingBot) runLegacyAutoConfiguration(ctx context.Context) {
	// Build market context to analyze current conditions
	marketCtx, err := bot.contextBuilder.BuildContext(ctx, bot.config.Pair)
	if err != nil {
		bot.log.WithError(err).Error("Failed to build market context for legacy auto-configuration")
		return
	}

	// Analyze market conditions using legacy method
	conditions := bot.analyzeMarketConditions(marketCtx)

	bot.log.WithFields(logrus.Fields{
		"trend":          conditions.Trend,
		"trend_strength": fmt.Sprintf("%.2f", conditions.TrendStrength),
		"volatility":     fmt.Sprintf("%.2f%%", conditions.Volatility*100),
		"is_range":       conditions.IsRange,
	}).Info("📊 Market conditions analyzed (legacy)")

	// Select optimal strategy based on conditions
	optimalStrategy := bot.selectOptimalStrategy(conditions)

	bot.log.WithFields(logrus.Fields{
		"current_strategy": "multi_timeframe", // Current strategy (hardcoded for now)
		"optimal_strategy": optimalStrategy,
	}).Info("🎯 Strategy selection complete (legacy)")

	// Adjust risk parameters based on volatility
	riskParams := bot.adjustRiskParameters(conditions.Volatility)

	bot.log.WithFields(logrus.Fields{
		"stop_loss_pct":        fmt.Sprintf("%.2f%%", riskParams.StopLossPct),
		"take_profit_pct":      fmt.Sprintf("%.2f%%", riskParams.TakeProfitPct),
		"max_position_size":    fmt.Sprintf("$%.2f", riskParams.MaxPositionSize),
		"confidence_threshold": fmt.Sprintf("%.2f%%", riskParams.ConfidenceThreshold*100),
	}).Info("⚙️ Risk parameters adjusted (legacy)")

	// Apply new configuration
	bot.applyAutoConfiguration(riskParams)

	// Use LLM for additional insights if available
	if bot.llmAnalyzer != nil {
		bot.getLLMConfigurationInsights(ctx, marketCtx, conditions, optimalStrategy)
	}

	bot.log.Info("✅ Legacy auto-configuration complete")
}

// updateBotConfiguration updates the bot's internal configuration with optimized parameters
func (bot *AITradingBot) updateBotConfiguration(params *OptimizedParameters) {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	// Update confidence threshold
	bot.config.ConfidenceThreshold = params.RiskParams.ConfidenceThreshold

	// Update trade size (convert from position size to USD)
	bot.config.TradeSize = params.RiskParams.MaxPositionSize

	// Update max open positions
	bot.config.MaxOpenPositions = params.RiskParams.MaxOpenPositions

	bot.log.WithFields(logrus.Fields{
		"strategy":             params.Strategy,
		"confidence_threshold": fmt.Sprintf("%.2f", params.RiskParams.ConfidenceThreshold),
		"trade_size":           fmt.Sprintf("%.2f", bot.config.TradeSize),
		"max_open_positions":   params.RiskParams.MaxOpenPositions,
	}).Info("🔧 Bot configuration updated")
}

// notifyConfigurationChange sends a notification about configuration changes
func (bot *AITradingBot) notifyConfigurationChange(ctx context.Context, params *OptimizedParameters) {
	if bot.notificationManager == nil {
		return
	}

	// Create a notification message
	message := fmt.Sprintf(`🔧 Auto-Configuration Update

Strategy: %s
Confidence: %.0f%%
Reasoning: %s

Risk Parameters:
• Stop Loss: %.2f%%
• Take Profit: %.2f%%
• Max Position: %.2f
• Max Positions: %d

Market Conditions:
• Trend: %s (%.2f strength)
• Volatility: %.2f%%
• Range-bound: %t

Updated at: %s`,
		params.Strategy,
		params.Recommendation.Confidence*100,
		params.Recommendation.Reasoning,
		params.RiskParams.StopLossPercent,
		params.RiskParams.TakeProfitPercent,
		params.RiskParams.MaxPositionSize,
		params.RiskParams.MaxOpenPositions,
		params.MarketConditions.Trend,
		params.MarketConditions.TrendStrength,
		params.MarketConditions.Volatility*100,
		params.MarketConditions.IsRange,
		params.Timestamp.Format("2006-01-02 15:04:05"),
	)

	// Create LLM callback function for enhanced notification
	llmCallFunc := func(ctx context.Context, prompt string) (string, error) {
		if bot.llmAnalyzer == nil {
			return message, nil
		}

		// Use LLM to enhance the notification message
		explanation, err := bot.llmAnalyzer.ExplainTrade(ctx, params, nil)
		if err != nil {
			return message, nil
		}

		return fmt.Sprintf("%s\n\nAI Analysis:\n%s", message, explanation.Analysis), nil
	}

	// Send notification
	err := bot.notificationManager.NotifyTradeWithAI(ctx, params, llmCallFunc)
	if err != nil {
		bot.log.WithError(err).Warn("Failed to send configuration change notification")
	}
}

// SetAutoConfigurator sets the auto-configurator component
func (bot *AITradingBot) SetAutoConfigurator(autoConfigurator AutoConfigurator) {
	bot.mu.Lock()
	defer bot.mu.Unlock()

	bot.autoConfigurator = autoConfigurator
	bot.log.Info("Auto-configurator component set")
}

// GetAutoConfigurator returns the auto-configurator component
func (bot *AITradingBot) GetAutoConfigurator() AutoConfigurator {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	return bot.autoConfigurator
}

// GetLearningSystem returns the SLTP learning system
func (bot *AITradingBot) GetLearningSystem() *ai.SLTPLearningSystem {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	return bot.learningSystem
}

// GetExecutionRepository returns the execution history repository
func (bot *AITradingBot) GetExecutionRepository() domain.ExecutionHistoryRepository {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	// The execution repository should be available through the learning system
	// or through the bot's dependencies. For now, return nil and handle gracefully
	// In a real implementation, this would be injected as a dependency
	return nil
}

// GetCostManager returns the cost manager
func (bot *AITradingBot) GetCostManager() llm.CostManager {
	bot.mu.RLock()
	defer bot.mu.RUnlock()

	return bot.costManager
}

// ----------------------------------------------------------------------------
// Hybrid AI module integration (consensus, regime, BTC, Kelly)
// All setters are optional - default behavior is unchanged when not called.
// ----------------------------------------------------------------------------

// SetConsensusEngine activates multi-LLM consensus voting. Pass nil to disable.
func (bot *AITradingBot) SetConsensusEngine(engine *llm.ConsensusEngine) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.consensusEngine = engine
	bot.consensusEnabled = engine != nil
	bot.log.WithField("enabled", bot.consensusEnabled).Info("Multi-LLM consensus engine configured")
}

// SetRegimeDetector activates market regime classification before trade decisions.
// Pass nil to disable.
func (bot *AITradingBot) SetRegimeDetector(detector *regime.Detector) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.regimeDetector = detector
	bot.useRegimeFilter = detector != nil
	bot.log.WithField("enabled", bot.useRegimeFilter).Info("Market regime detector configured")
}

// SetPairProvider wires the master agent (or any equivalent source) so the
// bot can route itself to the highest-scored pair on each cycle when
// PairSelection is enabled. Pass nil to detach.
func (bot *AITradingBot) SetPairProvider(p PairProvider) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.pairProvider = p
	bot.log.WithField("enabled", p != nil).Info("🎯 Pair provider configured")
}

// SetObserveOnlyFn registers a predicate that marks certain pairs as
// observe-only. The bot still scores/ranks those pairs via the master agent
// but will never switch to them for live trading. Pass nil to disable.
func (bot *AITradingBot) SetObserveOnlyFn(fn func(pair string) bool) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.observeOnlyFn = fn
	bot.log.WithField("enabled", fn != nil).Info("Observe-only predicate configured")
}

// SetAutoPauseMaxConsecutiveLosses sets the number of consecutive losses that
// triggers an automatic pair blacklist. 0 or negative disables the feature.
func (bot *AITradingBot) SetAutoPauseMaxConsecutiveLosses(n int) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.autoPauseMaxConsecutiveLosses = n
	bot.log.WithField("threshold", n).Info("Auto-pause consecutive-loss threshold configured")
}

// maybeSwitchPair consults the pair provider and, if a higher-scored pair is
// available and the bot is idle (no open positions) and outside the switch
// cooldown, updates bot.config.Pair so the next analysis cycle targets the
// new symbol. No-op when the feature is disabled, no provider is wired, or
// any of the safety gates fail.
func (bot *AITradingBot) maybeSwitchPair() {
	bot.mu.RLock()
	cfg := bot.config.PairSelection
	provider := bot.pairProvider
	currentPair := bot.config.Pair
	lastSwitch := bot.lastPairSwitchAt
	bot.mu.RUnlock()

	if !cfg.Enabled || provider == nil {
		return
	}
	// Never switch while the CURRENT pair has an open position — rotating away
	// would stop analyzing the pair we're actively trading. Positions on OTHER
	// pairs do NOT block a switch when cross-pair adoption is on: they are adopted,
	// independently managed by the monitor (trailing/breakeven/exit fusion), and
	// carry their own exchange SL, so leaving them to rotate to a fresh opportunity
	// does not orphan them. With cross-pair adoption OFF, preserve the original
	// conservative rule (block on ANY open position).
	if bot.positionManager != nil {
		if bot.adoptAllPairsEnabled() {
			if bot.openPositionsForPair(currentPair) > 0 {
				return
			}
		} else if len(bot.positionManager.GetAllPositions()) > 0 {
			return
		}
	}
	if cfg.SwitchCooldownSeconds > 0 && time.Since(lastSwitch) < time.Duration(cfg.SwitchCooldownSeconds)*time.Second {
		return
	}

	tops := provider.TopOpportunities()
	if len(tops) == 0 {
		// No opportunities — fall back only if explicitly configured.
		// An empty fallback_pair means "pause until master surfaces something",
		// which is the right behavior for a fully dynamic pair universe.
		if cfg.FallbackPair != "" && currentPair != cfg.FallbackPair && !bot.isPairBlacklisted(cfg.FallbackPair) {
			bot.applyPairSwitch(cfg.FallbackPair, 0, "", "fallback (no opportunities)")
		}
		return
	}

	// Walk the ranking and pick the first pair that is not blacklisted, beats
	// min_score, and differs from the current pair. This lets us skip past
	// exchange-refused pairs (margin-mode mismatch, etc.) without abandoning
	// the smart-routing behavior.
	// Computed ONCE for the whole ranking walk — see xsecOwnedPairs.
	xsecOwned := bot.xsecOwnedPairs(context.Background())
	for _, opp := range tops {
		if opp.Pair == "" {
			continue
		}
		if opp.Score < cfg.MinScore {
			break // ranking is descending; nothing below this will qualify
		}
		if bot.isPairBlacklisted(opp.Pair) {
			continue
		}
		// XSEC-ownership guard: never rotate the scalper onto a pair the
		// xsec-live basket trader currently holds (see xsecOwnedPairs).
		if xsecOwned != nil {
			if _, isXSEC := xsecOwned[normalizeSymbolForMatch(opp.Pair)]; isXSEC {
				continue
			}
		}
		// Skip pairs marked observe-only — they are ranked but never traded.
		bot.mu.RLock()
		observeFn := bot.observeOnlyFn
		bot.mu.RUnlock()
		if observeFn != nil && observeFn(opp.Pair) {
			bot.log.WithField("pair", opp.Pair).Debug("maybeSwitchPair: skipping observe-only pair")
			continue
		}
		if opp.Pair == currentPair {
			return // already on the best pair
		}
		// Capture the surfacing exchange (constant when only one is active).
		bot.applyPairSwitch(opp.Pair, opp.Score, opp.Exchange, "master agent top pick")
		return
	}
}

// HasTradablePair reports whether the bot currently has a pair configured to
// analyze. False when pair_selection is enabled with empty fallback and the
// master agent hasn't yet surfaced any opportunity that beats min_score —
// the analysis loop uses this to skip the cycle entirely.
func (bot *AITradingBot) HasTradablePair() bool {
	bot.mu.RLock()
	defer bot.mu.RUnlock()
	return strings.TrimSpace(bot.config.Pair) != ""
}

// pairBlacklistTTL is how long a pair stays out of rotation after the exchange
// refuses an order for a deterministic reason (margin-mode mismatch, position
// mode mismatch, etc.). One hour balances "give the operator time to fix it"
// against "auto-recover when the underlying issue is gone".
const pairBlacklistTTL = 1 * time.Hour

// conflictBlacklistTTL is the short cooldown for TRANSITORY blacklist reasons —
// another bot already holds the pair, or the exchange refused for a mode
// mismatch. These clear on their own quickly, so a short, in-memory-only pause
// is enough; persisting them would needlessly lock the best pairs across cycles.
const conflictBlacklistTTL = 5 * time.Minute

// blacklistPair flags a pair as temporarily un-tradable for a TRANSITORY reason
// (operational conflict, mode mismatch). In-memory only, short TTL — NOT
// persisted, so good pairs aren't locked out for an hour after a fleeting
// conflict. For toxic pairs (consecutive losses) use blacklistPairToxic.
func (bot *AITradingBot) blacklistPair(pair, reason string) {
	bot.blacklistPairFor(pair, reason, conflictBlacklistTTL, false)
}

// blacklistPairToxic flags a pair that lost money (auto-pause after consecutive
// losses). Persisted to the DB and restored on restart so a genuinely toxic pair
// stays paused, with the full pairBlacklistTTL window.
func (bot *AITradingBot) blacklistPairToxic(pair, reason string) {
	bot.blacklistPairFor(pair, reason, pairBlacklistTTL, true)
}

// blacklistPairFor is the shared implementation. persist=true also writes the
// entry to the durable store. Callers' current pair is cleared so the next cycle
// re-routes via maybeSwitchPair.
func (bot *AITradingBot) blacklistPairFor(pair, reason string, ttl time.Duration, persist bool) {
	if strings.TrimSpace(pair) == "" {
		return
	}
	bot.mu.Lock()
	if bot.pairBlacklist == nil {
		bot.pairBlacklist = make(map[string]time.Time)
	}
	until := time.Now().Add(ttl)
	bot.pairBlacklist[pair] = until
	// Drop the current pair so the next cycle picks a fresh one from the master.
	if bot.config.Pair == pair {
		bot.config.Pair = ""
	}
	store := bot.blacklistStore
	bot.mu.Unlock()

	// Only persist genuinely toxic pairs — transitory conflicts must not survive
	// restarts or they lock the highest-scored pairs out of rotation.
	if persist && store != nil {
		go func() {
			if err := store.Save(bot.effectiveUserID(), pair, reason, until); err != nil {
				bot.log.WithError(err).WithField("pair", pair).Warn("Failed to persist blacklist entry")
			}
		}()
	}

	bot.log.WithFields(logrus.Fields{
		"pair":      pair,
		"reason":    reason,
		"ttl_min":   ttl.Minutes(),
		"persisted": persist,
	}).Warn("🚫 Pair blacklisted — will not retry until cooldown expires")
}

// isPairBlacklisted reports whether pair is currently in the blacklist. Side
// effect: expired entries are evicted lazily on read so the map stays small.
func (bot *AITradingBot) isPairBlacklisted(pair string) bool {
	bot.mu.RLock()
	until, ok := bot.pairBlacklist[pair]
	bot.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(until) {
		bot.mu.Lock()
		delete(bot.pairBlacklist, pair)
		bot.mu.Unlock()
		return false
	}
	return true
}

// applyPairSwitch performs the actual swap under the write lock.
func (bot *AITradingBot) applyPairSwitch(newPair string, score float64, exchange, reason string) {
	bot.mu.Lock()
	oldPair := bot.config.Pair
	bot.config.Pair = newPair
	bot.lastPairSwitchAt = time.Now()
	bot.mu.Unlock()

	// Purge the outgoing pair's cached macro trend so a stale snapshot from the
	// old pair can never drive the new pair's direction (crossing safeguard).
	if oldPair != "" && oldPair != newPair {
		bot.macroTrendCacheMu.Lock()
		delete(bot.macroTrendCache, oldPair)
		bot.macroTrendCacheMu.Unlock()
	}

	bot.log.WithFields(logrus.Fields{
		"from":     oldPair,
		"to":       newPair,
		"score":    fmt.Sprintf("%.3f", score),
		"exchange": exchange,
		"reason":   reason,
	}).Info("🎯 Smart pair switch")
}

// SetBTCMonitorService activates BTC-aware decision adjustment. Wires the
// monitor into the underlying decision engine as well so EvaluateOpportunity
// applies the BTC filter natively. Pass nil to detach.
func (bot *AITradingBot) SetBTCMonitorService(monitor *ai.BTCCorrelationMonitor) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.btcMonitor = monitor
	if bot.decisionEngine != nil {
		bot.decisionEngine.SetBTCMonitor(monitor)
	}
	bot.log.WithField("enabled", monitor != nil).Info("BTC correlation monitor configured")
}

// trapDetectorAdapter wraps manipulation.Detector to satisfy ai.TrapDetector,
// avoiding a circular import between the ai and ai/manipulation packages.
type trapDetectorAdapter struct {
	d *manipulation.Detector
}

func (a *trapDetectorAdapter) AnalyzeRecentTrap(candles []ai.Candle) *ai.TrapResult {
	result := a.d.AnalyzeRecent(candles)
	if result == nil {
		return nil
	}
	return &ai.TrapResult{
		Detected:        result.Detected,
		TrapType:        string(result.TrapType),
		Confidence:      result.Confidence,
		SuggestedAction: result.SuggestedAction,
	}
}

// SetTrapDetector injects a manipulation detector into the decision engine.
func (bot *AITradingBot) SetTrapDetector(d *manipulation.Detector) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if bot.decisionEngine != nil {
		if d == nil {
			bot.decisionEngine.SetTrapDetector(nil)
		} else {
			bot.decisionEngine.SetTrapDetector(&trapDetectorAdapter{d: d})
		}
	}
	bot.log.WithField("enabled", d != nil).Info("Manipulation/trap detector configured")
}

// EnableKellySizing activates Kelly Criterion-based position sizing. When
// disabled (default), the bot uses the static config.TradeSize.
func (bot *AITradingBot) EnableKellySizing(enabled bool) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.kellyEnabled = enabled
	bot.log.WithField("enabled", enabled).Info("Kelly position sizing toggled")
}

// SetFeedbackLoop injects the post-trade feedback loop.
func (bot *AITradingBot) SetFeedbackLoop(loop *learning.FeedbackLoop) {
	bot.feedbackLoop = loop
}

// SetPatternLibrary injects the pattern recognition library.
func (bot *AITradingBot) SetPatternLibrary(lib *patterns.Library) {
	bot.patternLibrary = lib
}

// SetCalendarGate injects a macro-event gate that blocks entries during high-impact events.
func (bot *AITradingBot) SetCalendarGate(gate calendarGate) {
	bot.calendarGate = gate
}

// OnFlowAnalysis implements orderflow.FlowSubscriber.
// Called synchronously by the Feeder on each tick — must not block.
func (bot *AITradingBot) OnFlowAnalysis(pair string, analysis *orderflow.FlowAnalysis) {
	bot.orderFlowCacheMu.Lock()
	if bot.orderFlowCache == nil {
		bot.orderFlowCache = make(map[string]*orderflow.FlowAnalysis)
	}
	bot.orderFlowCache[pair] = analysis
	bot.orderFlowCacheMu.Unlock()
}

// GetOrderFlowSnapshot returns a copy of the current order-flow cache keyed by pair.
func (bot *AITradingBot) GetOrderFlowSnapshot() map[string]*orderflow.FlowAnalysis {
	bot.orderFlowCacheMu.RLock()
	defer bot.orderFlowCacheMu.RUnlock()
	if bot.orderFlowCache == nil {
		return nil
	}
	out := make(map[string]*orderflow.FlowAnalysis, len(bot.orderFlowCache))
	for k, v := range bot.orderFlowCache {
		cp := *v
		out[k] = &cp
	}
	return out
}

// GetFeedbackAdjustments returns recent parameter adjustments produced by the FeedbackLoop.
// Returns nil when FeedbackLoop is not wired.
func (bot *AITradingBot) GetFeedbackAdjustments() map[string]interface{} {
	if bot.feedbackLoop == nil {
		return nil
	}
	return bot.feedbackLoop.GetAdjustmentsSummary()
}

// callLLMAnalysis routes the LLM call to the consensus engine when enabled, and
// falls back to the single-provider analyzer otherwise. The return shape always
// matches what AnalyzeMarket would have returned, so downstream code is unchanged.
func (bot *AITradingBot) callLLMAnalysis(ctx context.Context, marketCtx *ai.MarketContext) (*llm.LLMAnalysis, error) {
	bot.mu.RLock()
	consensus := bot.consensusEnabled
	engine := bot.consensusEngine
	bot.mu.RUnlock()

	if !consensus || engine == nil {
		return bot.llmAnalyzer.AnalyzeMarket(ctx, marketCtx)
	}

	// Build a request mirroring what AnalyzeMarket would build internally so
	// each provider sees the exact same prompt shape.
	prompt := llm.MarketAnalysisPrompt(marketCtx)
	request := &llm.LLMRequest{
		Prompt:      prompt,
		Temperature: 0.3,
		MaxTokens:   500,
	}

	result, err := engine.Analyze(ctx, request)
	if err != nil {
		bot.log.WithError(err).Warn("Consensus call failed, falling back to single provider")
		return bot.llmAnalyzer.AnalyzeMarket(ctx, marketCtx)
	}

	if !result.HasConsensus {
		bot.log.WithFields(logrus.Fields{
			"agreement_ratio": result.AgreementRatio,
			"providers_voted": len(result.Responses),
			"winning":         result.WinningProviders,
			"dissenting":      result.DissentingProviders,
		}).Info("No LLM consensus reached - degrading to WAIT")
		return &llm.LLMAnalysis{
			Action:     "WAIT",
			Confidence: 0.0,
			Reasoning:  "no consensus among LLM providers",
			Timestamp:  time.Now(),
		}, nil
	}

	// Pick the winning analysis (the response from a winning provider with the
	// highest individual confidence) and override its confidence/reasoning with
	// the consensus values so downstream combiners see the aggregate signal.
	var winner *llm.LLMAnalysis
	for _, resp := range result.Responses {
		if strings.EqualFold(resp.Action, result.Action) {
			if winner == nil || resp.Confidence > winner.Confidence {
				winner = resp
			}
		}
	}
	if winner == nil && len(result.Responses) > 0 {
		winner = result.Responses[0]
	}
	if winner == nil {
		// Should not happen given HasConsensus=true, but guard anyway.
		return &llm.LLMAnalysis{
			Action:     result.Action,
			Confidence: result.Confidence,
			Reasoning:  fmt.Sprintf("[CONSENSUS %s %.0f%%] no individual response available", result.Action, result.AgreementRatio*100),
			Timestamp:  time.Now(),
		}, nil
	}

	winner.Confidence = result.Confidence
	winner.Reasoning = fmt.Sprintf("[CONSENSUS %s %.0f%%] %s",
		result.Action, result.AgreementRatio*100, winner.Reasoning)
	return winner, nil
}

// checkRegime runs regime classification using primary-timeframe candles.
// Returns nil silently if the detector is not configured or there is not
// enough data to analyze.
func (bot *AITradingBot) checkRegime(marketCtx *ai.MarketContext) *regime.RegimeAnalysis {
	bot.mu.RLock()
	enabled := bot.useRegimeFilter
	detector := bot.regimeDetector
	bot.mu.RUnlock()

	if !enabled || detector == nil || marketCtx == nil {
		return nil
	}

	primary := marketCtx.GetPrimaryContext()
	if primary == nil || len(primary.Candles) == 0 {
		bot.log.Debug("regime: no primary timeframe candles available")
		return nil
	}

	analysis, err := detector.Analyze(primary.Candles)
	if err != nil {
		bot.log.WithError(err).Debug("regime analysis failed")
		return nil
	}
	// Stamp the latest regime label so it can be attributed to a position opened
	// in this analysis cycle (feedback labeler per-regime calibration).
	bot.lastRegimeLabelMu.Lock()
	bot.lastRegimeLabel = string(analysis.Regime)
	bot.lastRegimeLabelMu.Unlock()

	bot.log.WithFields(logrus.Fields{
		"regime":          analysis.Regime,
		"confidence":      analysis.Confidence,
		"adx":             analysis.ADX,
		"size_multiplier": analysis.SuggestedSizeMultiplier,
		"trend_direction": analysis.TrendDirection,
	}).Info("📊 Regime classified")
	return analysis
}

// currentRegimeLabel returns the most recent regime label classified by
// checkRegime (empty if regime filtering is disabled or no analysis has run).
func (bot *AITradingBot) currentRegimeLabel() string {
	bot.lastRegimeLabelMu.Lock()
	defer bot.lastRegimeLabelMu.Unlock()
	return bot.lastRegimeLabel
}

// applyRegimeFilter mutates the decision in place based on the regime analysis.
// CHAOS regimes force WAIT; non-trend regimes scale confidence by the suggested
// size multiplier so downstream confidence-threshold checks gate weak setups.
// enforceRangingAllowlist blocks NEW entries on pairs outside the ranging allowlist
// when the entry gate is in ranging mode. The allowlist (bot.entry.ranging.pair_allowlist,
// comma-separated) restricts mean-reversion trading to the pairs whose RANGING edge is
// demonstrated out-of-sample. Empty list = no restriction. No-op outside ranging mode or
// for non-entry decisions. Exits/management are untouched (entry path only).
func (bot *AITradingBot) enforceRangingAllowlist(decision *ai.Decision, pair string) {
	if decision == nil || (decision.Action != "BUY" && decision.Action != "SELL") {
		return
	}
	snap := dynamic.SnapshotFromContext(context.Background())
	if snap == nil || snap.String("bot.entry.regime_gate.mode") != "ranging" {
		return
	}
	// Respect the enabled flag: if gate is off, allowlist doesn't apply.
	if !snap.Bool("bot.entry.regime_gate.enabled") {
		return
	}
	raw := strings.TrimSpace(snap.String("bot.entry.ranging.pair_allowlist"))
	if raw == "" {
		return // empty = allow all ranging pairs
	}
	want := normalizeSymbolForMatch(pair)
	for _, entry := range strings.Split(raw, ",") {
		if e := strings.TrimSpace(entry); e != "" && normalizeSymbolForMatch(e) == want {
			return // pair is in the certified ranging set
		}
	}
	bot.log.WithFields(logrus.Fields{
		"pair":   pair,
		"action": decision.Action,
	}).Warn("🚫 Ranging allowlist — pair not in certified ranging set, forcing WAIT")
	decision.Action = "WAIT"
	decision.Confidence = 0
	decision.Reason = fmt.Sprintf("pair %s not in ranging allowlist", pair)
}

// regimeAllowedByMode reports whether a regime is tradable under the given gate mode.
//   - "ranging":  RANGING / UNKNOWN — the mean-reversion (fade-the-extreme) base has its
//     edge here (backtest 2026-06-03: PF≈1.04 net-positive).
//   - "trending" (and any unrecognised value): TRENDING_UP / TRENDING_DOWN / BREAKOUT —
//     the historical default for a trend-following base.
//
// CHAOS is handled separately (always blocked) before this is consulted.
func regimeAllowedByMode(r regime.Regime, mode string) bool {
	switch mode {
	case "ranging":
		return r == regime.RegimeRanging || r == regime.RegimeUnknown
	default:
		return r == regime.RegimeTrendingUp || r == regime.RegimeTrendingDown || r == regime.RegimeBreakout ||
			r == regime.RegimeQuietTrendUp || r == regime.RegimeQuietTrendDown
	}
}

func (bot *AITradingBot) applyRegimeFilter(decision *ai.Decision, regimeAnalysis *regime.RegimeAnalysis) {
	if decision == nil || regimeAnalysis == nil {
		return
	}

	if regimeAnalysis.Regime == regime.RegimeChaos {
		decision.Action = "WAIT"
		decision.Confidence = 0
		decision.Reason = fmt.Sprintf("regime CHAOS - pausing trades (%s)", regimeAnalysis.Reasoning)
		bot.log.Warn("🚨 Regime detector forced WAIT due to CHAOS")
		return
	}

	// Quiet-trend counter-veto (default ON, independent of the regime_gate below):
	// QUIET_TREND_UP/DOWN carries a real directional edge from a slow,
	// low-amplitude grind that ADX/BB-width alone cannot see (post-mortem
	// 2026-07-04/06: PEPE-USDT bought "at support" through a steady 5%/7h
	// decline, COTI-USDT shorted "at resistance" through a steady 1.8%/1.5h
	// rise — both misread as RANGING). Block any new entry that fades against
	// the detected drift regardless of bot.entry.regime_gate mode/state, since
	// that gate is often disabled independently for other reasons.
	if decision.Action == "BUY" || decision.Action == "SELL" {
		quietVetoEnabled := true
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			if v, ok := snap.BoolOk("bot.entry.quiet_trend_veto.enabled"); ok {
				quietVetoEnabled = v
			}
		}
		counterTrend := (regimeAnalysis.Regime == regime.RegimeQuietTrendDown && decision.Action == "BUY") ||
			(regimeAnalysis.Regime == regime.RegimeQuietTrendUp && decision.Action == "SELL")
		if quietVetoEnabled && counterTrend {
			bot.log.WithFields(logrus.Fields{
				"regime":     regimeAnalysis.Regime,
				"action":     decision.Action,
				"confidence": fmt.Sprintf("%.2f", decision.Confidence),
			}).Warn("🚫 Quiet-trend veto — entry fades against a detected low-volatility drift, forcing WAIT")
			decision.Action = "WAIT"
			decision.Confidence = 0
			decision.Reason = fmt.Sprintf("regime %s - entry contradicts quiet-trend drift (%s)", regimeAnalysis.Regime, regimeAnalysis.Reasoning)
			return
		}
	}

	// Hard regime entry gate (default ON): only TRENDING_UP / TRENDING_DOWN /
	// BREAKOUT regimes carry a positive realized edge. RANGING / UNKNOWN do not —
	// observed 2026-06-02: in a RANGING market the bot shorted the range floor at
	// 0.85–0.95 confidence and lost 8/9 (WR 11%) as price mean-reverted up into the
	// hard stop. The confidence floor below CANNOT catch these because confidence is
	// already high (miscalibrated in directionless regimes); only a hard gate stops
	// the bleed. This blocks NEW ENTRIES only — position management/exits run on the
	// monitor path and are untouched. Gated by bot.entry.regime_gate.enabled so it
	// is reversible live; default ON because the directionless edge is negative.
	// gateMode selects which regimes are tradable (read once; also consulted by the
	// directionless confidence floor below). "trending" = the historical default
	// (trend-following base); "ranging" = mean-reversion base (trade RANGING/UNKNOWN,
	// block trends) per the regime-segmented backtest 2026-06-03; "off" = no filter.
	gateMode := "trending"
	if decision.Action != "WAIT" && decision.Action != "HOLD" {
		gateEnabled := true
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			if v, ok := snap.BoolOk("bot.entry.regime_gate.enabled"); ok {
				gateEnabled = v
			}
			if v := snap.String("bot.entry.regime_gate.mode"); v != "" {
				gateMode = v
			}
		}
		if gateEnabled && gateMode != "off" && !regimeAllowedByMode(regimeAnalysis.Regime, gateMode) {
			bot.log.WithFields(logrus.Fields{
				"regime":     regimeAnalysis.Regime,
				"adx":        regimeAnalysis.ADX,
				"gate_mode":  gateMode,
				"confidence": fmt.Sprintf("%.2f", decision.Confidence),
				"action":     decision.Action,
			}).Warn("🚫 Regime entry gate — regime not allowed in this gate mode, forcing WAIT")
			decision.Action = "WAIT"
			decision.Confidence = 0
			decision.Reason = fmt.Sprintf("regime %s blocked by regime gate (mode=%s)", regimeAnalysis.Regime, gateMode)
			return
		}
	}

	// Directionless-regime selectivity gate: in RANGING and UNKNOWN regimes the
	// realized edge is negative (data 2026-05-29: 12 hard stops at -1.68% avg vs
	// +0.85% wins, WR collapsing to 42% in a 99%-directionless market). Require a
	// substantially higher confidence floor so the bot only takes very strong
	// setups and stops churning marginal entries into -2% stops.
	//
	// The floor is dynamic-config driven (regime.{ranging,unknown}.min_confidence,
	// default 0.80) and evaluated on the RAW confidence before size scaling below.
	if decision.Action != "WAIT" && gateMode != "ranging" &&
		(regimeAnalysis.Regime == regime.RegimeRanging || regimeAnalysis.Regime == regime.RegimeUnknown) {
		minConf := 0.80
		key := "regime.ranging.min_confidence"
		if regimeAnalysis.Regime == regime.RegimeUnknown {
			key = "regime.unknown.min_confidence"
		}
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			if v := snap.Float(key); v > 0 {
				minConf = v
			}
		}
		if decision.Confidence < minConf {
			bot.log.WithFields(logrus.Fields{
				"regime":     regimeAnalysis.Regime,
				"adx":        regimeAnalysis.ADX,
				"confidence": fmt.Sprintf("%.2f", decision.Confidence),
				"min_conf":   fmt.Sprintf("%.2f", minConf),
				"action":     decision.Action,
			}).Warn("⚠️ Directionless regime — confidence below selectivity floor, forcing WAIT")
			decision.Action = "WAIT"
			decision.Confidence = 0
			decision.Reason = fmt.Sprintf("regime %s - confidence %.2f below selectivity floor %.2f (directionless market)",
				regimeAnalysis.Regime, decision.Confidence, minConf)
			return
		}
	}

	if decision.Confidence > 0 && regimeAnalysis.SuggestedSizeMultiplier > 0 &&
		regimeAnalysis.SuggestedSizeMultiplier < 1.0 {
		original := decision.Confidence
		decision.Confidence = original * regimeAnalysis.SuggestedSizeMultiplier
		decision.Reason = fmt.Sprintf("%s | regime %s adjusted size to %.0f%%",
			decision.Reason, regimeAnalysis.Regime, regimeAnalysis.SuggestedSizeMultiplier*100)
		bot.log.WithFields(logrus.Fields{
			"regime":          regimeAnalysis.Regime,
			"size_multiplier": regimeAnalysis.SuggestedSizeMultiplier,
			"orig_conf":       original,
			"new_conf":        decision.Confidence,
		}).Info("Regime filter scaled confidence")
	}
}

// calculateKellyOrderSize computes a position size in base-asset units using the
// Kelly Criterion. Returns 0 when the calculator decides not to trade (e.g.
// negative edge); the caller should keep its existing fallback in that case.
//
// Capital cap and per-trade max default to bot.config.TradeSize-derived bounds
// because the existing TradingBotConfig does not expose AvailableCapitalUSD or
// MaxPositionSizeUSD as separate fields.
func (bot *AITradingBot) calculateKellyOrderSize(decision *ai.Decision, marketCtx *ai.MarketContext, budget float64) float64 {
	if decision == nil || marketCtx == nil || marketCtx.CurrentPrice <= 0 {
		return 0
	}

	atr := 0.0
	if primary := marketCtx.GetPrimaryContext(); primary != nil && primary.Indicators != nil {
		atr = primary.Indicators.ATR
	}

	winRate, avgWin, avgLoss := bot.getHistoricalStats()

	// budget is the resolved notional of this entry (capital-relative sizing);
	// the YAML size is only the fallback when no budget was resolved.
	tradeSize := budget
	if tradeSize <= 0 {
		bot.mu.RLock()
		tradeSize = bot.config.TradeSize
		bot.mu.RUnlock()
	}

	// Kelly capital envelope is strictly bounded by the resolved trade size
	// so it cannot inflate the position beyond what the risk config allows.
	// availableCapital == tradeSize keeps the pool equal to the budget;
	// maxPositionUSD == tradeSize is the hard ceiling (never exceed configured size).
	availableCapital := tradeSize
	maxPositionUSD := tradeSize

	// P4.1: pull ML probability + sigma when the predictor is ready.
	var mlProb, mlSigma, mlWeight float64
	if bot.mlPredictor != nil && bot.mlPredictor.IsReady() {
		if primary := marketCtx.GetPrimaryContext(); primary != nil && len(primary.Candles) >= 35 {
			fCandles := make([]featureeng.Candle, len(primary.Candles))
			for i, c := range primary.Candles {
				fCandles[i] = featureeng.Candle{
					Open: c.Open, High: c.High, Low: c.Low,
					Close: c.Close, Volume: c.Volume,
					HourUTC: c.Timestamp.UTC().Hour(),
				}
			}
			if feat := featureeng.ExtractFeatures(fCandles); feat != nil {
				ctx2, cancel2 := context.WithTimeout(context.Background(), 150*time.Millisecond)
				if pred, err := bot.mlPredictor.Predict(ctx2, feat, ""); err == nil && pred != nil {
					mlProb = pred.Probability
					mlSigma = pred.Sigma
					mlWeight = 0.40 // blend: 40% ML, 60% historical
					// Stash for the feedback loop: stamped onto ManagedPosition.MLProb
					// at position creation so the labeler can credit the ML's opinion.
					bot.setLastMLProb(mlProb)
				}
				cancel2()
			}
		}
	}

	input := &ai.KellyInput{
		Confidence:       decision.Confidence,
		WinRate:          winRate,
		AvgWinPercent:    avgWin,
		AvgLossPercent:   avgLoss,
		AvailableCapital: availableCapital,
		CurrentPrice:     marketCtx.CurrentPrice,
		ATR:              atr,
		MaxPositionUSD:   maxPositionUSD,
		MinPositionUSD:   1.0,
		MLProbability:    mlProb,
		MLSigma:          mlSigma,
		MLWeight:         mlWeight,
	}
	result := bot.riskCalculator.CalculateKellyPositionSize(input)
	bot.log.WithFields(logrus.Fields{
		"kelly_fraction":    result.KellyFraction,
		"adjusted_fraction": result.AdjustedFraction,
		"size_usd":          result.SizeUSD,
		"size_units":        result.SizeUnits,
		"applied_caps":      result.AppliedCaps,
		"reasoning":         result.Reasoning,
	}).Debug("Kelly sizing computed")

	return result.SizeUnits
}

// setLastMLProb records the ML predictor's P(TP_first) from the current entry
// attempt for later stamping onto the opened position. Guarded by lastMLProbMu
// (independent of bot.mu) to avoid lock-ordering issues.
func (bot *AITradingBot) setLastMLProb(p float64) {
	bot.lastMLProbMu.Lock()
	bot.lastMLProb = p
	bot.lastMLProbMu.Unlock()
}

// consumeLastMLProb returns the ML probability stashed during this entry
// attempt and resets it to 0, so it cannot leak into a subsequent trade that
// does not run the ML predictor.
func (bot *AITradingBot) consumeLastMLProb() float64 {
	bot.lastMLProbMu.Lock()
	p := bot.lastMLProb
	bot.lastMLProb = 0
	bot.lastMLProbMu.Unlock()
	return p
}

// mlEntryVote queries the GBM predictor for the current setup and reports whether
// its directional call agrees with the intended trade. The model returns BUY
// (proba above its threshold), SELL (below), or WAIT (no conviction); a trade is
// only "agreed" when the model's action matches the bot's action. It also stashes
// the probability for Kelly sizing + the feedback loop, avoiding a second query.
//
// evaluated=false means the model was unavailable or features could not be built;
// callers must FAIL OPEN (never block a trade just because the ML is down). This
// is the entry-quality filter the bot was missing: the GBM (PF≈2.6 / AUC≈0.82 OOS)
// was computed only for sizing, never to refuse a low-probability or wrong-way
// entry — which is why high-confidence-but-wrong shorts kept hitting stops.
func (bot *AITradingBot) mlEntryVote(marketCtx *ai.MarketContext, action string) (agrees bool, prob float64, mlAction string, evaluated bool) {
	if bot.mlPredictor == nil || !bot.mlPredictor.IsReady() || marketCtx == nil {
		return false, 0, "", false
	}
	primary := marketCtx.GetPrimaryContext()
	if primary == nil || len(primary.Candles) < 35 {
		return false, 0, "", false
	}
	fCandles := make([]featureeng.Candle, len(primary.Candles))
	for i, c := range primary.Candles {
		fCandles[i] = featureeng.Candle{
			Open: c.Open, High: c.High, Low: c.Low,
			Close: c.Close, Volume: c.Volume,
			HourUTC: c.Timestamp.UTC().Hour(),
		}
	}
	feat := featureeng.ExtractFeatures(fCandles)
	if feat == nil {
		return false, 0, "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	pred, err := bot.mlPredictor.Predict(ctx, feat, bot.currentRegimeLabel())
	if err != nil || pred == nil {
		return false, 0, "", false
	}
	bot.setLastMLProb(pred.Probability) // reuse for sizing + feedback labeling
	mlAction = strings.ToUpper(strings.TrimSpace(pred.Action))
	intended := strings.ToUpper(strings.TrimSpace(action))
	if intended == "LONG" {
		intended = "BUY"
	} else if intended == "SHORT" {
		intended = "SELL"
	}
	return mlAction == intended, pred.Probability, mlAction, true
}

// TradeStat is a minimal shape of a closed trade used for Kelly inputs.
// Decouples the bot from any concrete storage type, breaking import cycles.
type TradeStat struct {
	PnLPct float64 // P&L as percent (e.g. 1.5 = +1.5%)
}

// TradeStatsReader is the minimal interface the bot needs from the trade
// history repository.
type TradeStatsReader interface {
	GetTradeStatsByPair(pair string, limit int) ([]TradeStat, error)
}

// SetTradeStatsReader connects a trade history reader so Kelly sizing can
// derive (winRate, avgWin, avgLoss) from real outcomes. Without this, Kelly
// stays in conservative 2%-of-capital fallback mode.
func (bot *AITradingBot) SetTradeStatsReader(reader TradeStatsReader) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.tradeRepo = reader
}

// ClosedTrade carries the data the bot persists to trade_history when a
// position closes. The cmd binary adapts it to storage.Trade (see
// tradeHistoryWriterAdapter in main.go) so services stays decoupled from storage.
type ClosedTrade struct {
	ID             string
	Pair           string
	Exchange       string
	Side           string
	EntryPrice     float64
	ExitPrice      float64
	Size           float64
	Leverage       int
	StopLoss       float64
	TakeProfit     float64
	PnL            float64
	PnLPct         float64
	Fees           float64 // round-trip trading fees in USD (entry + exit)
	NetPnL         float64 // PnL after fees
	Confidence     float64
	DecisionReason string
	ExitReason     string
	EntryTime      time.Time
	ExitTime       time.Time
}

// TradeHistoryWriter persists closed trades to durable storage. Optional —
// wired via SetTradeHistoryWriter. When nil the bot still runs the in-memory
// feedback loop, but nothing reaches the trade_history table.
type TradeHistoryWriter interface {
	SaveClosedTrade(userID string, t *ClosedTrade) error
}

// DailyLossLoader can seed the bot's dailyLoss counter from durable storage on
// restart, preventing the daily cap from being silently reset by a process restart.
type DailyLossLoader interface {
	GetTodayNetLossForUser(userID string) (float64, error)
}

// ConsecutiveLossSeeder can rebuild the per-pair consecutive-loss streaks from
// durable storage on restart. Without it the auto-pause counter lives only in
// memory and every deploy/crash wipes it mid-streak — observed 2026-07-02: UNI
// took 3 consecutive exchange_sl losses but a redeploy between losses reset the
// map to 1, so the pair never paused.
type ConsecutiveLossSeeder interface {
	GetTrailingConsecutiveLosses(userID string) (map[string]int, error)
}

// GlobalLossStreakReader reports the account-wide tail of consecutive losing
// closes straight from durable storage. Drives the global streak-pause entry
// throttle (bot.streak_pause.*): wins and losses cluster in account-wide time
// windows (measured 2026-06-10→07-09 over 898 closed trades: win rate 44.4%
// after a winning close vs 32.5% after a losing one, with no per-pair lift),
// so after K consecutive losses ANY new entry is likely another loss until
// market conditions rotate. Querying trade_history on demand keeps the streak
// shared across every fleet bot and restart-proof with nothing to seed.
type GlobalLossStreakReader interface {
	GlobalTailLossStreak(userID string) (int, time.Time, error)
}

// streakPauseVerdict is the pure decision core of the global streak-pause
// throttle, split out for testability. Blocks while the tail loss streak has
// reached maxLosses AND the cooldown since that streak's latest close hasn't
// elapsed. Once the cooldown expires entries reopen: the next close either
// seals the streak (win) or re-arms the pause through a fresher lastExit.
func streakPauseVerdict(streak, maxLosses int, lastExit time.Time, cooldown time.Duration, now time.Time) bool {
	if maxLosses <= 0 || streak < maxLosses || lastExit.IsZero() {
		return false
	}
	return now.Before(lastExit.Add(cooldown))
}

// globalStreakPaused resolves the dynamic bot.streak_pause.* config and the
// live loss tail, returning whether new entries are currently blocked plus
// the streak length and resume time for logging. Fails open on any error —
// the throttle is a loss limiter, never a reason to wedge the bot shut.
func (bot *AITradingBot) globalStreakPaused(ctx context.Context) (bool, int, time.Time) {
	snap := dynamic.SnapshotFromContext(ctx)
	if snap == nil {
		return false, 0, time.Time{}
	}
	enabled := true
	if v, ok := snap.BoolOk("bot.streak_pause.enabled"); ok {
		enabled = v
	}
	if !enabled {
		return false, 0, time.Time{}
	}
	maxLosses := 2
	if n := snap.Int("bot.streak_pause.max_consecutive_losses"); n > 0 {
		maxLosses = n
	}
	cooldownMin := 60
	if n := snap.Int("bot.streak_pause.cooldown_minutes"); n > 0 {
		cooldownMin = n
	}

	bot.mu.RLock()
	reader, _ := bot.tradeHistoryWriter.(GlobalLossStreakReader)
	userID := bot.userID
	bot.mu.RUnlock()
	if reader == nil {
		return false, 0, time.Time{}
	}

	streak, lastExit, err := reader.GlobalTailLossStreak(userID)
	if err != nil {
		bot.log.WithError(err).Debug("global streak-pause: streak query failed — failing open")
		return false, 0, time.Time{}
	}
	cooldown := time.Duration(cooldownMin) * time.Minute
	if streakPauseVerdict(streak, maxLosses, lastExit, cooldown, time.Now()) {
		return true, streak, lastExit.Add(cooldown)
	}
	return false, streak, time.Time{}
}

// SetTradeHistoryWriter connects the durable trade_history sink so closed
// positions are persisted for win-rate / profit-factor analytics and Master
// Agent feedback. Also seeds dailyLoss from today's DB records if the writer
// implements DailyLossLoader, so the daily cap survives process restarts.
// Safe to call with nil to disable persistence.
func (bot *AITradingBot) SetTradeHistoryWriter(w TradeHistoryWriter) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.tradeHistoryWriter = w
	if loader, ok := w.(DailyLossLoader); ok {
		if loss, err := loader.GetTodayNetLossForUser(bot.userID); err == nil && loss < 0 {
			bot.dailyLoss = loss
			bot.log.WithFields(logrus.Fields{
				"daily_loss_seeded": loss,
				"limit":             bot.config.MaxDailyLoss,
			}).Info("🔄 dailyLoss seeded from DB — daily cap survives restart")
		}
	}
	if seeder, ok := w.(ConsecutiveLossSeeder); ok {
		if streaks, err := seeder.GetTrailingConsecutiveLosses(bot.userID); err == nil && len(streaks) > 0 {
			if bot.consecutiveLossesByPair == nil {
				bot.consecutiveLossesByPair = make(map[string]int)
			}
			for pair, n := range streaks {
				if n > bot.consecutiveLossesByPair[pair] {
					bot.consecutiveLossesByPair[pair] = n
				}
			}
			bot.log.WithFields(logrus.Fields{
				"streaks": streaks,
			}).Info("🔄 per-pair consecutive-loss streaks seeded from DB — auto-pause survives restart")
		}
	}
}

// BlacklistStore persists the toxic-pair blacklist so auto-pause survives
// restarts. Optional — wired via SetBlacklistStore. The cmd binary adapts
// *storage.BlacklistRepository to this interface.
type BlacklistStore interface {
	Save(userID, pair, reason string, until time.Time) error
	Delete(userID, pair string) error
	LoadActive(userID string) (map[string]time.Time, error)
}

// ValidationGate certifies whether a pair currently holds a validated, non-expired
// out-of-sample edge. Satisfied by *storage.ValidationRepository. When the gate is
// enabled (bot.validation_gate.enabled), the bot refuses live entries on
// uncertified pairs — enforcing "only trade validated edges".
type ValidationGate interface {
	IsCertified(pair, exchange string) bool
}

// SetValidationGate wires the strategy-validation gate (optional).
func (bot *AITradingBot) SetValidationGate(g ValidationGate) {
	bot.mu.Lock()
	bot.validationGate = g
	bot.mu.Unlock()
}

// SetBlacklistStore wires the durable blacklist store and immediately restores
// any non-expired entries into the in-memory map so a restart does not wipe
// active toxic-pair pauses.
func (bot *AITradingBot) SetBlacklistStore(s BlacklistStore) {
	bot.mu.Lock()
	bot.blacklistStore = s
	bot.mu.Unlock()
	if s == nil {
		return
	}
	if active, err := s.LoadActive(bot.effectiveUserID()); err == nil && len(active) > 0 {
		bot.mu.Lock()
		if bot.pairBlacklist == nil {
			bot.pairBlacklist = make(map[string]time.Time)
		}
		for pair, until := range active {
			bot.pairBlacklist[pair] = until
		}
		bot.mu.Unlock()
		bot.log.WithField("restored", len(active)).Info("♻️ Restored persisted pair blacklist from DB")
	}
}

// XSECBookReader reads the xsec-live basket trader's ownership registry
// (xsec_live_book — see internal/xsec/livebook.go). Satisfied by a thin
// adapter the cmd binary builds around internal/xsec.ActiveLiveBookPairs; kept
// as an interface here (rather than importing internal/xsec directly) to
// avoid an import cycle through internal/storage and internal/master.
type XSECBookReader interface {
	// ActiveLiveBookPairs returns pair -> dir (+1 long, -1 short) for every
	// position xsec-live currently owns.
	ActiveLiveBookPairs() (map[string]int, error)
}

// SetXSECBookReader wires the reader used to consult the xsec_live_book
// ownership registry (optional — nil disables the exclusion entirely).
func (bot *AITradingBot) SetXSECBookReader(r XSECBookReader) {
	bot.mu.Lock()
	bot.xsecBookReader = r
	bot.mu.Unlock()
}

// xsecOwnedPairs returns the set of pairs currently owned by the XSEC basket
// trader (xsec_live_book), keyed by normalizeSymbolForMatch(pair) so lookups
// tolerate both the dash form ("BTC-USDT") and the futures form ("BTCUSDTM")
// used across this file — or nil when the exclusion is disabled or the
// registry is unreadable. Controlled by bot.entry.xsec_exclusion.enabled
// (default true, snapshot-driven so it is hot-toggleable without a restart).
//
// Fail-open by design: a registry read error only disables the scalper's OWN
// exclusion for this cycle (it keeps trading as if xsec-live were absent) —
// it must never block the scalper from operating. The error is logged so a
// persistently broken registry is visible.
func (bot *AITradingBot) xsecOwnedPairs(ctx context.Context) map[string]int {
	enabled := true
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.BoolOk("bot.entry.xsec_exclusion.enabled"); ok {
			enabled = v
		}
	}
	if !enabled {
		return nil
	}
	bot.mu.RLock()
	reader := bot.xsecBookReader
	bot.mu.RUnlock()
	if reader == nil {
		return nil
	}
	owned, err := reader.ActiveLiveBookPairs()
	if err != nil {
		bot.log.WithError(err).Warn("xsecOwnedPairs: could not read xsec_live_book registry — exclusion skipped for this cycle")
		return nil
	}
	if len(owned) == 0 {
		return nil
	}
	out := make(map[string]int, len(owned))
	for pair, dir := range owned {
		out[normalizeSymbolForMatch(pair)] = dir
	}
	return out
}

// volatilityAdjustment is the result of evaluating volatility risk for a trade.
type volatilityAdjustment struct {
	Applied       bool    // True if any adjustment was made
	Block         bool    // True if the trade should be blocked (extreme volatility)
	BlockReason   string  // Human-readable reason for blocking
	Tier          string  // "moderate" | "high" | "extreme"
	VolatilityPct float64 // Current ATR/price percentage
	AdjustedSize  float64 // New size (reduced if volatile)
	AdjustedSLPct float64 // New SL % (widened if volatile)
}

// applyVolatilityProtection is the unified volatility-based risk filter.
// It queries the dynamic volatility.Classifier for the pair's current tier and
// applies graduated protections:
//
//   - EXTREME (Tier == extreme):  BLOCK the trade entirely (circuit breaker).
//   - VOLATILE(Tier == volatile): Reduce size 50%, widen SL by 30%.
//   - NORMAL (Tier == normal):    Reduce size 30%, widen SL by 15% if ATR is climbing.
//   - LOW    (Tier == low):       No adjustment.
//
// The classifier maintains its own DB-backed table that's refreshed every 15 min
// from live ATR observations — so pairs migrate between tiers as their market
// behavior changes, with NO hardcoded volatile-pair lists.
//
// Falls back to legacy hardcoded behavior when no classifier is wired (safety net).
//
// Rationale: reducing tradeSize cuts the notional, which DIRECTLY reduces the
// absolute USD loss when SL hits. Widening SL gives the position room to survive
// normal-range wicks. Reducing leverage alone does NOT reduce absolute loss —
// only the margin requirement — so we no longer touch leverage here.
// effectiveMaxOpenPositions returns the live max-open-positions limit, reading
// the dynamic config table first (bot.max_open_positions) so the AI regulator
// and operators can change it in real time, falling back to the static config.
func (bot *AITradingBot) effectiveMaxOpenPositions(ctx context.Context) int {
	bot.mu.RLock()
	v := bot.config.MaxOpenPositions
	bot.mu.RUnlock()
	if v <= 0 {
		v = 2
	}
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if n := snap.Int("bot.max_open_positions"); n > 0 {
			v = n
		}
	}
	return v
}

// regulateMaxOpenPositions is the AI real-time regulator for the concurrent
// position limit. It derives a target from deployable capital, the current
// volatility regime and recent performance, then persists it to the dynamic
// config table (bot.max_open_positions) so every component — and the next
// restart — sees the same value. Disable via bot.max_open_positions.auto_regulate.
func (bot *AITradingBot) regulateMaxOpenPositions(ctx context.Context, volatility float64) {
	if bot.configStore == nil {
		return
	}
	snap := dynamic.SnapshotFromContext(ctx)
	if snap != nil {
		if v, ok := snap.BoolOk("bot.max_open_positions.auto_regulate"); ok && !v {
			return // operator pinned a manual value
		}
	}

	sizing := bot.SizingStatus(ctx)
	bot.mu.RLock()
	perPos := bot.config.TradeSize
	consecLosses := bot.consecutiveLosses
	dailyLoss := bot.dailyLoss
	maxDailyLoss := bot.config.MaxDailyLoss
	bot.mu.RUnlock()
	if sizing.BaseNotionalUSD > 0 {
		perPos = sizing.BaseNotionalUSD
	}
	if sizing.DailyLossLimitUSD > 0 {
		maxDailyLoss = sizing.DailyLossLimitUSD
	}
	if perPos <= 0 {
		perPos = 2.0
	}

	// Deployable capital: prefer the swarm config value, else a safe default.
	deployable := 30.0
	if snap != nil {
		if v := snap.Float("multi_pair.total_capital_usd"); v > 0 {
			deployable = v
		}
	}

	// Base target: how many positions the capital supports, keeping 20% reserve.
	target := int((deployable * 0.8) / perPos)
	reasons := make([]string, 0, 3)

	// Volatility regime — throttle exposure when the market is hot.
	switch {
	case volatility >= 0.045:
		target = 1
		reasons = append(reasons, "extreme_volatility")
	case volatility >= 0.025:
		target--
		reasons = append(reasons, "high_volatility")
	}

	// Recent performance — go defensive on a losing streak or deep drawdown.
	if consecLosses >= 2 {
		target = 1
		reasons = append(reasons, fmt.Sprintf("losing_streak=%d", consecLosses))
	}
	if maxDailyLoss > 0 && dailyLoss < 0 && (-dailyLoss) >= maxDailyLoss*0.6 {
		target = 1
		reasons = append(reasons, "daily_drawdown")
	}

	// Clamp to the manifest range [1, 6].
	if target < 1 {
		target = 1
	}
	if target > 6 {
		target = 6
	}

	current := bot.effectiveMaxOpenPositions(ctx)
	if target == current {
		return
	}

	val := &dyndomain.ParameterValue{
		Key:       "bot.max_open_positions",
		ScopeType: dyndomain.ScopeGlobal,
		ScopeID:   "*",
		Value:     strconv.Itoa(target),
		UpdatedBy: "bot_regulator",
		Source:    dyndomain.SourceFeedbackLoop,
	}
	if err := bot.configStore.Set(ctx, val, "AI real-time regulation of max open positions"); err != nil {
		bot.log.WithError(err).Warn("Failed to persist regulated max_open_positions")
		return
	}
	bot.mu.Lock()
	bot.config.MaxOpenPositions = target
	bot.mu.Unlock()
	bot.log.WithFields(logrus.Fields{
		"from":    current,
		"to":      target,
		"reasons": reasons,
		"vol_pct": fmt.Sprintf("%.2f%%", volatility*100),
	}).Info("🎚️ max_open_positions auto-regulated")
}

// resolveDynamicLeverage scales leverage down on volatile pairs. Leverage
// multiplies absolute $ PnL but not the price-% stop distance, so cutting it on
// volatile pairs directly shrinks the worst-case loss while leaving the SL where
// it is. Stable (low-tier) pairs keep full leverage to preserve upside.
// Controlled by dynamic key bot.dynamic_leverage.enabled (default on).
func (bot *AITradingBot) resolveDynamicLeverage(ctx context.Context, pair string, baseLeverage int) int {
	if bot.volClassifier == nil || baseLeverage <= 1 {
		return baseLeverage
	}
	enabled := true
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.BoolOk("bot.dynamic_leverage.enabled"); ok {
			enabled = v
		}
	}
	if !enabled {
		return baseLeverage
	}

	tier := bot.volClassifier.Classify(pair)
	lev := baseLeverage
	switch tier {
	case volatility.TierExtreme:
		lev = 1 // minimal exposure on extreme moves
	case volatility.TierVolatile:
		lev = baseLeverage / 2
	case volatility.TierNormal:
		lev = int(float64(baseLeverage) * 0.8)
	default: // TierLow — full leverage
		lev = baseLeverage
	}
	if lev < 1 {
		lev = 1
	}
	if lev != baseLeverage {
		bot.log.WithFields(logrus.Fields{
			"pair":              pair,
			"tier":              string(tier),
			"base_leverage":     baseLeverage,
			"adjusted_leverage": lev,
		}).Info("⚙️ Dynamic leverage by volatility tier")
	}
	return lev
}

func (bot *AITradingBot) applyVolatilityProtection(ctx context.Context, pair string, currentPrice, atrAbsolute, currentSize, currentSLPct float64) volatilityAdjustment {
	adj := volatilityAdjustment{
		AdjustedSize:  currentSize,
		AdjustedSLPct: currentSLPct,
	}

	if currentPrice <= 0 || currentSize <= 0 {
		return adj
	}

	// ── 1. Master toggle (default: enabled) ─────────────────────────────────
	enabled := true
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v, ok := snap.BoolOk("volatility.enabled"); ok {
			enabled = v
		}
	}
	if !enabled {
		return adj
	}

	// ── 2. Current ATR% — prefer caller-supplied value (fresh from marketCtx),
	// fall back to indicatorCache for downstream paths that may not have it. ─
	atr := atrAbsolute
	if atr <= 0 {
		atr = bot.estimateATR(pair)
	}
	if atr <= 0 {
		return adj // No ATR data yet — skip silently
	}
	volPct := (atr / currentPrice) * 100
	adj.VolatilityPct = volPct

	// ── 3. Determine tier dynamically from classifier ───────────────────────
	// When wired, the classifier returns the pair's current tier based on its
	// recent observed ATR/price ratio — fully dynamic, no hardcoded lists.
	// Also push the current observation back into the classifier so it learns
	// from this call too (cheap inline update).
	var tier volatility.Tier
	if bot.volClassifier != nil {
		_ = bot.volClassifier.RecordATR(ctx, pair, volPct)
		tier = bot.volClassifier.Classify(pair)
	} else {
		// Fallback: derive tier from default thresholds.
		tier = volatility.DefaultThresholds().TierFromATRPct(volPct)
	}

	// ── 4. Apply graduated protections based on the resolved tier ───────────
	switch tier {
	case volatility.TierExtreme:
		adj.Applied = true
		adj.Block = true
		adj.Tier = string(tier)
		adj.BlockReason = fmt.Sprintf("tier=extreme atr=%.2f%% (likely news/manipulation)", volPct)
	case volatility.TierVolatile:
		adj.Applied = true
		adj.Tier = string(tier)
		adj.AdjustedSize = currentSize * 0.50   // -50% size
		adj.AdjustedSLPct = currentSLPct * 1.30 // +30% SL room
	case volatility.TierNormal:
		// Only act if currently climbing past the normal upper bound.
		if cl, ok := bot.volClassifier.Get(pair); ok && cl.CurrentATRPct >= 2.0 {
			adj.Applied = true
			adj.Tier = string(tier)
			adj.AdjustedSize = currentSize * 0.70   // -30% size
			adj.AdjustedSLPct = currentSLPct * 1.15 // +15% SL room
		}
	case volatility.TierLow:
		// no adjustment
	}

	return adj
}

// computeATRFromCandles returns Wilder's smoothed ATR over the given candles.
//
// Used as a fallback when the upstream analyzer didn't populate Indicators.ATR
// (some paths leave it 0). It needs at least `period+1` candles; with fewer
// it returns the simple average of available true ranges, which is a
// reasonable approximation while we warm up.
//
// Math:
//
//	TR_i  = max(high_i-low_i, |high_i-close_{i-1}|, |low_i-close_{i-1}|)
//	ATR_0 = mean(TR_1 .. TR_period)
//	ATR_i = ((period-1) * ATR_{i-1} + TR_i) / period   (Wilder smoothing)
//
// macroTrendEntry is one cached per-pair macro-trend snapshot.
type macroTrendEntry struct {
	distancePct     float64 // (close - EMA) / EMA
	recentChangePct float64 // (close - close[-N]) / close[-N] over the momentum lookback
	ema             float64
	currentClose    float64
	at              time.Time
}

// macroTrendDistance returns the pair's distance from its higher-timeframe EMA
// ((close-EMA)/EMA), the recent momentum (price change over momentum_lookback
// bars), the EMA, and the latest close. ok=false when candles can't be fetched
// (callers fail open). Result is cached per-pair for bot.trend_filter.cache_ttl
// so the trend filter and the directional-bias path (runAnalysis) share ONE
// candle fetch per pair per cycle. Cache is keyed strictly by pair and
// mutex-guarded — no cross-pair leak.
func (bot *AITradingBot) macroTrendDistance(ctx context.Context, exchange, pair string) (distancePct, recentChangePct, ema, currentClose float64, ok bool) {
	if pair == "" || bot.gctClient == nil {
		return 0, 0, 0, 0, false
	}

	timeframe := "1h"
	emaPeriod := 50
	lookback := 60
	momentumLookback := 6
	fetchTimeout := 8 * time.Second
	cacheTTL := 30 * time.Second
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.String("bot.trend_filter.timeframe"); v != "" {
			timeframe = v
		}
		if v := snap.Int("bot.trend_filter.ema_period"); v > 0 {
			emaPeriod = v
		}
		if v := snap.Int("bot.trend_filter.lookback"); v > 0 {
			lookback = v
		}
		if v := snap.Int("bot.trend_filter.momentum_lookback"); v > 0 {
			momentumLookback = v
		}
		if v := snap.Duration("bot.trend_filter.fetch_timeout"); v > 0 {
			fetchTimeout = v
		}
		if v := snap.Duration("bot.trend_filter.cache_ttl"); v > 0 {
			cacheTTL = v
		}
	}
	if lookback < emaPeriod {
		lookback = emaPeriod
	}

	// Serve from cache when fresh.
	bot.macroTrendCacheMu.Lock()
	if e, found := bot.macroTrendCache[pair]; found && time.Since(e.at) < cacheTTL {
		bot.macroTrendCacheMu.Unlock()
		return e.distancePct, e.recentChangePct, e.ema, e.currentClose, true
	}
	bot.macroTrendCacheMu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	candles, err := bot.gctClient.GetCandles(fetchCtx, exchange, pair, timeframe, lookback)
	cancel()
	if err != nil || len(candles) < emaPeriod {
		return 0, 0, 0, 0, false
	}
	// Defensive guard: ensure ascending (GetCandles already guarantees this; no-op
	// when already ascending, never double-reverses).
	if len(candles) >= 2 && candles[0].Timestamp.After(candles[len(candles)-1].Timestamp) {
		for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
			candles[i], candles[j] = candles[j], candles[i]
		}
	}
	ema = computeEMAFromCandles(candles, emaPeriod)
	if ema <= 0 {
		return 0, 0, 0, 0, false
	}
	currentClose = candles[len(candles)-1].Close
	distancePct = (currentClose - ema) / ema

	// Recent momentum: price change over the last momentumLookback bars. Distinguishes
	// a healthy trend (rising/flat) from one that is rolling over (a pair can sit
	// above its lagging EMA50 while actively correcting — buying that "pullback" is
	// buying the reversal). Used by macroTrendLabel to gate the directional bias.
	if momentumLookback > 0 && len(candles) > momentumLookback {
		past := candles[len(candles)-1-momentumLookback].Close
		if past > 0 {
			recentChangePct = (currentClose - past) / past
		}
	}

	bot.macroTrendCacheMu.Lock()
	bot.macroTrendCache[pair] = macroTrendEntry{distancePct: distancePct, recentChangePct: recentChangePct, ema: ema, currentClose: currentClose, at: time.Now()}
	bot.macroTrendCacheMu.Unlock()

	return distancePct, recentChangePct, ema, currentClose, true
}

// macroTrendLabel classifies the pair's macro trend as "bullish" / "bearish" /
// "neutral" for the directional-bias feature. A direction is only confirmed when
// BOTH (a) price is on the right side of the EMA by more than the neutral band
// AND (b) recent momentum does not strongly contradict it. The momentum guard is
// the fix for "buying corrections": a pair above its lagging EMA50 but falling
// hard recently is rolling over, not pulling back — return neutral so trend_follow
// does not force a long into it (legacy mean-reversion then applies, usually WAIT).
// Returns "" when the trend can't be computed (directional bias then stays off).
func (bot *AITradingBot) macroTrendLabel(ctx context.Context, exchange, pair string) string {
	distancePct, recentChangePct, _, _, ok := bot.macroTrendDistance(ctx, exchange, pair)
	if !ok {
		return ""
	}
	band := 0.003      // 0.3%
	maxAdverse := 0.02 // 2.0% — max recent move AGAINST the trend before it's "rolling over"
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Float("bot.trend_filter.neutral_band_pct"); v > 0 {
			band = v / 100
		} else if v := snap.Float("bot.trend_filter.min_distance_pct"); v > 0 {
			band = v / 100
		}
		if v := snap.Float("bot.trend_filter.max_adverse_move_pct"); v > 0 {
			maxAdverse = v / 100
		}
	}
	// Bullish: above EMA AND not falling more than maxAdverse recently.
	if distancePct > band && recentChangePct >= -maxAdverse {
		return "bullish"
	}
	// Bearish: below EMA AND not rising more than maxAdverse recently.
	if distancePct < -band && recentChangePct <= maxAdverse {
		return "bearish"
	}
	return "neutral"
}

// ──────────────────────────────────────────────────────────────────────────
// Active Profit Engine — exit-confirmation TF ladder (Phase 2)
//
// "Operate on X, confirm the exit on a finer TF." When bot.exit_ladder.enabled,
// a soft profit-taking exit (wick/condition) is only allowed to fire if the
// finer confirmation TF ALSO shows the move turning against the position over
// `required_confirmations` recent candles — anti-whipsaw. The confirmation TF is
// derived automatically from the operating TF (5m→1m, 15m→5m, 1h→30m...) or set
// manually per pair. Disabled by default → behaviour byte-identical.
// ──────────────────────────────────────────────────────────────────────────

type exitLadderCacheEntry struct {
	candles []*gct.Candle
	at      time.Time
}

// positionIsFutures reports whether a position trades on the futures market
// (leveraged). Used to pick the supported-granularity ladder (futures lacks 3m).
func positionIsFutures(pos *ai.ManagedPosition) bool {
	return pos.Leverage > 0
}

// exitConfirmationTF resolves the finer confirmation timeframe for this position
// from bot.exit_ladder.* config, or "" when the ladder is disabled/unresolvable.
func (bot *AITradingBot) exitConfirmationTF(ctx context.Context, pos *ai.ManagedPosition) string {
	cfg := dynamic.SnapshotFromContext(ctx)
	if cfg == nil || !cfg.Bool("bot.exit_ladder.enabled") {
		return ""
	}
	mode := "auto"
	if v := cfg.String("bot.exit_ladder.mode"); v != "" {
		mode = v
	}
	if mode == "off" {
		return ""
	}
	isFutures := positionIsFutures(pos)
	marketAware := true
	if v, ok := cfg.BoolOk("bot.exit_ladder.market_aware"); ok {
		marketAware = v
	}
	supported := func(tf string) bool {
		return !marketAware || !isFutures || gct.IsGranularitySupported(tf, true)
	}

	if mode == "manual" {
		if tf := cfg.String("bot.exit_ladder.confirmation_tf"); tf != "" && supported(tf) {
			return tf
		}
		// Ordered CSV fallback: first market-supported TF wins.
		if csv := cfg.String("bot.exit_ladder.confirmation_tfs"); csv != "" {
			for _, raw := range strings.Split(csv, ",") {
				tf := strings.TrimSpace(raw)
				if tf != "" && supported(tf) {
					return tf
				}
			}
		}
		return ""
	}

	// auto: derive from the operating (primary) timeframe.
	primary := "5m"
	if len(bot.config.Timeframes) > 0 && bot.config.Timeframes[0] != "" {
		primary = bot.config.Timeframes[0]
	}
	steps := 1
	if v := cfg.Int("bot.exit_ladder.steps_down"); v > 0 {
		steps = v
	}
	return gct.DeriveConfirmationTF(primary, isFutures, steps)
}

// exitLadderCandles fetches (and caches per pair+TF) the finer confirmation-TF
// candles. Returns ascending candles; ok=false on fetch error so callers fail
// open (never gate the exit on a fetch failure).
func (bot *AITradingBot) exitLadderCandles(ctx context.Context, pos *ai.ManagedPosition, tf string, limit int, timeout, cacheTTL time.Duration) ([]*gct.Candle, bool) {
	if bot.gctClient == nil || pos.Symbol == "" {
		return nil, false
	}
	key := pos.Symbol + "|" + tf
	bot.exitLadderCacheMu.Lock()
	if e, found := bot.exitLadderCache[key]; found && time.Since(e.at) < cacheTTL {
		c := e.candles
		bot.exitLadderCacheMu.Unlock()
		return c, true
	}
	bot.exitLadderCacheMu.Unlock()

	exch := pos.Exchange
	if exch == "" {
		exch = bot.config.Exchange
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	candles, err := bot.gctClient.GetCandles(fetchCtx, exch, pos.Symbol, tf, limit)
	cancel()
	if err != nil || len(candles) == 0 {
		return nil, false
	}
	// Defensive ascending guard (GetCandles guarantees ascending; no-op when
	// already ascending). Futures candle timestamps can be unreliable, so we rely
	// on ORDER, never absolute timestamps.
	if len(candles) >= 2 && candles[0].Timestamp.After(candles[len(candles)-1].Timestamp) {
		for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
			candles[i], candles[j] = candles[j], candles[i]
		}
	}
	bot.exitLadderCacheMu.Lock()
	bot.exitLadderCache[key] = exitLadderCacheEntry{candles: candles, at: time.Now()}
	bot.exitLadderCacheMu.Unlock()
	return candles, true
}

// candlesConfirmReversal reports whether the last `required` candles ALL closed
// against the position (bearish for long, bullish for short). Pure for testing.
func candlesConfirmReversal(candles []*gct.Candle, isLong bool, required int) bool {
	if required < 1 || len(candles) < required {
		return false
	}
	for _, c := range candles[len(candles)-required:] {
		against := (isLong && c.Close < c.Open) || (!isLong && c.Close > c.Open)
		if !against {
			return false
		}
	}
	return true
}

// exitLadderConfirmsReversal gates a soft exit. Returns true to ALLOW the exit.
// When the ladder is disabled it always returns true (no gating, fail-open).
// When enabled, it returns true only if the finer confirmation TF shows
// `required_confirmations` recent candles closing against the position; if the
// finer TF does not confirm, it returns false so the exit is deferred. A fetch
// failure also fails open (returns true) so we never trap a position.
func (bot *AITradingBot) exitLadderConfirmsReversal(ctx context.Context, pos *ai.ManagedPosition) bool {
	tf := bot.exitConfirmationTF(ctx, pos)
	if tf == "" {
		return true // ladder disabled/unresolved — don't gate
	}
	cfg := dynamic.SnapshotFromContext(ctx)
	required, limit := 1, 100
	timeout := 8 * time.Second
	cacheTTL := 30 * time.Second
	if cfg != nil {
		if v := cfg.Int("bot.exit_ladder.required_confirmations"); v > 0 {
			required = v
		}
		if v := cfg.Int("bot.exit_ladder.candle_limit"); v > 0 {
			limit = v
		}
		if v := cfg.Duration("bot.exit_ladder.fetch_timeout"); v > 0 {
			timeout = v
		}
		if v := cfg.Duration("bot.exit_ladder.cache_ttl"); v > 0 {
			cacheTTL = v
		}
	}
	// 1m is noisy — require at least 2 confirming candles.
	if tf == "1m" && required < 2 {
		required = 2
	}
	candles, ok := bot.exitLadderCandles(ctx, pos, tf, limit, timeout, cacheTTL)
	if !ok || len(candles) < required {
		return true // fail-open: can't confirm → don't block the exit
	}
	isLong := pos.Side == "buy" || pos.Side == "long"
	confirmed := candlesConfirmReversal(candles, isLong, required)
	if !confirmed {
		bot.log.WithFields(logrus.Fields{
			"position_id": pos.ID, "symbol": pos.Symbol,
			"confirm_tf": tf, "required": required,
		}).Debug("⏳ Exit ladder: finer TF does not confirm reversal — deferring soft exit")
	}
	return confirmed
}

// computeEMAFromCandles returns the N-period EMA of close prices using
// exponential smoothing (k = 2/(period+1)). The first `period` closes seed the
// EMA as their simple average; subsequent closes are smoothed. Requires
// ≥`period` candles. The period is supplied by the caller (dynamic config:
// bot.trend_filter.ema_period) so the macro-trend timeframe is fully tunable.
func computeEMAFromCandles(candles []*gct.Candle, period int) float64 {
	if period <= 0 {
		period = 50
	}
	if len(candles) < period {
		return 0
	}
	// Seed with SMA of first period candles.
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	ema := sum / float64(period)
	k := 2.0 / float64(period+1)
	for i := period; i < len(candles); i++ {
		ema = candles[i].Close*k + ema*(1-k)
	}
	return ema
}

func computeATRFromCandles(candles []ai.Candle, period int) float64 {
	if period <= 0 {
		period = 14
	}
	if len(candles) < 2 {
		return 0
	}

	trs := make([]float64, 0, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		c := candles[i]
		prevClose := candles[i-1].Close
		hl := c.High - c.Low
		hc := math.Abs(c.High - prevClose)
		lc := math.Abs(c.Low - prevClose)
		tr := hl
		if hc > tr {
			tr = hc
		}
		if lc > tr {
			tr = lc
		}
		trs = append(trs, tr)
	}

	if len(trs) < period {
		// Not enough data for full Wilder smoothing — return simple mean.
		var sum float64
		for _, tr := range trs {
			sum += tr
		}
		return sum / float64(len(trs))
	}

	// Seed with simple mean of first `period` true ranges.
	var seed float64
	for i := 0; i < period; i++ {
		seed += trs[i]
	}
	atr := seed / float64(period)

	// Wilder smoothing for the remaining samples.
	for i := period; i < len(trs); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}
	return atr
}

// defaultPostSLCooldown is the re-entry block after a stop-loss hit on a pair.
// Configurable at runtime via dynamic config key bot.post_sl_cooldown.
const defaultPostSLCooldown = 5 * time.Minute

// defaultStaleCooldown is the re-analysis pause after staleSignalMaxConsecutive
// consecutive stale-signal blocks on the same pair. Prevents burning LLM quota
// when market price has moved far from the cached analysis snapshot.
const defaultStaleCooldown = 10 * time.Minute

// staleSignalMaxConsecutive is the number of consecutive stale-signal blocks
// before a stale cooldown is triggered for that pair.
const staleSignalMaxConsecutive = 3

// setPairCooldown blocks new entries on `pair` for the post-SL window.
// Called whenever a stop-loss is hit (either detected internally by
// checkPosition or externally by the exchange). Without this, the bot can
// reopen the same direction within seconds and bleed cumulative losses.
// The window is read live from dynamic config bot.post_sl_cooldown (the key was
// documented but previously never read — the cooldown was always the hardcoded
// 5min, which is why a configured 30min behaved like ~5-6min in practice).
func (bot *AITradingBot) setPairCooldown(ctx context.Context, pair string) {
	cooldown := defaultPostSLCooldown
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if d := snap.Duration("bot.post_sl_cooldown"); d > 0 {
			cooldown = d
		}
	}
	bot.setPairCooldownFor(pair, cooldown, "Post-SL cooldown")
}

// setPairCooldownFor blocks new entries on `pair` for duration d. Never shortens
// an existing longer cooldown (extend-only), so a manual re-entry hold can't be
// clobbered by a shorter post-SL cooldown.
func (bot *AITradingBot) setPairCooldownFor(pair string, d time.Duration, reason string) {
	if pair == "" || d <= 0 {
		return
	}
	bot.pairCooldownMu.Lock()
	defer bot.pairCooldownMu.Unlock()
	if bot.pairCooldowns == nil {
		bot.pairCooldowns = make(map[string]time.Time)
	}
	until := time.Now().Add(d)
	if existing, ok := bot.pairCooldowns[pair]; ok && existing.After(until) {
		return // keep the longer existing hold
	}
	bot.pairCooldowns[pair] = until
	bot.log.WithFields(logrus.Fields{
		"pair":           pair,
		"cooldown_until": until.Format("2006-01-02 15:04:05"),
		"reason":         reason,
	}).Info("🧊 Pair cooldown set — re-entry blocked")
}

// applyReentryPolicy applies the per-pair re-entry policy after a position
// closes, read live from dynamic config (bot.reentry.mode):
//   - "auto"     → no extra hold (legacy; post-SL cooldown still applies on losses)
//   - "cooldown" → block re-entry for bot.reentry.cooldown_minutes
//   - "manual"   → hold the pair until the operator re-enables it (set mode back
//     to auto, or clear the cooldown). This is what lets you take profit manually
//     on a pair (anticipating a pullback) without the bot re-opening it.
func (bot *AITradingBot) applyReentryPolicy(ctx context.Context, pair string) {
	if pair == "" {
		return
	}
	mode := "auto"
	cooldownMin := 30
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.String("bot.reentry.mode"); v != "" {
			mode = v
		}
		if n := snap.Int("bot.reentry.cooldown_minutes"); n > 0 {
			cooldownMin = n
		}
	}
	switch mode {
	case "cooldown":
		bot.setPairCooldownFor(pair, time.Duration(cooldownMin)*time.Minute, "reentry policy: cooldown")
	case "manual":
		bot.reentryHoldMu.Lock()
		if bot.reentryHolds == nil {
			bot.reentryHolds = make(map[string]bool)
		}
		bot.reentryHolds[pair] = true
		bot.reentryHoldMu.Unlock()
		bot.log.WithField("pair", pair).Info("✋ Manual re-entry hold — pair paused until operator sets bot.reentry.mode back to auto/cooldown")
	}
}

// reentryHeld reports whether `pair` is under a manual re-entry hold. The hold
// auto-clears the moment the operator switches that pair's bot.reentry.mode away
// from "manual" — so re-enabling is just a config change, no cooldown to clear.
func (bot *AITradingBot) reentryHeld(ctx context.Context, pair string) bool {
	bot.reentryHoldMu.Lock()
	held := bot.reentryHolds[pair]
	bot.reentryHoldMu.Unlock()
	if !held {
		return false
	}
	mode := "auto"
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.String("bot.reentry.mode"); v != "" {
			mode = v
		}
	}
	if mode != "manual" {
		// Operator re-enabled the pair — release the hold.
		bot.reentryHoldMu.Lock()
		delete(bot.reentryHolds, pair)
		bot.reentryHoldMu.Unlock()
		return false
	}
	return true
}

// pairCooldownRemaining returns how long until `pair` can be traded again, or
// 0 if no cooldown is active. Reads bot.post_sl_cooldown from dynamic config
// each call so changes apply without restart.
func (bot *AITradingBot) pairCooldownRemaining(pair string) time.Duration {
	if pair == "" {
		return 0
	}
	bot.pairCooldownMu.Lock()
	defer bot.pairCooldownMu.Unlock()
	if bot.pairCooldowns == nil {
		return 0
	}
	until, ok := bot.pairCooldowns[pair]
	if !ok {
		return 0
	}
	rem := time.Until(until)
	if rem <= 0 {
		delete(bot.pairCooldowns, pair)
		return 0
	}
	return rem
}

// recordStaleSignal increments the consecutive stale-signal counter for pair.
// After staleSignalMaxConsecutive hits, it triggers a stale cooldown via pairCooldowns
// to stop burning LLM quota while the analysis snapshot is outdated.
func (bot *AITradingBot) recordStaleSignal(pair string) {
	if pair == "" {
		return
	}
	bot.staleSignalCountMu.Lock()
	if bot.staleSignalCounts == nil {
		bot.staleSignalCounts = make(map[string]int)
	}
	bot.staleSignalCounts[pair]++
	count := bot.staleSignalCounts[pair]
	bot.staleSignalCountMu.Unlock()

	if count >= staleSignalMaxConsecutive {
		bot.staleSignalCountMu.Lock()
		bot.staleSignalCounts[pair] = 0 // reset so it can fire again later
		bot.staleSignalCountMu.Unlock()

		bot.pairCooldownMu.Lock()
		if bot.pairCooldowns == nil {
			bot.pairCooldowns = make(map[string]time.Time)
		}
		until := time.Now().Add(defaultStaleCooldown)
		bot.pairCooldowns[pair] = until
		bot.pairCooldownMu.Unlock()

		bot.log.WithFields(logrus.Fields{
			"pair":           pair,
			"stale_count":    count,
			"cooldown_until": until.Format("15:04:05"),
			"cooldown_min":   int(defaultStaleCooldown.Minutes()),
		}).Warn("⏸️ Stale signal cooldown — analysis snapshot too far from live price, pausing LLM analysis")
	}
}

// resetStaleSignalCount clears the stale-signal counter for pair (called on successful trade entry).
func (bot *AITradingBot) resetStaleSignalCount(pair string) {
	if pair == "" {
		return
	}
	bot.staleSignalCountMu.Lock()
	defer bot.staleSignalCountMu.Unlock()
	if bot.staleSignalCounts != nil {
		delete(bot.staleSignalCounts, pair)
	}
}

// recordTrendBlock increments the consecutive macro-trend-block counter for pair.
// After bot.trend_filter.max_blocks_before_rotate (default 3) consecutive blocks,
// it transiently blacklists the pair so maybeSwitchPair rotates to a tradeable
// opportunity — otherwise the bot spins forever generating counter-trend signals
// on the master's pumped top pick (e.g. shorting WLD +20% above its EMA50).
func (bot *AITradingBot) recordTrendBlock(ctx context.Context, pair string) {
	if pair == "" {
		return
	}
	maxBlocks := 3
	if snap := dynamic.SnapshotFromContext(ctx); snap != nil {
		if v := snap.Int("bot.trend_filter.max_blocks_before_rotate"); v > 0 {
			maxBlocks = v
		}
	}
	bot.trendBlockCountMu.Lock()
	if bot.trendBlockCounts == nil {
		bot.trendBlockCounts = make(map[string]int)
	}
	bot.trendBlockCounts[pair]++
	count := bot.trendBlockCounts[pair]
	if count >= maxBlocks {
		bot.trendBlockCounts[pair] = 0 // reset so it can fire again after rotation
	}
	bot.trendBlockCountMu.Unlock()

	if count >= maxBlocks {
		bot.log.WithFields(logrus.Fields{
			"pair":        pair,
			"block_count": count,
		}).Warn("🔄 Trend filter blocked entry repeatedly — rotating away from counter-trend pair")
		// Transitory blacklist (5 min) so the bot rotates to the next opportunity.
		bot.blacklistPair(pair, "trend filter: repeated counter-trend blocks")
	}
}

// resetTrendBlockCount clears the trend-block counter for pair (called when the
// trend filter passes / a trade succeeds).
func (bot *AITradingBot) resetTrendBlockCount(pair string) {
	if pair == "" {
		return
	}
	bot.trendBlockCountMu.Lock()
	defer bot.trendBlockCountMu.Unlock()
	if bot.trendBlockCounts != nil {
		delete(bot.trendBlockCounts, pair)
	}
}

// estimateATR reads the cached ATR for the pair from the latest indicator snapshot.
// Returns 0 if no snapshot is available yet (early bot lifecycle).
func (bot *AITradingBot) estimateATR(pair string) float64 {
	bot.indicatorCacheMu.RLock()
	defer bot.indicatorCacheMu.RUnlock()

	snap, exists := bot.indicatorCache[pair]
	if !exists || snap == nil {
		return 0
	}
	return snap.ATR
}

// historicalStatsSampleSize is how many recent closed trades feed the Kelly
// inputs. 30 is a sweet spot — enough for stable averages, recent enough to
// reflect current market regime.
const historicalStatsSampleSize = 30

// getHistoricalStats returns historical (winRate, avgWinPct, avgLossPct) for
// the configured pair from real closed trades. Returns zeros if no repo is
// wired or there's not enough data, which forces CalculateKellyPositionSize
// to fall back to its conservative 2%-of-capital sizing.
func (bot *AITradingBot) getHistoricalStats() (winRate, avgWinPct, avgLossPct float64) {
	if bot.tradeRepo == nil || bot.config == nil || bot.config.Pair == "" {
		return 0, 0, 0
	}

	trades, err := bot.tradeRepo.GetTradeStatsByPair(bot.config.Pair, historicalStatsSampleSize)
	if err != nil || len(trades) < 5 {
		return 0, 0, 0
	}

	var wins, losses int
	var sumWinPct, sumLossPct float64
	for _, t := range trades {
		pct := t.PnLPct / 100.0 // PnLPct is stored as %; Kelly expects fractional
		if pct > 0 {
			wins++
			sumWinPct += pct
		} else if pct < 0 {
			losses++
			sumLossPct += -pct
		}
	}

	total := wins + losses
	if total < 5 {
		return 0, 0, 0
	}
	winRate = float64(wins) / float64(total)
	if wins > 0 {
		avgWinPct = sumWinPct / float64(wins)
	}
	if losses > 0 {
		avgLossPct = sumLossPct / float64(losses)
	}
	return winRate, avgWinPct, avgLossPct
}

// --- ML weight store (P3.2 / P3.4) ---

// mlWeightStore returns the Bayesian-calibrated voter weight for the ML source.
// *feedback.Calibrator satisfies this with its MLWeightFor method.
// sourceWeightStore optionally exposes per-source Bayesian weights (technical/
// llm/ml/regime) for the exit-decision fusion when
// bot.exit.fusion.use_calibrator_weights is enabled. *feedback.Calibrator
// satisfies it via SourceWeightFor. The fusion type-asserts the wired
// mlWeightStore to this interface, so wiring stays optional.
type sourceWeightStore interface {
	SourceWeightFor(source, regime string) float64
}

type mlWeightStore interface {
	// MLWeightFor returns the effective weight for the ML voter in the given regime.
	// Returns 0 when no calibration data is available (caller uses fallback).
	MLWeightFor(regime string) float64
}

// SetMLWeightStore wires the Bayesian calibrator weight reader (optional).
func (bot *AITradingBot) SetMLWeightStore(s mlWeightStore) {
	bot.mu.Lock()
	bot.mlWeightStore = s
	bot.mu.Unlock()
}

// --- Feedback Labeler (P3.1) ---

// tradeLabeler labels closed trades in feature_store with real outcomes.
// *feedback.Labeler satisfies this interface.
type tradeLabeler interface {
	Label(ctx context.Context, o *feedbackOutcome)
}

// feedbackOutcome mirrors feedback.TradeOutcome to avoid the import cycle
// (services → feedback). The cmd binary wires the concrete type.
type feedbackOutcome struct {
	Pair          string
	Exchange      string
	Side          string
	EntryPrice    float64
	ExitPrice     float64
	EntryTime     time.Time
	ExitTime      time.Time
	PnLPct        float64
	ExitReason    string
	Confidence    float64
	Regime        string
	MLProb        float64
	TechnicalVote string
	LLMVote       string
	TechnicalConf float64
	LLMConf       float64
}

// SetFeedbackLabeler wires the post-trade labeler (optional).
func (bot *AITradingBot) SetFeedbackLabeler(l tradeLabeler) {
	bot.mu.Lock()
	bot.feedbackLabeler = l
	bot.mu.Unlock()
}

// --- ML Predictor voter (P2) ---

// mlPredictor is the minimal interface the bot uses to query the ML model voter.
// The concrete implementation (predictor.HTTPPredictor) lives in
// internal/ai/predictor — defined as an interface here to avoid coupling and to
// allow NoopPredictor / test doubles without importing the package.
type mlPredictor interface {
	// Predict returns an ML-based prediction for the current market snapshot.
	// regime selects the best-fit horizon model (P4.2/P4.4); pass "" for default.
	// Returns (nil, nil) when the model abstains (not an error).
	Predict(ctx context.Context, features *featureeng.Features, regime string) (*mlPrediction, error)
	// IsReady returns true when a trained model is loaded and can serve.
	IsReady() bool
}

// mlPrediction mirrors predictor.Prediction to avoid the import.
type mlPrediction struct {
	Action         string  `json:"action"`
	Probability    float64 `json:"probability"`
	ExpectedReturn float64 `json:"expected_return"`
	Sigma          float64 `json:"sigma"`
	HorizonMinutes int     `json:"horizon_minutes"`
	ModelVersion   string  `json:"model_version"`
}

// mlPredictionConfidence converts raw probability to [0,1] confidence
// (0.5 → 0 uncertainty, 0 or 1 → 1 certainty).
func mlPredictionConfidence(prob float64) float64 {
	c := (prob - 0.5) * 2
	if c < 0 {
		c = -c
	}
	return c
}

// SetMLPredictor wires the optional ML model voter. Pass nil to disable.
func (bot *AITradingBot) SetMLPredictor(p mlPredictor) {
	bot.mu.Lock()
	bot.mlPredictor = p
	bot.mu.Unlock()
}

// applyMLVote queries the ML predictor and adjusts combinedDecision in-place.
// It is called from combineLLMAndTechnicalAnalysis after the tech+LLM fusion step.
// Weight is read from dynamic config key "ml.voter_weight" (default 0.10).
func (bot *AITradingBot) applyMLVote(
	combinedDecision *ai.Decision,
	marketCtx *ai.MarketContext,
) {
	if bot.mlPredictor == nil || !bot.mlPredictor.IsReady() {
		return
	}

	// Extract features from the primary timeframe candles.
	primary := marketCtx.GetPrimaryContext()
	if primary == nil || len(primary.Candles) < 35 {
		return
	}

	fCandles := make([]featureeng.Candle, len(primary.Candles))
	for i, c := range primary.Candles {
		fCandles[i] = featureeng.Candle{
			Open:    c.Open,
			High:    c.High,
			Low:     c.Low,
			Close:   c.Close,
			Volume:  c.Volume,
			HourUTC: c.Timestamp.UTC().Hour(),
		}
	}
	features := featureeng.ExtractFeatures(fCandles)
	if features == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Pass current regime to ml-server for horizon selection (P4.2/P4.4).
	regime := ""
	if primary != nil {
		regime = primary.Trend // e.g. "bullish" → maps loosely; ml-server normalises
	}
	pred, err := bot.mlPredictor.Predict(ctx, features, regime)
	if err != nil || pred == nil {
		return
	}

	mlConf := mlPredictionConfidence(pred.Probability)

	// ML voter weight: precedence order:
	//  1. signal_source_weights table (Bayesian calibrator, P3.2/P3.4)
	//  2. ml.voter_weight dynamic config key
	//  3. hardcoded default 0.10
	mlWeight := 0.10
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if v := snap.Float("ml.voter_weight"); v > 0 && v <= 1.0 {
			mlWeight = v
		}
	}
	// mlWeightStore is set by main.go when the calibrator is wired.
	if bot.mlWeightStore != nil {
		regime := ""
		if pr := marketCtx.GetPrimaryContext(); pr != nil {
			regime = pr.Trend // best proxy available without importing regime package
		}
		if w := bot.mlWeightStore.MLWeightFor(regime); w > 0 {
			mlWeight = w
		}
	}

	// Consensus with ML: same action → boost by (mlConf * mlWeight).
	if pred.Action == combinedDecision.Action && pred.Action != "WAIT" {
		boost := mlConf * mlWeight
		combinedDecision.Confidence = math.Min(1.0, combinedDecision.Confidence*(1+boost))
		combinedDecision.Reason = fmt.Sprintf(
			"🤖 ML AGREE (%.0f%% P=%.2f +%.1f%%): %s",
			mlConf*100, pred.Probability, boost*100,
			combinedDecision.Reason,
		)
		bot.log.WithFields(logrus.Fields{
			"ml_action": pred.Action,
			"ml_prob":   fmt.Sprintf("%.3f", pred.Probability),
			"ml_conf":   fmt.Sprintf("%.2f%%", mlConf*100),
			"boost":     fmt.Sprintf("+%.1f%%", boost*100),
			"model":     pred.ModelVersion,
		}).Debug("🤖 ML voter: consensus boost")
		return
	}

	// Disagreement: apply a small penalty proportional to ML confidence.
	if pred.Action != "WAIT" && pred.Action != combinedDecision.Action {
		penalty := mlConf * mlWeight * 0.5
		combinedDecision.Confidence = math.Max(0, combinedDecision.Confidence*(1-penalty))
		bot.log.WithFields(logrus.Fields{
			"ml_action":  pred.Action,
			"bot_action": combinedDecision.Action,
			"ml_prob":    fmt.Sprintf("%.3f", pred.Probability),
			"penalty":    fmt.Sprintf("-%.1f%%", penalty*100),
		}).Debug("🤖 ML voter: disagreement penalty")
	}
	// WAIT from ML → no change (model abstains, other sources govern).
}

// deterministicOrderID derives a stable clientOrderId from the trade intent
// so a retry within a 1-minute window reuses the same ID and the exchange
// treats it as idempotent (preventing duplicate-order bugs on timeout).
// Format: "AI-<16-hex-chars>" derived from SHA-256(userID|pair|side|sizeTrunc|minuteBucket).
func deterministicOrderID(userID, pair, side string, sizeUSD float64) string {
	minute := time.Now().UTC().Truncate(time.Minute).Unix()
	raw := fmt.Sprintf("%s|%s|%s|%.2f|%d", userID, pair, side, sizeUSD, minute)
	sum := sha256.Sum256([]byte(raw))
	return "AI-" + hex.EncodeToString(sum[:])[:16]
}

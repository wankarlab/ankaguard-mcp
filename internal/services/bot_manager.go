package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	aiCore "github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/scalping/coordinator"
	"github.com/sirupsen/logrus"
)

// defaultExchange is the fallback exchange used when the user's stored config has no exchange set.
const defaultExchange = "kucoin"

// emergencyStopPositionsClosedTotal counts total positions closed by emergency stop operations.
var emergencyStopPositionsClosedTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "emergency_stop_positions_closed_total",
	Help: "Total positions closed by emergency stop operations",
})

// UserBotCfg is the minimal per-user bot configuration surface that BotManager
// reads from storage. It mirrors storage.UserBotConfig field-for-field to avoid
// importing the storage package (which would create an import cycle via
// storage → master → services).
//
// The concrete storage.UserBotConfigRepository satisfies UserBotConfigStore via
// an adapter in cmd/trading-mcp-server/main.go (or MU-5 handler layer).
type UserBotCfg struct {
	UserID              string
	Exchange            string
	Enabled             bool
	TradeSizeUSD        float64
	Leverage            int
	MarketType          string
	ModeSelection       string
	ConfidenceThreshold float64
	MaxDailyLossUSD     float64
	MaxOpenPositions    int
	DryRun              bool
	ConfigJSON          string
}

// UserBotConfigStore abstracts the storage.UserBotConfigRepository so
// BotManager does not need to import internal/storage.
type UserBotConfigStore interface {
	Get(ctx context.Context, userID, exchange string) (*UserBotCfg, error)
	Upsert(ctx context.Context, cfg *UserBotCfg) error
}

// BotDependencies carries the shared, stateless collaborators that every
// per-user AITradingBot needs. Market-data calls go through these; account-
// touching operations are routed through the user's ExchangeSession instead.
type BotDependencies struct {
	// ContextBuilder and DecisionEngine are shared (stateless analytics, no per-user state).
	ContextBuilder *aiCore.ContextBuilder
	DecisionEngine *aiCore.DecisionEngine

	// GCTClient is used for market-data operations that don't require user credentials.
	GCTClient gct.GCTClient

	// LLMAnalyzer is optional; nil disables LLM-enhanced decisions.
	LLMAnalyzer *llm.Analyzer

	// SLTPExecutor is optional; nil disables automatic SL/TP management.
	SLTPExecutor SLTPExecutor

	// NotificationManager is optional; nil disables trade notifications.
	NotificationManager NotificationManager

	// ConfigStore is optional; when non-nil it is forwarded to each bot via
	// SetConfigStore so per-user dynamic overrides (breakeven, sizing tiers) take
	// effect inside the bot's scope chain (MU-6).
	ConfigStore *dynamic.ConfigStore

	// ValidationGate is optional; when non-nil it is forwarded to each bot via
	// SetValidationGate so the certified-pair discipline applies to EVERY spawned
	// bot, not just the legacy single-pair instance. Without this, fleet bots
	// silently skipped the gate (validationGate==nil) — observed 2026-06-11:
	// orchestrated STG/AAVE bots entered real trades with 0 certified pairs
	// while the operator believed the gate was blocking the whole fleet.
	ValidationGate ValidationGate
}

// managedBot wraps a running AITradingBot with its lifecycle context.
type managedBot struct {
	bot       *AITradingBot
	cancel    context.CancelFunc
	userID    string
	startedAt time.Time
}

// BotManager runs one per-user AITradingBot on demand, with isolated
// credentials loaded from the SessionManager. It is the central integration
// point for Phase MU-2.
//
// Key invariants:
//   - At most maxBots concurrent bots.
//   - Each user's bot gets its own ExchangeSession (no shared credentials).
//   - Stopping a bot evicts the session and releases all coordinator claims.
//   - The existing single-bot path in main.go is unchanged.
//   - Per-user in-process failure counts are tracked for a lightweight circuit
//     breaker (MU-7.4). The durable FailureCount lives in
//     UserCredential.FailureCount (credentials package); these two counters are
//     independent — both must be checked before trusting a user's session.
type BotManager struct {
	mu            sync.RWMutex
	bots          map[string]*managedBot // key: userID
	failureCounts map[string]int         // per-user consecutive runtime failures (MU-7.4)
	cfgRepo       UserBotConfigStore
	sessionMgr    *gct.SessionManager
	pairCoord     *coordinator.PairCoordinator
	deps          BotDependencies
	maxBots       int
	log           *logrus.Logger

	// leaderElection is optional (nil = single-instance mode). When set, StartBot
	// acquires a distributed lock before launching the bot so that only one replica
	// runs a given user's bot at any time (H2.3.2).
	leaderElection BotLeaderElection

	// baseCtx is the manager's root context; cancelling it stops all bots.
	baseCtx    context.Context
	baseCancel context.CancelFunc
}

// NewBotManager constructs a BotManager. Callers must provide a non-nil cfgRepo,
// sessionMgr, and log. pairCoord may be nil (disables cross-component guard).
// maxBots <= 0 defaults to 5.
func NewBotManager(
	cfgRepo UserBotConfigStore,
	sessionMgr *gct.SessionManager,
	pairCoord *coordinator.PairCoordinator,
	deps BotDependencies,
	maxBots int,
	log *logrus.Logger,
) *BotManager {
	if cfgRepo == nil {
		panic("NewBotManager: cfgRepo must not be nil")
	}
	if sessionMgr == nil {
		panic("NewBotManager: sessionMgr must not be nil")
	}
	if log == nil {
		log = logrus.New()
	}
	if maxBots <= 0 {
		maxBots = 5
	}

	baseCtx, baseCancel := context.WithCancel(context.Background())

	return &BotManager{
		bots:          make(map[string]*managedBot),
		failureCounts: make(map[string]int),
		cfgRepo:       cfgRepo,
		sessionMgr:    sessionMgr,
		pairCoord:     pairCoord,
		deps:          deps,
		maxBots:       maxBots,
		log:           log,
		baseCtx:       baseCtx,
		baseCancel:    baseCancel,
	}
}

// StartBot launches a new AITradingBot for userID on the configured exchange.
// It is idempotent if the bot is already running (returns nil). It rejects the
// request if the global maxBots limit is reached.
//
// Flow:
//  1. Load UserBotConfig from cfgRepo (defaults if not found).
//  2. Resolve an ExchangeSession via SessionManager (caches credentials).
//  3. Build a per-user PositionManager (never shared).
//  4. Construct AITradingBot with the user config + session.
//  5. Start in a goroutine derived from the manager's base context.
func (bm *BotManager) StartBot(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("StartBot: userID is required")
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	// Idempotent: already running.
	if _, ok := bm.bots[userID]; ok {
		bm.log.WithField("user_id", userID).Debug("StartBot: bot already running, no-op")
		return nil
	}

	// Concurrency limit (MU-7.1).
	if len(bm.bots) >= bm.maxBots {
		return fmt.Errorf("StartBot: max concurrent bots (%d) already reached", bm.maxBots)
	}

	// Per-user circuit breaker (MU-7.4): block restart when the in-process
	// failure counter has tripped. Callers must call ResetFailures explicitly
	// after verifying the underlying cause (e.g. re-validating credentials).
	if bm.failureCounts[userID] >= maxConsecutiveFailures {
		return fmt.Errorf(
			"StartBot: user %q is circuit-broken after %d consecutive failures; call ResetFailures after fixing credentials",
			userID, bm.failureCounts[userID],
		)
	}

	// Load user config from repository.
	ubCfg, err := bm.cfgRepo.Get(ctx, userID, defaultExchange) // SEC-4: use named constant, not literal
	if err != nil {
		return fmt.Errorf("StartBot: failed to load config for user %q: %w", userID, err)
	}

	// Hard server-side risk validation (MU-7.6): reject configs that violate caps
	// before we ever touch exchange sessions or spin up a goroutine.
	if err := validateUserConfig(ubCfg); err != nil {
		return fmt.Errorf("StartBot: config rejected for user %q: %w", userID, err)
	}

	// Build TradingBotConfig from stored columns + optional JSON overlay.
	botCfg, err := buildBotConfig(ubCfg)
	if err != nil {
		return fmt.Errorf("StartBot: invalid bot config for user %q: %w", userID, err)
	}

	// DryRun safety gate (MU-7.5): log an explicit warning when real-money mode
	// is active so it is never silently engaged. The default is always DryRun=true
	// (set in DefaultTradingBotConfig and in the DB column default dry_run=1).
	// buildBotConfig copies the column value as-is — this guard makes the real-
	// money decision visible in logs and protects against accidental live trading.
	if !botCfg.DryRun {
		bm.log.WithFields(logrus.Fields{
			"user_id":    userID,
			"exchange":   botCfg.Exchange,
			"trade_size": botCfg.TradeSize,
			"leverage":   botCfg.Leverage,
		}).Warn("BotManager: REAL-MONEY mode active (dry_run=false) — ensure this is intentional")
	}

	// Resolve isolated session for this user.
	exchange := ubCfg.Exchange
	if exchange == "" {
		exchange = defaultExchange
	}
	session, err := bm.sessionMgr.GetSession(ctx, userID, exchange)
	if err != nil {
		return fmt.Errorf("StartBot: cannot obtain session for user %q on %s: %w", userID, exchange, err)
	}

	// Per-user PositionManager — NEVER shared.
	posConfig := &aiCore.PositionConfig{
		AutoClose:     true,
		CheckInterval: 5,
	}
	positionManager := aiCore.NewPositionManager(posConfig, bm.log)

	// Construct the bot. GCTClient is shared for market-data; account-touching
	// ops are routed through the user's ExchangeSession (set via WithUserSession).
	bot := NewAITradingBot(
		bm.deps.ContextBuilder,
		bm.deps.DecisionEngine,
		positionManager,
		nil, // riskCalculator: optional, set later
		bm.deps.SLTPExecutor,
		bm.deps.GCTClient, // market-data only; per-user ops use session
		bm.deps.LLMAnalyzer,
		bm.deps.NotificationManager,
		nil, // learningSystem: optional, add later
		nil, // autoConfigurator: optional, add later
		botCfg,
		bm.log.WithField("user_id", userID).Logger,
	)

	// Attach user identity + session (MU-2 hook).
	bot.WithUserSession(userID, session)

	// MU-6: wire dynamic ConfigStore so per-user scope chains resolve above YAML.
	if bm.deps.ConfigStore != nil {
		bot.SetConfigStore(bm.deps.ConfigStore)
	}

	// Certified-pair validation gate: every spawned bot must honor it, not just
	// the legacy single-pair instance (see BotDependencies.ValidationGate).
	if bm.deps.ValidationGate != nil {
		bot.SetValidationGate(bm.deps.ValidationGate)
	}

	// MU-7.4: wire error callback so every runtime failure increments the
	// per-user circuit breaker inside BotManager.
	bot.SetOnError(func(uid string) {
		if uid != "default" {
			bm.RecordFailure(uid)
		}
	})

	// Attach shared pair coordinator so user's bot doesn't conflict with others.
	if bm.pairCoord != nil {
		bot.SetPairCoordinator(bm.pairCoord)
	}

	// Leader election (H2.3.2): when a distributed election provider is configured,
	// attempt to acquire the per-user lock BEFORE registering the bot. This prevents
	// two replicas from running the same user's bot simultaneously.
	if bm.leaderElection != nil {
		acquired, err := bm.leaderElection.TryAcquire(ctx, userID)
		if err != nil {
			return fmt.Errorf("leader election error: %w", err)
		}
		if !acquired {
			return fmt.Errorf("another instance is already running bot for user %s", userID)
		}
	}

	// Derive a cancellable context for this bot's lifetime.
	botCtx, botCancel := context.WithCancel(bm.baseCtx)

	// Start the bot.
	if err := bot.StartWithContext(botCtx); err != nil {
		botCancel()
		// Release the leader lock if we acquired one but failed to start.
		if bm.leaderElection != nil {
			_ = bm.leaderElection.Release(ctx, userID)
		}
		return fmt.Errorf("StartBot: failed to start bot for user %q: %w", userID, err)
	}

	bm.bots[userID] = &managedBot{
		bot:       bot,
		cancel:    botCancel,
		userID:    userID,
		startedAt: time.Now(),
	}

	// BOT-01: keep the distributed lease alive for the bot's whole lifetime.
	// TryAcquire above only grants a 30s TTL lock; without periodic renewal any
	// bot running longer than that loses the lease and a second replica could
	// TryAcquire and start a duplicate bot for the same user (split-brain).
	if bm.leaderElection != nil {
		go bm.leaseRenewLoop(botCtx, userID)
	}

	bm.log.WithFields(logrus.Fields{
		"user_id":    userID,
		"exchange":   exchange,
		"pair":       botCfg.Pair,
		"dry_run":    botCfg.DryRun,
		"market":     botCfg.MarketType,
		"total_bots": len(bm.bots),
	}).Info("BotManager: user bot started")

	return nil
}

// leaseRenewLoop periodically renews the distributed leader lease for userID
// while the bot is running (BOT-01). It is launched from StartBot right after
// the lock is first acquired and stops when botCtx is cancelled (bot stopped).
// If Renew ever fails (lease lost/stolen — e.g. this instance was slow and
// another replica took over, or Valkey was unreachable across the whole TTL
// window), we can no longer guarantee we are the sole runner for this user's
// bot, so we force an immediate EmergencyStop rather than let a possible
// duplicate keep trading.
func (bm *BotManager) leaseRenewLoop(botCtx context.Context, userID string) {
	interval := bm.leaderElection.RenewInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-botCtx.Done():
			return
		case <-ticker.C:
			if err := bm.leaderElection.Renew(botCtx, userID); err != nil {
				bm.log.WithField("user_id", userID).WithError(err).
					Error("leaseRenewLoop: failed to renew leader lease — forcing emergency stop to avoid split-brain")

				// Use a detached context: botCtx may already be effectively
				// unreliable (lease lost) and EmergencyStop needs to close
				// real positions regardless of this bot's own lifecycle state.
				stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if stopErr := bm.EmergencyStop(stopCtx, userID); stopErr != nil {
					bm.log.WithField("user_id", userID).WithError(stopErr).
						Error("leaseRenewLoop: EmergencyStop after lease loss failed")
				}
				cancel()
				return
			}
		}
	}
}

// StopBot stops and cleans up the bot for userID. Returns nil if no bot is
// running for that user (idempotent).
func (bm *BotManager) StopBot(userID string) error {
	if userID == "" {
		return fmt.Errorf("StopBot: userID is required")
	}

	bm.mu.Lock()
	mb, ok := bm.bots[userID]
	if !ok {
		bm.mu.Unlock()
		bm.log.WithField("user_id", userID).Debug("StopBot: no running bot, no-op")
		return nil
	}
	delete(bm.bots, userID)
	bm.mu.Unlock()

	// Cancel the bot's context — this signals all goroutines to stop.
	mb.cancel()

	// Gracefully stop the bot's internal state machine.
	if err := mb.bot.Stop(); err != nil {
		bm.log.WithField("user_id", userID).WithError(err).Warn("StopBot: bot.Stop returned error")
	}

	// Evict the session so stale credentials are not reused.
	bm.sessionMgr.Evict(userID, mb.bot.config.Exchange)

	// Release the distributed leader lock if election is enabled.
	if bm.leaderElection != nil {
		if relErr := bm.leaderElection.Release(context.Background(), userID); relErr != nil {
			bm.log.WithField("user_id", userID).WithError(relErr).Warn("StopBot: failed to release leader lock")
		}
	}

	bm.log.WithFields(logrus.Fields{
		"user_id": userID,
		"uptime":  time.Since(mb.startedAt).String(),
	}).Info("BotManager: user bot stopped")

	return nil
}

// EmergencyStop closes all of the user's open positions (via their isolated
// ExchangeSession) and then stops and cleans up the bot. Unlike StopBot — which
// only halts analysis and leaves positions open — this is the hard-stop for
// real-money safety. Idempotent: returns nil if no bot is running for userID.
//
// A fresh, time-bounded context is used for the close orders so that cancelling
// the bot's lifecycle context cannot abort an in-flight position close.
func (bm *BotManager) EmergencyStop(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("EmergencyStop: userID is required")
	}

	bm.mu.Lock()
	mb, ok := bm.bots[userID]
	if !ok {
		bm.mu.Unlock()
		bm.log.WithField("user_id", userID).Debug("EmergencyStop: no running bot, no-op")
		return nil
	}
	delete(bm.bots, userID)
	bm.mu.Unlock()

	// Capture the open position count BEFORE EmergencyStopWithContext removes them,
	// so we can record the Prometheus metric regardless of close errors.
	openPositionsBefore := len(mb.bot.positionManager.GetAllPositions())

	// Close positions + stop the internal state machine. EmergencyStopWithContext
	// closes every open position through the bot's session (see closePositionViaGCT)
	// before halting. Use a detached, bounded context so the bot's own cancelled
	// lifecycle context can't kill the close orders mid-flight.
	closeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	stopErr := mb.bot.EmergencyStopWithContext(closeCtx)
	cancel()

	// Increment the Prometheus counter for each position that was attempted to be closed.
	if openPositionsBefore > 0 {
		emergencyStopPositionsClosedTotal.Add(float64(openPositionsBefore))
	}

	// Cancel the bot's lifecycle context to ensure all goroutines exit.
	mb.cancel()

	// Evict the session so stale credentials are not reused.
	bm.sessionMgr.Evict(userID, mb.bot.config.Exchange)

	// Release the distributed leader lock if election is enabled.
	if bm.leaderElection != nil {
		if relErr := bm.leaderElection.Release(ctx, userID); relErr != nil {
			bm.log.WithField("user_id", userID).WithError(relErr).Warn("EmergencyStop: failed to release leader lock")
		}
	}

	bm.log.WithFields(logrus.Fields{
		"user_id": userID,
		"uptime":  time.Since(mb.startedAt).String(),
	}).Warn("BotManager: user bot emergency-stopped (positions closed)")

	return stopErr
}

// GetStatus returns a snapshot of the running bot for userID, or nil if not running.
func (bm *BotManager) GetStatus(userID string) map[string]interface{} {
	bm.mu.RLock()
	mb, ok := bm.bots[userID]
	bm.mu.RUnlock()
	if !ok {
		return nil
	}
	status := mb.bot.GetStatus()
	status["user_id"] = userID
	status["managed_since"] = mb.startedAt
	return status
}

// ListActive returns the user IDs of all currently running bots.
func (bm *BotManager) ListActive() []string {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	ids := make([]string, 0, len(bm.bots))
	for id := range bm.bots {
		ids = append(ids, id)
	}
	return ids
}

// WithLeaderElection injects a BotLeaderElection dependency after construction.
// PR-8: exported so callers (main.go) can set it without needing a new constructor parameter.
// Must be called before the first StartBot.
func (bm *BotManager) WithLeaderElection(le BotLeaderElection) {
	bm.mu.Lock()
	bm.leaderElection = le
	bm.mu.Unlock()
}

// SetMaxBots adjusts the global concurrent-bot cap. Useful for per-tier enforcement.
// PR-8: the API layer reads the user's subscription MaxBots and calls this per-tenant
// before StartBot; set to the minimum of the tier limit and the server default.
func (bm *BotManager) SetMaxBots(n int) {
	if n <= 0 {
		n = 5
	}
	bm.mu.Lock()
	bm.maxBots = n
	bm.mu.Unlock()
}

// EmergencyStopAll stops ALL active bots across all users. Requires level>=4
// authorization at the API layer. Returns a summary of stopped/failed users.
// When dryRun=true it returns the list of active users without stopping them.
func (bm *BotManager) EmergencyStopAll(ctx context.Context, dryRun bool) (stopped []string, failed map[string]string) {
	failed = make(map[string]string)
	users := bm.ListActive()

	if dryRun {
		bm.log.WithField("would_stop", users).Warn("EmergencyStopAll dry-run — no bots stopped")
		return users, failed
	}

	for _, uid := range users {
		if err := bm.EmergencyStop(ctx, uid); err != nil {
			failed[uid] = err.Error()
			bm.log.WithField("user_id", uid).WithError(err).Error("EmergencyStopAll: failed to stop user bot")
		} else {
			stopped = append(stopped, uid)
		}
	}
	bm.log.WithFields(logrus.Fields{
		"stopped": len(stopped),
		"failed":  len(failed),
	}).Warn("EmergencyStopAll executed")
	return stopped, failed
}

// UpdateConfig persists the new config for userID and hot-reloads the running
// bot if one exists.
func (bm *BotManager) UpdateConfig(ctx context.Context, userID string, cfg *UserBotCfg) error {
	if userID == "" {
		return fmt.Errorf("UpdateConfig: userID is required")
	}
	if cfg == nil {
		return fmt.Errorf("UpdateConfig: cfg must not be nil")
	}
	cfg.UserID = userID

	// Persist to storage.
	if err := bm.cfgRepo.Upsert(ctx, cfg); err != nil {
		return fmt.Errorf("UpdateConfig: failed to persist config for user %q: %w", userID, err)
	}

	// Hot-reload into running bot, if any.
	bm.mu.RLock()
	mb, running := bm.bots[userID]
	bm.mu.RUnlock()

	if running {
		botCfg, err := buildBotConfig(cfg)
		if err != nil {
			bm.log.WithField("user_id", userID).WithError(err).Warn("UpdateConfig: could not build bot config for hot-reload")
		} else {
			mb.bot.UpdateConfigFromStruct(botCfg)
			bm.log.WithField("user_id", userID).Info("BotManager: hot-reloaded config into running bot")
		}
	}

	return nil
}

// SetPairCoordinator replaces the pair coordinator used when spawning new bots.
// Safe to call after construction (e.g. once the coordinator is wired in main.go).
// Already-running bots are unaffected; new bots started after this call will use
// the new coordinator.
func (bm *BotManager) SetPairCoordinator(c *coordinator.PairCoordinator) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.pairCoord = c
}

// RecordFailure increments the in-process consecutive-failure counter for userID
// (MU-7.4 per-user circuit breaker). When the count reaches maxConsecutiveFailures
// the bot is also stopped to prevent further damage. Callers — typically the bot's
// own error handler or the session manager — should call this on every consecutive
// runtime error that is not self-healed.
//
// This in-process counter is complementary to UserCredential.FailureCount
// (credentials package), which persists failures across restarts.
func (bm *BotManager) RecordFailure(userID string) {
	if userID == "" {
		return
	}

	bm.mu.Lock()
	bm.failureCounts[userID]++
	count := bm.failureCounts[userID]
	bm.mu.Unlock()

	bm.log.WithFields(logrus.Fields{
		"user_id":           userID,
		"failure_count":     count,
		"circuit_threshold": maxConsecutiveFailures,
	}).Warn("BotManager: recorded runtime failure for user")

	// Auto-stop the bot when the threshold is exceeded to halt further damage.
	if count >= maxConsecutiveFailures {
		bm.log.WithField("user_id", userID).Error(
			"BotManager: circuit breaker tripped — stopping bot for user",
		)
		if err := bm.StopBot(userID); err != nil {
			bm.log.WithField("user_id", userID).WithError(err).Warn(
				"BotManager: error stopping bot after circuit breaker trip",
			)
		}
	}
}

// ResetFailures clears the in-process failure counter for userID, re-opening the
// circuit breaker. Call this after the underlying problem (e.g. invalid credentials,
// connectivity issue) has been confirmed resolved — typically after a successful
// credential re-validation or explicit operator intervention.
func (bm *BotManager) ResetFailures(userID string) {
	if userID == "" {
		return
	}

	bm.mu.Lock()
	previous := bm.failureCounts[userID]
	delete(bm.failureCounts, userID)
	bm.mu.Unlock()

	bm.log.WithFields(logrus.Fields{
		"user_id":       userID,
		"cleared_count": previous,
	}).Info("BotManager: failure counter reset for user — circuit breaker re-opened")
}

// Shutdown gracefully stops all running bots and cancels the manager's base context.
func (bm *BotManager) Shutdown() {
	bm.log.Info("BotManager: shutting down all user bots")
	ids := bm.ListActive()
	for _, uid := range ids {
		if err := bm.StopBot(uid); err != nil {
			bm.log.WithField("user_id", uid).WithError(err).Warn("BotManager: error stopping bot during shutdown")
		}
	}
	bm.baseCancel()
	bm.log.Info("BotManager: shutdown complete")
}

// buildBotConfig merges a UserBotCfg (canonical scalar columns) with the
// optional ConfigJSON overlay to produce a TradingBotConfig.
// Column values always win over same-named JSON keys (JSON is for extra fields
// that the columns don't expose yet).
func buildBotConfig(ubCfg *UserBotCfg) (*TradingBotConfig, error) {
	// Start from safe defaults.
	botCfg := DefaultTradingBotConfig()

	// Apply JSON overlay (may be empty or "{}").
	if ubCfg.ConfigJSON != "" && ubCfg.ConfigJSON != "{}" {
		if err := json.Unmarshal([]byte(ubCfg.ConfigJSON), botCfg); err != nil {
			return nil, fmt.Errorf("invalid ConfigJSON: %w", err)
		}
	}

	// Canonical column overrides always take priority.
	if ubCfg.Exchange != "" {
		botCfg.Exchange = ubCfg.Exchange
	}
	if ubCfg.TradeSizeUSD > 0 {
		botCfg.TradeSize = ubCfg.TradeSizeUSD
	}
	if ubCfg.Leverage > 0 {
		botCfg.Leverage = ubCfg.Leverage
	}
	if ubCfg.MarketType != "" {
		botCfg.MarketType = ubCfg.MarketType
	}
	if ubCfg.ModeSelection != "" {
		botCfg.ModeSelection = ubCfg.ModeSelection
	}
	if ubCfg.ConfidenceThreshold > 0 {
		botCfg.ConfidenceThreshold = ubCfg.ConfidenceThreshold
	}
	if ubCfg.MaxDailyLossUSD > 0 {
		botCfg.MaxDailyLoss = ubCfg.MaxDailyLossUSD
	}
	if ubCfg.MaxOpenPositions > 0 {
		botCfg.MaxOpenPositions = ubCfg.MaxOpenPositions
	}
	// DryRun is always authoritative from the column.
	botCfg.DryRun = ubCfg.DryRun

	return botCfg, nil
}

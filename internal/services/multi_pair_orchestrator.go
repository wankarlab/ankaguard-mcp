package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
)

// BotFactory is a function that constructs a new AITradingBot for the given
// pair and capital allocation. The caller (main.go) injects the factory so the
// orchestrator does not need to know how to wire all AITradingBot dependencies.
type BotFactory func(pair string, capitalUSD float64) (*AITradingBot, error)

// OrchestratorConfig holds all tunable parameters for MultiPairAIOrchestrator.
// Every field has a safe default; zero values are replaced by those defaults
// in NewMultiPairAIOrchestrator.
type OrchestratorConfig struct {
	// MaxBots is the maximum number of concurrent AITradingBot instances.
	MaxBots int // default 3
	// SpawnThreshold is the minimum normalised opportunity score (0–1) required
	// before a new bot is spawned for a pair.
	SpawnThreshold float64 // default 0.40
	// KillThreshold is the score below which an active pair bot is stopped.
	KillThreshold float64 // default 0.30
	// ReservePercent is the fraction of totalCapitalUSD kept liquid (not allocated).
	ReservePercent float64 // default 0.20
	// MinCapitalPerBot is the floor (USD) for any single bot allocation.
	MinCapitalPerBot float64 // default 3.0
	// MaxCapitalPerBot is the ceiling (USD) for any single bot allocation.
	MaxCapitalPerBot float64 // default 15.0
	// ReconcileInterval controls how often the orchestrator re-evaluates opportunities.
	ReconcileInterval time.Duration // default 5min
}

func (c *OrchestratorConfig) applyDefaults() {
	if c.MaxBots <= 0 {
		c.MaxBots = 3
	}
	if c.SpawnThreshold <= 0 {
		c.SpawnThreshold = 0.40
	}
	if c.KillThreshold <= 0 {
		c.KillThreshold = 0.30
	}
	if c.ReservePercent <= 0 {
		c.ReservePercent = 0.20
	}
	if c.MinCapitalPerBot <= 0 {
		c.MinCapitalPerBot = 3.0
	}
	if c.MaxCapitalPerBot <= 0 {
		c.MaxCapitalPerBot = 15.0
	}
	if c.ReconcileInterval <= 0 {
		c.ReconcileInterval = 5 * time.Minute
	}
}

// OpportunitySource is implemented by any component that can provide a ranked
// list of market opportunities (e.g. *master.MasterAgent via GetState()).
type OpportunitySource interface {
	// GetOpportunities returns the current ranked opportunities (score 0–100).
	GetOpportunities() []OrchestratorOpportunity
}

// OrchestratorOpportunity is a minimal opportunity view consumed by the
// orchestrator. It intentionally mirrors master.MarketOpportunity without
// importing the master package.
type OrchestratorOpportunity struct {
	Pair             string
	Exchange         string
	OpportunityScore float64 // 0–100
}

// OrchestratorBotStatus is a snapshot of one managed bot's state.
type OrchestratorBotStatus struct {
	Pair       string
	Exchange   string
	CapitalUSD float64
	StartedAt  time.Time
	NormScore  float64
}

// pairBotEntry tracks a single running bot instance.
type pairBotEntry struct {
	pair       string
	exchange   string
	bot        *AITradingBot
	cancel     context.CancelFunc
	startedAt  time.Time
	capitalUSD float64
	normScore  float64
}

// MultiPairAIOrchestrator manages N independent AITradingBot instances, one
// per pair, driven by opportunity scores from a master agent.
//
// Usage:
//
//	orc := services.NewMultiPairAIOrchestrator(cfg, log, factory)
//	orc.Start(ctx, opportunitySource, totalCapitalUSD)
//	defer orc.Stop()
type MultiPairAIOrchestrator struct {
	cfg     OrchestratorConfig
	log     *logrus.Logger
	factory BotFactory

	mu   sync.RWMutex
	bots map[string]*pairBotEntry // key = pair string

	// excludedPairs holds statically-excluded pairs (set once at init).
	excludedPairs map[string]struct{}

	// dynamicExclude, if non-nil, is called on every reconcile to get the pair
	// the legacy single-bot is currently operating on. This prevents double-up
	// when pair_selection switches the legacy bot away from its config pair.
	dynamicExclude func() string

	cancelFn context.CancelFunc
	stopped  chan struct{}
}

// NewMultiPairAIOrchestrator creates a new orchestrator. Zero-value fields in
// cfg are replaced by safe defaults before use.
func NewMultiPairAIOrchestrator(cfg OrchestratorConfig, log *logrus.Logger, factory BotFactory) *MultiPairAIOrchestrator {
	cfg.applyDefaults()
	if log == nil {
		log = logrus.New()
	}
	return &MultiPairAIOrchestrator{
		cfg:           cfg,
		log:           log,
		factory:       factory,
		bots:          make(map[string]*pairBotEntry),
		excludedPairs: make(map[string]struct{}),
		stopped:       make(chan struct{}),
	}
}

// ExcludePair marks a pair as statically off-limits for the orchestrator.
// Must be called before Start.
func (o *MultiPairAIOrchestrator) ExcludePair(pair string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.excludedPairs[pair] = struct{}{}
}

// SetDynamicExclude registers a function that returns the pair currently held
// by the legacy single-bot. It is called on every reconcile tick so the
// orchestrator avoids double-entering when pair_selection switches the legacy
// bot to a new pair.
func (o *MultiPairAIOrchestrator) SetDynamicExclude(fn func() string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dynamicExclude = fn
}

// Start launches the reconciliation loop. opportunitySource is polled every
// ReconcileInterval; totalCapitalUSD is the deployable capital ceiling.
// Start is non-blocking — it launches a goroutine and returns immediately.
func (o *MultiPairAIOrchestrator) Start(ctx context.Context, opportunitySource OpportunitySource, totalCapitalUSD float64) {
	loopCtx, cancel := context.WithCancel(ctx)
	o.cancelFn = cancel

	o.log.WithFields(logrus.Fields{
		"max_bots":           o.cfg.MaxBots,
		"spawn_threshold":    o.cfg.SpawnThreshold,
		"kill_threshold":     o.cfg.KillThreshold,
		"reserve_pct":        o.cfg.ReservePercent,
		"reconcile_interval": o.cfg.ReconcileInterval,
		"total_capital_usd":  totalCapitalUSD,
	}).Info("multi_pair_orchestrator: starting reconciliation loop")

	go o.loop(loopCtx, opportunitySource, totalCapitalUSD)
}

// Stop gracefully shuts down all active bots and the reconciliation loop.
func (o *MultiPairAIOrchestrator) Stop() {
	if o.cancelFn != nil {
		o.cancelFn()
	}
	<-o.stopped

	o.log.Info("multi_pair_orchestrator: stopped; all bots drained")
}

// UpdateConfig replaces the orchestrator config at runtime (hot-reload path).
// Takes effect on the next reconciliation tick.
func (o *MultiPairAIOrchestrator) UpdateConfig(cfg OrchestratorConfig) {
	cfg.applyDefaults()
	o.mu.Lock()
	o.cfg = cfg
	o.mu.Unlock()
}

// GetActiveBots returns a snapshot of all currently running bots.
func (o *MultiPairAIOrchestrator) GetActiveBots() []OrchestratorBotStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()

	out := make([]OrchestratorBotStatus, 0, len(o.bots))
	for _, e := range o.bots {
		out = append(out, OrchestratorBotStatus{
			Pair:       e.pair,
			Exchange:   e.exchange,
			CapitalUSD: e.capitalUSD,
			StartedAt:  e.startedAt,
			NormScore:  e.normScore,
		})
	}
	return out
}

// ManagedBotView is a richer per-bot snapshot for the control panel. Unlike
// OrchestratorBotStatus it reaches into the live AITradingBot for paper-mode
// (dry_run) and open-position count so the Motor screen can render the fleet.
type ManagedBotView struct {
	Pair          string
	Exchange      string
	CapitalUSD    float64
	NormScore     float64
	StartedAt     time.Time
	PaperMode     bool // mirrors AITradingBot dry_run
	OpenPositions int
}

// ListManagedBots returns a detailed snapshot of every running bot, including
// live paper-mode and open-position counts read from each AITradingBot.
func (o *MultiPairAIOrchestrator) ListManagedBots() []ManagedBotView {
	o.mu.RLock()
	defer o.mu.RUnlock()

	out := make([]ManagedBotView, 0, len(o.bots))
	for _, e := range o.bots {
		v := ManagedBotView{
			Pair:       e.pair,
			Exchange:   e.exchange,
			CapitalUSD: e.capitalUSD,
			NormScore:  e.normScore,
			StartedAt:  e.startedAt,
		}
		// Reading from the bot uses the bot's own locks (bot.mu / positionManager),
		// which are independent of o.mu, so this cannot deadlock the orchestrator.
		if e.bot != nil {
			if cfg := e.bot.GetConfig(); cfg != nil {
				v.PaperMode = cfg.DryRun
			}
			v.OpenPositions = len(e.bot.GetOpenPositions())
		}
		out = append(out, v)
	}
	return out
}

// KillBot stops the bot managing the given pair and removes it from the fleet.
// Returns an error matching "bot not found" when no bot manages that pair, so the
// HTTP layer can map it to 404. The orchestrator may respawn the pair on a later
// reconcile if it still scores above the spawn threshold (same as the scalping
// spawner's behaviour) — a kill is a manual reset, not a permanent ban.
func (o *MultiPairAIOrchestrator) KillBot(pair string, reason string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	entry, ok := o.bots[pair]
	if !ok {
		return fmt.Errorf("bot not found")
	}
	o.log.WithFields(logrus.Fields{
		"pair":   pair,
		"reason": reason,
	}).Warn("multi_pair_orchestrator: killing bot via manual request")
	entry.cancel()
	delete(o.bots, pair)
	return nil
}

// SetBotPaperMode toggles paper mode (dry_run) on the bot managing the given pair.
// Returns "bot not found" when no bot manages that pair. Only affects future trade
// evaluations on that bot; open positions are unaffected.
func (o *MultiPairAIOrchestrator) SetBotPaperMode(pair string, enabled bool) error {
	o.mu.RLock()
	entry, ok := o.bots[pair]
	var bot *AITradingBot
	if ok {
		bot = entry.bot
	}
	o.mu.RUnlock()

	if !ok || bot == nil {
		return fmt.Errorf("bot not found")
	}
	return bot.UpdateConfig(map[string]interface{}{"dry_run": enabled})
}

// Reconcile is the core logic: given a fresh slice of opportunities and the
// current total deployable capital, start bots for qualifying pairs and stop
// bots for pairs that no longer qualify.
//
// It is exported so it can be called from tests or external triggers without
// going through the timed loop.
func (o *MultiPairAIOrchestrator) Reconcile(opportunities []OrchestratorOpportunity, totalCapitalUSD float64) {
	o.mu.Lock()
	defer o.mu.Unlock()

	cfg := o.cfg // local copy for this reconcile pass

	// rangingAllow, when non-nil, restricts which pairs the orchestrator may spawn:
	// in ranging gate mode it only deploys bots on pairs with a demonstrated RANGING
	// edge, so bot slots are not wasted on pairs that would just sit at WAIT.
	var rangingAllow map[string]struct{}

	// Hot-reload thresholds from dynamic Postgres config — changes apply
	// on the next reconcile cycle without restart.
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if v := snap.Float("ai_bot.multi_pair.spawn_threshold"); v > 0 {
			cfg.SpawnThreshold = v
		}
		if v := snap.Float("ai_bot.multi_pair.kill_threshold"); v > 0 {
			cfg.KillThreshold = v
		}
		if v := snap.Float("ai_bot.multi_pair.min_capital_per_bot_usd"); v > 0 {
			cfg.MinCapitalPerBot = v
		}
		if v := snap.Float("ai_bot.multi_pair.max_capital_per_bot_usd"); v > 0 {
			cfg.MaxCapitalPerBot = v
		}
		if v := snap.Int("ai_bot.multi_pair.max_bots"); v > 0 {
			cfg.MaxBots = v
		}
		rangingAllow = rangingAllowSet(snap)
	}

	// Build the effective exclusion set: static pairs + dynamic (legacy bot's current pair).
	effectiveExclude := make(map[string]struct{}, len(o.excludedPairs)+1)
	for p := range o.excludedPairs {
		effectiveExclude[p] = struct{}{}
	}
	if o.dynamicExclude != nil {
		if p := o.dynamicExclude(); p != "" {
			effectiveExclude[p] = struct{}{}
		}
	}

	// ── 1. Filter, normalise, and rank opportunities ──────────────────────────
	type scored struct {
		opp       OrchestratorOpportunity
		normScore float64
	}

	var candidates []scored
	skippedOffAllowlist := 0
	for _, opp := range opportunities {
		if _, excluded := effectiveExclude[opp.Pair]; excluded {
			continue
		}
		// In ranging mode, only spawn bots on pairs with a demonstrated ranging edge.
		if rangingAllow != nil {
			if _, ok := rangingAllow[normalizeSymbolForMatch(opp.Pair)]; !ok {
				skippedOffAllowlist++
				continue
			}
		}
		norm := opp.OpportunityScore / 100.0
		if norm < cfg.SpawnThreshold {
			continue
		}
		candidates = append(candidates, scored{opp: opp, normScore: norm})
	}
	if skippedOffAllowlist > 0 {
		o.log.WithFields(logrus.Fields{
			"skipped":   skippedOffAllowlist,
			"allowlist": len(rangingAllow),
		}).Info("multi_pair_orchestrator: ranging mode — skipped opportunities outside the ranging allowlist")
	}

	// Sort descending by score.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].normScore > candidates[j].normScore
	})

	// Trim to MaxBots.
	if len(candidates) > cfg.MaxBots {
		candidates = candidates[:cfg.MaxBots]
	}

	// Build a quick-lookup set of qualifying pairs.
	wantedPairs := make(map[string]scored, len(candidates))
	for _, c := range candidates {
		wantedPairs[c.opp.Pair] = c
	}

	// ── 2. Stop bots for pairs that no longer qualify ─────────────────────────
	// NEVER kill a bot that still holds an open position. Killing it abandons the
	// trade: no one trails the stop, takes profit, or reacts to a reversal, so a
	// winning position can ride all the way back to a loss (observed 2026-05-29:
	// WLD short +3.26% abandoned when WLD dropped out of the ranking). Keep the
	// bot alive — it keeps monitoring AND analysing its own pair, so the position
	// retains full protection — and let a later reconcile reap it once it is flat.
	for pair, entry := range o.bots {
		c, stillWanted := wantedPairs[pair]
		shouldKill := !stillWanted || c.normScore < cfg.KillThreshold
		if !shouldKill {
			continue
		}
		if entry.bot != nil && len(entry.bot.GetOpenPositions()) > 0 {
			o.log.WithFields(logrus.Fields{
				"pair":           pair,
				"open_positions": len(entry.bot.GetOpenPositions()),
			}).Info("multi_pair_orchestrator: pair no longer qualifies but bot holds an open position — keeping it alive to manage the trade")
			continue
		}
		o.log.WithFields(logrus.Fields{
			"pair":         pair,
			"still_wanted": stillWanted,
		}).Info("multi_pair_orchestrator: stopping bot (pair no longer qualifies)")
		entry.cancel()
		delete(o.bots, pair)
	}

	// ── 3. Compute capital allocation among active + new bots ─────────────────
	deployable := totalCapitalUSD * (1 - cfg.ReservePercent)
	if deployable <= 0 {
		o.log.Warn("multi_pair_orchestrator: deployable capital is 0, skipping spawn")
		return
	}

	totalScore := 0.0
	for _, c := range candidates {
		totalScore += c.normScore
	}

	allocations := make(map[string]float64, len(candidates))
	for _, c := range candidates {
		if totalScore <= 0 {
			allocations[c.opp.Pair] = deployable / float64(len(candidates))
			continue
		}
		raw := deployable * (c.normScore / totalScore)
		if raw < cfg.MinCapitalPerBot {
			raw = cfg.MinCapitalPerBot
		}
		if raw > cfg.MaxCapitalPerBot {
			raw = cfg.MaxCapitalPerBot
		}
		allocations[c.opp.Pair] = raw
	}

	// ── 4. Spawn new bots for pairs not yet active ────────────────────────────
	for _, c := range candidates {
		pair := c.opp.Pair
		if _, already := o.bots[pair]; already {
			// Bot already running — capital rebalancing can be done later.
			continue
		}

		capitalUSD := allocations[pair]
		if capitalUSD < cfg.MinCapitalPerBot {
			o.log.WithFields(logrus.Fields{
				"pair":        pair,
				"capital_usd": capitalUSD,
				"min_capital": cfg.MinCapitalPerBot,
			}).Warn("multi_pair_orchestrator: skipping spawn (allocated capital below minimum)")
			continue
		}

		bot, err := o.factory(pair, capitalUSD)
		if err != nil {
			o.log.WithFields(logrus.Fields{
				"pair": pair,
				"err":  err,
			}).Error("multi_pair_orchestrator: factory failed to create bot")
			continue
		}
		if bot == nil {
			o.log.WithField("pair", pair).Error("multi_pair_orchestrator: factory returned nil bot")
			continue
		}

		botCtx, botCancel := context.WithCancel(context.Background())

		if startErr := bot.StartWithContext(botCtx); startErr != nil {
			botCancel()
			o.log.WithFields(logrus.Fields{
				"pair": pair,
				"err":  startErr,
			}).Error("multi_pair_orchestrator: bot.StartWithContext failed")
			continue
		}

		o.bots[pair] = &pairBotEntry{
			pair:       pair,
			exchange:   c.opp.Exchange,
			bot:        bot,
			cancel:     botCancel,
			startedAt:  time.Now(),
			capitalUSD: capitalUSD,
			normScore:  c.normScore,
		}

		o.log.WithFields(logrus.Fields{
			"pair":        pair,
			"exchange":    c.opp.Exchange,
			"capital_usd": fmt.Sprintf("%.2f", capitalUSD),
			"norm_score":  fmt.Sprintf("%.3f", c.normScore),
		}).Info("multi_pair_orchestrator: spawned new AI bot")
	}
}

// ── internal loop ─────────────────────────────────────────────────────────────

func (o *MultiPairAIOrchestrator) loop(ctx context.Context, src OpportunitySource, totalCapitalUSD float64) {
	defer close(o.stopped)
	defer o.killAllBots()

	ticker := time.NewTicker(o.cfg.ReconcileInterval)
	defer ticker.Stop()

	// Run an initial reconcile immediately on startup.
	o.reconcileFromSource(src, totalCapitalUSD)

	for {
		select {
		case <-ctx.Done():
			o.log.Info("multi_pair_orchestrator: context cancelled, exiting loop")
			return
		case <-ticker.C:
			o.reconcileFromSource(src, totalCapitalUSD)
		}
	}
}

func (o *MultiPairAIOrchestrator) reconcileFromSource(src OpportunitySource, totalCapitalUSD float64) {
	opportunities := src.GetOpportunities()
	if len(opportunities) == 0 {
		o.log.Debug("multi_pair_orchestrator: no opportunities from source, skipping reconcile")
		return
	}
	o.Reconcile(opportunities, totalCapitalUSD)
}

// rangingAllowSet returns the normalised set of pairs the orchestrator may spawn
// when the entry gate is in ranging mode. Returns nil (no restriction) when not in
// ranging mode or when the allowlist is empty — mirroring AITradingBot.enforceRangingAllowlist
// so the orchestrator's spawn decision and the bot's entry decision stay consistent.
func rangingAllowSet(snap *dynamic.ScopedSnapshot) map[string]struct{} {
	if snap == nil || snap.String("bot.entry.regime_gate.mode") != "ranging" {
		return nil
	}
	raw := strings.TrimSpace(snap.String("bot.entry.ranging.pair_allowlist"))
	if raw == "" {
		return nil
	}
	set := parseAllowlist(raw)
	if len(set) == 0 {
		return nil
	}
	return set
}

// parseAllowlist turns a comma-separated pair list into a set keyed by the
// normalised symbol, so membership tests treat SOL-USDT and SOLUSDTM as equal.
func parseAllowlist(raw string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, e := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(e); s != "" {
			set[normalizeSymbolForMatch(s)] = struct{}{}
		}
	}
	return set
}

func (o *MultiPairAIOrchestrator) killAllBots() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for pair, entry := range o.bots {
		o.log.WithField("pair", pair).Info("multi_pair_orchestrator: stopping bot on shutdown")
		entry.cancel()
	}
	o.bots = make(map[string]*pairBotEntry)
}

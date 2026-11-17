package master

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/services"
	"github.com/sirupsen/logrus"
)

// MasterConfig captures runtime config for the master agent loop.
type MasterConfig struct {
	Exchange         string
	LoopInterval     time.Duration
	ScanTopN         int
	SpawnThreshold   float64
	KillThreshold    float64
	FeedbackInterval time.Duration
	PaperTrading     bool
	// MultiPairEnabled gates bot spawning. When false the master agent scans
	// and scores pairs (for monitoring) but never spawns or kills bots.
	// Maps directly to config.yaml multi_pair.enabled.
	MultiPairEnabled bool
}

// MasterAgent is the supreme orchestrator. It runs a periodic loop that scans
// the market, plans bot allocations, and reconciles them against active bots.
type MasterAgent struct {
	log              *logrus.Logger
	config           MasterConfig
	scanner          *MarketScanner
	configGen        *ConfigGenerator
	lifecycle        *BotLifecycle
	feedback         *FeedbackCollector
	capitalAllocator *services.CapitalAllocator

	state   *MasterState
	stateMu sync.RWMutex

	running bool
	stopCh  chan struct{}
	runMu   sync.Mutex

	metrics *Metrics // optional Prometheus metrics; nil disables metrics

	// persistence is an optional backend that lets the agent survive restarts.
	// nil means in-memory only — state is lost on shutdown.
	persistence MasterPersistence

	// radar is an optional opportunity notification engine. nil disables it.
	radar *Radar

	// scanExchanges is the optional set of exchanges to scan as a matrix. When
	// empty the agent scans only config.Exchange (single-exchange behavior,
	// byte-for-byte identical to the prior Scan path).
	scanExchanges []string

	// balanceFetcher provides live USDT balance for the configured exchange.
	// Optional: when nil the capital allocator retains its last known value.
	balanceFetcher BalanceFetcher

	// liveBalanceIntervalScans controls how often the balance is refreshed.
	// A value of 12 means every 12 scans (~60 min at 5-min intervals).
	// Zero or negative disables the live balance refresh.
	liveBalanceIntervalScans int64
}

// SetScanExchanges configures the (pair × exchange) scan matrix. Pass the set
// of enabled exchanges to scan across all of them; leave unset (or pass empty)
// to preserve single-exchange behavior over config.Exchange.
func (ma *MasterAgent) SetScanExchanges(exchanges []string) {
	ma.scanExchanges = exchanges
}

// EnablePersistence attaches a persistence backend and warms the agent with
// any previously saved scoring weights and state snapshot. Errors during
// load are logged but never bubble up: the agent always falls back to its
// in-memory defaults.
func (ma *MasterAgent) EnablePersistence(p MasterPersistence) {
	ma.persistence = p
	if p == nil {
		return
	}

	if w, err := p.LoadScoringWeights(); err != nil {
		ma.log.WithError(err).Warn("master: failed to load persisted scoring weights")
	} else if w != nil && ma.scanner != nil {
		// Guard against a corrupted all-zero snapshot overwriting valid defaults.
		sum := w.Volatility + w.Volume + w.TrendStrength + w.Sentiment +
			w.RegimeMatch + w.BTCCorrelationPenalty + w.RecentLossPenalty + w.ProfitFactor
		if sum == 0 {
			ma.log.Warn("master: persisted scoring weights are all zero — ignoring, using defaults")
		} else {
			ma.scanner.SetWeights(w)
			ma.log.Info("master: loaded persisted scoring weights")
		}
	}

	if s, err := p.LoadStateSnapshot(); err != nil {
		ma.log.WithError(err).Warn("master: failed to load persisted state snapshot")
	} else if s != nil {
		ma.stateMu.Lock()
		ma.state.ScanCount = s.ScanCount
		ma.state.LastScanAt = s.LastScanAt
		ma.state.TotalCapitalUSD = s.TotalCapitalUSD
		ma.stateMu.Unlock()
		ma.log.WithFields(logrus.Fields{
			"scan_count":   s.ScanCount,
			"last_scan_at": s.LastScanAt,
		}).Info("master: restored prior state snapshot")
	}
}

// SaveCheckpoint persists the current state snapshot and scanner weights.
// Best-effort: errors are logged and never returned. Safe to call from any
// goroutine.
func (ma *MasterAgent) SaveCheckpoint() {
	if ma.persistence == nil {
		return
	}

	state := ma.GetState()
	if err := ma.persistence.SaveStateSnapshot(state); err != nil {
		ma.log.WithError(err).Warn("master: failed to save state snapshot")
	}

	if ma.scanner != nil {
		if w := ma.scanner.Weights(); w != nil {
			if err := ma.persistence.SaveScoringWeights(w); err != nil {
				ma.log.WithError(err).Warn("master: failed to save scoring weights")
			}
		}
	}
}

// GetWeights returns a copy of the current scanner scoring weights.
func (ma *MasterAgent) GetWeights() *ScoringWeights {
	if ma.scanner == nil {
		return nil
	}
	return ma.scanner.Weights()
}

// UpdateWeights sets new scoring weights on the scanner at runtime.
func (ma *MasterAgent) UpdateWeights(w *ScoringWeights) {
	if ma.scanner == nil || w == nil {
		return
	}
	ma.scanner.SetWeights(w)
}

// EnableMetrics attaches a Prometheus metrics collector to the master agent.
// Pass nil to disable. The caller is responsible for registering m with a
// prometheus.Registerer (see Metrics.Register) before or after calling this.
func (ma *MasterAgent) EnableMetrics(m *Metrics) {
	ma.metrics = m
	if m != nil {
		m.SetRunning(ma.IsRunning())
	}
}

// SetRadar attaches an opportunity radar. Call before Start. nil is a no-op.
func (ma *MasterAgent) SetRadar(r *Radar) {
	ma.radar = r
}

// SetBalanceFetcher attaches a live balance source and sets how often (every N
// scans) the allocator's TotalCapitalUSD is refreshed.
// intervalScans ≤ 0 uses the default of 12 (~60 min at 5-min loop intervals).
// Call before Start.
func (ma *MasterAgent) SetBalanceFetcher(f BalanceFetcher, intervalScans int64) {
	ma.balanceFetcher = f
	if intervalScans <= 0 {
		intervalScans = 12
	}
	ma.liveBalanceIntervalScans = intervalScans
}

// NewMasterAgent constructs a new master agent.
func NewMasterAgent(
	log *logrus.Logger,
	config MasterConfig,
	scanner *MarketScanner,
	configGen *ConfigGenerator,
	lifecycle *BotLifecycle,
	feedback *FeedbackCollector,
	allocator *services.CapitalAllocator,
) *MasterAgent {
	if log == nil {
		log = logrus.New()
	}
	if config.LoopInterval <= 0 {
		config.LoopInterval = 5 * time.Minute
	}
	if config.FeedbackInterval <= 0 {
		config.FeedbackInterval = 1 * time.Hour
	}
	if config.ScanTopN <= 0 {
		config.ScanTopN = 20
	}
	if config.Exchange == "" {
		config.Exchange = "kucoin"
	}
	return &MasterAgent{
		log:              log,
		config:           config,
		scanner:          scanner,
		configGen:        configGen,
		lifecycle:        lifecycle,
		feedback:         feedback,
		capitalAllocator: allocator,
		state: &MasterState{
			Allocations: make(map[string]*services.AllocationResult),
		},
		stopCh: make(chan struct{}),
	}
}

// Start kicks off the master loop in a goroutine.
func (ma *MasterAgent) Start(ctx context.Context) error {
	ma.runMu.Lock()
	if ma.running {
		ma.runMu.Unlock()
		return fmt.Errorf("master agent already running")
	}
	ma.running = true
	ma.stopCh = make(chan struct{})
	ma.runMu.Unlock()

	ma.stateMu.Lock()
	ma.state.Running = true
	ma.state.StartedAt = time.Now()
	ma.state.MultiPairEnabled = ma.config.MultiPairEnabled
	ma.state.SpawnThreshold = ma.config.SpawnThreshold
	ma.stateMu.Unlock()

	go ma.loop(ctx)

	if ma.metrics != nil {
		ma.metrics.SetRunning(true)
	}

	ma.log.WithFields(logrus.Fields{
		"exchange":      ma.config.Exchange,
		"loop_interval": ma.config.LoopInterval,
		"scan_top_n":    ma.config.ScanTopN,
		"paper":         ma.config.PaperTrading,
	}).Info("master_agent: started")
	return nil
}

// Stop signals the loop to stop. Returns immediately; the goroutine drains.
func (ma *MasterAgent) Stop() error {
	ma.runMu.Lock()
	defer ma.runMu.Unlock()
	if !ma.running {
		return fmt.Errorf("master agent not running")
	}
	close(ma.stopCh)
	ma.running = false

	ma.stateMu.Lock()
	ma.state.Running = false
	ma.stateMu.Unlock()

	if ma.metrics != nil {
		ma.metrics.SetRunning(false)
	}

	ma.log.Info("master_agent: stop signaled")
	return nil
}

// IsRunning reports whether the loop is active.
func (ma *MasterAgent) IsRunning() bool {
	ma.runMu.Lock()
	defer ma.runMu.Unlock()
	return ma.running
}

// GetState returns a snapshot of the master state.
func (ma *MasterAgent) GetState() *MasterState {
	ma.stateMu.RLock()
	defer ma.stateMu.RUnlock()

	cp := *ma.state
	cp.Opportunities = append([]*MarketOpportunity(nil), ma.state.Opportunities...)
	cp.Allocations = make(map[string]*services.AllocationResult, len(ma.state.Allocations))
	for k, v := range ma.state.Allocations {
		copyVal := *v
		cp.Allocations[k] = &copyVal
	}
	// Always reflect live config values (immutable after Start).
	cp.MultiPairEnabled = ma.config.MultiPairEnabled
	cp.SpawnThreshold = ma.config.SpawnThreshold
	return &cp
}

// ForceTick runs a single iteration immediately (useful for tests or manual triggers).
func (ma *MasterAgent) ForceTick(ctx context.Context) error {
	return ma.tick(ctx)
}

// loop is the main scheduling loop.
func (ma *MasterAgent) loop(ctx context.Context) {
	scanTicker := time.NewTicker(ma.config.LoopInterval)
	defer scanTicker.Stop()
	feedbackTicker := time.NewTicker(ma.config.FeedbackInterval)
	defer feedbackTicker.Stop()

	if err := ma.tick(ctx); err != nil {
		ma.log.WithError(err).Warn("master_agent: initial tick failed")
	}

	for {
		select {
		case <-ctx.Done():
			ma.log.Info("master_agent: context done, exiting loop")
			return
		case <-ma.stopCh:
			ma.log.Info("master_agent: stop signal received, exiting loop")
			return
		case <-scanTicker.C:
			if err := ma.tick(ctx); err != nil {
				ma.log.WithError(err).Warn("master_agent: tick failed")
			}
		case <-feedbackTicker.C:
			ma.feedbackCycle()
		}
	}
}

// tick performs one full master cycle.
func (ma *MasterAgent) tick(ctx context.Context) error {
	start := time.Now()
	defer func() {
		ma.stateMu.Lock()
		ma.state.LastScanAt = start
		ma.state.LastScanDuration = time.Since(start)
		ma.state.ScanCount++
		ma.stateMu.Unlock()
	}()

	if ma.scanner == nil || ma.configGen == nil || ma.lifecycle == nil {
		err := fmt.Errorf("master agent: missing required component")
		ma.recordError(err)
		if ma.metrics != nil {
			ma.metrics.RecordScanError()
		}
		return err
	}

	// Resolve the scan matrix: explicit scanExchanges when set, otherwise the
	// single configured exchange (identical to the prior Scan behavior).
	exchanges := ma.scanExchanges
	if len(exchanges) == 0 {
		exchanges = []string{ma.config.Exchange}
	}

	opportunities, err := ma.scanner.ScanMatrix(ctx, exchanges, ma.config.ScanTopN)
	if err != nil {
		ma.recordError(err)
		if ma.metrics != nil {
			ma.metrics.RecordScanError()
		}
		return fmt.Errorf("scan: %w", err)
	}

	// Observational cross-exchange arbitrage signals (multi-exchange only).
	// Logged for visibility; feeds future arb engines. Nothing is executed.
	if len(exchanges) > 1 {
		const arbMinScoreDiff = 5.0 // OpportunityScore points
		for _, signal := range DetectCrossExchangeArb(opportunities, arbMinScoreDiff) {
			ma.log.WithField("signal", signal).Info("🔀 cross-exchange arb signal")
		}
	}

	// Log top-5 opportunities every scan so operators can validate the ranking
	// without depending on the HTTP /master/opportunities endpoint (which can
	// 404 if route registration conflicts). Scanner returns sorted desc by score.
	if n := len(opportunities); n > 0 {
		top := n
		if top > 5 {
			top = 5
		}
		ranks := make([]string, 0, top)
		for i := 0; i < top; i++ {
			o := opportunities[i]
			ranks = append(ranks, fmt.Sprintf("%s=%.3f", o.Pair, o.OpportunityScore/100.0))
		}
		ma.log.WithField("top5", strings.Join(ranks, " ")).WithField("total", n).Info("📊 master scan ranking")
	}

	// Build plans for opportunities that meet the spawn threshold.
	plans := make(map[string]*BotPlan)
	pairOps := make([]*services.PairOpportunity, 0, len(opportunities))

	for _, opp := range opportunities {
		normalized := opp.OpportunityScore / 100.0
		if normalized < ma.config.SpawnThreshold {
			continue
		}

		// Reuse the regime analysis cached by the scanner when available.
		// This avoids a second GetCandles + Analyze round-trip that silently
		// drops ALL pairs when GCT is slow or temporarily unavailable, which
		// left pairOps empty and caused "no valid opportunities" even when
		// the scanner had scored pairs at 50-54 points.
		analysis := opp.CachedAnalysis
		if analysis == nil {
			// Fallback: fetch candles and run regime analysis (legacy path for
			// opportunities not populated by the current scanner version).
			candles, err := ma.scanner.gctClient.GetCandles(ctx, ma.config.Exchange, opp.Pair, "60", 200)
			if err != nil {
				ma.log.WithError(err).WithField("pair", opp.Pair).Debug("master: candles fetch failed, skipping plan")
				continue
			}
			var aerr error
			analysis, aerr = ma.scanner.regimeDetector.Analyze(candles)
			if aerr != nil {
				ma.log.WithError(aerr).WithField("pair", opp.Pair).Debug("master: regime analysis failed, skipping plan")
				continue
			}
		}

		cfg := ma.configGen.Generate(opp, analysis)
		if cfg == nil {
			continue // chaos regime or invalid input
		}
		plans[opp.Pair] = &BotPlan{
			Pair:   opp.Pair,
			Config: cfg,
			Score:  normalized,
		}
		pairOps = append(pairOps, &services.PairOpportunity{
			Pair:             opp.Pair,
			OpportunityScore: opp.OpportunityScore,
			Volatility:       opp.Volatility,
			Confidence:       normalized,
			BTCCorrelation:   opp.BTCCorrelation,
		})
	}

	// Refresh capital from exchange periodically to keep allocation accurate.
	// Errors are non-fatal: the allocator retains its last known value.
	ma.stateMu.RLock()
	currentScanCount := ma.state.ScanCount
	ma.stateMu.RUnlock()
	interval := ma.liveBalanceIntervalScans
	if interval <= 0 {
		interval = 12
	}
	if ma.balanceFetcher != nil && ma.capitalAllocator != nil && currentScanCount%interval == 0 {
		if balance, err := ma.balanceFetcher.GetUSDTBalance(ctx, ma.config.Exchange); err != nil {
			ma.log.WithError(err).Warn("master: live balance fetch failed, retaining last known capital")
		} else if balance > 0 {
			ma.capitalAllocator.SetTotalCapital(balance)
			ma.log.WithField("usdt_balance", balance).Info("master: updated capital allocator from live balance")
		}
	}

	// Distribute capital via the allocator.
	allocations := map[string]*services.AllocationResult{}
	if ma.capitalAllocator != nil {
		allocations = ma.capitalAllocator.Allocate(pairOps)
		for pair, alloc := range allocations {
			if plan, ok := plans[pair]; ok {
				plan.AllocatedUSD = alloc.AllocatedUSD
			}
		}
		// Drop plans the allocator decided to give zero capital.
		for pair, plan := range plans {
			if plan.AllocatedUSD <= 0 {
				delete(plans, pair)
			}
		}
	}

	// When multi_pair.enabled=false: scan + score + log opportunities but do NOT
	// spawn or kill bots. This lets operators validate the master agent in
	// observation mode before flipping the flag to activate live spawning.
	if !ma.config.MultiPairEnabled {
		ma.log.WithField("opportunities", len(plans)).
			Info("master: multi_pair.enabled=false — scan-only mode, bot spawn/kill skipped")
		ma.stateMu.Lock()
		ma.state.Opportunities = opportunities
		ma.state.Allocations = allocations
		ma.state.LastError = ""
		ma.stateMu.Unlock()
		// Radar evaluation is intentionally skipped in scan-only mode because
		// the caller has not selected pairs yet (no active mode to evaluate).
		return nil
	}

	activeBots := ma.lifecycle.spawner.ListActiveBots()
	actions := ma.lifecycle.Reconcile(plans, activeBots)

	for _, a := range actions {
		entry := ma.log.WithFields(logrus.Fields{
			"pair":   a.Pair,
			"action": a.Action,
			"reason": a.Reason,
		})
		if a.Error != "" {
			entry.WithField("error", a.Error).Warn("master: lifecycle action error")
		} else {
			entry.Debug("master: lifecycle action")
		}
		if ma.metrics != nil {
			ma.metrics.RecordLifecycleDecision(a.Action)
		}
	}

	totalAlloc := 0.0
	for _, a := range allocations {
		totalAlloc += a.AllocatedUSD
	}

	ma.stateMu.Lock()
	ma.state.Opportunities = opportunities
	ma.state.Allocations = allocations
	ma.state.ActiveBots = len(ma.lifecycle.spawner.ListActiveBots())
	ma.state.AllocatedUSD = totalAlloc
	ma.state.LastError = ""
	ma.stateMu.Unlock()

	// Opportunity radar — notify users about high-score pairs they haven't
	// selected. Wiring (notifier + device tokens) is injected via SetRadar;
	// when radar is nil the block is a no-op.
	if ma.radar != nil {
		// Pass nil selected list — full delivery wiring (user selection repo +
		// notifier + device tokens) is left for a follow-up wire-up; for now
		// the radar decision core is in place and will short-circuit cleanly.
		radarCandidates := ma.radar.Candidates(opportunities, nil, time.Now())
		_ = radarCandidates // notifier wire-up: call radar.Notify per candidate
	}

	// Persist asynchronously — failures are logged, never propagated.
	if ma.persistence != nil {
		scanID := uuid.NewString()
		oppsCopy := append([]*MarketOpportunity(nil), opportunities...)
		stateCopy := ma.GetState()
		go func() {
			if err := ma.persistence.SaveOpportunities(oppsCopy, scanID); err != nil {
				ma.log.WithError(err).Debug("master: failed to persist opportunities")
			}
			if err := ma.persistence.SaveStateSnapshot(stateCopy); err != nil {
				ma.log.WithError(err).Debug("master: failed to persist state snapshot")
			}
		}()
	}

	ma.log.WithFields(logrus.Fields{
		"opportunities": len(opportunities),
		"plans":         len(plans),
		"actions":       len(actions),
		"allocated_usd": totalAlloc,
		"duration":      time.Since(start),
	}).Info("master_agent: tick complete")

	if ma.metrics != nil {
		ma.metrics.RecordScan(time.Since(start), opportunities)
		ma.metrics.UpdateActiveBots(ma.state.ActiveBots)
		perPair := make(map[string]float64, len(allocations))
		totalCapital := 0.0
		for pair, alloc := range allocations {
			if alloc == nil {
				continue
			}
			perPair[pair] = alloc.AllocatedUSD
			totalCapital += alloc.AllocatedUSD
		}
		// TODO: thread the allocator's actual total-capital figure through
		// here once CapitalAllocator exposes it; for now total ≈ allocated.
		ma.metrics.UpdateCapital(totalCapital, totalAlloc, perPair)
	}

	return nil
}

// feedbackCycle aggregates trade history and adjusts scanner weights.
func (ma *MasterAgent) feedbackCycle() {
	if ma.feedback == nil {
		return
	}
	fb := ma.feedback.Collect(7)
	ma.feedback.AdjustWeights(fb)
	if ma.scanner != nil {
		ma.scanner.SetFeedback(fb)
	}
	// Persist the freshly adjusted weights so they survive restarts.
	if ma.persistence != nil && ma.scanner != nil {
		if w := ma.scanner.Weights(); w != nil {
			weightsCopy := *w
			go func() {
				if err := ma.persistence.SaveScoringWeights(&weightsCopy); err != nil {
					ma.log.WithError(err).Debug("master: failed to persist scoring weights")
				}
			}()
		}
	}
	if ma.metrics != nil {
		ma.metrics.RecordFeedbackCycle()
		// TODO: emit RecordWeightAdjustment per scoring feature once
		// FeedbackCollector.AdjustWeights returns the set of features that
		// were actually mutated this cycle.
	}
	ma.log.WithField("pairs", len(fb)).Info("master_agent: feedback cycle complete")
}

func (ma *MasterAgent) recordError(err error) {
	ma.stateMu.Lock()
	ma.state.LastError = err.Error()
	ma.stateMu.Unlock()
}

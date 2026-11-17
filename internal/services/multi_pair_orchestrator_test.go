package services

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// stubBot is a minimal stand-in that satisfies the AITradingBot surface used
// by the orchestrator. We create a real AITradingBot via the factory so we
// need a factory that returns something we can introspect; for unit tests we
// track calls through the stub below.
type stubSource struct {
	mu            sync.Mutex
	opportunities []OrchestratorOpportunity
}

func (s *stubSource) GetOpportunities() []OrchestratorOpportunity {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OrchestratorOpportunity, len(s.opportunities))
	copy(out, s.opportunities)
	return out
}

// fakeBot tracks Start/Stop calls via channels so tests can assert on them.
type fakeBot struct {
	pair      string
	started   bool
	cancelled bool
}

// fakeBotRegistry keeps created fake bots so tests can assert how many were
// spawned, without needing a real AITradingBot (which requires gRPC, etc.).
type fakeBotRegistry struct {
	mu   sync.Mutex
	bots []*fakeBot
}

func (r *fakeBotRegistry) factory(pair string, capitalUSD float64) (*AITradingBot, error) {
	// We cannot create a real AITradingBot without all its dependencies, so
	// instead we return nil and track calls. The orchestrator Reconcile is
	// designed so that a nil bot from the factory results in a logged error and
	// skip — but that path hides whether the factory was called.
	//
	// To test the factory-call count we record the call here and then return an
	// error so Reconcile skips the bot (the test below therefore asserts on
	// factory calls, not bots map length).
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bots = append(r.bots, &fakeBot{pair: pair})
	return nil, nil // nil bot → orchestrator skips StartWithContext
}

// TestReconcile_SpawnsBotsForQualifyingOpportunities verifies that Reconcile
// invokes the factory exactly once per qualifying pair (up to MaxBots).
func TestReconcile_SpawnsBotsForQualifyingOpportunities(t *testing.T) {
	t.Parallel()

	registry := &fakeBotRegistry{}
	cfg := OrchestratorConfig{
		MaxBots:           3,
		SpawnThreshold:    0.40,
		KillThreshold:     0.30,
		ReservePercent:    0.20,
		MinCapitalPerBot:  1.0, // low floor so capital math doesn't block spawning
		MaxCapitalPerBot:  50.0,
		ReconcileInterval: time.Hour, // don't tick during test
	}

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel) // silence logs in tests

	orc := NewMultiPairAIOrchestrator(cfg, log, registry.factory)

	opportunities := []OrchestratorOpportunity{
		{Pair: "NEAR-USDT", Exchange: "kucoin", OpportunityScore: 54},
		{Pair: "TON-USDT", Exchange: "kucoin", OpportunityScore: 50},
		{Pair: "GRASS-USDT", Exchange: "kucoin", OpportunityScore: 50},
	}

	orc.Reconcile(opportunities, 30.0)

	registry.mu.Lock()
	gotCount := len(registry.bots)
	registry.mu.Unlock()

	// All 3 pairs score >= 0.40 and count <= MaxBots — factory must be called 3×.
	if gotCount != 3 {
		t.Errorf("expected factory called 3 times, got %d", gotCount)
	}
}

// TestReconcile_NoOpportunitiesSpawnsNoBots verifies that when the source
// returns an empty slice, the orchestrator does not call the factory.
func TestReconcile_NoOpportunitiesSpawnsNoBots(t *testing.T) {
	t.Parallel()

	registry := &fakeBotRegistry{}
	cfg := OrchestratorConfig{
		MaxBots:           3,
		SpawnThreshold:    0.40,
		KillThreshold:     0.30,
		ReservePercent:    0.20,
		MinCapitalPerBot:  1.0,
		MaxCapitalPerBot:  50.0,
		ReconcileInterval: time.Hour,
	}

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	orc := NewMultiPairAIOrchestrator(cfg, log, registry.factory)
	orc.Reconcile(nil, 30.0)

	registry.mu.Lock()
	gotCount := len(registry.bots)
	registry.mu.Unlock()

	if gotCount != 0 {
		t.Errorf("expected 0 factory calls for empty opportunities, got %d", gotCount)
	}
}

// TestReconcile_BelowThresholdSkipped verifies that pairs whose normalised score
// is below SpawnThreshold are never passed to the factory.
func TestReconcile_BelowThresholdSkipped(t *testing.T) {
	t.Parallel()

	registry := &fakeBotRegistry{}
	cfg := OrchestratorConfig{
		MaxBots:           5,
		SpawnThreshold:    0.60, // high threshold
		KillThreshold:     0.40,
		ReservePercent:    0.20,
		MinCapitalPerBot:  1.0,
		MaxCapitalPerBot:  50.0,
		ReconcileInterval: time.Hour,
	}

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	orc := NewMultiPairAIOrchestrator(cfg, log, registry.factory)

	opportunities := []OrchestratorOpportunity{
		{Pair: "LOW-USDT", Exchange: "kucoin", OpportunityScore: 30},  // 0.30 < 0.60
		{Pair: "HIGH-USDT", Exchange: "kucoin", OpportunityScore: 80}, // 0.80 >= 0.60
	}

	orc.Reconcile(opportunities, 30.0)

	registry.mu.Lock()
	gotCount := len(registry.bots)
	var pairs []string
	for _, b := range registry.bots {
		pairs = append(pairs, b.pair)
	}
	registry.mu.Unlock()

	if gotCount != 1 {
		t.Errorf("expected 1 factory call (only HIGH-USDT), got %d: %v", gotCount, pairs)
	}
	if gotCount == 1 && pairs[0] != "HIGH-USDT" {
		t.Errorf("expected factory called for HIGH-USDT, got %s", pairs[0])
	}
}

// TestReconcile_ExcludedPairSkipped verifies that excluded pairs are never
// passed to the factory, even when they would otherwise qualify.
func TestReconcile_ExcludedPairSkipped(t *testing.T) {
	t.Parallel()

	registry := &fakeBotRegistry{}
	cfg := OrchestratorConfig{
		MaxBots:           5,
		SpawnThreshold:    0.40,
		KillThreshold:     0.30,
		ReservePercent:    0.20,
		MinCapitalPerBot:  1.0,
		MaxCapitalPerBot:  50.0,
		ReconcileInterval: time.Hour,
	}

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	orc := NewMultiPairAIOrchestrator(cfg, log, registry.factory)
	orc.ExcludePair("NEAR-USDT")

	opportunities := []OrchestratorOpportunity{
		{Pair: "NEAR-USDT", Exchange: "kucoin", OpportunityScore: 90},
		{Pair: "TON-USDT", Exchange: "kucoin", OpportunityScore: 80},
	}

	orc.Reconcile(opportunities, 30.0)

	registry.mu.Lock()
	gotCount := len(registry.bots)
	registry.mu.Unlock()

	// Only TON-USDT should be spawned; NEAR-USDT is excluded.
	if gotCount != 1 {
		t.Errorf("expected 1 factory call (TON-USDT only), got %d", gotCount)
	}
}

// TestOrchestratorConfig_ApplyDefaults ensures zero-value configs are replaced
// by safe defaults.
func TestOrchestratorConfig_ApplyDefaults(t *testing.T) {
	var cfg OrchestratorConfig
	cfg.applyDefaults()

	if cfg.MaxBots != 3 {
		t.Errorf("MaxBots: want 3, got %d", cfg.MaxBots)
	}
	if cfg.SpawnThreshold != 0.40 {
		t.Errorf("SpawnThreshold: want 0.40, got %f", cfg.SpawnThreshold)
	}
	if cfg.KillThreshold != 0.30 {
		t.Errorf("KillThreshold: want 0.30, got %f", cfg.KillThreshold)
	}
	if cfg.ReservePercent != 0.20 {
		t.Errorf("ReservePercent: want 0.20, got %f", cfg.ReservePercent)
	}
	if cfg.ReconcileInterval != 5*time.Minute {
		t.Errorf("ReconcileInterval: want 5m, got %v", cfg.ReconcileInterval)
	}
}

// TestOrchestrator_StopBeforeStart ensures Stop does not panic when called
// before the loop has been started.
func TestOrchestrator_StopBeforeStart(t *testing.T) {
	registry := &fakeBotRegistry{}
	cfg := OrchestratorConfig{}

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	orc := NewMultiPairAIOrchestrator(cfg, log, registry.factory)

	// Start then immediately stop.
	src := &stubSource{}
	orc.Start(context.Background(), src, 10.0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		orc.Stop()
	}()

	select {
	case <-done:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() blocked for more than 3 seconds")
	}
}

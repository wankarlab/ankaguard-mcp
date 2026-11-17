package master

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the Prometheus collectors for the Master Agent.
//
// All metrics are namespaced with the prefix "trading_mcp_master_" to avoid
// collisions with other collectors registered under the global trading-mcp
// registry (see internal/monitoring/metrics.go).
//
// Usage is opt-in: callers must invoke MasterAgent.EnableMetrics with a
// non-nil *Metrics. When metrics are disabled (the default), the Master
// Agent behaves identically and incurs no overhead beyond a nil check.
type Metrics struct {
	// Scanning
	ScanCount        prometheus.Counter
	ScanDuration     prometheus.Histogram
	ScanErrors       prometheus.Counter
	OpportunityScore *prometheus.GaugeVec // labelled by pair
	PairsScanned    prometheus.Gauge

	// Lifecycle decisions
	SpawnDecisions *prometheus.CounterVec // labelled by action (SPAWN, KILL, MAINTAIN, BLOCKED_COOLDOWN, SKIP_BELOW_SPAWN)
	ActiveBots     prometheus.Gauge

	// Capital
	TotalCapital      prometheus.Gauge
	AllocatedCapital  prometheus.Gauge
	AllocationsByPair *prometheus.GaugeVec // labelled by pair

	// Feedback
	FeedbackCycles    prometheus.Counter
	WeightAdjustments *prometheus.CounterVec // labelled by feature (volatility, volume, etc.)

	// Health
	LastScanAt  prometheus.Gauge // unix timestamp of last successful scan
	LoopRunning prometheus.Gauge // 1 if running, 0 otherwise

	registered bool
	mu         sync.Mutex
}

// NewMetrics constructs the metrics with a "trading_mcp_master_" prefix.
// The collectors are not yet registered; call Register to attach them to a
// prometheus.Registerer.
func NewMetrics() *Metrics {
	return &Metrics{
		ScanCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_mcp_master_scan_count_total",
			Help: "Total number of market scans performed by the Master Agent",
		}),
		ScanDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "trading_mcp_master_scan_duration_seconds",
			Help:    "Duration of each Master Agent scan tick",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60},
		}),
		ScanErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_mcp_master_scan_errors_total",
			Help: "Total number of scan errors encountered",
		}),
		OpportunityScore: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trading_mcp_master_opportunity_score",
			Help: "Last opportunity score per pair (0-100)",
		}, []string{"pair"}),
		PairsScanned: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_mcp_master_pairs_scanned",
			Help: "Number of pairs scanned in the last cycle",
		}),
		SpawnDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trading_mcp_master_lifecycle_decisions_total",
			Help: "Bot lifecycle decisions emitted by the Master Agent",
		}, []string{"action"}),
		ActiveBots: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_mcp_master_active_bots",
			Help: "Number of bots currently managed by the Master Agent",
		}),
		TotalCapital: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_mcp_master_total_capital_usd",
			Help: "Total capital available to the Master Agent in USD",
		}),
		AllocatedCapital: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_mcp_master_allocated_capital_usd",
			Help: "Total capital currently allocated across active bots in USD",
		}),
		AllocationsByPair: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trading_mcp_master_allocation_by_pair_usd",
			Help: "Capital allocated to each pair in USD",
		}, []string{"pair"}),
		FeedbackCycles: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_mcp_master_feedback_cycles_total",
			Help: "Number of feedback adjustment cycles run",
		}),
		WeightAdjustments: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trading_mcp_master_weight_adjustments_total",
			Help: "Number of times each scoring feature was adjusted",
		}, []string{"feature"}),
		LastScanAt: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_mcp_master_last_scan_unix_seconds",
			Help: "Unix timestamp of the last successful scan",
		}),
		LoopRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_mcp_master_loop_running",
			Help: "1 if the Master Agent loop is running, 0 otherwise",
		}),
	}
}

// Register registers all collectors with the given registry.
// Calling Register more than once is a no-op. AlreadyRegistered errors from
// individual collectors are tolerated to keep this safe across shared
// registries.
func (m *Metrics) Register(reg prometheus.Registerer) error {
	if reg == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registered {
		return nil
	}
	collectors := []prometheus.Collector{
		m.ScanCount, m.ScanDuration, m.ScanErrors, m.OpportunityScore,
		m.PairsScanned, m.SpawnDecisions, m.ActiveBots, m.TotalCapital,
		m.AllocatedCapital, m.AllocationsByPair, m.FeedbackCycles,
		m.WeightAdjustments, m.LastScanAt, m.LoopRunning,
	}
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			// Already-registered → ignore (defensive in case registry shared)
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				return err
			}
		}
	}
	m.registered = true
	return nil
}

// Helper recorders — used by master_agent.go callsites.

// RecordScan records a successful scan tick: increments the scan counter,
// observes the duration, updates per-pair opportunity scores, and stamps
// the last-scan timestamp.
func (m *Metrics) RecordScan(duration time.Duration, opportunities []*MarketOpportunity) {
	m.ScanCount.Inc()
	m.ScanDuration.Observe(duration.Seconds())
	m.PairsScanned.Set(float64(len(opportunities)))
	m.LastScanAt.Set(float64(time.Now().Unix()))
	for _, opp := range opportunities {
		if opp == nil {
			continue
		}
		m.OpportunityScore.WithLabelValues(opp.Pair).Set(opp.OpportunityScore)
	}
}

// RecordScanError increments the scan error counter.
func (m *Metrics) RecordScanError() {
	m.ScanErrors.Inc()
}

// RecordLifecycleDecision increments the decision counter for the given
// action label (e.g. SPAWN, KILL, MAINTAIN, BLOCKED_COOLDOWN).
func (m *Metrics) RecordLifecycleDecision(action string) {
	m.SpawnDecisions.WithLabelValues(action).Inc()
}

// UpdateCapital sets the total/allocated capital gauges and the per-pair
// allocation gauge.
func (m *Metrics) UpdateCapital(total, allocated float64, perPair map[string]float64) {
	m.TotalCapital.Set(total)
	m.AllocatedCapital.Set(allocated)
	for pair, usd := range perPair {
		m.AllocationsByPair.WithLabelValues(pair).Set(usd)
	}
}

// UpdateActiveBots sets the active-bots gauge.
func (m *Metrics) UpdateActiveBots(n int) {
	m.ActiveBots.Set(float64(n))
}

// RecordFeedbackCycle increments the feedback-cycle counter.
func (m *Metrics) RecordFeedbackCycle() {
	m.FeedbackCycles.Inc()
}

// RecordWeightAdjustment increments the weight-adjustment counter for the
// given scoring feature label.
func (m *Metrics) RecordWeightAdjustment(feature string) {
	m.WeightAdjustments.WithLabelValues(feature).Inc()
}

// SetRunning sets the loop-running gauge to 1 (running) or 0 (stopped).
func (m *Metrics) SetRunning(running bool) {
	if running {
		m.LoopRunning.Set(1)
	} else {
		m.LoopRunning.Set(0)
	}
}

// Package backtest — runner.go implements the async worker that processes pending backtest runs.
package backtest

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/backtest/repository"
)

const (
	backtestTimeout = 5 * time.Minute
	pollInterval    = 5 * time.Second
	// Cleanup is triggered every cleanupCycles polling ticks (≈ 24h if cycle = 5s and we use 17280).
	// Using a smaller default of 720 (1h) for responsiveness.
	cleanupCycles = 720
	cleanupMaxAge = 30 * 24 * time.Hour
)

// Prometheus metrics — registered lazily; promauto handles double-registration gracefully.
var (
	backtestRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "backtest_runs_total",
		Help: "Total number of backtest runs by terminal status.",
	}, []string{"status"})

	backtestDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "backtest_duration_seconds",
		Help:    "Duration of completed backtest runs in seconds.",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s … 512s
	})
)

// Runner is the async worker that dequeues pending backtest runs, executes them via
// Engine, persists results, and performs periodic cleanup.
type Runner struct {
	engine *Engine
	repo   repository.Repository
	log    *logrus.Logger
}

// NewRunner constructs a Runner.
func NewRunner(engine *Engine, repo repository.Repository, log *logrus.Logger) *Runner {
	return &Runner{engine: engine, repo: repo, log: log}
}

// Run is the main loop. It polls for pending runs every 5 seconds and processes them
// serially. Cleanup of old runs is triggered every ~cleanupCycles iterations.
// The loop exits when ctx is cancelled (e.g. on graceful shutdown).
func (r *Runner) Run(ctx context.Context) {
	r.log.Info("backtest runner: started")
	tick := 0
	for {
		select {
		case <-ctx.Done():
			r.log.Info("backtest runner: shutting down")
			return
		case <-time.After(pollInterval):
			tick++
			r.processPending(ctx)

			// Periodic cleanup: every cleanupCycles ticks delete runs older than 30 days.
			if tick%cleanupCycles == 0 {
				r.cleanup(ctx)
			}
		}
	}
}

// processPending fetches and executes one pending run per poll cycle.
func (r *Runner) processPending(ctx context.Context) {
	run, err := r.repo.FetchPending(ctx)
	if err != nil {
		r.log.WithError(err).Warn("backtest runner: FetchPending error")
		return
	}
	if run == nil {
		return // Nothing pending.
	}
	r.execute(ctx, run)
}

// execute runs the engine for a single BacktestRun and persists the outcome.
func (r *Runner) execute(ctx context.Context, run *domain.BacktestRun) {
	start := time.Now()
	r.log.WithFields(logrus.Fields{
		"run_id":   run.ID,
		"pair":     run.Pair,
		"exchange": run.Exchange,
	}).Info("backtest runner: executing run")

	// Apply a hard 5-minute timeout per backtest.
	btCtx, cancel := context.WithTimeout(ctx, backtestTimeout)
	defer cancel()

	req := domain.BacktestRequest{
		Overrides:      run.ConfigOverrides,
		BaselinePreset: run.Baseline,
		Pair:           run.Pair,
		Exchange:       run.Exchange,
		From:           run.HorizonFrom,
		To:             run.HorizonTo,
		TriggeredBy:    run.TriggeredBy,
	}

	result, err := r.engine.Run(btCtx, req)
	elapsed := time.Since(start)

	if err != nil {
		r.log.WithError(err).WithField("run_id", run.ID).Error("backtest runner: run failed")
		if markErr := r.repo.MarkFailed(ctx, run.ID, err.Error()); markErr != nil {
			r.log.WithError(markErr).Error("backtest runner: MarkFailed error")
		}
		backtestRunsTotal.WithLabelValues("failed").Inc()
		return
	}

	// Persist trades in bulk.
	if len(result.Trades) > 0 {
		if bulkErr := r.repo.BulkInsertTrades(ctx, run.ID, result.Trades); bulkErr != nil {
			r.log.WithError(bulkErr).Warn("backtest runner: BulkInsertTrades error")
		}
	}

	// Mark done with computed metrics.
	if markErr := r.repo.MarkDone(ctx, run.ID, result.Metrics); markErr != nil {
		r.log.WithError(markErr).Error("backtest runner: MarkDone error")
		backtestRunsTotal.WithLabelValues("failed").Inc()
		return
	}

	backtestRunsTotal.WithLabelValues("done").Inc()
	backtestDurationSeconds.Observe(elapsed.Seconds())

	r.log.WithFields(logrus.Fields{
		"run_id":   run.ID,
		"elapsed":  elapsed.Round(time.Millisecond).String(),
		"n_trades": result.Metrics.NTrades,
		"win_rate": result.Metrics.WinRate,
	}).Info("backtest runner: run done")
}

// cleanup removes runs older than cleanupMaxAge.
func (r *Runner) cleanup(ctx context.Context) {
	if err := r.repo.CleanupOldRuns(ctx, cleanupMaxAge); err != nil {
		r.log.WithError(err).Warn("backtest runner: cleanup error")
	} else {
		r.log.Debug("backtest runner: old runs cleanup completed")
	}
}

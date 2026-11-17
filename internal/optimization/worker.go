package optimization

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	optdomain "github.com/rantipay/trading-mcp/internal/optimization/domain"
)

// Worker wraps OptimizerService and provides scheduled execution.
// It runs on a weekly schedule (Sundays at 04:00 UTC) to keep suggestions fresh.
type Worker struct {
	svc    *OptimizerService
	optRepo interface {
		CreateRun(ctx context.Context, run *optdomain.OptimizerRun) (int64, error)
	}
	log *logrus.Logger

	// activePairs is the list of pairs to optimise each scheduled run.
	// TODO: replace hardcoded list with a query on bot_instances table once that table exists.
	activePairs []string
}

// NewWorker creates a Worker that will optimise the given pairs on each scheduled run.
func NewWorker(svc *OptimizerService, optRepo interface {
	CreateRun(ctx context.Context, run *optdomain.OptimizerRun) (int64, error)
}, log *logrus.Logger) *Worker {
	return &Worker{
		svc:    svc,
		optRepo: optRepo,
		log:    log,
		// TODO: query bot_instances table for active pairs; hardcoded for now.
		activePairs: []string{"NEAR-USDT", "CHZ-USDT", "BTC-USDT"},
	}
}

// RunScheduled blocks, checking every minute whether it is Sunday 04:00 UTC,
// and fires an optimization run for each active pair when the condition is met.
// Call this in a goroutine; the loop exits when ctx is cancelled.
//
// NOTE: robfig/cron is not in go.mod; using a simple ticker approach.
// TODO: add robfig/cron for more precise scheduling if needed.
func (w *Worker) RunScheduled(ctx context.Context) {
	w.log.Info("optimizer worker: scheduled worker started (runs Sundays 04:00 UTC)")
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	var lastRunDate string

	for {
		select {
		case <-ctx.Done():
			w.log.Info("optimizer worker: stopping (context cancelled)")
			return
		case t := <-ticker.C:
			utc := t.UTC()
			if utc.Weekday() == time.Sunday && utc.Hour() == 4 {
				dateKey := utc.Format("2006-01-02")
				if dateKey == lastRunDate {
					// Already ran today; skip until next minute check advances past hour 4.
					continue
				}
				lastRunDate = dateKey
				w.log.WithField("date", dateKey).Info("optimizer worker: Sunday 04:00 UTC — starting scheduled runs")
				w.runAllPairs(ctx)
			}
		}
	}
}

// TriggerNow runs the optimizer immediately for all active pairs (useful for manual testing).
func (w *Worker) TriggerNow(ctx context.Context) {
	w.log.Info("optimizer worker: manual trigger")
	w.runAllPairs(ctx)
}

// runAllPairs runs the optimizer for each active pair sequentially.
func (w *Worker) runAllPairs(ctx context.Context) {
	horizon := 14 * 24 * time.Hour
	now := time.Now().UTC()

	for _, pair := range w.activePairs {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log := w.log.WithField("pair", pair)
		log.Info("optimizer worker: starting run for pair")

		// Create a run record first.
		run := &optdomain.OptimizerRun{
			Pair:        pair,
			Objective:   "maximize_sharpe",
			SpaceName:   "scoring",
			Iterations:  50,
			TriggeredBy: "scheduler",
		}
		runID, err := w.optRepo.CreateRun(ctx, run)
		if err != nil {
			log.WithError(err).Error("optimizer worker: failed to create run record")
			continue
		}

		req := OptimizeRequest{
			Space:         ScoringSpace,
			SpaceName:     "scoring",
			ObjectiveName: "maximize_sharpe",
			Objective:     MaximizeSharpe,
			Pair:          pair,
			Exchange:      "KuCoin", // default exchange; TODO: derive from bot_instances
			HorizonFrom:   now.Add(-horizon),
			HorizonTo:     now,
			Iterations:    50,
			By:            "scheduler",
		}

		result, err := w.svc.Run(ctx, runID, req)
		if err != nil {
			log.WithError(err).Error("optimizer worker: run failed")
			continue
		}

		// TODO: send Telegram/push notification:
		// "Optimizer done. Top preset: <name> sharpe=<score>. Review at /admin/config."
		log.WithFields(logrus.Fields{
			"best_score":  result.BestScore,
			"top_presets": result.TopPresets,
		}).Info("optimizer worker: run completed — review presets in /api/v1/config/presets")
	}
}

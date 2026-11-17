// Package postgres implements the backtest Repository interface backed by PostgreSQL via pgx/v5.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/backtest/repository"
)

// repoImpl is the PostgreSQL-backed implementation of repository.Repository.
type repoImpl struct {
	pool *pgxpool.Pool
}

// New creates a new postgres-backed backtest repository.
func New(pool *pgxpool.Pool) repository.Repository {
	return &repoImpl{pool: pool}
}

// CreateRun inserts a new backtest_runs row with status='pending' and returns its ID.
func (r *repoImpl) CreateRun(ctx context.Context, req domain.BacktestRequest) (int64, error) {
	overridesJSON, err := json.Marshal(req.Overrides)
	if err != nil {
		return 0, fmt.Errorf("marshal overrides: %w", err)
	}

	baseline := req.BaselinePreset
	if baseline == "" {
		baseline = "current"
	}

	var id int64
	err = r.pool.QueryRow(ctx,
		`INSERT INTO backtest_runs
		 (config_overrides, baseline, pair, exchange, horizon_from, horizon_to, candle_count, status, triggered_by)
		 VALUES ($1, $2, $3, $4, $5, $6, 0, 'pending', $7)
		 RETURNING id`,
		overridesJSON, baseline, req.Pair, req.Exchange,
		req.From, req.To, req.TriggeredBy,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert backtest run: %w", err)
	}
	return id, nil
}

// FetchPending atomically selects one pending run (SKIP LOCKED) and returns it.
// Returns (nil, nil) when no pending runs exist.
func (r *repoImpl) FetchPending(ctx context.Context) (*domain.BacktestRun, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx,
		`SELECT id, config_overrides, baseline, pair, exchange, horizon_from, horizon_to,
		        candle_count, triggered_by, created_at
		 FROM backtest_runs
		 WHERE status = 'pending'
		 ORDER BY created_at ASC
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED`,
	)

	var run domain.BacktestRun
	var overridesJSON []byte
	err = row.Scan(
		&run.ID, &overridesJSON, &run.Baseline, &run.Pair, &run.Exchange,
		&run.HorizonFrom, &run.HorizonTo, &run.CandleCount, &run.TriggeredBy, &run.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		tx.Rollback(ctx) //nolint:errcheck
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan pending run: %w", err)
	}

	if err := json.Unmarshal(overridesJSON, &run.ConfigOverrides); err != nil {
		return nil, fmt.Errorf("unmarshal overrides: %w", err)
	}

	// Mark as running within the same transaction.
	now := time.Now()
	_, err = tx.Exec(ctx,
		`UPDATE backtest_runs SET status = 'running', started_at = $1 WHERE id = $2`,
		now, run.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("mark running: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	run.Status = "running"
	run.StartedAt = &now
	return &run, nil
}

// MarkRunning updates a run's status to 'running' if FetchPending wasn't used directly.
func (r *repoImpl) MarkRunning(ctx context.Context, id int64) error {
	now := time.Now()
	_, err := r.pool.Exec(ctx,
		`UPDATE backtest_runs SET status = 'running', started_at = $1 WHERE id = $2`,
		now, id,
	)
	return err
}

// MarkDone records successful completion and persists computed metrics.
func (r *repoImpl) MarkDone(ctx context.Context, id int64, metrics domain.Metrics) error {
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`UPDATE backtest_runs
		 SET status = 'done', finished_at = NOW(), metrics = $1
		 WHERE id = $2`,
		metricsJSON, id,
	)
	return err
}

// MarkFailed records a failure and stores the error message.
func (r *repoImpl) MarkFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE backtest_runs
		 SET status = 'failed', finished_at = NOW(), error = $1
		 WHERE id = $2`,
		errMsg, id,
	)
	return err
}

// InsertTrade inserts a single trade record.
func (r *repoImpl) InsertTrade(ctx context.Context, runID int64, trade domain.BacktestTrade) error {
	factorsJSON, err := json.Marshal(trade.SignalFactors)
	if err != nil {
		return fmt.Errorf("marshal signal factors: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO backtest_trades
		 (run_id, candle_time, side, entry_price, exit_price, exit_time, exit_reason, pnl_pct, confidence, signal_factors)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		runID, trade.CandleTime, trade.Side, trade.EntryPrice,
		trade.ExitPrice, trade.ExitTime, trade.ExitReason,
		trade.PnLPct, trade.Confidence, factorsJSON,
	)
	return err
}

// BulkInsertTrades inserts multiple trades in one pgx batch for efficiency.
func (r *repoImpl) BulkInsertTrades(ctx context.Context, runID int64, trades []domain.BacktestTrade) error {
	if len(trades) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, t := range trades {
		factorsJSON, err := json.Marshal(t.SignalFactors)
		if err != nil {
			factorsJSON = []byte("{}")
		}
		batch.Queue(
			`INSERT INTO backtest_trades
			 (run_id, candle_time, side, entry_price, exit_price, exit_time, exit_reason, pnl_pct, confidence, signal_factors)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			runID, t.CandleTime, t.Side, t.EntryPrice,
			t.ExitPrice, t.ExitTime, t.ExitReason,
			t.PnLPct, t.Confidence, factorsJSON,
		)
	}

	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()

	for range trades {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("bulk insert trade: %w", err)
		}
	}
	return nil
}

// GetRun retrieves the full run record for the given ID.
func (r *repoImpl) GetRun(ctx context.Context, id int64) (*domain.BacktestRun, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, config_overrides, baseline, pair, exchange,
		        horizon_from, horizon_to, candle_count, status,
		        started_at, finished_at, metrics, triggered_by, error, created_at
		 FROM backtest_runs WHERE id = $1`, id,
	)

	var run domain.BacktestRun
	var overridesJSON []byte
	var metricsJSON []byte
	err := row.Scan(
		&run.ID, &overridesJSON, &run.Baseline, &run.Pair, &run.Exchange,
		&run.HorizonFrom, &run.HorizonTo, &run.CandleCount, &run.Status,
		&run.StartedAt, &run.FinishedAt, &metricsJSON, &run.TriggeredBy, &run.Error, &run.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("backtest run %d not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("scan run: %w", err)
	}

	if err := json.Unmarshal(overridesJSON, &run.ConfigOverrides); err != nil {
		return nil, fmt.Errorf("unmarshal overrides: %w", err)
	}
	if len(metricsJSON) > 0 && string(metricsJSON) != "null" {
		m := &domain.Metrics{}
		if err := json.Unmarshal(metricsJSON, m); err != nil {
			return nil, fmt.Errorf("unmarshal metrics: %w", err)
		}
		run.Metrics = m
	}
	return &run, nil
}

// ListTrades returns up to limit trade records for the given run, ordered by candle_time ASC.
func (r *repoImpl) ListTrades(ctx context.Context, runID int64, limit int) ([]domain.BacktestTrade, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT candle_time, side, entry_price, exit_price, exit_time, exit_reason, pnl_pct, confidence, signal_factors
		 FROM backtest_trades WHERE run_id = $1 ORDER BY candle_time ASC LIMIT $2`,
		runID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query trades: %w", err)
	}
	defer rows.Close()

	var trades []domain.BacktestTrade
	for rows.Next() {
		var t domain.BacktestTrade
		var factorsJSON []byte
		t.RunID = runID
		if err := rows.Scan(
			&t.CandleTime, &t.Side, &t.EntryPrice, &t.ExitPrice,
			&t.ExitTime, &t.ExitReason, &t.PnLPct, &t.Confidence, &factorsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan trade: %w", err)
		}
		if len(factorsJSON) > 0 {
			_ = json.Unmarshal(factorsJSON, &t.SignalFactors)
		}
		trades = append(trades, t)
	}
	return trades, rows.Err()
}

// CleanupOldRuns hard-deletes runs older than the given duration.
// Cascade on backtest_trades handles the child rows automatically.
func (r *repoImpl) CleanupOldRuns(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM backtest_runs WHERE created_at < $1`, cutoff,
	)
	if err != nil {
		return fmt.Errorf("cleanup old backtest runs: %w", err)
	}
	_ = tag // rows deleted — logged by caller
	return nil
}

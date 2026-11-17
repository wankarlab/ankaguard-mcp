// Package repository defines the persistence interface for the backtesting subsystem.
package repository

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/backtest/domain"
)

// Repository defines all database operations required by the backtesting system.
type Repository interface {
	// CreateRun inserts a new backtest_runs row with status='pending' and returns its ID.
	CreateRun(ctx context.Context, req domain.BacktestRequest) (id int64, err error)

	// FetchPending atomically selects one pending run and marks it as running.
	// Returns (nil, nil) when no pending runs exist.
	FetchPending(ctx context.Context) (*domain.BacktestRun, error)

	// MarkRunning updates the run's status to 'running' and records started_at.
	MarkRunning(ctx context.Context, id int64) error

	// MarkDone updates the run's status to 'done', records finished_at, and persists metrics.
	MarkDone(ctx context.Context, id int64, metrics domain.Metrics) error

	// MarkFailed updates the run's status to 'failed' and records the error message.
	MarkFailed(ctx context.Context, id int64, errMsg string) error

	// InsertTrade inserts a single simulated trade record linked to a run.
	InsertTrade(ctx context.Context, runID int64, trade domain.BacktestTrade) error

	// BulkInsertTrades inserts multiple trades in one batch for performance.
	BulkInsertTrades(ctx context.Context, runID int64, trades []domain.BacktestTrade) error

	// GetRun returns the full run record for the given ID.
	GetRun(ctx context.Context, id int64) (*domain.BacktestRun, error)

	// ListTrades returns up to limit trades for the given run, ordered by candle_time ASC.
	ListTrades(ctx context.Context, runID int64, limit int) ([]domain.BacktestTrade, error)

	// CleanupOldRuns hard-deletes runs (and their trades via CASCADE) older than olderThan duration.
	CleanupOldRuns(ctx context.Context, olderThan time.Duration) error
}

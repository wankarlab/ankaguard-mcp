// Package repository defines the persistence interface for the optimizer subsystem.
package repository

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/optimization/domain"
)

// OptimizerRepository defines all database operations required by the optimizer.
type OptimizerRepository interface {
	// CreateRun inserts a new optimizer_runs row with status='pending' and returns its ID.
	CreateRun(ctx context.Context, run *domain.OptimizerRun) (id int64, err error)

	// MarkRunning updates run status to 'running' and records started_at.
	MarkRunning(ctx context.Context, id int64) error

	// MarkDone updates run status to 'done', records finished_at, best score, top presets.
	MarkDone(ctx context.Context, id int64, bestScore float64, topPresets []string) error

	// MarkFailed updates run status to 'failed' and records the error message.
	MarkFailed(ctx context.Context, id int64, errMsg string) error

	// GetRun returns the full run record for the given ID.
	GetRun(ctx context.Context, id int64) (*domain.OptimizerRun, error)

	// ListRuns returns runs optionally filtered by status and/or pair, newest first.
	// Pass empty strings to skip those filters.
	ListRuns(ctx context.Context, statusFilter, pairFilter string, limit int) ([]*domain.OptimizerRun, error)

	// InsertTrial persists one trial result linked to a run.
	InsertTrial(ctx context.Context, trial *domain.OptimizerTrial) error

	// ListTrials returns all trials for a run, ordered by iteration ASC.
	ListTrials(ctx context.Context, runID int64) ([]*domain.OptimizerTrial, error)

	// CancelRun transitions a pending/running run to 'cancelled'.
	CancelRun(ctx context.Context, id int64) error

	// CleanupOldRuns removes runs (and trials via CASCADE) older than the given duration.
	CleanupOldRuns(ctx context.Context, olderThan time.Duration) error
}

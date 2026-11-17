// Package postgres implements the OptimizerRepository interface backed by PostgreSQL via pgx/v5.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rantipay/trading-mcp/internal/optimization/domain"
	"github.com/rantipay/trading-mcp/internal/optimization/repository"
)

// repoImpl is the PostgreSQL-backed implementation of repository.OptimizerRepository.
type repoImpl struct {
	pool *pgxpool.Pool
}

// New returns a new postgres-backed OptimizerRepository.
func New(pool *pgxpool.Pool) repository.OptimizerRepository {
	return &repoImpl{pool: pool}
}

// CreateRun inserts a new optimizer_runs row and returns its ID.
func (r *repoImpl) CreateRun(ctx context.Context, run *domain.OptimizerRun) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO optimizer_runs (pair, objective, space_name, iterations, status, triggered_by)
		 VALUES ($1, $2, $3, $4, 'pending', $5) RETURNING id`,
		run.Pair, run.Objective, run.SpaceName, run.Iterations, run.TriggeredBy,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create optimizer run: %w", err)
	}
	return id, nil
}

// MarkRunning sets status='running' and records started_at.
func (r *repoImpl) MarkRunning(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE optimizer_runs SET status='running', started_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark optimizer run running %d: %w", id, err)
	}
	return nil
}

// MarkDone sets status='done', records finished_at, best_score, and top_presets.
func (r *repoImpl) MarkDone(ctx context.Context, id int64, bestScore float64, topPresets []string) error {
	presetsJSON, err := json.Marshal(topPresets)
	if err != nil {
		return fmt.Errorf("marshal top presets: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`UPDATE optimizer_runs
		 SET status='done', finished_at=NOW(), best_score=$2, top_presets=$3
		 WHERE id=$1`,
		id, bestScore, presetsJSON,
	)
	if err != nil {
		return fmt.Errorf("mark optimizer run done %d: %w", id, err)
	}
	return nil
}

// MarkFailed sets status='failed' and records the error message.
func (r *repoImpl) MarkFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE optimizer_runs SET status='failed', finished_at=NOW(), error=$2 WHERE id=$1`,
		id, errMsg,
	)
	if err != nil {
		return fmt.Errorf("mark optimizer run failed %d: %w", id, err)
	}
	return nil
}

// GetRun returns the full run record.
func (r *repoImpl) GetRun(ctx context.Context, id int64) (*domain.OptimizerRun, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, pair, objective, space_name, iterations, status,
		        started_at, finished_at, best_score, top_presets, triggered_by, error, created_at
		 FROM optimizer_runs WHERE id=$1`, id)
	run, err := scanRun(row)
	if err != nil {
		return nil, fmt.Errorf("get optimizer run %d: %w", id, err)
	}
	return run, nil
}

// ListRuns returns runs filtered by optional status and pair, newest first.
func (r *repoImpl) ListRuns(ctx context.Context, statusFilter, pairFilter string, limit int) ([]*domain.OptimizerRun, error) {
	query := `SELECT id, pair, objective, space_name, iterations, status,
	                 started_at, finished_at, best_score, top_presets, triggered_by, error, created_at
	          FROM optimizer_runs WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if statusFilter != "" {
		query += fmt.Sprintf(" AND status=$%d", argIdx)
		args = append(args, statusFilter)
		argIdx++
	}
	if pairFilter != "" {
		query += fmt.Sprintf(" AND pair=$%d", argIdx)
		args = append(args, pairFilter)
		argIdx++
	}
	query += " ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, limit)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list optimizer runs: %w", err)
	}
	defer rows.Close()

	var runs []*domain.OptimizerRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// InsertTrial persists a single trial result.
func (r *repoImpl) InsertTrial(ctx context.Context, trial *domain.OptimizerTrial) error {
	paramsJSON, err := json.Marshal(trial.Params)
	if err != nil {
		return fmt.Errorf("marshal trial params: %w", err)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO optimizer_trials (run_id, iteration, params, metrics, objective)
		 VALUES ($1, $2, $3, $4, $5)`,
		trial.RunID, trial.Iteration, paramsJSON, trial.Metrics, trial.Objective,
	)
	if err != nil {
		return fmt.Errorf("insert optimizer trial: %w", err)
	}
	return nil
}

// ListTrials returns all trials for a run ordered by iteration.
func (r *repoImpl) ListTrials(ctx context.Context, runID int64) ([]*domain.OptimizerTrial, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, run_id, iteration, params, metrics, objective, created_at
		 FROM optimizer_trials WHERE run_id=$1 ORDER BY iteration ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("list optimizer trials for run %d: %w", runID, err)
	}
	defer rows.Close()

	var trials []*domain.OptimizerTrial
	for rows.Next() {
		t := &domain.OptimizerTrial{}
		var paramsJSON, metricsJSON []byte
		if err := rows.Scan(&t.ID, &t.RunID, &t.Iteration, &paramsJSON, &metricsJSON, &t.Objective, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan trial row: %w", err)
		}
		if err := json.Unmarshal(paramsJSON, &t.Params); err != nil {
			return nil, fmt.Errorf("unmarshal trial params: %w", err)
		}
		t.Metrics = json.RawMessage(metricsJSON)
		trials = append(trials, t)
	}
	return trials, rows.Err()
}

// CancelRun transitions a pending/running run to 'cancelled'.
func (r *repoImpl) CancelRun(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE optimizer_runs SET status='cancelled', finished_at=NOW()
		 WHERE id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return fmt.Errorf("cancel optimizer run %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("run %d not found or already in terminal state", id)
	}
	return nil
}

// CleanupOldRuns removes runs (and their trials via CASCADE) older than olderThan.
func (r *repoImpl) CleanupOldRuns(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().UTC().Add(-olderThan)
	_, err := r.pool.Exec(ctx,
		`DELETE FROM optimizer_runs WHERE created_at < $1`, cutoff)
	if err != nil {
		return fmt.Errorf("cleanup optimizer runs: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanRun scans a single optimizer_runs row.
func scanRun(row rowScanner) (*domain.OptimizerRun, error) {
	run := &domain.OptimizerRun{}
	var presetsJSON []byte
	var errStr *string

	err := row.Scan(
		&run.ID, &run.Pair, &run.Objective, &run.SpaceName, &run.Iterations,
		&run.Status, &run.StartedAt, &run.FinishedAt, &run.BestScore,
		&presetsJSON, &run.TriggeredBy, &errStr, &run.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("run not found: %w", err)
		}
		return nil, fmt.Errorf("scan optimizer run: %w", err)
	}
	if errStr != nil {
		run.Error = *errStr
	}
	if len(presetsJSON) > 0 {
		_ = json.Unmarshal(presetsJSON, &run.TopPresets)
	}
	return run, nil
}

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ModePerformanceRow mirrors one row in the mode_performance table.
type ModePerformanceRow struct {
	ID           int64
	Pair         string
	Regime       string
	Mode         string
	Trades       int
	Wins         int
	NetPnL       float64
	ProfitFactor float64
	UpdatedAt    time.Time
}

// ModePerformanceRepository persists per-mode trading performance stats.
type ModePerformanceRepository struct {
	db *sql.DB
}

// NewModePerformanceRepository creates a new ModePerformanceRepository backed by db.
func NewModePerformanceRepository(db *sql.DB) *ModePerformanceRepository {
	return &ModePerformanceRepository{db: db}
}

// Upsert inserts or updates a single performance row for the "default" user,
// identified by (pair, regime, mode). Use UpsertForUser in multi-user contexts.
func (r *ModePerformanceRepository) Upsert(ctx context.Context, row ModePerformanceRow) error {
	return r.UpsertForUser(ctx, "default", row)
}

// UpsertForUser inserts or updates a performance row attributed to the given userID,
// identified by (user_id, pair, regime, mode).
func (r *ModePerformanceRepository) UpsertForUser(ctx context.Context, userID string, row ModePerformanceRow) error {
	if userID == "" {
		userID = "default"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO mode_performance (pair, regime, mode, trades, wins, net_pnl, profit_factor, updated_at, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pair, regime, mode) DO UPDATE SET
			trades        = excluded.trades,
			wins          = excluded.wins,
			net_pnl       = excluded.net_pnl,
			profit_factor = excluded.profit_factor,
			updated_at    = excluded.updated_at
	`, row.Pair, row.Regime, row.Mode, row.Trades, row.Wins, row.NetPnL, row.ProfitFactor, time.Now(), userID)
	if err != nil {
		return fmt.Errorf("mode_performance: failed to upsert (user=%q pair=%q regime=%q mode=%q): %w",
			userID, row.Pair, row.Regime, row.Mode, err)
	}
	return nil
}

// GetByPairAndRegime returns all mode performance rows for a given pair+regime combination
// for the "default" user. Returns an empty slice (no error) when no rows exist.
// Use GetByPairAndRegimeForUser in multi-user contexts.
func (r *ModePerformanceRepository) GetByPairAndRegime(ctx context.Context, pair, regime string) ([]*ModePerformanceRow, error) {
	return r.GetByPairAndRegimeForUser(ctx, "default", pair, regime)
}

// GetByPairAndRegimeForUser returns all mode performance rows for a given user+pair+regime.
// Returns an empty slice (no error) when no rows exist.
func (r *ModePerformanceRepository) GetByPairAndRegimeForUser(ctx context.Context, userID, pair, regime string) ([]*ModePerformanceRow, error) {
	if userID == "" {
		userID = "default"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, pair, regime, mode, trades, wins, net_pnl, profit_factor, updated_at
		FROM mode_performance
		WHERE pair = ? AND regime = ? AND user_id = ?
		ORDER BY profit_factor DESC
	`, pair, regime, userID)
	if err != nil {
		return nil, fmt.Errorf("mode_performance: failed to query user=%q pair=%q regime=%q: %w", userID, pair, regime, err)
	}
	defer rows.Close()
	return scanModePerformanceRows(rows)
}

// GetAllRegimes returns all mode performance rows for the "default" user (all pairs, all regimes).
// Useful for seeding in-memory caches on startup.
// Use GetAllRegimesForUser in multi-user contexts.
func (r *ModePerformanceRepository) GetAllRegimes(ctx context.Context) ([]*ModePerformanceRow, error) {
	return r.GetAllRegimesForUser(ctx, "default")
}

// GetAllRegimesForUser returns all mode performance rows for a specific user (all pairs, all regimes).
func (r *ModePerformanceRepository) GetAllRegimesForUser(ctx context.Context, userID string) ([]*ModePerformanceRow, error) {
	if userID == "" {
		userID = "default"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, pair, regime, mode, trades, wins, net_pnl, profit_factor, updated_at
		FROM mode_performance
		WHERE user_id = ?
		ORDER BY pair ASC, regime ASC, profit_factor DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("mode_performance: failed to query all rows for user=%q: %w", userID, err)
	}
	defer rows.Close()
	return scanModePerformanceRows(rows)
}

// scanModePerformanceRows scans all rows from a *sql.Rows into a slice of ModePerformanceRow.
func scanModePerformanceRows(rows *sql.Rows) ([]*ModePerformanceRow, error) {
	var result []*ModePerformanceRow
	for rows.Next() {
		r := &ModePerformanceRow{}
		if err := rows.Scan(&r.ID, &r.Pair, &r.Regime, &r.Mode,
			&r.Trades, &r.Wins, &r.NetPnL, &r.ProfitFactor, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("mode_performance: failed to scan row: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mode_performance: rows error: %w", err)
	}
	return result, nil
}

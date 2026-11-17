package launchapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

// ListStrategies returns every strategy with its versions (newest first).
func ListStrategies(ctx context.Context, db core.DB) ([]Strategy, error) {
	rows, err := db.Query(ctx, `SELECT id, name, status FROM launch.strategies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := []Strategy{}
	idx := map[int64]int{}
	for rows.Next() {
		var s Strategy
		if err := rows.Scan(&s.ID, &s.Name, &s.Status); err != nil {
			rows.Close()
			return nil, err
		}
		s.Versions = []StrategyVersion{}
		idx[s.ID] = len(out)
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	vrows, err := db.Query(ctx,
		`SELECT strategy_id, id, version, params, author, reason, created_at
		 FROM launch.strategy_versions ORDER BY strategy_id, version DESC`)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var sid int64
		var v StrategyVersion
		if err := vrows.Scan(&sid, &v.ID, &v.Version, &v.Params, &v.Author, &v.Reason, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.CreatedAt = v.CreatedAt.UTC()
		if i, ok := idx[sid]; ok {
			out[i].Versions = append(out[i].Versions, v)
		}
	}
	return out, vrows.Err()
}

// ErrInvalidParams wraps a StrategyParams validation failure.
type ErrInvalidParams struct{ Err error }

func (e ErrInvalidParams) Error() string { return e.Err.Error() }
func (e ErrInvalidParams) Unwrap() error { return e.Err }

// CreateStrategyVersion validates params and inserts version max+1 of the
// strategy in one transaction. Returns ErrNotFound if the strategy does not
// exist and ErrInvalidParams if validation fails.
func CreateStrategyVersion(ctx context.Context, pool *pgxpool.Pool, strategyID int64, params core.StrategyParams, author, reason string) (StrategyVersion, error) {
	if err := params.Validate(); err != nil {
		return StrategyVersion{}, ErrInvalidParams{Err: err}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return StrategyVersion{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return StrategyVersion{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var userID string
	if err := tx.QueryRow(ctx, `SELECT user_id FROM launch.strategies WHERE id = $1 FOR UPDATE`, strategyID).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StrategyVersion{}, ErrNotFound
		}
		return StrategyVersion{}, err
	}
	var next int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM launch.strategy_versions WHERE strategy_id = $1`, strategyID).Scan(&next); err != nil {
		return StrategyVersion{}, err
	}
	v := StrategyVersion{Version: next, Params: raw, Author: author, Reason: reason}
	if err := tx.QueryRow(ctx,
		`INSERT INTO launch.strategy_versions (strategy_id, version, params, author, reason, user_id)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		strategyID, next, raw, author, reason, userID).Scan(&v.ID, &v.CreatedAt); err != nil {
		return StrategyVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StrategyVersion{}, err
	}
	v.CreatedAt = v.CreatedAt.UTC()
	return v, nil
}

// Package postgres provides PostgreSQL utilities for the trading-mcp service,
// including Row-Level Security (RLS) helpers for multi-tenant isolation.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithUserContext sets the app.user_id session variable for RLS enforcement.
// Must be called within a transaction before any query that requires RLS.
// The setting is transaction-local (third argument to set_config is true).
func WithUserContext(ctx context.Context, tx pgx.Tx, userID string) error {
	if userID == "" {
		return fmt.Errorf("userID cannot be empty for RLS context")
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.user_id', $1, true)", userID)
	return err
}

// RunInUserContext executes fn within a transaction with the RLS user context set.
// The transaction is automatically rolled back on fn error or panic; committed on success.
func RunInUserContext(ctx context.Context, pool *pgxpool.Pool, userID string, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — rollback error is irrelevant after commit failure

	if err := WithUserContext(ctx, tx, userID); err != nil {
		return fmt.Errorf("set user context: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

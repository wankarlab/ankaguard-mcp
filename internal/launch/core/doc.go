// Package core holds the shared domain vocabulary of the AnkaLaunch phase-1
// pipeline (discovery → screening → decision → shadow simulation → gate).
// It has no dependencies on other launch packages so every stage can import it.
// Spec: openspec/changes/ankalaunch-phase1-shadow.
package core

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// NetworkSolana is the only network of phase 1.
const NetworkSolana = "solana"

// DB is the subset of *pgxpool.Pool (and pgx.Tx) used by launch repositories,
// so tests can run against a scratch database or a transaction.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

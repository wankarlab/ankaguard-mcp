// Package postgres provides PostgreSQL connection pool utilities for the
// trading-mcp service. It is intentionally thin: callers own lifecycle
// (pool.Close) and query logic.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// Config holds the parameters required to open a pgxpool connection pool.
type Config struct {
	Host              string
	Port              int
	Database          string
	User              string
	Password          string
	MaxConnections    int32
	MinConnections    int32
	MaxConnLifetime   time.Duration
	HealthCheckPeriod time.Duration
}

// NewConfigPool creates and validates a pgxpool.Pool using the supplied Config.
// It returns an error if the DSN is invalid or the initial ping fails.
func NewConfigPool(ctx context.Context, cfg Config, log *logrus.Logger) (*pgxpool.Pool, error) {
	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database,
	)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}

	if cfg.MaxConnections > 0 {
		poolCfg.MaxConns = cfg.MaxConnections
	}
	if cfg.MinConnections > 0 {
		poolCfg.MinConns = cfg.MinConnections
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	if err := HealthCheck(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: initial ping failed: %w", err)
	}

	log.WithFields(logrus.Fields{
		"host":     cfg.Host,
		"port":     cfg.Port,
		"database": cfg.Database,
		"max_conn": cfg.MaxConnections,
	}).Info("postgres: config DB pool connected")

	return pool, nil
}

// HealthCheck verifies the pool is reachable by executing a lightweight
// SELECT 1 query with a 2-second deadline.
func HealthCheck(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
		return fmt.Errorf("postgres: health check failed: %w", err)
	}
	return nil
}

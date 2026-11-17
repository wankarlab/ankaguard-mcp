-- Migration 006: Portfolio Snapshots
-- Created: 2026-05-10
-- Description: Stores hourly portfolio snapshots for real performance history tracking

CREATE TABLE IF NOT EXISTS portfolio_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    snapshot_time DATETIME NOT NULL,
    total_value_usdt REAL NOT NULL DEFAULT 0,
    available_balance REAL NOT NULL DEFAULT 0,
    unrealized_pnl REAL NOT NULL DEFAULT 0,
    positions_count INTEGER NOT NULL DEFAULT 0,
    assets_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_portfolio_snapshots_time ON portfolio_snapshots(snapshot_time);
CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_created ON portfolio_snapshots(created_at);

-- Migration 010: Per-mode trading performance stats
-- Created: 2026-05-24
-- Description: Persists per-mode (spot/futures/margin) performance statistics
-- broken down by pair and regime. Written by the feedback collector at the end
-- of each aggregation cycle so that mode preferences survive service restarts.

CREATE TABLE IF NOT EXISTS mode_performance (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    pair           TEXT    NOT NULL DEFAULT '',
    regime         TEXT    NOT NULL DEFAULT '',
    mode           TEXT    NOT NULL,
    trades         INTEGER NOT NULL DEFAULT 0,
    wins           INTEGER NOT NULL DEFAULT 0,
    net_pnl        REAL    NOT NULL DEFAULT 0,
    profit_factor  REAL    NOT NULL DEFAULT 0,
    updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(pair, regime, mode)
);

CREATE INDEX IF NOT EXISTS idx_mode_performance_pair_regime
    ON mode_performance (pair, regime);

CREATE INDEX IF NOT EXISTS idx_mode_performance_mode
    ON mode_performance (mode);

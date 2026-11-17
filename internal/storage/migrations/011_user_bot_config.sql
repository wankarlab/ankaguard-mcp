-- Migration 011: User trading bot configuration
-- Created: 2026-05-25
-- Description: Persists per-user trading bot configuration, including trade size,
-- leverage, market type, mode selection, risk limits, and full config JSON.
-- Enables Phase MU-1: per-user bot config restoration after restart.

CREATE TABLE IF NOT EXISTS user_bot_config (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id              TEXT    NOT NULL,
    exchange             TEXT    NOT NULL DEFAULT 'kucoin',
    enabled              INTEGER NOT NULL DEFAULT 0,
    trade_size_usd       REAL    NOT NULL DEFAULT 2.0,
    leverage             INTEGER NOT NULL DEFAULT 5,
    market_type          TEXT    NOT NULL DEFAULT 'futures',
    mode_selection       TEXT    NOT NULL DEFAULT 'auto',
    confidence_threshold REAL    NOT NULL DEFAULT 0.65,
    max_daily_loss_usd   REAL    NOT NULL DEFAULT 20.0,
    max_open_positions   INTEGER NOT NULL DEFAULT 1,
    dry_run              INTEGER NOT NULL DEFAULT 1,
    config_json          TEXT    NOT NULL DEFAULT '{}',
    updated_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, exchange)
);

CREATE INDEX IF NOT EXISTS idx_user_bot_config_user
    ON user_bot_config(user_id);

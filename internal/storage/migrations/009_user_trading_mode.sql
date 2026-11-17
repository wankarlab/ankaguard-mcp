-- Migration 009: User trading mode preferences
-- Created: 2026-05-24
-- Description: Stores per-user trading mode preferences at global or per-pair scope.
-- Enables the manual/hybrid control surface (Wave 2-A) for selectable market type
-- (spot/futures/margin), mode selection (auto/manual/hybrid), and leverage overrides.

CREATE TABLE IF NOT EXISTS user_trading_mode (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id        TEXT    NOT NULL DEFAULT 'default',
    scope          TEXT    NOT NULL DEFAULT 'global',
    pair           TEXT    NOT NULL DEFAULT '',
    market_type    TEXT    NOT NULL DEFAULT 'futures',
    mode_selection TEXT    NOT NULL DEFAULT 'auto',
    leverage       REAL    NOT NULL DEFAULT 0,
    updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, scope, pair)
);

CREATE INDEX IF NOT EXISTS idx_user_trading_mode_scope_pair
    ON user_trading_mode (scope, pair);

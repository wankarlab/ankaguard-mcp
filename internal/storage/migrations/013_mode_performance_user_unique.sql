-- Migration 013: Expand mode_performance UNIQUE constraint to include user_id (MU-8.6)
-- Created: 2026-05-25
-- Description: The original UNIQUE(pair, regime, mode) constraint added in migration 010
--              does not include user_id (added in 012), causing upserts from different
--              users to collide.  This migration recreates the table with the corrected
--              constraint UNIQUE(user_id, pair, regime, mode) while preserving all
--              existing columns and data.
--
-- SQLite does not support DROP CONSTRAINT, so we use the standard table-rename dance.
-- The migration runner wraps each file in a single transaction — no PRAGMA needed.

CREATE TABLE mode_performance_new (
    id             INTEGER   PRIMARY KEY AUTOINCREMENT,
    pair           TEXT      NOT NULL DEFAULT '',
    regime         TEXT      NOT NULL DEFAULT '',
    mode           TEXT      NOT NULL,
    trades         INTEGER   NOT NULL DEFAULT 0,
    wins           INTEGER   NOT NULL DEFAULT 0,
    net_pnl        REAL      NOT NULL DEFAULT 0,
    profit_factor  REAL      NOT NULL DEFAULT 0,
    updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user_id        TEXT      NOT NULL DEFAULT 'default',
    UNIQUE(user_id, pair, regime, mode)
);

INSERT INTO mode_performance_new (
    id, pair, regime, mode, trades, wins, net_pnl,
    profit_factor, updated_at, user_id
)
SELECT
    id, pair, regime, mode, trades, wins, net_pnl,
    profit_factor, updated_at, user_id
FROM mode_performance;

DROP TABLE mode_performance;

ALTER TABLE mode_performance_new RENAME TO mode_performance;

-- Recreate indexes from migration 010 (pair+regime composite, mode)
CREATE INDEX IF NOT EXISTS idx_mode_performance_pair_regime
    ON mode_performance (pair, regime);

CREATE INDEX IF NOT EXISTS idx_mode_performance_mode
    ON mode_performance (mode);

-- Recreate index from migration 012 (user_id)
CREATE INDEX IF NOT EXISTS idx_mode_performance_user
    ON mode_performance (user_id);

INSERT OR IGNORE INTO schema_migrations (version, name)
    VALUES (13, '013_mode_performance_user_unique');

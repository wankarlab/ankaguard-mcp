-- Migration 004: Master Agent persistence
-- Created: 2026-05-06
-- Description: Persists scoring weights, opportunity history, and master state snapshot
-- so the MasterAgent survives restarts and does not lose learned scoring adjustments.

-- ============================================================================
-- Table: master_scoring_weights
-- Purpose: Single-row table holding the latest tuned scanner weights.
-- ============================================================================
CREATE TABLE IF NOT EXISTS master_scoring_weights (
    id                       INTEGER PRIMARY KEY,  -- always 1, single row
    volatility               REAL NOT NULL,
    volume                   REAL NOT NULL,
    trend_strength           REAL NOT NULL,
    sentiment                REAL NOT NULL,
    regime_match             REAL NOT NULL,
    btc_correlation_penalty  REAL NOT NULL,
    recent_loss_penalty      REAL NOT NULL,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================================
-- Table: master_opportunity_history
-- Purpose: Historical record of opportunities produced by each scan cycle.
-- ============================================================================
CREATE TABLE IF NOT EXISTS master_opportunity_history (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    pair              TEXT NOT NULL,
    exchange          TEXT NOT NULL,
    opportunity_score REAL NOT NULL,
    volatility        REAL NOT NULL,
    volume_24h_usd    REAL NOT NULL,
    trend_strength    REAL NOT NULL,
    sentiment_score   REAL NOT NULL,
    btc_correlation   REAL NOT NULL,
    regime_match      REAL NOT NULL,
    reasoning         TEXT,
    detected_at       DATETIME NOT NULL,
    scan_id           TEXT NOT NULL  -- groups opportunities from the same scan
);

CREATE INDEX IF NOT EXISTS idx_master_opp_history_pair ON master_opportunity_history(pair);
CREATE INDEX IF NOT EXISTS idx_master_opp_history_detected ON master_opportunity_history(detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_master_opp_history_scan ON master_opportunity_history(scan_id);

-- ============================================================================
-- Table: master_state_snapshot
-- Purpose: Single-row snapshot of MasterAgent runtime state for warm restart.
-- ============================================================================
CREATE TABLE IF NOT EXISTS master_state_snapshot (
    id                    INTEGER PRIMARY KEY,  -- always 1, single row
    running               INTEGER NOT NULL DEFAULT 0,
    started_at            DATETIME,
    last_scan_at          DATETIME,
    last_scan_duration_ms INTEGER NOT NULL DEFAULT 0,
    scan_count            INTEGER NOT NULL DEFAULT 0,
    active_bots           INTEGER NOT NULL DEFAULT 0,
    total_capital_usd     REAL NOT NULL DEFAULT 0,
    allocated_usd         REAL NOT NULL DEFAULT 0,
    last_error            TEXT,
    updated_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Insert this migration
INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (4, '004_master_state');

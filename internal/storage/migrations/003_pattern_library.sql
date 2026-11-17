-- Migration 003: Pattern Library Persistence
-- Created: 2026-05-06
-- Description: Persists AI pattern library statistics so the in-memory cache can be seeded across restarts.

-- ============================================================================
-- Table: pattern_library
-- Purpose: Store learned pattern statistics by hash for cross-restart persistence
-- ============================================================================
CREATE TABLE IF NOT EXISTS pattern_library (
    hash             TEXT PRIMARY KEY,
    pair             TEXT NOT NULL,
    timeframe        TEXT NOT NULL DEFAULT '',
    sample_size      INTEGER NOT NULL DEFAULT 0,
    bullish_count    INTEGER NOT NULL DEFAULT 0,
    bearish_count    INTEGER NOT NULL DEFAULT 0,
    neutral_count    INTEGER NOT NULL DEFAULT 0,
    avg_price_change REAL NOT NULL DEFAULT 0,
    success_rate     REAL NOT NULL DEFAULT 0,
    reliability      TEXT NOT NULL DEFAULT 'INSUFFICIENT',
    last_seen        DATETIME NOT NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pattern_library_pair ON pattern_library(pair);
CREATE INDEX IF NOT EXISTS idx_pattern_library_pair_reliability ON pattern_library(pair, reliability);
CREATE INDEX IF NOT EXISTS idx_pattern_library_last_seen ON pattern_library(last_seen);
CREATE INDEX IF NOT EXISTS idx_pattern_library_success_rate ON pattern_library(success_rate DESC);

-- Insert this migration
INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (3, '003_pattern_library');

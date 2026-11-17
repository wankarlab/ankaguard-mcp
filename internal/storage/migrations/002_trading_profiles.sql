-- Migration 002: Trading Profiles for Dynamic Configuration
-- Created: 2025-12-06
-- Description: Creates table for storing trading configuration profiles with hot-reload support

-- ============================================================================
-- Table: trading_profiles
-- Purpose: Store dynamic trading configuration profiles
-- ============================================================================
CREATE TABLE IF NOT EXISTS trading_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    config TEXT NOT NULL, -- JSON serialized DynamicTradingConfig
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_default BOOLEAN DEFAULT 0,
    tags TEXT -- JSON array of tags
);

CREATE INDEX idx_profiles_name ON trading_profiles(name);
CREATE INDEX idx_profiles_is_default ON trading_profiles(is_default);
CREATE INDEX idx_profiles_created_at ON trading_profiles(created_at DESC);

-- Insert this migration
INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (2, '002_trading_profiles');

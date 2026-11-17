-- Migration 008: User pair selection tracking
-- Created: 2026-05-24
-- Description: Stores per-user, per-exchange pair selection preferences including
-- mode (auto/manual/hybrid), selected pairs list, and custom overrides for
-- dynamic pair universe management.

CREATE TABLE IF NOT EXISTS user_pair_selection (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL DEFAULT 'default',
    exchange TEXT NOT NULL DEFAULT 'kucoin',
    mode TEXT NOT NULL DEFAULT 'auto',
    pairs TEXT NOT NULL DEFAULT '[]',
    overrides TEXT NOT NULL DEFAULT '{}',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, exchange)
);

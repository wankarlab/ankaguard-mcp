-- Migration 005: Add created_by column to trading_profiles
-- Created: 2026-05-06
-- Description: Adds an authorship column so profiles produced by the master
-- agent can be distinguished from user-created ones. Idempotent guards rely
-- on the migration runner skipping already-applied versions.

ALTER TABLE trading_profiles ADD COLUMN created_by TEXT NOT NULL DEFAULT 'user';

CREATE INDEX IF NOT EXISTS idx_profiles_created_by ON trading_profiles(created_by);

-- Insert this migration
INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (5, '005_trading_profiles_created_by');

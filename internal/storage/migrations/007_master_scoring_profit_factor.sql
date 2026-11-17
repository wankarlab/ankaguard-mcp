-- Migration 007: Add profit_factor column to master_scoring_weights
-- Created: 2026-05-18
-- Description: Adds expectancy-per-trade weight so the master scorer can
-- favor pairs with positive realized PnL and penalize losing pairs harder
-- than the structural RecentLossPenalty.

ALTER TABLE master_scoring_weights ADD COLUMN profit_factor REAL NOT NULL DEFAULT 0.10;

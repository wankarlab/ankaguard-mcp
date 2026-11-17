-- Migration 014: Per-user uniqueness for portfolio_snapshots (MU-4 gap fix)
-- Created: 2026-05-25
-- Description: Migration 006 created a UNIQUE index on snapshot_time alone. After
--              migration 012 added user_id, two different users taking a snapshot
--              at the same instant would collide on that index — the second
--              INSERT OR IGNORE is silently dropped, losing the other user's data.
--              This migration replaces the single-column unique index with a
--              composite UNIQUE(user_id, snapshot_time), which matches what
--              PortfolioSnapshotRepository.SaveForUser already assumes ("ignoring
--              duplicates (same snapshot_time + user_id)").
--
-- Safety: existing rows all carry user_id='default' and the old index already
-- guaranteed snapshot_time was unique, so (user_id, snapshot_time) pairs are also
-- unique — the new index builds without collisions. DROP/CREATE INDEX run inside
-- the runner's per-file transaction.

DROP INDEX IF EXISTS idx_portfolio_snapshots_time;

CREATE UNIQUE INDEX IF NOT EXISTS idx_portfolio_snapshots_user_time
    ON portfolio_snapshots(user_id, snapshot_time);

INSERT OR IGNORE INTO schema_migrations (version, name)
    VALUES (14, '014_portfolio_snapshots_user_unique');

-- Migration 012: Per-user attribution for trade/PnL persistence (MU-4.1 + MU-4.2)
-- Created: 2026-05-25
-- Description: Adds user_id column to trade_history, performance_metrics,
--              portfolio_snapshots and mode_performance so that every write can be
--              attributed to a specific user/session.  Existing rows receive the
--              sentinel value 'default' (backwards-compatible).

ALTER TABLE trade_history       ADD COLUMN user_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE performance_metrics ADD COLUMN user_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE portfolio_snapshots ADD COLUMN user_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE mode_performance    ADD COLUMN user_id TEXT NOT NULL DEFAULT 'default';

CREATE INDEX IF NOT EXISTS idx_trade_history_user ON trade_history(user_id, entry_time);
CREATE INDEX IF NOT EXISTS idx_perf_metrics_user  ON performance_metrics(user_id);
CREATE INDEX IF NOT EXISTS idx_portfolio_snaps_user ON portfolio_snapshots(user_id);
CREATE INDEX IF NOT EXISTS idx_mode_performance_user ON mode_performance(user_id);

INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (12, '012_add_user_id_attribution');

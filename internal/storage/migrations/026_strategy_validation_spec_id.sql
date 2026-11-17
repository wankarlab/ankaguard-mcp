-- DM-3b: add spec_id to strategy_validation for FK linkage to strategy_specs.
-- SQLite ALTER TABLE ADD COLUMN is idempotent per session (migration table prevents re-runs).
ALTER TABLE strategy_validation ADD COLUMN spec_id TEXT;

CREATE INDEX IF NOT EXISTS idx_strategy_validation_spec_id
    ON strategy_validation (spec_id) WHERE spec_id IS NOT NULL;

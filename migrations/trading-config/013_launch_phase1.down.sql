-- Reverts 013_launch_phase1.up.sql. Destroys phase-1 gate history and decisions.
BEGIN;
DROP TABLE IF EXISTS launch.service_state;
DROP TABLE IF EXISTS launch.gate_evaluations;
DROP TABLE IF EXISTS launch.decisions;
DROP TABLE IF EXISTS launch.event_keys;
DROP INDEX IF EXISTS launch.idx_launch_positions_channel;
ALTER TABLE launch.positions
    DROP COLUMN IF EXISTS entry_channel, DROP COLUMN IF EXISTS invested_usd,
    DROP COLUMN IF EXISTS gross_pnl_usd, DROP COLUMN IF EXISTS net_pnl_usd,
    DROP COLUMN IF EXISTS costs, DROP COLUMN IF EXISTS exit_reason,
    DROP COLUMN IF EXISTS size_reason, DROP COLUMN IF EXISTS fraud_flag;
DROP INDEX IF EXISTS launch.idx_launch_opps_band;
ALTER TABLE launch.opportunities
    DROP COLUMN IF EXISTS source_kind, DROP COLUMN IF EXISTS graduated,
    DROP COLUMN IF EXISTS screen_result, DROP COLUMN IF EXISTS thesis,
    DROP COLUMN IF EXISTS market_snapshot, DROP COLUMN IF EXISTS decided_at;
DROP INDEX IF EXISTS launch.idx_launch_raw_events_subject;
ALTER TABLE launch.raw_events
    DROP COLUMN IF EXISTS source, DROP COLUMN IF EXISTS chain_ts,
    DROP COLUMN IF EXISTS subject, DROP COLUMN IF EXISTS duplicate;
COMMIT;

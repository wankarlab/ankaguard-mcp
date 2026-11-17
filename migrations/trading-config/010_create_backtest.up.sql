BEGIN;
CREATE TABLE IF NOT EXISTS backtest_runs (
    id              BIGSERIAL PRIMARY KEY,
    config_overrides JSONB NOT NULL,  -- los params cambiados respecto al baseline
    baseline        TEXT NOT NULL,    -- 'current' | 'preset:default' | 'preset:<name>'
    pair            TEXT NOT NULL,
    exchange        TEXT NOT NULL,
    horizon_from    TIMESTAMPTZ NOT NULL,
    horizon_to      TIMESTAMPTZ NOT NULL,
    candle_count    INT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending|running|done|failed
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    metrics         JSONB,  -- {win_rate, sharpe, max_dd, profit_factor, n_trades, ...}
    triggered_by    TEXT NOT NULL,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS backtest_trades (
    id              BIGSERIAL PRIMARY KEY,
    run_id          BIGINT NOT NULL REFERENCES backtest_runs(id) ON DELETE CASCADE,
    candle_time     TIMESTAMPTZ NOT NULL,
    side            TEXT NOT NULL,    -- buy|sell
    entry_price     NUMERIC(20,8) NOT NULL,
    exit_price      NUMERIC(20,8),
    exit_time       TIMESTAMPTZ,
    exit_reason     TEXT,             -- stop_loss|take_profit|timeout|opposite_signal
    pnl_pct         NUMERIC(10,4),
    confidence      NUMERIC(5,4),
    signal_factors  JSONB
);

CREATE INDEX IF NOT EXISTS idx_backtest_runs_status ON backtest_runs(status);
CREATE INDEX IF NOT EXISTS idx_backtest_trades_run ON backtest_trades(run_id);
COMMIT;

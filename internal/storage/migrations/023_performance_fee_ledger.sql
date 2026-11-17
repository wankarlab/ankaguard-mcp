-- SQLite mirror of migrations/operational/027_performance_fee_ledger.up.sql
-- No RLS; collection inert at creation.
CREATE TABLE IF NOT EXISTS performance_fee_ledger (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         TEXT    NOT NULL,
    tenant_id       TEXT,
    period_start    TIMESTAMP NOT NULL,
    period_end      TIMESTAMP NOT NULL,
    high_water_mark REAL    NOT NULL DEFAULT 0,
    period_net_pnl_sum REAL NOT NULL DEFAULT 0,
    accrued_fee_usd REAL    NOT NULL DEFAULT 0,
    rate            REAL    NOT NULL DEFAULT 0.20,
    status          TEXT    NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (user_id, period_start, period_end)
);

CREATE INDEX IF NOT EXISTS idx_perf_fee_ledger_user
    ON performance_fee_ledger (user_id, period_start DESC);

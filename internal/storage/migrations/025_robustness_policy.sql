-- SQLite mirror of migrations/operational/029_robustness_policy.up.sql
CREATE TABLE IF NOT EXISTS robustness_policy (
    version    INTEGER NOT NULL,
    factor_key TEXT    NOT NULL,
    weight     REAL    NOT NULL,
    threshold  REAL,
    min_val    REAL,
    max_val    REAL,
    created_by TEXT    NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    active     INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (version, factor_key)
);

INSERT OR IGNORE INTO robustness_policy (version, factor_key, weight, min_val, max_val, created_by, active)
VALUES
    (1, 'oos_profit_factor',    0.25, 1.0, 3.0,  'system_seed', 0),
    (1, 'consistency',          0.25, 0.0, 1.0,  'system_seed', 0),
    (1, 'n_trades',             0.15, 30,  500,  'system_seed', 0),
    (1, 'inverse_drawdown',     0.15, 0.0, 0.3,  'system_seed', 0),
    (1, 'sharpe',               0.10, 0.0, 3.0,  'system_seed', 0),
    (1, 'temporal_degradation', 0.10, 0.0, 0.5,  'system_seed', 0);

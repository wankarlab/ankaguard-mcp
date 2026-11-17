-- 020_signal_calibration: feedback loop tables (P3.2 / P3.5).
-- signal_calibration: prediction vs outcome per source and regime.
CREATE TABLE IF NOT EXISTS signal_calibration (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT    NOT NULL,
    regime      TEXT    NOT NULL,
    pair        TEXT    NOT NULL,
    predicted   TEXT    NOT NULL,
    actual      TEXT    NOT NULL,
    confidence  REAL    NOT NULL DEFAULT 0,
    pnl_pct     REAL    NOT NULL DEFAULT 0,
    ts          INTEGER NOT NULL DEFAULT (strftime('%s','now'))
);
CREATE INDEX IF NOT EXISTS idx_sigcal_source_regime ON signal_calibration (source, regime, ts);
CREATE INDEX IF NOT EXISTS idx_sigcal_pair_ts ON signal_calibration (pair, ts);

-- signal_source_weights: current Bayesian-calibrated weight per source/regime.
CREATE TABLE IF NOT EXISTS signal_source_weights (
    source      TEXT NOT NULL,
    regime      TEXT NOT NULL,
    weight      REAL NOT NULL DEFAULT 0.33,
    hit_rate    REAL NOT NULL DEFAULT 0.5,
    sample_n    INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    PRIMARY KEY (source, regime)
);
INSERT OR IGNORE INTO signal_source_weights (source, regime, weight, hit_rate, sample_n) VALUES
  ('technical', 'TRENDING_UP',   0.33, 0.50, 0),
  ('technical', 'TRENDING_DOWN', 0.33, 0.50, 0),
  ('technical', 'RANGING',       0.33, 0.50, 0),
  ('technical', 'BREAKOUT',      0.33, 0.50, 0),
  ('technical', 'CHAOS',         0.33, 0.50, 0),
  ('technical', 'UNKNOWN',       0.33, 0.50, 0),
  ('llm',       'TRENDING_UP',   0.33, 0.50, 0),
  ('llm',       'TRENDING_DOWN', 0.33, 0.50, 0),
  ('llm',       'RANGING',       0.33, 0.50, 0),
  ('llm',       'BREAKOUT',      0.33, 0.50, 0),
  ('llm',       'CHAOS',         0.33, 0.50, 0),
  ('llm',       'UNKNOWN',       0.33, 0.50, 0),
  ('ml',        'TRENDING_UP',   0.10, 0.50, 0),
  ('ml',        'TRENDING_DOWN', 0.10, 0.50, 0),
  ('ml',        'RANGING',       0.10, 0.50, 0),
  ('ml',        'BREAKOUT',      0.10, 0.50, 0),
  ('ml',        'CHAOS',         0.10, 0.50, 0),
  ('ml',        'UNKNOWN',       0.10, 0.50, 0);

-- model_registry: ML model version history with shadow-mode gating.
CREATE TABLE IF NOT EXISTS model_registry (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    model_path    TEXT    NOT NULL,
    model_version TEXT    NOT NULL UNIQUE,
    horizon_min   INTEGER NOT NULL,
    oos_pf        REAL    NOT NULL,
    oos_auc       REAL    NOT NULL,
    oos_brier     REAL    NOT NULL,
    oos_trades    INTEGER NOT NULL,
    train_rows    INTEGER NOT NULL,
    status        TEXT    NOT NULL DEFAULT 'shadow',
    shadow_until  INTEGER,
    created_at    INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    promoted_at   INTEGER,
    retired_at    INTEGER,
    notes         TEXT
);
CREATE INDEX IF NOT EXISTS idx_model_registry_status ON model_registry (status, created_at DESC);

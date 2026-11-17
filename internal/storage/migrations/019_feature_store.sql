-- 019_feature_store: ML training dataset (P1.1).
-- Persists deterministic features at time t plus forward-looking labels.
-- Anti-leakage is enforced by the dataset builder (cmd/build-dataset), not here.
CREATE TABLE IF NOT EXISTS feature_store (
    pair TEXT NOT NULL,
    timeframe TEXT NOT NULL,
    ts INTEGER NOT NULL,
    regime TEXT NOT NULL,
    volatility_tier TEXT NOT NULL,
    features_json TEXT NOT NULL,
    horizon_minutes INTEGER NOT NULL,
    label_fwd_return REAL,
    label_hit_tp INTEGER,
    label_hit_sl INTEGER,
    label_direction TEXT,
    created_at INTEGER DEFAULT (strftime('%s','now')),
    PRIMARY KEY (pair, timeframe, ts, horizon_minutes)
);
CREATE INDEX IF NOT EXISTS idx_feature_store_pair_ts ON feature_store (pair, timeframe, ts);

-- SQLite mirror of migrations/operational/026_cost_models.up.sql (GLOBAL no-RLS)
CREATE TABLE IF NOT EXISTS cost_models (
    cost_model_id   TEXT    NOT NULL PRIMARY KEY,
    exchange        TEXT    NOT NULL,
    tier            TEXT    NOT NULL DEFAULT 'default',
    taker_fee_pct   REAL    NOT NULL,
    maker_fee_pct   REAL    NOT NULL,
    funding_fee_pct REAL    NOT NULL DEFAULT 0,
    slippage_pct    REAL    NOT NULL DEFAULT 0,
    description     TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cost_models_exchange
    ON cost_models (exchange, tier);

INSERT OR IGNORE INTO cost_models (cost_model_id, exchange, tier, taker_fee_pct, maker_fee_pct, funding_fee_pct, slippage_pct, description)
VALUES ('kucoin_futures_default', 'kucoin', 'default', 0.06, 0.02, 0.06, 0.05,
        'KuCoin Futures VIP0 default');

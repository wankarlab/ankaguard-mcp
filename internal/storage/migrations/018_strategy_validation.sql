-- Strategy validation verdicts: the certification that gates live trading.
-- A (pair, strategy) may only trade live while it holds a non-expired PASS here,
-- produced by the walk-forward validator over the candle_archive. Verdicts expire
-- so a decaying edge is automatically de-certified until re-validated.
CREATE TABLE IF NOT EXISTS strategy_validation (
    pair          TEXT    NOT NULL,
    exchange      TEXT    NOT NULL DEFAULT 'kucoin',
    strategy_hash TEXT    NOT NULL DEFAULT 'live',  -- 'live' = current dynamic config
    pass          INTEGER NOT NULL,                  -- 1 = certified, 0 = rejected
    reason        TEXT,
    profit_factor REAL,
    consistency   REAL,
    n_trades      INTEGER,
    worst_drawdown REAL,
    validated_at  TIMESTAMP NOT NULL,
    expires_at    TIMESTAMP NOT NULL,
    PRIMARY KEY (pair, exchange, strategy_hash)
);

CREATE INDEX IF NOT EXISTS idx_strategy_validation_lookup
    ON strategy_validation (pair, exchange, strategy_hash, expires_at);

-- Durable historical candle archive for backtesting and strategy validation.
-- Unlike timeframe_data_cache (TTL-transient, for live analysis), this table is
-- append-only and never expires — it accumulates months of history so the
-- walk-forward validator has enough out-of-sample data to certify a real edge.
CREATE TABLE IF NOT EXISTS candle_archive (
    pair       TEXT    NOT NULL,
    timeframe  TEXT    NOT NULL,
    timestamp  INTEGER NOT NULL,  -- unix seconds (candle open time)
    open       REAL    NOT NULL,
    high       REAL    NOT NULL,
    low        REAL    NOT NULL,
    close      REAL    NOT NULL,
    volume     REAL    NOT NULL,
    PRIMARY KEY (pair, timeframe, timestamp)
);

CREATE INDEX IF NOT EXISTS idx_candle_archive_range
    ON candle_archive (pair, timeframe, timestamp);

-- Pair Volatility Classification
-- Auto-updated table that tracks each pair's current volatility tier.
-- The classifier goroutine refreshes this every 15 minutes based on live ATR/price.
-- Pairs migrate between tiers as their behavior changes — no hardcoded lists.

CREATE TABLE IF NOT EXISTS pair_volatility_classification (
    pair                TEXT PRIMARY KEY,
    tier                TEXT NOT NULL DEFAULT 'normal',  -- low | normal | volatile | extreme
    current_atr_pct     REAL NOT NULL DEFAULT 0,         -- latest ATR/price * 100
    avg_atr_pct_24h     REAL DEFAULT 0,                  -- rolling 24h average (smoothed)
    max_atr_pct_24h     REAL DEFAULT 0,                  -- worst spike in last 24h
    last_spike_atr_pct  REAL DEFAULT 0,                  -- value of most recent spike
    last_spike_at       TIMESTAMP,                       -- when last spike was recorded
    samples_count       INTEGER NOT NULL DEFAULT 0,      -- how many classifications recorded
    tier_changed_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    classified_by       TEXT NOT NULL DEFAULT 'auto'     -- 'auto' or 'manual_override'
);

CREATE INDEX IF NOT EXISTS idx_pair_vol_tier
    ON pair_volatility_classification(tier);

CREATE INDEX IF NOT EXISTS idx_pair_vol_updated
    ON pair_volatility_classification(updated_at DESC);

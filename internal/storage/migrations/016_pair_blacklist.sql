-- Persistent toxic-pair blacklist so auto-pause survives restarts.
-- Without this the in-memory blacklist was wiped on every restart, letting a
-- losing pair (e.g. NEAR-USDT) be re-traded immediately after a bounce.
CREATE TABLE IF NOT EXISTS pair_blacklist (
    user_id    TEXT      NOT NULL DEFAULT 'default',
    pair       TEXT      NOT NULL,
    reason     TEXT,
    until_ts   TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, pair)
);

CREATE INDEX IF NOT EXISTS idx_pair_blacklist_until ON pair_blacklist(until_ts);

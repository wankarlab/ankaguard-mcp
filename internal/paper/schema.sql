-- Paper trading broker (OpenSpec guarded-mcp-product, D3). Every table is keyed
-- by `holder` (the token subject) so two holders never see each other's state.
-- Idempotent: the same DDL is applied by internal/paper at startup.

CREATE TABLE IF NOT EXISTS paper_accounts (
    holder          TEXT    NOT NULL PRIMARY KEY,
    balance         REAL    NOT NULL,
    initial_balance REAL    NOT NULL,
    leverage_json   TEXT    NOT NULL DEFAULT '{}',
    reset_count     INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS paper_orders (
    id              TEXT    NOT NULL PRIMARY KEY,
    holder          TEXT    NOT NULL,
    exchange        TEXT    NOT NULL DEFAULT '',
    pair            TEXT    NOT NULL,
    symbol          TEXT    NOT NULL,
    market          TEXT    NOT NULL,
    side            TEXT    NOT NULL,
    type            TEXT    NOT NULL,
    amount          REAL    NOT NULL,
    qty             REAL    NOT NULL,
    price           REAL    NOT NULL DEFAULT 0,
    stop_price      REAL    NOT NULL DEFAULT 0,
    stop_dir        TEXT    NOT NULL DEFAULT '',
    leverage        REAL    NOT NULL DEFAULT 1,
    reduce_only     INTEGER NOT NULL DEFAULT 0,
    reserved        REAL    NOT NULL DEFAULT 0,
    status          TEXT    NOT NULL,
    fill_price      REAL    NOT NULL DEFAULT 0,
    fee             REAL    NOT NULL DEFAULT 0,
    reason          TEXT    NOT NULL DEFAULT '',
    closed_by_reset INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_paper_orders_holder_status ON paper_orders (holder, status);

CREATE TABLE IF NOT EXISTS paper_positions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    holder          TEXT    NOT NULL,
    exchange        TEXT    NOT NULL DEFAULT '',
    pair            TEXT    NOT NULL,
    symbol          TEXT    NOT NULL,
    market          TEXT    NOT NULL,
    side            TEXT    NOT NULL,
    size            REAL    NOT NULL,
    entry_price     REAL    NOT NULL,
    leverage        REAL    NOT NULL DEFAULT 1,
    margin          REAL    NOT NULL DEFAULT 0,
    realized_pnl    REAL    NOT NULL DEFAULT 0,
    status          TEXT    NOT NULL,
    close_reason    TEXT    NOT NULL DEFAULT '',
    close_price     REAL    NOT NULL DEFAULT 0,
    closed_by_reset INTEGER NOT NULL DEFAULT 0,
    opened_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at       TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_paper_positions_open
    ON paper_positions (holder, symbol, market) WHERE status = 'open';

CREATE TABLE IF NOT EXISTS paper_fills (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    holder          TEXT    NOT NULL,
    order_id        TEXT    NOT NULL,
    position_id     INTEGER NOT NULL DEFAULT 0,
    exchange        TEXT    NOT NULL DEFAULT '',
    pair            TEXT    NOT NULL,
    market          TEXT    NOT NULL,
    side            TEXT    NOT NULL,
    qty             REAL    NOT NULL,
    price           REAL    NOT NULL,
    fee             REAL    NOT NULL,
    realized_pnl    REAL    NOT NULL DEFAULT 0,
    liquidation     INTEGER NOT NULL DEFAULT 0,
    closed_by_reset INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_paper_fills_holder ON paper_fills (holder, created_at);

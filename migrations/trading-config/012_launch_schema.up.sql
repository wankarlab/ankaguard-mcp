-- AnkaLaunch phase 0: transactional schema for the on-chain launch bot.
-- Data model: docs/bot/frd.md section 6. Every table carries user_id so the
-- schema is multi-tenant ready; RLS policies live in
-- migrations/operational/051_launch_rls.up.sql (enabled with
-- TRADING_OPERATIONAL_MIGRATIONS). Amounts in native units are BIGINT
-- (lamports/wei); prices and USD values are NUMERIC to avoid float drift.
BEGIN;

CREATE SCHEMA IF NOT EXISTS launch;

CREATE TABLE IF NOT EXISTS launch.tokens (
    id           BIGSERIAL PRIMARY KEY,
    network      TEXT NOT NULL,
    address      TEXT NOT NULL,
    symbol       TEXT,
    decimals     SMALLINT,
    program      TEXT NOT NULL CHECK (program IN ('spl', 'token2022', 'erc20')),
    deployer     TEXT,
    deployed_at  TIMESTAMPTZ,
    authorities  JSONB NOT NULL DEFAULT '{}',
    extensions   JSONB NOT NULL DEFAULT '[]',
    tags         TEXT[] NOT NULL DEFAULT '{}',
    user_id      TEXT NOT NULL DEFAULT 'default',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (network, address, user_id)
);

CREATE TABLE IF NOT EXISTS launch.pools (
    id              BIGSERIAL PRIMARY KEY,
    token_id        BIGINT NOT NULL REFERENCES launch.tokens(id),
    dex             TEXT NOT NULL,
    pool_address    TEXT NOT NULL,
    source_kind     TEXT NOT NULL,
    pool_created_at TIMESTAMPTZ,
    first_swap_at   TIMESTAMPTZ,
    lp_locked_pct   NUMERIC(7,4),
    graduated_from  TEXT,
    user_id         TEXT NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (dex, pool_address, user_id)
);

-- Raw chain events, partitioned by day (retention: launch.events.retention_days).
-- Daily partitions are created by launch-bot; the default partition catches the rest.
CREATE TABLE IF NOT EXISTS launch.raw_events (
    id            BIGSERIAL,
    network       TEXT NOT NULL,
    kind          TEXT NOT NULL,
    slot          BIGINT,
    tx_signature  TEXT,
    payload       JSONB NOT NULL,
    ts            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id       TEXT NOT NULL DEFAULT 'default',
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);
CREATE TABLE IF NOT EXISTS launch.raw_events_default PARTITION OF launch.raw_events DEFAULT;
CREATE INDEX IF NOT EXISTS idx_launch_raw_events_sig ON launch.raw_events (tx_signature);

CREATE TABLE IF NOT EXISTS launch.data_quality_records (
    id           BIGSERIAL PRIMARY KEY,
    entity_type  TEXT NOT NULL,
    entity_id    BIGINT NOT NULL,
    field        TEXT NOT NULL,
    value        TEXT,
    source       TEXT NOT NULL,
    ts           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ,
    confidence   NUMERIC(5,4) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    method       TEXT,
    user_id      TEXT NOT NULL DEFAULT 'default'
);
CREATE INDEX IF NOT EXISTS idx_launch_dq_entity ON launch.data_quality_records (entity_type, entity_id, field);

CREATE TABLE IF NOT EXISTS launch.strategies (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft',
    user_id     TEXT NOT NULL DEFAULT 'default',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, user_id)
);

CREATE TABLE IF NOT EXISTS launch.strategy_versions (
    id           BIGSERIAL PRIMARY KEY,
    strategy_id  BIGINT NOT NULL REFERENCES launch.strategies(id),
    version      INT NOT NULL,
    params       JSONB NOT NULL,
    author       TEXT NOT NULL,
    reason       TEXT NOT NULL,
    user_id      TEXT NOT NULL DEFAULT 'default',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (strategy_id, version)
);

CREATE TABLE IF NOT EXISTS launch.opportunities (
    id                   BIGSERIAL PRIMARY KEY,
    token_id             BIGINT NOT NULL REFERENCES launch.tokens(id),
    pool_id              BIGINT REFERENCES launch.pools(id),
    strategy_version_id  BIGINT REFERENCES launch.strategy_versions(id),
    channel              TEXT NOT NULL,
    score                NUMERIC(7,4),
    band                 TEXT,
    status               TEXT NOT NULL,
    reject_reason        TEXT,
    expires_at           TIMESTAMPTZ,
    user_id              TEXT NOT NULL DEFAULT 'default',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_launch_opps_status ON launch.opportunities (status, created_at DESC);

CREATE TABLE IF NOT EXISTS launch.score_breakdowns (
    id              BIGSERIAL PRIMARY KEY,
    opportunity_id  BIGINT NOT NULL REFERENCES launch.opportunities(id) ON DELETE CASCADE,
    factor          TEXT NOT NULL,
    weight          NUMERIC(7,4) NOT NULL,
    value           TEXT,
    result          NUMERIC(7,4),
    confidence      NUMERIC(5,4),
    ts              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id         TEXT NOT NULL DEFAULT 'default'
);

CREATE TABLE IF NOT EXISTS launch.wallets (
    id                     BIGSERIAL PRIMARY KEY,
    network                TEXT NOT NULL,
    address                TEXT NOT NULL,
    role                   TEXT NOT NULL CHECK (role IN ('hot', 'treasury')),
    cap_usd                NUMERIC(20,2),
    gas_reserve_lamports   BIGINT NOT NULL DEFAULT 0,
    signer_ref             TEXT NOT NULL,
    user_id                TEXT NOT NULL DEFAULT 'default',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (network, address)
);

CREATE TABLE IF NOT EXISTS launch.positions (
    id                   BIGSERIAL PRIMARY KEY,
    opportunity_id       BIGINT REFERENCES launch.opportunities(id),
    strategy_version_id  BIGINT REFERENCES launch.strategy_versions(id),
    mode                 TEXT NOT NULL CHECK (mode IN ('simulated', 'shadow', 'real')),
    state                TEXT NOT NULL,
    wallet_id            BIGINT REFERENCES launch.wallets(id),
    size                 NUMERIC(38,0) NOT NULL,
    entry_price          NUMERIC(38,18) CHECK (entry_price > 0),
    cost_basis           NUMERIC(38,18),
    exit_plan            JSONB NOT NULL,
    partials_done        JSONB NOT NULL DEFAULT '[]',
    peak_price           NUMERIC(38,18),
    opened_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at            TIMESTAMPTZ,
    user_id              TEXT NOT NULL DEFAULT 'default'
);
CREATE INDEX IF NOT EXISTS idx_launch_positions_open ON launch.positions (state) WHERE closed_at IS NULL;

CREATE TABLE IF NOT EXISTS launch.orders (
    id                 BIGSERIAL PRIMARY KEY,
    position_id        BIGINT NOT NULL REFERENCES launch.positions(id),
    idempotency_key    TEXT NOT NULL UNIQUE,
    attempt_seq        INT NOT NULL DEFAULT 1,
    side               TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    status             TEXT NOT NULL,
    tx_signature       TEXT,
    commitment         TEXT,
    quote_price        NUMERIC(38,18),
    fill_price         NUMERIC(38,18),
    expected_slippage  NUMERIC(9,6),
    actual_slippage    NUMERIC(9,6),
    fee                BIGINT,
    gas                BIGINT,
    priority_fee       BIGINT,
    rent               BIGINT,
    token_tax          NUMERIC(9,6),
    user_id            TEXT NOT NULL DEFAULT 'default',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS launch.circuit_breaker_events (
    id          BIGSERIAL PRIMARY KEY,
    scope       TEXT NOT NULL,
    from_state  TEXT NOT NULL,
    to_state    TEXT NOT NULL,
    trigger     TEXT NOT NULL,
    condition   JSONB NOT NULL DEFAULT '{}',
    actor       TEXT NOT NULL,
    reason      TEXT NOT NULL,
    user_id     TEXT NOT NULL DEFAULT 'default',
    ts          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS launch.reconciliation_reports (
    id           BIGSERIAL PRIMARY KEY,
    position_id  BIGINT REFERENCES launch.positions(id),
    wallet_id    BIGINT REFERENCES launch.wallets(id),
    asset        TEXT NOT NULL,
    expected     NUMERIC(38,0) NOT NULL,
    actual       NUMERIC(38,0) NOT NULL,
    diff         NUMERIC(38,0) NOT NULL,
    class        TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'open',
    resolved_by  TEXT,
    reason       TEXT,
    user_id      TEXT NOT NULL DEFAULT 'default',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS launch.alerts (
    id              BIGSERIAL PRIMARY KEY,
    level           TEXT NOT NULL,
    event           TEXT NOT NULL,
    entity_type     TEXT,
    entity_id       BIGINT,
    cause           TEXT NOT NULL,
    action_taken    TEXT,
    recommendation  TEXT,
    status          TEXT NOT NULL DEFAULT 'open',
    dedup_key       TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT 'default',
    ts              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS launch.alert_state (
    dedup_key     TEXT NOT NULL,
    user_id       TEXT NOT NULL DEFAULT 'default',
    last_sent_at  TIMESTAMPTZ NOT NULL,
    count         INT NOT NULL DEFAULT 1,
    PRIMARY KEY (dedup_key, user_id)
);

CREATE TABLE IF NOT EXISTS launch.feedback (
    id           BIGSERIAL PRIMARY KEY,
    position_id  BIGINT NOT NULL REFERENCES launch.positions(id),
    tags         TEXT[] NOT NULL DEFAULT '{}',
    comment      TEXT,
    user_id      TEXT NOT NULL DEFAULT 'default',
    ts           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS launch.watch_lists (
    id           BIGSERIAL PRIMARY KEY,
    list_type    TEXT NOT NULL CHECK (list_type IN ('allow', 'block', 'watch')),
    entity_type  TEXT NOT NULL,
    value        TEXT NOT NULL,
    reason       TEXT NOT NULL,
    author       TEXT NOT NULL,
    user_id      TEXT NOT NULL DEFAULT 'default',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (list_type, entity_type, value, user_id)
);

-- Append-only audit trail: UPDATE and DELETE are rejected by trigger.
CREATE TABLE IF NOT EXISTS launch.audit_log (
    id       BIGSERIAL PRIMARY KEY,
    actor    TEXT NOT NULL,
    action   TEXT NOT NULL,
    entity   TEXT NOT NULL,
    before   JSONB,
    after    JSONB,
    reason   TEXT NOT NULL,
    ip       TEXT,
    user_id  TEXT NOT NULL DEFAULT 'default',
    ts       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION launch.reject_audit_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'launch.audit_log is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_log_append_only ON launch.audit_log;
CREATE TRIGGER audit_log_append_only
    BEFORE UPDATE OR DELETE ON launch.audit_log
    FOR EACH ROW EXECUTE FUNCTION launch.reject_audit_mutation();

-- Operator commands (kill switch, approve, close) for the Postgres CommandBus.
-- trading-mcp-server inserts a row and NOTIFYs launch_commands with its id;
-- launch-bot claims it. The row is the audit record of the command.
CREATE TABLE IF NOT EXISTS launch.commands (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}',
    actor        TEXT NOT NULL,
    reason       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed', 'done', 'failed')),
    result       TEXT,
    user_id      TEXT NOT NULL DEFAULT 'default',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_launch_commands_pending ON launch.commands (id) WHERE status = 'pending';

COMMIT;

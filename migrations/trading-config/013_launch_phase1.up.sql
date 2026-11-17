-- AnkaLaunch phase 1 (SHADOW): edge-gate verdicts, human decisions on the
-- manual-approval queue, launch-bot heartbeat and the entry channel of each
-- position. Spec: openspec/changes/ankalaunch-phase1-shadow.
BEGIN;

-- Dedup key of the discovery pipeline: one row per (network, pool_or_mint,
-- tx_signature, kind) across all sources.
CREATE TABLE IF NOT EXISTS launch.event_keys (
    network       TEXT NOT NULL,
    subject       TEXT NOT NULL,
    tx_signature  TEXT NOT NULL,
    kind          TEXT NOT NULL,
    first_source  TEXT NOT NULL,
    seen_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (network, subject, tx_signature, kind)
);

-- raw_events gains the source and the on-chain timestamp (event age is computed
-- from chain time, never from the time it was received).
ALTER TABLE launch.raw_events ADD COLUMN IF NOT EXISTS source TEXT;
ALTER TABLE launch.raw_events ADD COLUMN IF NOT EXISTS chain_ts TIMESTAMPTZ;
ALTER TABLE launch.raw_events ADD COLUMN IF NOT EXISTS subject TEXT;
ALTER TABLE launch.raw_events ADD COLUMN IF NOT EXISTS duplicate BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_launch_raw_events_subject ON launch.raw_events (subject, ts);

-- Opportunities: graduated flag, filter outcome and the data snapshot used to
-- score them (needed for replay and for the human-decision snapshot).
ALTER TABLE launch.opportunities ADD COLUMN IF NOT EXISTS source_kind TEXT;
ALTER TABLE launch.opportunities ADD COLUMN IF NOT EXISTS graduated BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE launch.opportunities ADD COLUMN IF NOT EXISTS screen_result TEXT;
ALTER TABLE launch.opportunities ADD COLUMN IF NOT EXISTS thesis JSONB NOT NULL DEFAULT '{}';
ALTER TABLE launch.opportunities ADD COLUMN IF NOT EXISTS market_snapshot JSONB NOT NULL DEFAULT '{}';
ALTER TABLE launch.opportunities ADD COLUMN IF NOT EXISTS decided_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_launch_opps_band ON launch.opportunities (band, status, expires_at);

-- Positions: how the entry was decided, PnL breakdown and exit cause.
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS entry_channel TEXT NOT NULL DEFAULT 'auto'
    CHECK (entry_channel IN ('auto', 'manual', 'counterfactual'));
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS invested_usd NUMERIC(20,8);
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS gross_pnl_usd NUMERIC(20,8);
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS net_pnl_usd NUMERIC(20,8);
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS costs JSONB NOT NULL DEFAULT '{}';
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS exit_reason TEXT;
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS size_reason TEXT;
ALTER TABLE launch.positions ADD COLUMN IF NOT EXISTS fraud_flag BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_launch_positions_channel ON launch.positions (entry_channel, closed_at);

-- Human decisions on MANUAL_APPROVAL opportunities. launch-bot consumes them
-- (NOTIFY launch_decisions) and opens a manual or counterfactual position.
CREATE TABLE IF NOT EXISTS launch.decisions (
    id              BIGSERIAL PRIMARY KEY,
    opportunity_id  BIGINT NOT NULL REFERENCES launch.opportunities(id),
    action          TEXT NOT NULL CHECK (action IN ('approve', 'reject', 'expire')),
    category        TEXT CHECK (category IN ('narrative', 'team', 'community', 'chart', 'other')),
    comment         TEXT,
    actor           TEXT NOT NULL,
    snapshot        JSONB NOT NULL DEFAULT '{}',
    processed_at    TIMESTAMPTZ,
    position_id     BIGINT REFERENCES launch.positions(id),
    user_id         TEXT NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (opportunity_id)
);
CREATE INDEX IF NOT EXISTS idx_launch_decisions_pending ON launch.decisions (id) WHERE processed_at IS NULL;

-- Edge-gate evaluations (append-only history).
CREATE TABLE IF NOT EXISTS launch.gate_evaluations (
    id                   BIGSERIAL PRIMARY KEY,
    strategy_version_id  BIGINT REFERENCES launch.strategy_versions(id),
    mode                 TEXT NOT NULL,
    verdict              TEXT NOT NULL CHECK (verdict IN ('INSUFFICIENT_DATA', 'NO_EDGE', 'EDGE')),
    trades               INT NOT NULL,
    shadow_weeks         NUMERIC(9,4) NOT NULL,
    net_pnl_usd          NUMERIC(20,8) NOT NULL,
    mean_pnl_usd         NUMERIC(20,8),
    ci_low               NUMERIC(20,8),
    ci_high              NUMERIC(20,8),
    ci_level             NUMERIC(5,4) NOT NULL,
    profit_factor        NUMERIC(20,8),
    max_drawdown_usd     NUMERIC(20,8),
    fraud_buys           INT NOT NULL,
    inputs               JSONB NOT NULL DEFAULT '{}',
    detail               TEXT,
    user_id              TEXT NOT NULL DEFAULT 'default',
    evaluated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_launch_gate_eval_ts ON launch.gate_evaluations (evaluated_at DESC);

-- launch-bot heartbeat and per-source health (one row per instance).
CREATE TABLE IF NOT EXISTS launch.service_state (
    instance      TEXT PRIMARY KEY,
    heartbeat_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    network_state JSONB NOT NULL DEFAULT '{}',
    sources       JSONB NOT NULL DEFAULT '{}',
    entries_paused BOOLEAN NOT NULL DEFAULT FALSE,
    pause_reason  TEXT,
    version       TEXT
);

COMMIT;

-- C1.3 / C3.2 / C3.3 — net-new governance domains for Anka Arena.
-- All tables are idempotent (CREATE TABLE IF NOT EXISTS) so the embedded
-- migration runner can apply them on every deploy without error. SQLite is the
-- service-local DB; tenant_id columns are kept for multi-tenant parity (filtered
-- at the repository layer) — RLS only applies on Postgres deployments.

-- ── C1.3: informed-consent gate (pre-copy) ─────────────────────────────────
-- consent_template — versioned legal text keyed by kind. The body lives in the
-- DB so legal copy is auditable/updatable without a redeploy. Seeded in Go.
CREATE TABLE IF NOT EXISTS consent_template (
    kind        TEXT      NOT NULL,
    version     INTEGER   NOT NULL,
    body_md     TEXT      NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (kind, version)
);

CREATE INDEX IF NOT EXISTS idx_consent_template_kind
    ON consent_template (kind, version);

-- consent_record — one acceptance per (user, kind, template_version). Append-only
-- legal audit trail; never modified after insert. UNIQUE enforces idempotency.
CREATE TABLE IF NOT EXISTS consent_record (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id           TEXT      NOT NULL,
    tenant_id         TEXT,
    kind              TEXT      NOT NULL,
    template_version  INTEGER   NOT NULL,
    accepted_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ip                TEXT,
    UNIQUE (user_id, kind, template_version)
);

CREATE INDEX IF NOT EXISTS idx_consent_record_user
    ON consent_record (user_id, kind);

-- ── C3.2: adversarial review queue ─────────────────────────────────────────
-- adversarial_item — one row per spec queued for adversarial review.
CREATE TABLE IF NOT EXISTS adversarial_item (
    id          TEXT      NOT NULL PRIMARY KEY,
    spec_id     TEXT      NOT NULL,
    tenant_id   TEXT,
    status      TEXT      NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at  TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_adversarial_item_status
    ON adversarial_item (status, created_at);

-- adversarial_flag — a reviewer flag against a queued item.
CREATE TABLE IF NOT EXISTS adversarial_flag (
    id           TEXT      NOT NULL PRIMARY KEY,
    item_id      TEXT      NOT NULL,
    reviewer_id  TEXT      NOT NULL,
    tenant_id    TEXT,
    reason       TEXT      NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_adversarial_flag_item
    ON adversarial_flag (item_id, created_at);

-- ── C3.3: appeals (tribunal) ───────────────────────────────────────────────
-- appeal — an appellant's request to review a spec decision.
CREATE TABLE IF NOT EXISTS appeal (
    id            TEXT      NOT NULL PRIMARY KEY,
    spec_id       TEXT      NOT NULL,
    appellant_id  TEXT      NOT NULL,
    tenant_id     TEXT,
    status        TEXT      NOT NULL DEFAULT 'open',
    reason        TEXT      NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at    TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_appeal_status
    ON appeal (status, created_at);

CREATE INDEX IF NOT EXISTS idx_appeal_appellant
    ON appeal (appellant_id, created_at);

-- RBAC-7: consent_records — append-only informed risk-consent audit trail.
-- Stores one record per (user_id, consent_version) pair.
-- This table is NEVER modified after insert — it is a legal audit trail.
CREATE TABLE IF NOT EXISTS consent_records (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          TEXT      NOT NULL,
    tenant_id        TEXT,
    consent_version  TEXT      NOT NULL DEFAULT 'v1',
    consented_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ip_address       TEXT,
    attestations_json TEXT,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (user_id, consent_version)
);

CREATE INDEX IF NOT EXISTS idx_consent_user
    ON consent_records (user_id, consent_version);

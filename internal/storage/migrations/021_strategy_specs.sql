-- SQLite mirror of migrations/operational/025_strategy_specs.up.sql
-- No RLS (SQLite uses physical file isolation per user).
-- TEXT JSON replaces JSONB; TIMESTAMP replaces TIMESTAMPTZ.
CREATE TABLE IF NOT EXISTS strategy_specs (
    spec_id            TEXT    NOT NULL,
    user_id            TEXT    NOT NULL,
    tenant_id          TEXT,
    name               TEXT    NOT NULL,
    author             TEXT    NOT NULL,
    revision           INTEGER NOT NULL DEFAULT 1,
    parent_revision    TEXT,
    status             TEXT    NOT NULL DEFAULT 'draft',
    description        TEXT,
    scope_json         TEXT    NOT NULL DEFAULT '{}',
    config_override    TEXT    NOT NULL DEFAULT '{}',
    validation_verdict TEXT    NOT NULL DEFAULT '{}',
    created_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (spec_id, user_id),
    UNIQUE (user_id, name, revision)
);

CREATE INDEX IF NOT EXISTS idx_strategy_specs_user_status
    ON strategy_specs (user_id, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_strategy_specs_spec_id
    ON strategy_specs (spec_id);

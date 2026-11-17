BEGIN;
CREATE TABLE IF NOT EXISTS parameter_presets (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    scope_type      TEXT NOT NULL DEFAULT 'global',
    scope_id        TEXT NOT NULL DEFAULT '*',
    params_json     JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      TEXT NOT NULL DEFAULT 'system',
    is_active       BOOLEAN NOT NULL DEFAULT FALSE,
    last_applied_at TIMESTAMPTZ,
    performance_score JSONB
);
CREATE INDEX idx_param_presets_active ON parameter_presets(is_active);
CREATE INDEX idx_param_presets_scope ON parameter_presets(scope_type, scope_id);
COMMIT;

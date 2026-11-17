BEGIN;
CREATE TABLE IF NOT EXISTS parameter_history (
    id              BIGSERIAL PRIMARY KEY,
    key             TEXT NOT NULL,
    scope_type      TEXT NOT NULL,
    scope_id        TEXT NOT NULL,
    old_value       TEXT,
    new_value       TEXT NOT NULL,
    changed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    changed_by      TEXT NOT NULL,
    change_reason   TEXT DEFAULT '',
    preset_name     TEXT,
    rollback_of     BIGINT REFERENCES parameter_history(id)
);
CREATE INDEX idx_param_history_key ON parameter_history(key, changed_at DESC);
CREATE INDEX idx_param_history_by ON parameter_history(changed_by, changed_at DESC);
CREATE INDEX idx_param_history_preset ON parameter_history(preset_name) WHERE preset_name IS NOT NULL;
COMMIT;

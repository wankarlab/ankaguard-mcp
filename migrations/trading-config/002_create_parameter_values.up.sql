BEGIN;
CREATE TABLE IF NOT EXISTS parameter_values (
    id              BIGSERIAL PRIMARY KEY,
    key             TEXT NOT NULL,
    scope_type      TEXT NOT NULL CHECK (scope_type IN ('global','exchange','pair','bot_instance')),
    scope_id        TEXT NOT NULL,
    value           TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by      TEXT NOT NULL DEFAULT 'system',
    source          TEXT NOT NULL DEFAULT 'manual'
                    CHECK (source IN ('manual','preset','optimizer','feedback_loop','migration')),
    UNIQUE (key, scope_type, scope_id),
    FOREIGN KEY (key) REFERENCES parameter_registry(key) ON DELETE CASCADE
);
CREATE INDEX idx_param_values_key ON parameter_values(key);
CREATE INDEX idx_param_values_scope ON parameter_values(scope_type, scope_id);
CREATE INDEX idx_param_values_source ON parameter_values(source);
COMMIT;

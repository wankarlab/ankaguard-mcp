BEGIN;
CREATE TABLE IF NOT EXISTS parameter_registry (
    key                TEXT PRIMARY KEY,
    type               TEXT NOT NULL CHECK (type IN ('int','float','bool','string','duration','json')),
    group_name         TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    default_value      TEXT NOT NULL,
    min_value          TEXT,
    max_value          TEXT,
    enum_values        JSONB,
    requires_restart   BOOLEAN NOT NULL DEFAULT FALSE,
    scope_levels       TEXT NOT NULL DEFAULT 'global',
    deprecated         BOOLEAN NOT NULL DEFAULT FALSE,
    deprecation_note   TEXT DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_param_registry_group ON parameter_registry(group_name);
CREATE INDEX idx_param_registry_active ON parameter_registry(deprecated) WHERE deprecated = FALSE;
COMMIT;

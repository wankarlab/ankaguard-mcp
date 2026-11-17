-- C0.5: seeded StrategySpec templates served by GET /api/v1/specs/templates.
-- The Spec Studio editor opens from a backend template (or an existing spec),
-- never from a hardcoded client constant. Seeding is done in Go (seed-if-empty)
-- so the template body stays a real StrategySpec document, not duplicated SQL.
CREATE TABLE IF NOT EXISTS spec_templates (
    template_id  TEXT    NOT NULL PRIMARY KEY,
    name         TEXT    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    regime       TEXT    NOT NULL DEFAULT 'UNKNOWN',
    kind         TEXT    NOT NULL DEFAULT 'humano',
    spec_json    TEXT    NOT NULL DEFAULT '{}',
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_spec_templates_sort
    ON spec_templates (sort_order, name);

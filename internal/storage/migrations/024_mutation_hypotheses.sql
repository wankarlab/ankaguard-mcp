-- SQLite mirror of migrations/operational/028_mutation_hypotheses.up.sql
CREATE TABLE IF NOT EXISTS mutation_hypotheses (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    source_signal TEXT    NOT NULL CHECK (source_signal IN ('ranking_verdict', 'drift_diagnostic')),
    spec_id       TEXT,
    regime        TEXT,
    metrics_json  TEXT    NOT NULL DEFAULT '{}',
    status        TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'reviewed', 'rejected')),
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_mutation_hyp_source
    ON mutation_hypotheses (source_signal, status, created_at DESC);

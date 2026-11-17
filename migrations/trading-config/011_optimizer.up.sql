BEGIN;

CREATE TABLE IF NOT EXISTS optimizer_runs (
    id BIGSERIAL PRIMARY KEY,
    pair TEXT NOT NULL,
    objective TEXT NOT NULL,
    space_name TEXT NOT NULL,
    iterations INT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    best_score NUMERIC(10,4),
    top_presets JSONB,
    triggered_by TEXT NOT NULL,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS optimizer_trials (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES optimizer_runs(id) ON DELETE CASCADE,
    iteration INT NOT NULL,
    params JSONB NOT NULL,
    metrics JSONB NOT NULL,
    objective NUMERIC(10,4) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_optimizer_runs_status ON optimizer_runs(status);
CREATE INDEX IF NOT EXISTS idx_optimizer_runs_pair   ON optimizer_runs(pair);
CREATE INDEX IF NOT EXISTS idx_optimizer_trials_run  ON optimizer_trials(run_id);

COMMIT;

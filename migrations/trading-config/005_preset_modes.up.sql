BEGIN;
ALTER TABLE parameter_presets ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'preset';
ALTER TABLE parameter_presets ADD COLUMN IF NOT EXISTS risk_label TEXT;
ALTER TABLE parameter_presets ADD COLUMN IF NOT EXISTS editable BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE parameter_presets ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_presets_category ON parameter_presets(category);
COMMIT;

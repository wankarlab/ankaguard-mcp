BEGIN;
DROP INDEX IF EXISTS idx_presets_category;
ALTER TABLE parameter_presets DROP COLUMN IF EXISTS updated_at;
ALTER TABLE parameter_presets DROP COLUMN IF EXISTS editable;
ALTER TABLE parameter_presets DROP COLUMN IF EXISTS risk_label;
ALTER TABLE parameter_presets DROP COLUMN IF EXISTS category;
COMMIT;

-- Register bot.validation_gate.enabled in the parameter registry so it can be
-- toggled at runtime via the dynamic-config API without a server restart.
-- Default OFF: the gate blocks live entries on pairs without a certified edge
-- (PF >= 1.30 net of fees, >= 60% windows green). Enable after backfilling >= 6 months
-- of candle history and confirming the validation service is producing verdicts.
--
-- Schema (001_create_parameter_registry / 002_create_parameter_values):
--   parameter_registry(key, type, group_name, description, default_value,
--                      min_value, max_value, enum_values JSONB, ...)
--   parameter_values(key, scope_type CHECK(global|exchange|pair|bot_instance),
--                    scope_id, value, updated_by, source CHECK(...,'migration'))
INSERT INTO parameter_registry (key, type, group_name, description, default_value, enum_values)
VALUES (
    'bot.validation_gate.enabled',
    'bool',
    'bot',
    'When true, the bot refuses live entries on pairs that have not passed walk-forward out-of-sample validation (net PF >= 1.30, >= 60% windows profitable). Enable only after candle backfill is complete and verdicts are being generated.',
    'false',
    '["true", "false"]'::jsonb
)
ON CONFLICT (key) DO UPDATE SET
    description   = EXCLUDED.description,
    default_value = EXCLUDED.default_value,
    enum_values   = EXCLUDED.enum_values;

-- Seed the initial OFF value at global scope (explicit, auditable).
INSERT INTO parameter_values (key, scope_type, scope_id, value, updated_by, source)
VALUES (
    'bot.validation_gate.enabled',
    'global',
    'global',
    'false',
    'migration',
    'migration'
)
ON CONFLICT (key, scope_type, scope_id) DO NOTHING;

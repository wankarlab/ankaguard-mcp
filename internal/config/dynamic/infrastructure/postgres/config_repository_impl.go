package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// configRepository implements repository.ConfigRepository backed by PostgreSQL via pgx/v5.
type configRepository struct {
	pool *pgxpool.Pool
}

// New creates a new ConfigRepository backed by the supplied connection pool.
func New(pool *pgxpool.Pool) repository.ConfigRepository {
	return &configRepository{pool: pool}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// ListRegistry returns all parameter metadata rows, optionally filtered by group_name.
// Passing an empty string returns all groups.
func (r *configRepository) ListRegistry(ctx context.Context, group string) ([]*domain.ParameterRegistry, error) {
	var (
		rows pgx.Rows
		err  error
	)

	const baseSQL = `
SELECT key, type, group_name, description, default_value,
       min_value, max_value, enum_values, requires_restart, scope_levels, deprecated
FROM parameter_registry`

	if group == "" {
		rows, err = r.pool.Query(ctx, baseSQL+" ORDER BY group_name, key")
	} else {
		rows, err = r.pool.Query(ctx, baseSQL+" WHERE group_name = $1 ORDER BY key", group)
	}
	if err != nil {
		return nil, fmt.Errorf("ListRegistry query: %w", err)
	}
	defer rows.Close()

	var result []*domain.ParameterRegistry
	for rows.Next() {
		reg, scanErr := scanRegistry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("ListRegistry scan: %w", scanErr)
		}
		result = append(result, reg)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ListRegistry rows: %w", err)
	}
	return result, nil
}

// GetRegistry returns the metadata for a single parameter key.
func (r *configRepository) GetRegistry(ctx context.Context, key string) (*domain.ParameterRegistry, error) {
	const sql = `
SELECT key, type, group_name, description, default_value,
       min_value, max_value, enum_values, requires_restart, scope_levels, deprecated
FROM parameter_registry
WHERE key = $1`

	row := r.pool.QueryRow(ctx, sql, key)
	reg, err := scanRegistry(row)
	if err != nil {
		return nil, fmt.Errorf("GetRegistry %q: %w", key, err)
	}
	return reg, nil
}

// UpsertRegistry inserts or updates parameter metadata (keyed on `key`).
func (r *configRepository) UpsertRegistry(ctx context.Context, reg *domain.ParameterRegistry) error {
	enumJSON, err := json.Marshal(reg.EnumValues)
	if err != nil {
		return fmt.Errorf("UpsertRegistry marshal enum_values: %w", err)
	}
	scopeCSV := scopeLevelsToCSV(reg.ScopeLevels)

	const sql = `
INSERT INTO parameter_registry
    (key, type, group_name, description, default_value,
     min_value, max_value, enum_values, requires_restart, scope_levels, deprecated)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (key) DO UPDATE SET
    type             = EXCLUDED.type,
    group_name       = EXCLUDED.group_name,
    description      = EXCLUDED.description,
    default_value    = EXCLUDED.default_value,
    min_value        = EXCLUDED.min_value,
    max_value        = EXCLUDED.max_value,
    enum_values      = EXCLUDED.enum_values,
    requires_restart = EXCLUDED.requires_restart,
    scope_levels     = EXCLUDED.scope_levels,
    deprecated       = EXCLUDED.deprecated`

	_, err = r.pool.Exec(ctx, sql,
		reg.Key,
		string(reg.Type),
		reg.GroupName,
		reg.Description,
		reg.DefaultValue,
		reg.MinValue,
		reg.MaxValue,
		enumJSON,
		reg.RequiresRestart,
		scopeCSV,
		reg.Deprecated,
	)
	if err != nil {
		return fmt.Errorf("UpsertRegistry exec %q: %w", reg.Key, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Values
// ---------------------------------------------------------------------------

// GetValue returns the current value for the given key + scope combination.
func (r *configRepository) GetValue(ctx context.Context, key string, scope domain.ScopeType, scopeID string) (*domain.ParameterValue, error) {
	const sql = `
SELECT id, key, scope_type, scope_id, value, updated_at, updated_by, source
FROM parameter_values
WHERE key = $1 AND scope_type = $2 AND scope_id = $3`

	row := r.pool.QueryRow(ctx, sql, key, string(scope), scopeID)
	val, err := scanValue(row)
	if err != nil {
		return nil, fmt.Errorf("GetValue %q/%s/%s: %w", key, scope, scopeID, err)
	}
	return val, nil
}

// ListValues returns all scoped values for a parameter key.
func (r *configRepository) ListValues(ctx context.Context, key string) ([]*domain.ParameterValue, error) {
	const sql = `
SELECT id, key, scope_type, scope_id, value, updated_at, updated_by, source
FROM parameter_values
WHERE key = $1
ORDER BY scope_type, scope_id`

	rows, err := r.pool.Query(ctx, sql, key)
	if err != nil {
		return nil, fmt.Errorf("ListValues query %q: %w", key, err)
	}
	defer rows.Close()
	return collectValues(rows)
}

// ListAllValues returns every value row (for snapshot rebuilds).
func (r *configRepository) ListAllValues(ctx context.Context) ([]*domain.ParameterValue, error) {
	const sql = `
SELECT id, key, scope_type, scope_id, value, updated_at, updated_by, source
FROM parameter_values
ORDER BY key, scope_type, scope_id`

	rows, err := r.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("ListAllValues query: %w", err)
	}
	defer rows.Close()
	return collectValues(rows)
}

// SetValue inserts or updates a single value and writes a history row, all in one transaction.
func (r *configRepository) SetValue(ctx context.Context, val *domain.ParameterValue, reason string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("SetValue begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	oldVal, err := fetchCurrentValue(ctx, tx, val.Key, val.ScopeType, val.ScopeID)
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("SetValue fetch old: %w", err)
	}

	if err = upsertValue(ctx, tx, val); err != nil {
		return fmt.Errorf("SetValue upsert: %w", err)
	}
	if err = insertHistory(ctx, tx, val.Key, val.ScopeType, val.ScopeID, oldVal, val.Value, val.UpdatedBy, reason, nil, nil); err != nil {
		return fmt.Errorf("SetValue history: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("SetValue commit: %w", err)
	}
	return nil
}

// DeleteValue removes a parameter value row and records the deletion in history.
func (r *configRepository) DeleteValue(ctx context.Context, key string, scope domain.ScopeType, scopeID string, by string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("DeleteValue begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	oldVal, err := fetchCurrentValue(ctx, tx, key, scope, scopeID)
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("DeleteValue fetch old: %w", err)
	}

	const delSQL = `DELETE FROM parameter_values WHERE key = $1 AND scope_type = $2 AND scope_id = $3`
	if _, err = tx.Exec(ctx, delSQL, key, string(scope), scopeID); err != nil {
		return fmt.Errorf("DeleteValue exec: %w", err)
	}

	oldStr := ""
	if oldVal != nil {
		oldStr = *oldVal
	}
	deletedSentinel := "(deleted)"
	if err = insertHistory(ctx, tx, key, scope, scopeID, &oldStr, deletedSentinel, by, "value deleted", nil, nil); err != nil {
		return fmt.Errorf("DeleteValue history: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("DeleteValue commit: %w", err)
	}
	return nil
}

// BulkSetValues atomically applies all value changes in a single transaction.
// On any error the transaction is rolled back.
func (r *configRepository) BulkSetValues(ctx context.Context, vals []*domain.ParameterValue, by, reason string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("BulkSetValues begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, val := range vals {
		// Override the actor if the caller provided one.
		if by != "" {
			val.UpdatedBy = by
		}

		oldVal, fetchErr := fetchCurrentValue(ctx, tx, val.Key, val.ScopeType, val.ScopeID)
		if fetchErr != nil && fetchErr != pgx.ErrNoRows {
			return fmt.Errorf("BulkSetValues fetch old %q: %w", val.Key, fetchErr)
		}

		if upsertErr := upsertValue(ctx, tx, val); upsertErr != nil {
			return fmt.Errorf("BulkSetValues upsert %q: %w", val.Key, upsertErr)
		}
		if histErr := insertHistory(ctx, tx, val.Key, val.ScopeType, val.ScopeID, oldVal, val.Value, val.UpdatedBy, reason, nil, nil); histErr != nil {
			return fmt.Errorf("BulkSetValues history %q: %w", val.Key, histErr)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("BulkSetValues commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// History
// ---------------------------------------------------------------------------

// ListHistory returns history rows matching the filter, ordered newest-first.
func (r *configRepository) ListHistory(ctx context.Context, filter repository.HistoryFilter, limit int) ([]*domain.ParameterHistory, error) {
	var (
		args   []any
		wheres []string
		idx    = 1
	)

	addCond := func(cond string, val any) {
		wheres = append(wheres, fmt.Sprintf(cond, idx))
		args = append(args, val)
		idx++
	}

	if filter.Key != "" {
		addCond("key = $%d", filter.Key)
	}
	if filter.ChangedBy != "" {
		addCond("changed_by = $%d", filter.ChangedBy)
	}
	if !filter.Since.IsZero() {
		addCond("changed_at >= $%d", filter.Since)
	}
	if !filter.Until.IsZero() {
		addCond("changed_at <= $%d", filter.Until)
	}

	sql := `
SELECT id, key, scope_type, scope_id, old_value, new_value,
       changed_at, changed_by, change_reason, preset_name, rollback_of
FROM parameter_history`

	if len(wheres) > 0 {
		sql += " WHERE " + strings.Join(wheres, " AND ")
	}
	sql += " ORDER BY changed_at DESC"

	if limit > 0 {
		sql += fmt.Sprintf(" LIMIT $%d", idx)
		args = append(args, limit)
	}

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("ListHistory query: %w", err)
	}
	defer rows.Close()

	var result []*domain.ParameterHistory
	for rows.Next() {
		h, scanErr := scanHistory(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("ListHistory scan: %w", scanErr)
		}
		result = append(result, h)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ListHistory rows: %w", err)
	}
	return result, nil
}

// GetHistory returns a single history entry by ID.
func (r *configRepository) GetHistory(ctx context.Context, id int64) (*domain.ParameterHistory, error) {
	const sql = `
SELECT id, key, scope_type, scope_id, old_value, new_value,
       changed_at, changed_by, change_reason, preset_name, rollback_of
FROM parameter_history
WHERE id = $1`

	row := r.pool.QueryRow(ctx, sql, id)
	h, err := scanHistory(row)
	if err != nil {
		return nil, fmt.Errorf("GetHistory %d: %w", id, err)
	}
	return h, nil
}

// ---------------------------------------------------------------------------
// Presets
// ---------------------------------------------------------------------------

// presetSelectColumns is the canonical column list (in scanPreset order) used by
// every preset SELECT so the row scan stays in sync with the queries.
const presetSelectColumns = `id, name, description, scope_type, scope_id, params_json,
       created_at, created_by, is_active, last_applied_at, performance_score,
       category, risk_label, editable`

// ListPresets returns all defined presets.
func (r *configRepository) ListPresets(ctx context.Context) ([]*domain.ParameterPreset, error) {
	sql := `SELECT ` + presetSelectColumns + ` FROM parameter_presets ORDER BY name`

	rows, err := r.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("ListPresets query: %w", err)
	}
	defer rows.Close()

	var result []*domain.ParameterPreset
	for rows.Next() {
		p, scanErr := scanPreset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("ListPresets scan: %w", scanErr)
		}
		result = append(result, p)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ListPresets rows: %w", err)
	}
	return result, nil
}

// ListPresetsByCategory returns all presets in the given category, ordered by name.
func (r *configRepository) ListPresetsByCategory(ctx context.Context, category string) ([]*domain.ParameterPreset, error) {
	sql := `SELECT ` + presetSelectColumns + ` FROM parameter_presets WHERE category = $1 ORDER BY name`

	rows, err := r.pool.Query(ctx, sql, category)
	if err != nil {
		return nil, fmt.Errorf("ListPresetsByCategory %q query: %w", category, err)
	}
	defer rows.Close()

	var result []*domain.ParameterPreset
	for rows.Next() {
		p, scanErr := scanPreset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("ListPresetsByCategory %q scan: %w", category, scanErr)
		}
		result = append(result, p)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ListPresetsByCategory %q rows: %w", category, err)
	}
	return result, nil
}

// GetPreset returns a single preset by name.
func (r *configRepository) GetPreset(ctx context.Context, name string) (*domain.ParameterPreset, error) {
	sql := `SELECT ` + presetSelectColumns + ` FROM parameter_presets WHERE name = $1`

	row := r.pool.QueryRow(ctx, sql, name)
	p, err := scanPreset(row)
	if err != nil {
		return nil, fmt.Errorf("GetPreset %q: %w", name, err)
	}
	return p, nil
}

// GetActiveMode returns the preset with is_active=true and category='mode'.
func (r *configRepository) GetActiveMode(ctx context.Context) (*domain.ParameterPreset, error) {
	sql := `SELECT ` + presetSelectColumns + ` FROM parameter_presets
WHERE is_active = TRUE AND category = 'mode'
ORDER BY last_applied_at DESC NULLS LAST
LIMIT 1`

	row := r.pool.QueryRow(ctx, sql)
	p, err := scanPreset(row)
	if err != nil {
		return nil, fmt.Errorf("GetActiveMode: %w", err)
	}
	return p, nil
}

// CreatePreset persists a new preset. Returns an error if the name already exists.
func (r *configRepository) CreatePreset(ctx context.Context, preset *domain.ParameterPreset) error {
	const sql = `
INSERT INTO parameter_presets
    (name, description, scope_type, scope_id, params_json, created_by, is_active,
     category, risk_label, editable)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`

	category := preset.Category
	if category == "" {
		category = "preset"
	}

	_, err := r.pool.Exec(ctx, sql,
		preset.Name,
		preset.Description,
		string(preset.ScopeType),
		preset.ScopeID,
		[]byte(preset.ParamsJSON),
		preset.CreatedBy,
		preset.IsActive,
		category,
		preset.RiskLabel,
		preset.Editable,
	)
	if err != nil {
		return fmt.Errorf("CreatePreset %q: %w", preset.Name, err)
	}
	return nil
}

// UpdatePreset updates params, description and risk_label of an existing preset.
// Returns pgx.ErrNoRows (wrapped) when the preset does not exist.
func (r *configRepository) UpdatePreset(ctx context.Context, name string, preset *domain.ParameterPreset) error {
	const sql = `
UPDATE parameter_presets
SET params_json = $1, description = $2, risk_label = $3, updated_at = NOW()
WHERE name = $4`

	ct, err := r.pool.Exec(ctx, sql,
		[]byte(preset.ParamsJSON),
		preset.Description,
		preset.RiskLabel,
		name,
	)
	if err != nil {
		return fmt.Errorf("UpdatePreset %q: %w", name, err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("UpdatePreset %q: %w", name, pgx.ErrNoRows)
	}
	return nil
}

// DeletePreset removes a preset by name. It refuses to delete a non-editable preset.
func (r *configRepository) DeletePreset(ctx context.Context, name string) error {
	const sql = `DELETE FROM parameter_presets WHERE name = $1 AND editable = TRUE`

	ct, err := r.pool.Exec(ctx, sql, name)
	if err != nil {
		return fmt.Errorf("DeletePreset %q: %w", name, err)
	}
	if ct.RowsAffected() == 0 {
		// Either the preset does not exist or it is protected (editable=false).
		return fmt.Errorf("DeletePreset %q: preset not found or not editable", name)
	}
	return nil
}

// ApplyPreset atomically applies all parameters from the preset and updates last_applied_at.
// Returns a snapshotID string (format "preset:<name>@<unix>") for rollback tracing.
func (r *configRepository) ApplyPreset(ctx context.Context, name, by, reason string) (string, error) {
	// Fetch the preset outside the bulk-tx so we can parse params first.
	preset, err := r.GetPreset(ctx, name)
	if err != nil {
		return "", fmt.Errorf("ApplyPreset fetch %q: %w", name, err)
	}

	// Parse params_json into map[string]string.
	var params map[string]string
	if err = json.Unmarshal(preset.ParamsJSON, &params); err != nil {
		return "", fmt.Errorf("ApplyPreset parse params_json for %q: %w", name, err)
	}

	// Build ParameterValue slice from the preset.
	now := time.Now().UTC()
	vals := make([]*domain.ParameterValue, 0, len(params))
	for k, v := range params {
		vals = append(vals, &domain.ParameterValue{
			Key:       k,
			ScopeType: preset.ScopeType,
			ScopeID:   preset.ScopeID,
			Value:     v,
			UpdatedAt: now,
			UpdatedBy: by,
			Source:    domain.SourcePreset,
		})
	}

	// Open a single transaction for all value upserts + history rows.
	tx, txErr := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if txErr != nil {
		return "", fmt.Errorf("ApplyPreset begin tx: %w", txErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	presetNamePtr := &name
	applyReason := reason
	if applyReason == "" {
		applyReason = fmt.Sprintf("preset applied: %s", name)
	}

	for _, val := range vals {
		oldVal, fetchErr := fetchCurrentValue(ctx, tx, val.Key, val.ScopeType, val.ScopeID)
		if fetchErr != nil && fetchErr != pgx.ErrNoRows {
			return "", fmt.Errorf("ApplyPreset fetch old %q: %w", val.Key, fetchErr)
		}
		if upsertErr := upsertValue(ctx, tx, val); upsertErr != nil {
			return "", fmt.Errorf("ApplyPreset upsert %q: %w", val.Key, upsertErr)
		}
		if histErr := insertHistory(ctx, tx, val.Key, val.ScopeType, val.ScopeID, oldVal, val.Value, by, applyReason, presetNamePtr, nil); histErr != nil {
			return "", fmt.Errorf("ApplyPreset history %q: %w", val.Key, histErr)
		}
	}

	// Update last_applied_at on the preset.
	const updateSQL = `UPDATE parameter_presets SET last_applied_at = $1 WHERE name = $2`
	if _, err = tx.Exec(ctx, updateSQL, now, name); err != nil {
		return "", fmt.Errorf("ApplyPreset update last_applied_at: %w", err)
	}

	// Single-active-mode invariant: when the applied preset is a "mode", make it
	// the only active mode. Clear is_active on every mode, then set it on this one.
	if preset.Category == "mode" {
		if _, err = tx.Exec(ctx, `UPDATE parameter_presets SET is_active = FALSE WHERE category = 'mode'`); err != nil {
			return "", fmt.Errorf("ApplyPreset clear active modes: %w", err)
		}
		if _, err = tx.Exec(ctx, `UPDATE parameter_presets SET is_active = TRUE WHERE name = $1`, name); err != nil {
			return "", fmt.Errorf("ApplyPreset set active mode: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("ApplyPreset commit: %w", err)
	}

	snapshotID := fmt.Sprintf("preset:%s@%d", name, now.Unix())
	return snapshotID, nil
}

// UpdatePresetPerformance stores backtest performance metrics for a preset.
func (r *configRepository) UpdatePresetPerformance(ctx context.Context, name string, perf *domain.PresetPerformance) error {
	perfJSON, err := json.Marshal(perf)
	if err != nil {
		return fmt.Errorf("UpdatePresetPerformance marshal: %w", err)
	}

	const sql = `UPDATE parameter_presets SET performance_score = $1 WHERE name = $2`
	ct, err := r.pool.Exec(ctx, sql, perfJSON, name)
	if err != nil {
		return fmt.Errorf("UpdatePresetPerformance exec %q: %w", name, err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("UpdatePresetPerformance %q: %w", name, pgx.ErrNoRows)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers — scan
// ---------------------------------------------------------------------------

// rowScanner is the minimal interface satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRegistry(row rowScanner) (*domain.ParameterRegistry, error) {
	var (
		reg         domain.ParameterRegistry
		typ         string
		enumRaw     []byte
		scopeLevCSV string
	)
	err := row.Scan(
		&reg.Key,
		&typ,
		&reg.GroupName,
		&reg.Description,
		&reg.DefaultValue,
		&reg.MinValue,
		&reg.MaxValue,
		&enumRaw,
		&reg.RequiresRestart,
		&scopeLevCSV,
		&reg.Deprecated,
	)
	if err != nil {
		return nil, err
	}
	reg.Type = domain.ParamType(typ)

	if len(enumRaw) > 0 && string(enumRaw) != "null" {
		if err = json.Unmarshal(enumRaw, &reg.EnumValues); err != nil {
			return nil, fmt.Errorf("unmarshal enum_values: %w", err)
		}
	}
	reg.ScopeLevels = csvToScopeLevels(scopeLevCSV)
	return &reg, nil
}

func scanValue(row rowScanner) (*domain.ParameterValue, error) {
	var (
		v   domain.ParameterValue
		st  string
		src string
	)
	err := row.Scan(&v.ID, &v.Key, &st, &v.ScopeID, &v.Value, &v.UpdatedAt, &v.UpdatedBy, &src)
	if err != nil {
		return nil, err
	}
	v.ScopeType = domain.ScopeType(st)
	v.Source = domain.ParamSource(src)
	return &v, nil
}

func scanHistory(row rowScanner) (*domain.ParameterHistory, error) {
	var (
		h  domain.ParameterHistory
		st string
	)
	err := row.Scan(
		&h.ID, &h.Key, &st, &h.ScopeID,
		&h.OldValue, &h.NewValue,
		&h.ChangedAt, &h.ChangedBy, &h.ChangeReason,
		&h.PresetName, &h.RollbackOf,
	)
	if err != nil {
		return nil, err
	}
	h.ScopeType = domain.ScopeType(st)
	return &h, nil
}

func scanPreset(row rowScanner) (*domain.ParameterPreset, error) {
	var (
		p       domain.ParameterPreset
		st      string
		params  []byte
		perfRaw []byte
	)
	var riskLabel *string
	err := row.Scan(
		&p.ID, &p.Name, &p.Description, &st, &p.ScopeID,
		&params, &p.CreatedAt, &p.CreatedBy, &p.IsActive,
		&p.LastAppliedAt, &perfRaw,
		&p.Category, &riskLabel, &p.Editable,
	)
	if err != nil {
		return nil, err
	}
	p.ScopeType = domain.ScopeType(st)
	p.ParamsJSON = json.RawMessage(params)
	if riskLabel != nil {
		p.RiskLabel = *riskLabel
	}

	if len(perfRaw) > 0 && string(perfRaw) != "null" {
		p.PerformanceScore = &domain.PresetPerformance{}
		if err = json.Unmarshal(perfRaw, p.PerformanceScore); err != nil {
			return nil, fmt.Errorf("unmarshal performance_score: %w", err)
		}
	}
	return &p, nil
}

// ---------------------------------------------------------------------------
// Helpers — DML inside a transaction
// ---------------------------------------------------------------------------

func upsertValue(ctx context.Context, tx pgx.Tx, val *domain.ParameterValue) error {
	const sql = `
INSERT INTO parameter_values (key, scope_type, scope_id, value, updated_at, updated_by, source)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (key, scope_type, scope_id) DO UPDATE SET
    value      = EXCLUDED.value,
    updated_at = EXCLUDED.updated_at,
    updated_by = EXCLUDED.updated_by,
    source     = EXCLUDED.source`

	updAt := val.UpdatedAt
	if updAt.IsZero() {
		updAt = time.Now().UTC()
	}
	_, err := tx.Exec(ctx, sql,
		val.Key, string(val.ScopeType), val.ScopeID,
		val.Value, updAt, val.UpdatedBy, string(val.Source),
	)
	return err
}

func insertHistory(
	ctx context.Context,
	tx pgx.Tx,
	key string,
	scope domain.ScopeType,
	scopeID string,
	oldVal *string,
	newVal string,
	changedBy string,
	reason string,
	presetName *string,
	rollbackOf *int64,
) error {
	const sql = `
INSERT INTO parameter_history
    (key, scope_type, scope_id, old_value, new_value, changed_at,
     changed_by, change_reason, preset_name, rollback_of)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`

	_, err := tx.Exec(ctx, sql,
		key, string(scope), scopeID,
		oldVal, newVal, time.Now().UTC(),
		changedBy, reason, presetName, rollbackOf,
	)
	return err
}

// fetchCurrentValue reads the current value text for a key+scope within a transaction.
// Returns (nil, pgx.ErrNoRows) when no row exists.
func fetchCurrentValue(ctx context.Context, tx pgx.Tx, key string, scope domain.ScopeType, scopeID string) (*string, error) {
	const sql = `SELECT value FROM parameter_values WHERE key=$1 AND scope_type=$2 AND scope_id=$3`
	var val string
	err := tx.QueryRow(ctx, sql, key, string(scope), scopeID).Scan(&val)
	if err == pgx.ErrNoRows {
		return nil, pgx.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	return &val, nil
}

// ---------------------------------------------------------------------------
// Helpers — collect rows
// ---------------------------------------------------------------------------

func collectValues(rows pgx.Rows) ([]*domain.ParameterValue, error) {
	var result []*domain.ParameterValue
	for rows.Next() {
		v, err := scanValue(rows)
		if err != nil {
			return nil, fmt.Errorf("collectValues scan: %w", err)
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collectValues rows: %w", err)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers — type conversions
// ---------------------------------------------------------------------------

// scopeLevelsToCSV converts a []ScopeType slice to a comma-separated string for DB storage.
func scopeLevelsToCSV(levels []domain.ScopeType) string {
	parts := make([]string, len(levels))
	for i, l := range levels {
		parts[i] = string(l)
	}
	return strings.Join(parts, ",")
}

// csvToScopeLevels parses a comma-separated scope_levels string from the DB.
func csvToScopeLevels(csv string) []domain.ScopeType {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	levels := make([]domain.ScopeType, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			levels = append(levels, domain.ScopeType(p))
		}
	}
	return levels
}

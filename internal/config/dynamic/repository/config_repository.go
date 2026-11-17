package repository

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// HistoryFilter specifies optional filters when querying parameter history.
// Zero-value fields are ignored (no filtering on that field).
type HistoryFilter struct {
	Key       string
	ChangedBy string
	Since     time.Time
	Until     time.Time
}

// ConfigRepository defines all persistence operations for the dynamic configuration system.
type ConfigRepository interface {
	// Registry operations — manage parameter schema / metadata.

	// ListRegistry returns all registered parameters, optionally filtered by group.
	// Pass an empty string to return all groups.
	ListRegistry(ctx context.Context, group string) ([]*domain.ParameterRegistry, error)

	// GetRegistry returns the metadata for a single parameter by key.
	// Returns pgx.ErrNoRows (wrapped) when the key is not found.
	GetRegistry(ctx context.Context, key string) (*domain.ParameterRegistry, error)

	// UpsertRegistry inserts or updates parameter metadata.
	UpsertRegistry(ctx context.Context, reg *domain.ParameterRegistry) error

	// Value operations — manage the current effective values per scope.

	// GetValue returns the value of a parameter for a specific scope.
	// Returns pgx.ErrNoRows (wrapped) when no value exists for the given key+scope.
	GetValue(ctx context.Context, key string, scope domain.ScopeType, scopeID string) (*domain.ParameterValue, error)

	// ListValues returns all scoped values for a given parameter key.
	ListValues(ctx context.Context, key string) ([]*domain.ParameterValue, error)

	// ListAllValues returns every value row; used for snapshot rebuilds.
	ListAllValues(ctx context.Context) ([]*domain.ParameterValue, error)

	// SetValue inserts or updates a single parameter value and writes a history row.
	SetValue(ctx context.Context, val *domain.ParameterValue, reason string) error

	// DeleteValue removes a parameter value for the given scope and records the deletion
	// in history with the provided actor identifier.
	DeleteValue(ctx context.Context, key string, scope domain.ScopeType, scopeID string, by string) error

	// BulkSetValues atomically applies a set of value changes inside a single transaction.
	// All changes are attributed to by with the supplied reason.
	// On any error the transaction is rolled back and no changes are persisted.
	BulkSetValues(ctx context.Context, vals []*domain.ParameterValue, by, reason string) error

	// History operations — immutable audit trail of every value change.

	// ListHistory returns history rows matching the supplied filter, newest first.
	// limit caps the number of returned rows; pass 0 for no limit.
	ListHistory(ctx context.Context, filter HistoryFilter, limit int) ([]*domain.ParameterHistory, error)

	// GetHistory returns a single history entry by its ID.
	// Returns pgx.ErrNoRows (wrapped) when not found.
	GetHistory(ctx context.Context, id int64) (*domain.ParameterHistory, error)

	// Preset operations — named snapshots of parameter sets.

	// ListPresets returns all defined presets.
	ListPresets(ctx context.Context) ([]*domain.ParameterPreset, error)

	// ListPresetsByCategory returns all presets in the given category, ordered by name.
	ListPresetsByCategory(ctx context.Context, category string) ([]*domain.ParameterPreset, error)

	// GetPreset returns a single preset by name.
	// Returns pgx.ErrNoRows (wrapped) when not found.
	GetPreset(ctx context.Context, name string) (*domain.ParameterPreset, error)

	// GetActiveMode returns the preset with is_active=true and category='mode'.
	// Returns pgx.ErrNoRows (wrapped) when no active mode exists.
	GetActiveMode(ctx context.Context) (*domain.ParameterPreset, error)

	// CreatePreset persists a new preset. Fails if a preset with the same name already exists.
	CreatePreset(ctx context.Context, preset *domain.ParameterPreset) error

	// UpdatePreset updates the params, description and risk_label of an existing preset.
	UpdatePreset(ctx context.Context, name string, preset *domain.ParameterPreset) error

	// DeletePreset removes a preset by name. Fails if the preset is not editable.
	DeletePreset(ctx context.Context, name string) error

	// ApplyPreset atomically applies all parameter values from the named preset and marks it
	// as last applied. Returns a snapshotID string that can be used for rollback tracing.
	ApplyPreset(ctx context.Context, name, by, reason string) (snapshotID string, err error)

	// UpdatePresetPerformance stores backtest performance metrics for a preset.
	UpdatePresetPerformance(ctx context.Context, name string, perf *domain.PresetPerformance) error
}

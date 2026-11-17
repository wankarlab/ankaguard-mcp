package domain

import (
	"encoding/json"
	"time"
)

// ParamType defines the data type of a configuration parameter.
type ParamType string

const (
	ParamTypeInt      ParamType = "int"
	ParamTypeFloat    ParamType = "float"
	ParamTypeBool     ParamType = "bool"
	ParamTypeString   ParamType = "string"
	ParamTypeDuration ParamType = "duration"
	ParamTypeJSON     ParamType = "json"
)

// ScopeType defines the scope level at which a parameter value applies.
type ScopeType string

const (
	ScopeGlobal      ScopeType = "global"
	ScopeExchange    ScopeType = "exchange"
	ScopePair        ScopeType = "pair"
	ScopeBotInstance ScopeType = "bot_instance"
)

// ParamSource identifies what originated a parameter value change.
type ParamSource string

const (
	SourceManual       ParamSource = "manual"
	SourcePreset       ParamSource = "preset"
	SourceOptimizer    ParamSource = "optimizer"
	SourceFeedbackLoop ParamSource = "feedback_loop"
	SourceMigration    ParamSource = "migration"
)

// ParameterRegistry defines the schema / metadata for a configuration parameter.
// scope_levels is stored as CSV TEXT in DB and parsed into []ScopeType on read.
// enum_values is stored as JSONB in DB and parsed into []string on read.
type ParameterRegistry struct {
	Key             string
	Type            ParamType
	GroupName       string
	Description     string
	DefaultValue    string
	MinValue        *string
	MaxValue        *string
	EnumValues      []string // JSON array stored as JSONB in DB
	RequiresRestart bool
	ScopeLevels     []ScopeType // parsed from CSV TEXT in DB
	Deprecated      bool
}

// ParameterValue holds the current effective value of a parameter for a given scope.
type ParameterValue struct {
	ID        int64
	Key       string
	ScopeType ScopeType
	ScopeID   string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string
	Source    ParamSource
}

// ParameterHistory records every change made to a parameter value.
type ParameterHistory struct {
	ID           int64
	Key          string
	ScopeType    ScopeType
	ScopeID      string
	OldValue     *string
	NewValue     string
	ChangedAt    time.Time
	ChangedBy    string
	ChangeReason string
	PresetName   *string
	RollbackOf   *int64
}

// ParameterPreset is a named collection of parameter values that can be applied atomically.
// ParamsJSON contains a JSON object mapping parameter keys to string values: {"key": "value"}.
// PerformanceScore is nil until Phase 6 backtest results populate it.
type ParameterPreset struct {
	ID               int64
	Name             string
	Description      string
	ScopeType        ScopeType
	ScopeID          string
	ParamsJSON       json.RawMessage // {"key": "value"}
	CreatedAt        time.Time
	CreatedBy        string
	IsActive         bool
	LastAppliedAt    *time.Time
	PerformanceScore *PresetPerformance // nil until Phase 6 fills it
	Category         string             // "preset" | "mode" | custom
	RiskLabel        string             // free text risk label
	Editable         bool               // false → protected from edit/delete
}

// PresetPerformance holds backtest performance metrics for a preset.
type PresetPerformance struct {
	Sharpe      float64   `json:"sharpe"`
	WinRate     float64   `json:"win_rate"`
	MaxDrawdown float64   `json:"max_drawdown"`
	NTrades     int       `json:"n_trades"`
	BacktestAt  time.Time `json:"backtest_at"`
}

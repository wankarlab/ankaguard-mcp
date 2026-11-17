// Package domain defines the persistence types for the optimizer subsystem.
package domain

import (
	"encoding/json"
	"time"
)

// OptimizerRun is the persisted record of one optimization job.
type OptimizerRun struct {
	ID          int64            `json:"id"`
	Pair        string           `json:"pair"`
	Objective   string           `json:"objective"`
	SpaceName   string           `json:"space_name"`
	Iterations  int              `json:"iterations"`
	Status      string           `json:"status"` // pending|running|done|failed|cancelled
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
	BestScore   *float64         `json:"best_score,omitempty"`
	TopPresets  []string         `json:"top_presets,omitempty"` // stored as JSONB
	TriggeredBy string           `json:"triggered_by"`
	Error       string           `json:"error,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`

	// Populated on detailed queries, not stored directly.
	Trials []*OptimizerTrial `json:"trials,omitempty"`
}

// OptimizerTrial is the result of a single backtest evaluation within an optimization run.
type OptimizerTrial struct {
	ID        int64             `json:"id"`
	RunID     int64             `json:"run_id"`
	Iteration int               `json:"iteration"`
	Params    map[string]string `json:"params"`
	Metrics   json.RawMessage   `json:"metrics"` // backtestdomain.Metrics serialised as JSONB
	Objective float64           `json:"objective"`
	CreatedAt time.Time         `json:"created_at"`
}

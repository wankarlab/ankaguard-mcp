package backtest

import (
	"context"
	"database/sql"
	"fmt"
	"math"
)

// RobustnessFactor is one of the 6 weighted scoring dimensions.
type RobustnessFactor struct {
	Key        string  // matches robustness_policy.factor_key
	Value      float64 // raw metric value
	Normalized float64 // clamp-normalized to [0, 1]
	Weight     float64 // from robustness_policy
	Score      float64 // Weight × Normalized × 100
}

// RobustnessResult holds the [0..100] composite score and per-factor breakdown.
type RobustnessResult struct {
	Score         float64            // composite [0..100]; 0 when Pass==false
	PolicyVersion int                // robustness_policy.version used
	Factors       []RobustnessFactor // per-factor breakdown for API/UI
}

// RobustnessScorer computes the governed robustness score for a certified Spec.
// EV-11: score is ONLY computed when ValidationVerdict.Pass==true.
// Default-OFF shadow mode: returned score is logged but does not gate spawn.
type RobustnessScorer struct {
	db *sql.DB
}

// NewRobustnessScorer creates a scorer backed by the robustness_policy catalog.
func NewRobustnessScorer(db *sql.DB) *RobustnessScorer {
	return &RobustnessScorer{db: db}
}

// Score computes the RobustnessScore for the given ValidationVerdict.
// Returns (zero result, error) if Pass==false or no active policy version.
// EV-11: computes OOS PF 25, consistency 25, n_trades 15, inverse drawdown 15,
// Sharpe 10, temporal degradation 10 weighted by active robustness_policy version.
func (rs *RobustnessScorer) Score(ctx context.Context, v *ValidationVerdict) (RobustnessResult, error) {
	if !v.Pass {
		return RobustnessResult{}, fmt.Errorf("robustness score requires Pass==true; this Spec is not certified")
	}

	policy, version, err := rs.loadActivePolicy(ctx)
	if err != nil {
		return RobustnessResult{}, fmt.Errorf("load robustness policy: %w", err)
	}
	if len(policy) == 0 {
		return RobustnessResult{}, fmt.Errorf("no active robustness policy version found")
	}

	rawValues := map[string]float64{
		"oos_profit_factor":    v.AggProfitFactor,
		"consistency":          v.Consistency,
		"n_trades":             float64(v.NTradesTotal),
		"inverse_drawdown":     inverseDDFactor(v.WorstDrawdown),
		"sharpe":               v.AggregateMetric.Sharpe,
		"temporal_degradation": temporalDegradation(v.Windows),
	}

	var totalScore float64
	factors := make([]RobustnessFactor, 0, len(policy))

	for _, row := range policy {
		raw, ok := rawValues[row.factorKey]
		if !ok {
			continue
		}
		norm := clampNormalize(raw, row.minVal, row.maxVal)
		score := row.weight * norm * 100
		totalScore += score
		factors = append(factors, RobustnessFactor{
			Key:        row.factorKey,
			Value:      raw,
			Normalized: norm,
			Weight:     row.weight,
			Score:      score,
		})
	}

	return RobustnessResult{
		Score:         math.Round(totalScore*10) / 10, // one decimal place
		PolicyVersion: version,
		Factors:       factors,
	}, nil
}

// policyRow is one row from robustness_policy.
type policyRow struct {
	factorKey string
	weight    float64
	minVal    float64
	maxVal    float64
}

// loadActivePolicy reads the single active version from robustness_policy.
func (rs *RobustnessScorer) loadActivePolicy(ctx context.Context) ([]policyRow, int, error) {
	rows, err := rs.db.QueryContext(ctx, `
		SELECT version, factor_key, weight, COALESCE(min_val, 0), COALESCE(max_val, 1)
		FROM robustness_policy
		WHERE active = 1 OR active = true
		ORDER BY version DESC, factor_key
	`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []policyRow
	var version int
	for rows.Next() {
		var row policyRow
		if err := rows.Scan(&version, &row.factorKey, &row.weight, &row.minVal, &row.maxVal); err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	return out, version, rows.Err()
}

// ClampNormalize maps raw to [0, 1] via linear interpolation between min and max.
// Exported for testing.
func ClampNormalize(raw, min, max float64) float64 {
	return clampNormalize(raw, min, max)
}

// InverseDDFactor is the exported test surface for inverseDDFactor.
func InverseDDFactor(worstDDPct float64) float64 { return inverseDDFactor(worstDDPct) }

// TemporalDegradation is the exported test surface for temporalDegradation.
func TemporalDegradation(windows []WindowResult) float64 { return temporalDegradation(windows) }

// clampNormalize maps raw to [0, 1] via linear interpolation between min and max.
func clampNormalize(raw, min, max float64) float64 {
	if max <= min {
		return 0
	}
	n := (raw - min) / (max - min)
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

// inverseDDFactor converts a worst-drawdown percentage to a score where lower DD = higher factor.
// A 0% drawdown yields 1.0; a 30%+ drawdown yields 0.0.
func inverseDDFactor(worstDDPct float64) float64 {
	const maxDD = 30.0
	if worstDDPct <= 0 {
		return 1.0
	}
	if worstDDPct >= maxDD {
		return 0.0
	}
	return 1.0 - (worstDDPct / maxDD)
}

// temporalDegradation measures how consistently the strategy performs across time windows.
// Returns the coefficient of variation of per-window PF (lower variation = higher stability).
// Returns 0.5 (neutral) when fewer than 2 windows are available.
func temporalDegradation(windows []WindowResult) float64 {
	if len(windows) < 2 {
		return 0.5
	}
	var sum float64
	for _, w := range windows {
		sum += w.Metrics.ProfitFactor
	}
	mean := sum / float64(len(windows))
	if mean == 0 {
		return 0
	}
	var variance float64
	for _, w := range windows {
		d := w.Metrics.ProfitFactor - mean
		variance += d * d
	}
	variance /= float64(len(windows))
	cv := math.Sqrt(variance) / mean // coefficient of variation
	// cv=0 → perfect stability=1.0; cv=0.5+ → instability=0
	return clampNormalize(1.0-cv*2, 0, 1)
}

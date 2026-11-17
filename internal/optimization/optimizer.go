// Package optimization implements the auto-tuning subsystem that suggests configuration
// improvements based on backtest performance, without ever applying changes automatically.
// Decision D5: humans always approve — this package only produces suggestions.
package optimization

import (
	"context"
	"math/rand/v2"
	"sort"
	"strconv"

	configdomain "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// Optimizer suggests parameter combinations to evaluate given prior trial history.
// Implementations range from random search to Gaussian Process Expected Improvement.
type Optimizer interface {
	// Suggest returns a new parameter map to evaluate next.
	// history contains all previously evaluated trials (may be empty on first call).
	Suggest(ctx context.Context, space ParamSpace, history []TrialResult) (map[string]string, error)
}

// ParamSpace describes the search space for an optimization run.
type ParamSpace struct {
	Params []ParamRange
}

// ParamRange defines one parameter's type and valid range/choices.
type ParamRange struct {
	Key         string
	Type        configdomain.ParamType // "float", "int", "string" (categorical)
	Min         string
	Max         string
	Categorical []string // used when Type == "string"
}

// TrialResult holds the evaluated params and resulting objective value for one trial.
type TrialResult struct {
	Params    map[string]string
	Objective float64
}

// ──────────────────────────────────────────────────────────────────────────────
// RandomSearchOptimizer
// ──────────────────────────────────────────────────────────────────────────────

// RandomSearchOptimizer implements Optimizer via uniform random sampling.
// It ignores history and simply draws a fresh random point each call.
// This is sufficient for 5-10 dimensional spaces with 50-200 iterations.
type RandomSearchOptimizer struct{}

// NewRandomSearchOptimizer returns a new RandomSearchOptimizer.
func NewRandomSearchOptimizer() Optimizer {
	return &RandomSearchOptimizer{}
}

// Suggest draws a uniformly random combination from the param space.
func (r *RandomSearchOptimizer) Suggest(_ context.Context, space ParamSpace, _ []TrialResult) (map[string]string, error) {
	return SampleUniform(space), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// LocalRefinementOptimizer
// ──────────────────────────────────────────────────────────────────────────────

// LocalRefinementOptimizer refines around the best known trial by searching within
// ±10% of each parameter value. Falls back to random search when history is empty.
type LocalRefinementOptimizer struct {
	fallback Optimizer
}

// NewLocalRefinementOptimizer creates an optimizer that refines the current best.
func NewLocalRefinementOptimizer() Optimizer {
	return &LocalRefinementOptimizer{fallback: NewRandomSearchOptimizer()}
}

// Suggest searches within ±10% of the best known params; random if no history.
func (l *LocalRefinementOptimizer) Suggest(ctx context.Context, space ParamSpace, history []TrialResult) (map[string]string, error) {
	if len(history) == 0 {
		return l.fallback.Suggest(ctx, space, history)
	}

	// Find best trial.
	best := history[0]
	for _, t := range history[1:] {
		if t.Objective > best.Objective {
			best = t
		}
	}

	result := make(map[string]string, len(space.Params))
	for _, p := range space.Params {
		bestVal := best.Params[p.Key]
		result[p.Key] = refineParam(p, bestVal)
	}
	return result, nil
}

// refineParam perturbs a single param value by ±10% within its defined bounds.
func refineParam(p ParamRange, bestVal string) string {
	switch p.Type {
	case configdomain.ParamTypeFloat:
		minF, _ := strconv.ParseFloat(p.Min, 64)
		maxF, _ := strconv.ParseFloat(p.Max, 64)
		cur, err := strconv.ParseFloat(bestVal, 64)
		if err != nil {
			cur = minF + rand.Float64()*(maxF-minF)
		}
		span := (maxF - minF) * 0.10
		lo := cur - span
		hi := cur + span
		if lo < minF {
			lo = minF
		}
		if hi > maxF {
			hi = maxF
		}
		v := lo + rand.Float64()*(hi-lo)
		return strconv.FormatFloat(v, 'f', 6, 64)

	case configdomain.ParamTypeInt:
		minI, _ := strconv.ParseInt(p.Min, 10, 64)
		maxI, _ := strconv.ParseInt(p.Max, 10, 64)
		cur, err := strconv.ParseInt(bestVal, 10, 64)
		if err != nil {
			cur = minI + rand.Int64N(maxI-minI+1)
		}
		span := int64(float64(maxI-minI) * 0.10)
		if span < 1 {
			span = 1
		}
		lo := cur - span
		hi := cur + span
		if lo < minI {
			lo = minI
		}
		if hi > maxI {
			hi = maxI
		}
		v := lo + rand.Int64N(hi-lo+1)
		return strconv.FormatInt(v, 10)

	case configdomain.ParamTypeString:
		if len(p.Categorical) == 0 {
			return bestVal
		}
		return p.Categorical[rand.IntN(len(p.Categorical))]

	default:
		// For bool/duration/json: just keep the best value unchanged.
		return bestVal
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// BayesianOptimizer (pragmatic: RandomSearch + LocalRefinement chain)
// ──────────────────────────────────────────────────────────────────────────────

// bayesianChainOptimizer chains random search with local refinement.
// After every 5 random trials it performs one refinement step.
// TODO: replace with true Gaussian Process + Expected Improvement (gonum GP-EI)
// once the GP implementation is stable.
type bayesianChainOptimizer struct {
	random    Optimizer
	refine    Optimizer
	callCount int
}

// NewBayesianOptimizer returns an optimizer that approximates Bayesian search via
// random exploration followed by local refinement steps.
//
// TODO: Implement true Gaussian Process Expected Improvement using gonum's
// stat/distmv package to model the posterior and pick candidates that maximize
// E[max(0, f(x) - f_best)].
func NewBayesianOptimizer() Optimizer {
	return &bayesianChainOptimizer{
		random: NewRandomSearchOptimizer(),
		refine: NewLocalRefinementOptimizer(),
	}
}

// Suggest alternates between random (exploration) and local refinement (exploitation).
func (b *bayesianChainOptimizer) Suggest(ctx context.Context, space ParamSpace, history []TrialResult) (map[string]string, error) {
	b.callCount++
	// Explore for the first 5 calls, then refine every other call.
	if b.callCount <= 5 || b.callCount%2 == 0 || len(history) == 0 {
		return b.random.Suggest(ctx, space, history)
	}
	return b.refine.Suggest(ctx, space, history)
}

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

// SelectTopN returns up to n TrialResults sorted by Objective descending.
// Trials with -Inf objective (penalty) are excluded.
func SelectTopN(history []TrialResult, n int) []TrialResult {
	filtered := make([]TrialResult, 0, len(history))
	for _, t := range history {
		if isFinite(t.Objective) {
			filtered = append(filtered, t)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Objective > filtered[j].Objective
	})
	if n > len(filtered) {
		n = len(filtered)
	}
	return filtered[:n]
}

func isFinite(f float64) bool {
	return !isInf(f) && !isNaN(f)
}

func isInf(f float64) bool {
	return f > 1e308 || f < -1e308
}

func isNaN(f float64) bool {
	return f != f
}

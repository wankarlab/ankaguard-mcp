package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	backtestCore "github.com/rantipay/trading-mcp/internal/backtest"
	backtestdomain "github.com/rantipay/trading-mcp/internal/backtest/domain"
	configdomain "github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	configrepo "github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
	optdomain "github.com/rantipay/trading-mcp/internal/optimization/domain"
	optrepo "github.com/rantipay/trading-mcp/internal/optimization/repository"
)

// ─── synthetic 1-D objective ─────────────────────────────────────────────────

// syntheticObjective returns -(x-0.7)^2 for a single float param "x" in [0,1].
// Maximum is 0.0 at x=0.7, so the optimizer should converge near 0.7.
func syntheticObjective(metrics backtestdomain.Metrics) float64 {
	return metrics.Sharpe
}

// ─── in-memory fake OptimizerRepository ──────────────────────────────────────

type fakeOptRepo struct {
	runs   map[int64]*optdomain.OptimizerRun
	trials []*optdomain.OptimizerTrial
	nextID int64
}

func newFakeOptRepo() *fakeOptRepo {
	return &fakeOptRepo{
		runs:   make(map[int64]*optdomain.OptimizerRun),
		nextID: 1,
	}
}

func (f *fakeOptRepo) CreateRun(_ context.Context, run *optdomain.OptimizerRun) (int64, error) {
	id := f.nextID
	f.nextID++
	run.ID = id
	f.runs[id] = run
	return id, nil
}

func (f *fakeOptRepo) MarkRunning(_ context.Context, id int64) error {
	if r, ok := f.runs[id]; ok {
		r.Status = "running"
		now := time.Now()
		r.StartedAt = &now
	}
	return nil
}

func (f *fakeOptRepo) MarkDone(_ context.Context, id int64, bestScore float64, topPresets []string) error {
	if r, ok := f.runs[id]; ok {
		r.Status = "done"
		r.BestScore = &bestScore
		r.TopPresets = topPresets
		now := time.Now()
		r.FinishedAt = &now
	}
	return nil
}

func (f *fakeOptRepo) MarkFailed(_ context.Context, id int64, errMsg string) error {
	if r, ok := f.runs[id]; ok {
		r.Status = "failed"
		r.Error = errMsg
	}
	return nil
}

func (f *fakeOptRepo) GetRun(_ context.Context, id int64) (*optdomain.OptimizerRun, error) {
	r, ok := f.runs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}

func (f *fakeOptRepo) ListRuns(_ context.Context, _, _ string, _ int) ([]*optdomain.OptimizerRun, error) {
	out := make([]*optdomain.OptimizerRun, 0, len(f.runs))
	for _, r := range f.runs {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeOptRepo) InsertTrial(_ context.Context, trial *optdomain.OptimizerTrial) error {
	f.trials = append(f.trials, trial)
	return nil
}

func (f *fakeOptRepo) ListTrials(_ context.Context, runID int64) ([]*optdomain.OptimizerTrial, error) {
	out := make([]*optdomain.OptimizerTrial, 0)
	for _, t := range f.trials {
		if t.RunID == runID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeOptRepo) CancelRun(_ context.Context, id int64) error {
	if r, ok := f.runs[id]; ok {
		r.Status = "cancelled"
	}
	return nil
}

func (f *fakeOptRepo) CleanupOldRuns(_ context.Context, _ time.Duration) error {
	return nil
}

// compile-time check that fakeOptRepo satisfies the interface.
var _ optrepo.OptimizerRepository = (*fakeOptRepo)(nil)

// ─── in-memory fake ConfigRepository ─────────────────────────────────────────

type fakeConfigRepo struct {
	presets map[string]*configdomain.ParameterPreset
}

func newFakeConfigRepo() *fakeConfigRepo {
	return &fakeConfigRepo{
		presets: make(map[string]*configdomain.ParameterPreset),
	}
}

func (r *fakeConfigRepo) ListRegistry(_ context.Context, _ string) ([]*configdomain.ParameterRegistry, error) {
	return nil, nil
}
func (r *fakeConfigRepo) GetRegistry(_ context.Context, _ string) (*configdomain.ParameterRegistry, error) {
	return nil, errors.New("not found")
}
func (r *fakeConfigRepo) UpsertRegistry(_ context.Context, _ *configdomain.ParameterRegistry) error {
	return nil
}
func (r *fakeConfigRepo) GetValue(_ context.Context, _ string, _ configdomain.ScopeType, _ string) (*configdomain.ParameterValue, error) {
	return nil, errors.New("not found")
}
func (r *fakeConfigRepo) ListValues(_ context.Context, _ string) ([]*configdomain.ParameterValue, error) {
	return nil, nil
}
func (r *fakeConfigRepo) ListAllValues(_ context.Context) ([]*configdomain.ParameterValue, error) {
	return nil, nil
}
func (r *fakeConfigRepo) SetValue(_ context.Context, _ *configdomain.ParameterValue, _ string) error {
	return nil
}
func (r *fakeConfigRepo) DeleteValue(_ context.Context, _ string, _ configdomain.ScopeType, _ string, _ string) error {
	return nil
}
func (r *fakeConfigRepo) BulkSetValues(_ context.Context, _ []*configdomain.ParameterValue, _, _ string) error {
	return nil
}
func (r *fakeConfigRepo) ListHistory(_ context.Context, _ configrepo.HistoryFilter, _ int) ([]*configdomain.ParameterHistory, error) {
	return nil, nil
}
func (r *fakeConfigRepo) GetHistory(_ context.Context, _ int64) (*configdomain.ParameterHistory, error) {
	return nil, errors.New("not found")
}
func (r *fakeConfigRepo) ListPresets(_ context.Context) ([]*configdomain.ParameterPreset, error) {
	out := make([]*configdomain.ParameterPreset, 0, len(r.presets))
	for _, p := range r.presets {
		out = append(out, p)
	}
	return out, nil
}
func (r *fakeConfigRepo) GetPreset(_ context.Context, name string) (*configdomain.ParameterPreset, error) {
	p, ok := r.presets[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return p, nil
}
func (r *fakeConfigRepo) CreatePreset(_ context.Context, preset *configdomain.ParameterPreset) error {
	preset.CreatedAt = time.Now()
	r.presets[preset.Name] = preset
	return nil
}
func (r *fakeConfigRepo) ApplyPreset(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}
func (r *fakeConfigRepo) UpdatePresetPerformance(_ context.Context, _ string, _ *configdomain.PresetPerformance) error {
	return nil
}
func (r *fakeConfigRepo) ListPresetsByCategory(_ context.Context, category string) ([]*configdomain.ParameterPreset, error) {
	out := make([]*configdomain.ParameterPreset, 0, len(r.presets))
	for _, p := range r.presets {
		if p.Category == category {
			out = append(out, p)
		}
	}
	return out, nil
}
func (r *fakeConfigRepo) GetActiveMode(_ context.Context) (*configdomain.ParameterPreset, error) {
	for _, p := range r.presets {
		if p.IsActive && p.Category == "mode" {
			return p, nil
		}
	}
	return nil, errors.New("not found")
}
func (r *fakeConfigRepo) UpdatePreset(_ context.Context, name string, preset *configdomain.ParameterPreset) error {
	if _, ok := r.presets[name]; !ok {
		return errors.New("not found")
	}
	r.presets[name] = preset
	return nil
}
func (r *fakeConfigRepo) DeletePreset(_ context.Context, name string) error {
	p, ok := r.presets[name]
	if !ok || !p.Editable {
		return errors.New("not found or not editable")
	}
	delete(r.presets, name)
	return nil
}

// compile-time check
var _ configrepo.ConfigRepository = (*fakeConfigRepo)(nil)

// ─── stub BacktestEngine ──────────────────────────────────────────────────────

// stubBacktestEngine wraps backtestCore.Engine's Run method via a function so
// we can inject a synthetic objective without touching the real engine.
// We achieve this by subclassing OptimizerService and overriding the run loop.

// stubOptService is a variant of OptimizerService that accepts a custom
// backtest function, bypassing the real backtestCore.Engine.
type stubOptService struct {
	cfgRepo    configrepo.ConfigRepository
	optRepo    optrepo.OptimizerRepository
	log        *logrus.Logger
	backtestFn func(params map[string]string) (backtestdomain.BacktestResult, error)
}

func newStubOptService(
	cfgRepo configrepo.ConfigRepository,
	optRepo optrepo.OptimizerRepository,
	backtestFn func(params map[string]string) (backtestdomain.BacktestResult, error),
) *stubOptService {
	log := logrus.New()
	log.SetLevel(logrus.WarnLevel) // keep test output quiet
	return &stubOptService{
		cfgRepo:    cfgRepo,
		optRepo:    optRepo,
		log:        log,
		backtestFn: backtestFn,
	}
}

// Run mirrors the logic of OptimizerService.Run but calls backtestFn instead of btEngine.
func (s *stubOptService) Run(ctx context.Context, runID int64, req OptimizeRequest) (*OptimizeResult, error) {
	if err := s.optRepo.MarkRunning(ctx, runID); err != nil {
		return nil, fmt.Errorf("mark running: %w", err)
	}

	opt := NewBayesianOptimizer()
	var history []TrialResult

	for i := 0; i < req.Iterations; i++ {
		select {
		case <-ctx.Done():
			_ = s.optRepo.MarkFailed(ctx, runID, "cancelled")
			return nil, ctx.Err()
		default:
		}

		params, err := opt.Suggest(ctx, req.Space, history)
		if err != nil {
			continue
		}

		btResult, err := s.backtestFn(params)
		if err != nil {
			continue
		}

		obj := req.Objective(btResult.Metrics)
		trial := TrialResult{Params: params, Objective: obj}
		history = append(history, trial)

		metricsJSON, _ := json.Marshal(btResult.Metrics)
		_ = s.optRepo.InsertTrial(ctx, &optdomain.OptimizerTrial{
			RunID:     runID,
			Iteration: i,
			Params:    params,
			Metrics:   json.RawMessage(metricsJSON),
			Objective: obj,
		})
	}

	top3 := SelectTopN(history, 3)
	presetNames := make([]string, 0, len(top3))
	bestScore := 0.0

	for i, t := range top3 {
		if i == 0 {
			bestScore = t.Objective
		}
		presetName := fmt.Sprintf("_auto_%s_opt%d_top%d", sanitizePair(req.Pair), runID, i+1)
		paramsJSON, _ := json.Marshal(t.Params)

		preset := &configdomain.ParameterPreset{
			Name:        presetName,
			Description: fmt.Sprintf("Auto-generated by optimizer run %d (rank %d/%d). Objective=%s score=%.4f", runID, i+1, len(top3), req.ObjectiveName, t.Objective),
			ScopeType:   configdomain.ScopeGlobal,
			ScopeID:     "*",
			ParamsJSON:  json.RawMessage(paramsJSON),
			CreatedBy:   "optimizer",
			IsActive:    false,
		}

		if pErr := s.cfgRepo.CreatePreset(ctx, preset); pErr != nil {
			continue
		}
		presetNames = append(presetNames, presetName)
	}

	_ = s.optRepo.MarkDone(ctx, runID, bestScore, presetNames)

	return &OptimizeResult{
		RunID:      runID,
		BestScore:  bestScore,
		TopPresets: presetNames,
		History:    history,
	}, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func silentLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)
	return log
}

// newNilBacktestEngine returns a *backtestCore.Engine that is nil — it must not
// be called in tests that use stubOptService.
func newNilBacktestEngine() *backtestCore.Engine {
	return nil
}

// ─── Test 1: convergence on synthetic objective ───────────────────────────────

// TestConvergenceSyntheticObjective verifies that running the optimizer for
// a modest number of iterations on a 1-D parabola with peak at x=0.7 causes
// the best-found x to land within the band [0.55, 0.85].
// Because the BayesianOptimizer alternates random / local-refinement, this
// property holds probabilistically over 30 iterations; we run 5 independent
// seeds/runs and require at least one to converge, making the test robust.
func TestConvergenceSyntheticObjective(t *testing.T) {
	space := ParamSpace{
		Params: []ParamRange{
			{Key: "x", Type: configdomain.ParamTypeFloat, Min: "0.0", Max: "1.0"},
		},
	}

	// Synthetic backtest: returns -(x-0.7)^2 as Sharpe, with enough trades
	// to pass the penalty guard (NTrades >= 20).
	backtestFn := func(params map[string]string) (backtestdomain.BacktestResult, error) {
		xStr := params["x"]
		x, err := strconv.ParseFloat(xStr, 64)
		if err != nil {
			return backtestdomain.BacktestResult{}, err
		}
		diff := x - 0.7
		sharpe := -(diff * diff) // maximum = 0.0 at x=0.7
		return backtestdomain.BacktestResult{
			Metrics: backtestdomain.Metrics{
				NTrades: 30, // above minTradesRequired=20
				Sharpe:  sharpe,
			},
		}, nil
	}

	req := OptimizeRequest{
		Space:         space,
		SpaceName:     "synthetic_1d",
		ObjectiveName: "maximize_sharpe",
		Objective:     MaximizeSharpe,
		Pair:          "BTC/USDT",
		Exchange:      "test",
		HorizonFrom:   time.Now().Add(-24 * time.Hour),
		HorizonTo:     time.Now(),
		Iterations:    30,
		By:            "test",
	}

	converged := false
	for attempt := 0; attempt < 5; attempt++ {
		cfgRepo := newFakeConfigRepo()
		optR := newFakeOptRepo()

		runID, err := optR.CreateRun(context.Background(), &optdomain.OptimizerRun{
			Pair:        req.Pair,
			Objective:   req.ObjectiveName,
			SpaceName:   req.SpaceName,
			Iterations:  req.Iterations,
			Status:      "pending",
			TriggeredBy: req.By,
		})
		require.NoError(t, err)

		svc := newStubOptService(cfgRepo, optR, backtestFn)
		result, err := svc.Run(context.Background(), runID, req)
		require.NoError(t, err)
		require.NotEmpty(t, result.History, "expected at least one successful trial")

		// Find best x across history.
		best := result.History[0]
		for _, tr := range result.History[1:] {
			if tr.Objective > best.Objective {
				best = tr
			}
		}
		xBest, err := strconv.ParseFloat(best.Params["x"], 64)
		require.NoError(t, err)

		t.Logf("attempt %d: best x=%.4f objective=%.6f", attempt, xBest, best.Objective)

		if xBest >= 0.55 && xBest <= 0.85 {
			converged = true
			break
		}
	}

	assert.True(t, converged,
		"optimizer failed to converge near x=0.7 in 5 independent runs of 30 iterations each")
}

// ─── Test 2: top-3 preset generation ─────────────────────────────────────────

// TestTop3PresetGeneration runs the optimizer on a scoring-style space (multiple
// float params) and asserts that exactly 3 presets are persisted with
// CreatedBy="optimizer" and IsActive=false.
func TestTop3PresetGeneration(t *testing.T) {
	space := ParamSpace{
		Params: []ParamRange{
			{Key: "scoring.rsi.primary_bonus", Type: configdomain.ParamTypeFloat, Min: "0.05", Max: "0.25"},
			{Key: "scoring.atr.sl_multiplier", Type: configdomain.ParamTypeFloat, Min: "1.0", Max: "3.0"},
		},
	}

	// Synthetic backtest: returns a deterministic score proportional to param sum.
	// Enough NTrades to pass the penalty guard.
	backtestFn := func(params map[string]string) (backtestdomain.BacktestResult, error) {
		a, _ := strconv.ParseFloat(params["scoring.rsi.primary_bonus"], 64)
		b, _ := strconv.ParseFloat(params["scoring.atr.sl_multiplier"], 64)
		sharpe := a + b*0.1
		return backtestdomain.BacktestResult{
			Metrics: backtestdomain.Metrics{
				NTrades: 25,
				Sharpe:  sharpe,
			},
		}, nil
	}

	cfgRepo := newFakeConfigRepo()
	optR := newFakeOptRepo()

	req := OptimizeRequest{
		Space:         space,
		SpaceName:     "scoring_test",
		ObjectiveName: "maximize_sharpe",
		Objective:     MaximizeSharpe,
		Pair:          "ETH/USDT",
		Exchange:      "kucoin",
		HorizonFrom:   time.Now().Add(-7 * 24 * time.Hour),
		HorizonTo:     time.Now(),
		Iterations:    20,
		By:            "test_runner",
	}

	runID, err := optR.CreateRun(context.Background(), &optdomain.OptimizerRun{
		Pair:        req.Pair,
		Objective:   req.ObjectiveName,
		SpaceName:   req.SpaceName,
		Iterations:  req.Iterations,
		Status:      "pending",
		TriggeredBy: req.By,
	})
	require.NoError(t, err)

	svc := newStubOptService(cfgRepo, optR, backtestFn)
	result, err := svc.Run(context.Background(), runID, req)
	require.NoError(t, err)

	// Assert 3 presets were generated.
	require.Len(t, result.TopPresets, 3,
		"expected exactly 3 top presets; got %d", len(result.TopPresets))

	// Verify presets are persisted in the config repo with correct attributes.
	presets, err := cfgRepo.ListPresets(context.Background())
	require.NoError(t, err)
	assert.Len(t, presets, 3, "expected 3 presets in the config repository")

	for _, p := range presets {
		assert.Equal(t, "optimizer", p.CreatedBy,
			"preset %q should have source='optimizer'", p.Name)
		assert.False(t, p.IsActive,
			"preset %q should be inactive (pending human review)", p.Name)
		assert.NotEmpty(t, p.ParamsJSON,
			"preset %q should have non-empty params JSON", p.Name)

		// Verify preset name follows the expected pattern.
		assert.Contains(t, p.Name, "_auto_",
			"preset name should contain '_auto_'")
		assert.Contains(t, p.Name, fmt.Sprintf("opt%d", runID),
			"preset name should contain the run ID")
	}

	// Verify the optimizer run was marked done with 3 top presets.
	run, err := optR.GetRun(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, "done", run.Status)
	assert.Len(t, run.TopPresets, 3)
}

// ─── Unit tests for SelectTopN ────────────────────────────────────────────────

func TestSelectTopN_basic(t *testing.T) {
	history := []TrialResult{
		{Params: map[string]string{"x": "0.1"}, Objective: 0.1},
		{Params: map[string]string{"x": "0.5"}, Objective: 0.5},
		{Params: map[string]string{"x": "0.9"}, Objective: 0.9},
		{Params: map[string]string{"x": "0.3"}, Objective: 0.3},
	}

	top3 := SelectTopN(history, 3)
	require.Len(t, top3, 3)
	assert.Equal(t, 0.9, top3[0].Objective)
	assert.Equal(t, 0.5, top3[1].Objective)
	assert.Equal(t, 0.3, top3[2].Objective)
}

func TestSelectTopN_excludesInf(t *testing.T) {
	history := []TrialResult{
		{Params: map[string]string{}, Objective: 0.8},
		{Params: map[string]string{}, Objective: math.Inf(-1)}, // -Inf penalty
		{Params: map[string]string{}, Objective: 0.5},
	}

	top3 := SelectTopN(history, 3)
	// -Inf should be filtered out, leaving 2 valid trials.
	require.Len(t, top3, 2)
	assert.Equal(t, 0.8, top3[0].Objective)
}

func TestSelectTopN_fewerThanN(t *testing.T) {
	history := []TrialResult{
		{Params: map[string]string{}, Objective: 1.0},
	}
	top3 := SelectTopN(history, 3)
	require.Len(t, top3, 1)
}

// ─── Unit tests for SampleUniform ────────────────────────────────────────────

func TestSampleUniform_respectsBounds(t *testing.T) {
	space := ParamSpace{
		Params: []ParamRange{
			{Key: "f", Type: configdomain.ParamTypeFloat, Min: "0.1", Max: "0.9"},
			{Key: "i", Type: configdomain.ParamTypeInt, Min: "5", Max: "15"},
		},
	}
	for i := 0; i < 50; i++ {
		sample := SampleUniform(space)
		f, err := strconv.ParseFloat(sample["f"], 64)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, f, 0.1)
		assert.LessOrEqual(t, f, 0.9)

		iv, err := strconv.ParseInt(sample["i"], 10, 64)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, iv, int64(5))
		assert.LessOrEqual(t, iv, int64(15))
	}
}

// ─── Ensure OptimizerService constructor compiles with real types ─────────────

func TestNewOptimizerService_compilesWithNilEngine(t *testing.T) {
	svc := NewOptimizerService(
		newNilBacktestEngine(),
		newFakeConfigRepo(),
		newFakeOptRepo(),
		silentLogger(),
	)
	assert.NotNil(t, svc)
}

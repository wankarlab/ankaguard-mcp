// Package gate computes the edge-gate verdict of the shadow phase: per
// strategy version and mode, over CLOSED positions with entry_channel in
// {auto, manual}. Counterfactual positions never count (they feed only the
// human-lift report). DROPPED entries count as closed trades whose net PnL is
// minus their fees (the fee was really lost). Spec: launch-shadow-simulation
// ("Compuerta de edge"), design D7.
package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"time"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

// Verdicts.
const (
	VerdictInsufficient = "INSUFFICIENT_DATA"
	VerdictNoEdge       = "NO_EDGE"
	VerdictEdge         = "EDGE"
)

// Requirements are launch.gate.min_trades / min_shadow_weeks / max_fraud_buys.
type Requirements struct {
	MinTrades      int     `json:"min_trades"`
	MinShadowWeeks float64 `json:"min_shadow_weeks"`
	MaxFraudBuys   int     `json:"max_fraud_buys"`
}

// Config is the gate configuration (launch.gate.*).
type Config struct {
	Requirements
	CILevel   float64
	Resamples int // 0 = 10 000
}

// Evaluation is one persisted gate result; JSON matches GateEvaluation in
// docs/bot/launch-api-phase1.md.
type Evaluation struct {
	ID                int64        `json:"id"`
	StrategyVersionID int64        `json:"strategy_version_id"`
	Mode              string       `json:"mode"`
	Verdict           string       `json:"verdict"`
	Trades            int          `json:"trades"`
	ShadowWeeks       float64      `json:"shadow_weeks"`
	NetPnLUSD         float64      `json:"net_pnl_usd"`
	MeanPnLUSD        *float64     `json:"mean_pnl_usd"`
	CILow             *float64     `json:"ci_low"`
	CIHigh            *float64     `json:"ci_high"`
	CILevel           float64      `json:"ci_level"`
	ProfitFactor      *float64     `json:"profit_factor"`
	MaxDrawdownUSD    float64      `json:"max_drawdown_usd"`
	FraudBuys         int          `json:"fraud_buys"`
	Requirements      Requirements `json:"requirements"`
	Detail            string       `json:"detail"`
	EvaluatedAt       time.Time    `json:"evaluated_at"`
}

// Group is the input of one evaluation.
type Group struct {
	StrategyVersionID int64
	Mode              string
	Trades            []Trade   // closed / dropped auto+manual positions
	FirstOpenedAt     time.Time // earliest opened_at of any auto+manual position (zero if none)
}

// Compute produces the evaluation of one group. rng drives the bootstrap.
func Compute(g Group, cfg Config, now time.Time, rng *rand.Rand) Evaluation {
	trades := append([]Trade(nil), g.Trades...)
	sort.SliceStable(trades, func(i, j int) bool { return trades[i].ClosedAt.Before(trades[j].ClosedAt) })
	ev := Evaluation{StrategyVersionID: g.StrategyVersionID, Mode: g.Mode, Trades: len(trades),
		CILevel: cfg.CILevel, Requirements: cfg.Requirements, EvaluatedAt: now}
	nets := make([]float64, len(trades))
	for i, t := range trades {
		nets[i] = t.NetPnLUSD
		ev.NetPnLUSD += t.NetPnLUSD
		if t.Fraud {
			ev.FraudBuys++
		}
	}
	if !g.FirstOpenedAt.IsZero() && now.After(g.FirstOpenedAt) {
		ev.ShadowWeeks = now.Sub(g.FirstOpenedAt).Hours() / (24 * 7)
	}
	if n := len(trades); n > 0 {
		m := ev.NetPnLUSD / float64(n)
		ev.MeanPnLUSD = &m
	}
	ev.ProfitFactor = profitFactor(nets)
	ev.MaxDrawdownUSD = maxDrawdown(trades)
	res := cfg.Resamples
	if res <= 0 {
		res = 10000
	}
	haveCI := false
	if lo, hi, ok := Bootstrap(nets, cfg.CILevel, res, rng); ok {
		ev.CILow, ev.CIHigh, haveCI = &lo, &hi, true
	}

	needTrades := cfg.MinTrades - ev.Trades
	needWeeks := cfg.MinShadowWeeks - ev.ShadowWeeks
	switch {
	case needTrades > 0 || needWeeks > 0:
		ev.Verdict = VerdictInsufficient
		ev.Detail = "needs"
		sep := " "
		if needTrades > 0 {
			ev.Detail += fmt.Sprintf("%s%d more trades", sep, needTrades)
			sep = " and "
		}
		if needWeeks > 0 {
			ev.Detail += fmt.Sprintf("%s%.1f more weeks", sep, needWeeks)
		}
	case ev.FraudBuys > cfg.MaxFraudBuys:
		ev.Verdict = VerdictNoEdge
		ev.Detail = fmt.Sprintf("fraud_buys %d > max %d", ev.FraudBuys, cfg.MaxFraudBuys)
	case !haveCI || *ev.CILow <= 0:
		ev.Verdict = VerdictNoEdge
		if haveCI {
			ev.Detail = fmt.Sprintf("%.0f%% CI of mean net PnL [%.4f, %.4f] includes zero", cfg.CILevel*100, *ev.CILow, *ev.CIHigh)
		} else {
			ev.Detail = "no confidence interval available"
		}
	default:
		ev.Verdict = VerdictEdge
		ev.Detail = fmt.Sprintf("%.0f%% CI of mean net PnL [%.4f, %.4f] excludes zero", cfg.CILevel*100, *ev.CILow, *ev.CIHigh)
	}
	return ev
}

// Loader supplies the groups to evaluate.
type Loader interface {
	Groups(ctx context.Context) ([]Group, error)
}

// Eligible is the channel rule of the gate: auto and manual only.
func Eligible(ch core.EntryChannel) bool {
	return ch == core.ChannelAuto || ch == core.ChannelManual
}

// Evaluator runs the gate job.
type Evaluator struct {
	db     core.DB
	loader Loader
	cfg    func() Config
	now    func() time.Time
	seed   func() int64
	log    *slog.Logger
}

// NewEvaluator returns an Evaluator over db with the Postgres loader. cfg is
// read at every run (hot reload). now and seed may be nil (time.Now, clock
// based seed); tests inject them. The seed used is stored in the inputs JSON.
func NewEvaluator(db core.DB, cfg func() Config, now func() time.Time, seed func() int64, log *slog.Logger) *Evaluator {
	return NewEvaluatorWithLoader(db, &pgLoader{db: db}, cfg, now, seed, log)
}

// NewEvaluatorWithLoader is NewEvaluator with a custom Loader.
func NewEvaluatorWithLoader(db core.DB, l Loader, cfg func() Config, now func() time.Time, seed func() int64, log *slog.Logger) *Evaluator {
	if now == nil {
		now = time.Now
	}
	if seed == nil {
		seed = func() int64 { return time.Now().UnixNano() }
	}
	if log == nil {
		log = slog.Default()
	}
	return &Evaluator{db: db, loader: l, cfg: cfg, now: now, seed: seed, log: log}
}

// Evaluate computes and persists one evaluation per (strategy version, mode)
// and returns them.
func (e *Evaluator) Evaluate(ctx context.Context) ([]Evaluation, error) {
	groups, err := e.loader.Groups(ctx)
	if err != nil {
		return nil, fmt.Errorf("gate: load: %w", err)
	}
	cfg := e.cfg()
	now := e.now()
	var out []Evaluation
	for _, g := range groups {
		seed := e.seed()
		ev := Compute(g, cfg, now, rand.New(rand.NewSource(seed)))
		if err := e.persist(ctx, &ev, seed); err != nil {
			return out, err
		}
		out = append(out, ev)
		e.log.Info("gate evaluated", "version", ev.StrategyVersionID, "mode", ev.Mode, "verdict", ev.Verdict,
			"trades", ev.Trades, "detail", ev.Detail)
	}
	return out, nil
}

// RunEvery evaluates immediately and then every interval() until ctx ends
// (launch.gate.eval_interval_h, re-read each cycle).
func (e *Evaluator) RunEvery(ctx context.Context, interval func() time.Duration) error {
	for {
		if _, err := e.Evaluate(ctx); err != nil && ctx.Err() == nil {
			e.log.Error("gate evaluation failed", "err", err)
		}
		d := interval()
		if d <= 0 {
			d = 6 * time.Hour
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (e *Evaluator) persist(ctx context.Context, ev *Evaluation, seed int64) error {
	inputs, err := json.Marshal(map[string]any{
		"requirements": ev.Requirements, "ci_level": ev.CILevel, "resamples": resamplesOf(e.cfg()), "seed": seed,
		"channels": []string{string(core.ChannelAuto), string(core.ChannelManual)},
	})
	if err != nil {
		return err
	}
	err = e.db.QueryRow(ctx, `
		INSERT INTO launch.gate_evaluations
		 (strategy_version_id, mode, verdict, trades, shadow_weeks, net_pnl_usd, mean_pnl_usd, ci_low, ci_high, ci_level,
		  profit_factor, max_drawdown_usd, fraud_buys, inputs, detail, evaluated_at)
		VALUES (NULLIF($1::bigint,0),$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id`,
		ev.StrategyVersionID, ev.Mode, ev.Verdict, ev.Trades, ev.ShadowWeeks, ev.NetPnLUSD, ev.MeanPnLUSD, ev.CILow, ev.CIHigh,
		ev.CILevel, ev.ProfitFactor, ev.MaxDrawdownUSD, ev.FraudBuys, inputs, ev.Detail, ev.EvaluatedAt).Scan(&ev.ID)
	if err != nil {
		return fmt.Errorf("gate: persist: %w", err)
	}
	return nil
}

func resamplesOf(c Config) int {
	if c.Resamples <= 0 {
		return 10000
	}
	return c.Resamples
}

const evalCols = `id, COALESCE(strategy_version_id,0), mode, verdict, trades, shadow_weeks::float8, net_pnl_usd::float8,
	mean_pnl_usd::float8, ci_low::float8, ci_high::float8, ci_level::float8, profit_factor::float8, max_drawdown_usd::float8,
	fraud_buys, inputs, COALESCE(detail,''), evaluated_at`

func scanEval(row interface{ Scan(...any) error }) (Evaluation, error) {
	var ev Evaluation
	var maxDD *float64
	var inputs []byte
	if err := row.Scan(&ev.ID, &ev.StrategyVersionID, &ev.Mode, &ev.Verdict, &ev.Trades, &ev.ShadowWeeks, &ev.NetPnLUSD,
		&ev.MeanPnLUSD, &ev.CILow, &ev.CIHigh, &ev.CILevel, &ev.ProfitFactor, &maxDD, &ev.FraudBuys, &inputs, &ev.Detail,
		&ev.EvaluatedAt); err != nil {
		return ev, err
	}
	if maxDD != nil {
		ev.MaxDrawdownUSD = *maxDD
	}
	var in struct {
		Requirements Requirements `json:"requirements"`
	}
	_ = json.Unmarshal(inputs, &in)
	ev.Requirements = in.Requirements
	return ev, nil
}

// Latest returns the most recent evaluation, or nil when none exists.
func Latest(ctx context.Context, db core.DB) (*Evaluation, error) {
	rows, err := History(ctx, db, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// History returns up to limit evaluations, newest first (limit <= 0 = 20).
func History(ctx context.Context, db core.DB, limit int) ([]Evaluation, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(ctx, `SELECT `+evalCols+` FROM launch.gate_evaluations ORDER BY evaluated_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("gate history: %w", err)
	}
	defer rows.Close()
	out := []Evaluation{}
	for rows.Next() {
		ev, err := scanEval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

type pgLoader struct{ db core.DB }

func (l *pgLoader) Groups(ctx context.Context) ([]Group, error) {
	rows, err := l.db.Query(ctx, `
		SELECT strategy_version_id, mode, state, COALESCE(net_pnl_usd,0)::float8, opened_at, closed_at, fraud_flag
		FROM launch.positions
		WHERE entry_channel IN ('auto','manual') AND mode IN ('shadow','simulated') AND strategy_version_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	type key struct {
		v    int64
		mode string
	}
	idx := map[key]*Group{}
	var order []key
	get := func(k key) *Group {
		g, ok := idx[k]
		if !ok {
			g = &Group{StrategyVersionID: k.v, Mode: k.mode}
			idx[k] = g
			order = append(order, k)
		}
		return g
	}
	for rows.Next() {
		var v int64
		var mode, state string
		var net float64
		var opened time.Time
		var closed *time.Time
		var fraud bool
		if err := rows.Scan(&v, &mode, &state, &net, &opened, &closed, &fraud); err != nil {
			rows.Close()
			return nil, err
		}
		g := get(key{v, mode})
		if g.FirstOpenedAt.IsZero() || opened.Before(g.FirstOpenedAt) {
			g.FirstOpenedAt = opened
		}
		if closed != nil && (state == core.PosStateClosed || state == core.PosStateDropped) {
			g.Trades = append(g.Trades, Trade{NetPnLUSD: net, ClosedAt: *closed, Fraud: fraud})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Active strategy versions always get an evaluation, even with no trades.
	vr, err := l.db.Query(ctx, `
		SELECT DISTINCT ON (s.id) v.id FROM launch.strategies s
		JOIN launch.strategy_versions v ON v.strategy_id = s.id
		WHERE s.status = 'simulation' ORDER BY s.id, v.version DESC`)
	if err != nil {
		return nil, err
	}
	for vr.Next() {
		var id int64
		if err := vr.Scan(&id); err != nil {
			vr.Close()
			return nil, err
		}
		get(key{id, core.ModeShadow})
	}
	vr.Close()
	if err := vr.Err(); err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(order))
	for _, k := range order {
		out = append(out, *idx[k])
	}
	return out, nil
}

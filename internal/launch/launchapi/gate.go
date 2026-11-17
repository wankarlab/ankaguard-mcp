package launchapi

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/launch/core"
	"github.com/rantipay/trading-mcp/internal/launch/gate"
)

const gateSelect = `
SELECT id, strategy_version_id, mode, verdict, trades, shadow_weeks::float8, net_pnl_usd::float8, mean_pnl_usd::float8,
       ci_low::float8, ci_high::float8, ci_level::float8, profit_factor::float8, max_drawdown_usd::float8,
       fraud_buys, COALESCE(detail, ''), evaluated_at
FROM launch.gate_evaluations`

func scanGate(rows pgx.Rows, req GateRequirements) ([]GateEvaluation, error) {
	defer rows.Close()
	out := []GateEvaluation{}
	for rows.Next() {
		var g GateEvaluation
		if err := rows.Scan(&g.ID, &g.StrategyVersionID, &g.Mode, &g.Verdict, &g.Trades, &g.ShadowWeeks, &g.NetPnLUSD,
			&g.MeanPnLUSD, &g.CILow, &g.CIHigh, &g.CILevel, &g.ProfitFactor, &g.MaxDrawdownUSD, &g.FraudBuys,
			&g.Detail, &g.EvaluatedAt); err != nil {
			return nil, err
		}
		g.EvaluatedAt = g.EvaluatedAt.UTC()
		g.Requirements = req
		out = append(out, g)
	}
	return out, rows.Err()
}

// GateLatest returns the newest gate evaluation, or nil when none exists.
func GateLatest(ctx context.Context, db core.DB, req GateRequirements) (*GateEvaluation, error) {
	rows, err := db.Query(ctx, gateSelect+" ORDER BY evaluated_at DESC, id DESC LIMIT 1")
	if err != nil {
		return nil, err
	}
	items, err := scanGate(rows, req)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

// GateHistory returns the newest evaluations first.
func GateHistory(ctx context.Context, db core.DB, req GateRequirements, limit int) ([]GateEvaluation, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	rows, err := db.Query(ctx, gateSelect+" ORDER BY evaluated_at DESC, id DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	return scanGate(rows, req)
}

// Gate returns latest + history.
func Gate(ctx context.Context, db core.DB, req GateRequirements, limit int) (GateOverview, error) {
	hist, err := GateHistory(ctx, db, req, limit)
	if err != nil {
		return GateOverview{}, err
	}
	ov := GateOverview{History: hist}
	if len(hist) > 0 {
		ov.Latest = &hist[0]
	}
	return ov, nil
}

// HumanLift compares manual approvals against the counterfactual (rejected or
// expired) outcomes over closed shadow positions.
func HumanLift(ctx context.Context, db core.DB, minTrades int) (HumanLiftReport, error) {
	// Single source of truth: the gate package computes the report the bot
	// and the gate use; convert it to the contract type through JSON (same tags).
	rep, err := gate.HumanLift(ctx, db, minTrades)
	if err != nil {
		return HumanLiftReport{}, err
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		return HumanLiftReport{}, err
	}
	var out HumanLiftReport
	if err := json.Unmarshal(raw, &out); err != nil {
		return HumanLiftReport{}, err
	}
	return out, nil
}

// RequirementsFromStore reads the gate thresholds from the dynamic config
// store, falling back to the manifest defaults when the store is nil.
func RequirementsFromStore(store *dynamic.ConfigStore) RequirementsFunc {
	return func() GateRequirements {
		req := DefaultRequirements()
		if store == nil {
			return req
		}
		snap := store.Snapshot()
		if snap == nil {
			return req
		}
		s := snap.ToScopedSnapshot()
		if v, ok := s.IntOk("launch.gate.min_trades"); ok && v > 0 {
			req.MinTrades = v
		}
		if v, ok := s.IntOk("launch.gate.min_shadow_weeks"); ok && v > 0 {
			req.MinShadowWeeks = v
		}
		if v, ok := s.IntOk("launch.gate.max_fraud_buys"); ok && v >= 0 {
			req.MaxFraudBuys = v
		}
		return req
	}
}

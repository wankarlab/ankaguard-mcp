package gate

import (
	"context"
	"fmt"
	"sort"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

// ChannelStats aggregates closed positions of one channel.
type ChannelStats struct {
	Trades       int      `json:"trades"`
	NetPnLUSD    float64  `json:"net_pnl_usd"`
	WinRate      float64  `json:"win_rate"`
	ProfitFactor *float64 `json:"profit_factor"`
}

// Channels groups the stats of the three entry channels.
type Channels struct {
	Manual         ChannelStats `json:"manual"`
	Counterfactual ChannelStats `json:"counterfactual"`
	Auto           ChannelStats `json:"auto"`
}

// CategoryLift compares manual and counterfactual for one reason category.
type CategoryLift struct {
	Category       string       `json:"category"`
	Manual         ChannelStats `json:"manual"`
	Counterfactual ChannelStats `json:"counterfactual"`
}

// HumanLiftReport is GET /reports/human-lift (docs/bot/launch-api-phase1.md).
type HumanLiftReport struct {
	Status                          string         `json:"status"` // INSUFFICIENT_DATA | OK
	MinTrades                       int            `json:"min_trades"`
	Channels                        Channels       `json:"channels"`
	ByCategory                      []CategoryLift `json:"by_category"`
	ManualMinusCounterfactualNetUSD *float64       `json:"manual_minus_counterfactual_net_usd"`
}

// LiftRow is one closed position as the report sees it.
type LiftRow struct {
	Channel  core.EntryChannel
	Category string // decision category, "none" when the decision has none
	NetUSD   float64
}

func stats(nets []float64) ChannelStats {
	s := ChannelStats{Trades: len(nets)}
	wins := 0
	for _, v := range nets {
		s.NetPnLUSD += v
		if v > 0 {
			wins++
		}
	}
	if len(nets) > 0 {
		s.WinRate = float64(wins) / float64(len(nets))
	}
	s.ProfitFactor = profitFactor(nets)
	return s
}

// BuildHumanLift aggregates rows (pure; used by HumanLift and by tests).
func BuildHumanLift(rows []LiftRow, minTrades int) HumanLiftReport {
	by := map[core.EntryChannel][]float64{}
	cat := map[string]map[core.EntryChannel][]float64{}
	for _, r := range rows {
		by[r.Channel] = append(by[r.Channel], r.NetUSD)
		if r.Channel == core.ChannelManual || r.Channel == core.ChannelCounterfactual {
			if cat[r.Category] == nil {
				cat[r.Category] = map[core.EntryChannel][]float64{}
			}
			cat[r.Category][r.Channel] = append(cat[r.Category][r.Channel], r.NetUSD)
		}
	}
	rep := HumanLiftReport{MinTrades: minTrades, ByCategory: []CategoryLift{},
		Channels: Channels{Manual: stats(by[core.ChannelManual]), Counterfactual: stats(by[core.ChannelCounterfactual]), Auto: stats(by[core.ChannelAuto])}}
	names := make([]string, 0, len(cat))
	for c := range cat {
		names = append(names, c)
	}
	sort.Strings(names)
	for _, c := range names {
		rep.ByCategory = append(rep.ByCategory, CategoryLift{Category: c,
			Manual: stats(cat[c][core.ChannelManual]), Counterfactual: stats(cat[c][core.ChannelCounterfactual])})
	}
	if rep.Channels.Manual.Trades < minTrades {
		rep.Status = VerdictInsufficient
		return rep
	}
	rep.Status = "OK"
	d := rep.Channels.Manual.NetPnLUSD - rep.Channels.Counterfactual.NetPnLUSD
	rep.ManualMinusCounterfactualNetUSD = &d
	return rep
}

// HumanLift builds the human-lift report from closed shadow positions. A
// position counts once closed (CLOSED, or DROPPED with its fees as loss). The
// category comes from launch.decisions (counterfactual positions of expired
// opportunities have none -> "none").
func HumanLift(ctx context.Context, db core.DB, minTrades int) (HumanLiftReport, error) {
	rows, err := db.Query(ctx, `
		SELECT p.entry_channel, COALESCE(d.category,'none'), COALESCE(p.net_pnl_usd,0)::float8
		FROM launch.positions p
		LEFT JOIN launch.decisions d ON d.opportunity_id = p.opportunity_id
		WHERE p.closed_at IS NOT NULL AND p.mode IN ('shadow','simulated')`)
	if err != nil {
		return HumanLiftReport{}, fmt.Errorf("human lift: %w", err)
	}
	defer rows.Close()
	var lr []LiftRow
	for rows.Next() {
		var ch, cat string
		var net float64
		if err := rows.Scan(&ch, &cat, &net); err != nil {
			return HumanLiftReport{}, err
		}
		lr = append(lr, LiftRow{Channel: core.EntryChannel(ch), Category: cat, NetUSD: net})
	}
	if err := rows.Err(); err != nil {
		return HumanLiftReport{}, err
	}
	return BuildHumanLift(lr, minTrades), nil
}

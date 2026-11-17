package launchapi

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

const positionSelect = `
SELECT pos.id, pos.opportunity_id, COALESCE(t.address, ''), COALESCE(t.symbol, ''), pos.mode, pos.entry_channel, pos.state,
       COALESCE(pos.invested_usd, 0)::float8, COALESCE(pos.entry_price, 0)::float8, pos.size::float8, COALESCE(t.decimals, 0),
       pos.gross_pnl_usd::float8, pos.net_pnl_usd::float8, pos.costs, pos.exit_reason, pos.size_reason, pos.fraud_flag,
       pos.strategy_version_id, pos.opened_at, pos.closed_at
FROM launch.positions pos
LEFT JOIN launch.opportunities o ON o.id = pos.opportunity_id
LEFT JOIN launch.tokens t ON t.id = o.token_id`

func scanPositions(rows pgx.Rows) ([]Position, error) {
	defer rows.Close()
	out := []Position{}
	for rows.Next() {
		var p Position
		var size float64
		var decimals int16
		var costsRaw []byte
		if err := rows.Scan(&p.ID, &p.OpportunityID, &p.Mint, &p.Symbol, &p.Mode, &p.EntryChannel, &p.State,
			&p.InvestedUSD, &p.EntryPriceUSD, &size, &decimals, &p.GrossPnLUSD, &p.NetPnLUSD, &costsRaw,
			&p.ExitReason, &p.SizeReason, &p.FraudFlag, &p.StrategyVersionID, &p.OpenedAt, &p.ClosedAt); err != nil {
			return nil, err
		}
		// size is stored in native units; expose UI units.
		p.RemainingTokens = size / math.Pow10(int(decimals))
		if len(costsRaw) > 0 {
			_ = json.Unmarshal(costsRaw, &p.Costs)
			// The simulator keeps the live remaining size (after partial sells)
			// inside the costs JSON; prefer it over the initial size column.
			var extra struct {
				RemainingTokens *float64 `json:"remaining_tokens"`
			}
			if json.Unmarshal(costsRaw, &extra) == nil && extra.RemainingTokens != nil {
				p.RemainingTokens = *extra.RemainingTokens
			}
		}
		p.OpenedAt = p.OpenedAt.UTC()
		p.ClosedAt = utcPtr(p.ClosedAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPositions returns a page ordered by id descending.
func ListPositions(ctx context.Context, db core.DB, f PositionFilter) (PositionPage, error) {
	limit := clampLimit(f.Limit)
	var w where
	if f.Channel != "" {
		w.add("pos.entry_channel = ?", f.Channel)
	}
	if f.State != "" {
		w.add("pos.state = ?", f.State)
	}
	if f.BeforeID > 0 {
		w.add("pos.id < ?", f.BeforeID)
	}
	w.args = append(w.args, limit+1)
	rows, err := db.Query(ctx, positionSelect+w.clause()+" ORDER BY pos.id DESC LIMIT $"+strconv.Itoa(len(w.args)), w.args...)
	if err != nil {
		return PositionPage{}, err
	}
	items, err := scanPositions(rows)
	if err != nil {
		return PositionPage{}, err
	}
	page := PositionPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		next := items[limit-1].ID
		page.NextBeforeID = &next
	}
	return page, nil
}

// GetPosition returns one position with its orders, or ErrNotFound.
func GetPosition(ctx context.Context, db core.DB, id int64) (PositionDetail, error) {
	rows, err := db.Query(ctx, positionSelect+" WHERE pos.id = $1", id)
	if err != nil {
		return PositionDetail{}, err
	}
	items, err := scanPositions(rows)
	if err != nil {
		return PositionDetail{}, err
	}
	if len(items) == 0 {
		return PositionDetail{}, ErrNotFound
	}
	detail := PositionDetail{Position: items[0], Orders: []OrderView{}}
	orows, err := db.Query(ctx,
		`SELECT id, side, status, tx_signature, quote_price::float8, fill_price::float8,
		        expected_slippage::float8, actual_slippage::float8, created_at
		 FROM launch.orders WHERE position_id = $1 ORDER BY id`, id)
	if err != nil {
		return PositionDetail{}, err
	}
	defer orows.Close()
	for orows.Next() {
		var o OrderView
		if err := orows.Scan(&o.ID, &o.Side, &o.Status, &o.TxSignature, &o.QuotePrice, &o.FillPrice,
			&o.ExpectedSlippage, &o.ActualSlippage, &o.CreatedAt); err != nil {
			return PositionDetail{}, err
		}
		o.CreatedAt = o.CreatedAt.UTC()
		detail.Orders = append(detail.Orders, o)
	}
	return detail, orows.Err()
}

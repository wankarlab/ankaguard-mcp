package launchapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

// ErrNotFound is returned when the requested row does not exist.
var ErrNotFound = errors.New("launchapi: not found")

const opportunitySelect = `
SELECT o.id, COALESCE(t.address, ''), COALESCE(t.symbol, ''), COALESCE(p.pool_address, ''), COALESCE(p.dex, ''),
       COALESCE(o.source_kind, p.source_kind, ''), o.graduated, COALESCE(o.screen_result, ''), o.reject_reason,
       o.score::float8, o.band, o.status, o.expires_at, o.created_at, o.thesis, o.market_snapshot,
       d.action, d.category, d.comment, d.actor, d.created_at,
       (SELECT pos.id FROM launch.positions pos WHERE pos.opportunity_id = o.id ORDER BY pos.id LIMIT 1)
FROM launch.opportunities o
LEFT JOIN launch.tokens t ON t.id = o.token_id
LEFT JOIN launch.pools p ON p.id = o.pool_id
LEFT JOIN launch.decisions d ON d.opportunity_id = o.id`

func scanOpportunities(rows pgx.Rows) ([]Opportunity, error) {
	defer rows.Close()
	out := []Opportunity{}
	for rows.Next() {
		var o Opportunity
		var thesisRaw, snapRaw []byte
		var dAction, dActor *string
		var dCat, dComment *string
		var dAt *time.Time
		if err := rows.Scan(&o.ID, &o.Mint, &o.Symbol, &o.Pool, &o.Dex, &o.SourceKind, &o.Graduated,
			&o.ScreenResult, &o.RejectReason, &o.Score, &o.Band, &o.Status, &o.ExpiresAt, &o.CreatedAt,
			&thesisRaw, &snapRaw, &dAction, &dCat, &dComment, &dActor, &dAt, &o.PositionID); err != nil {
			return nil, err
		}
		o.ExpiresAt = utcPtr(o.ExpiresAt)
		o.CreatedAt = o.CreatedAt.UTC()
		o.Breakdown = []core.ScoreFactor{}
		o.Snapshot = json.RawMessage("{}")
		if len(snapRaw) > 0 {
			o.Snapshot = snapRaw
		}
		o.Thesis = Thesis{For: []string{}, Against: []string{}}
		if len(thesisRaw) > 0 {
			_ = json.Unmarshal(thesisRaw, &o.Thesis)
		}
		if o.Thesis.For == nil {
			o.Thesis.For = []string{}
		}
		if o.Thesis.Against == nil {
			o.Thesis.Against = []string{}
		}
		if dAction != nil && dAt != nil {
			o.Decision = &DecisionView{Action: *dAction, Category: dCat, Comment: dComment, CreatedAt: dAt.UTC()}
			if dActor != nil {
				o.Decision.Actor = *dActor
			}
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// attachBreakdowns loads the score breakdown of the given opportunities.
func attachBreakdowns(ctx context.Context, db core.DB, items []Opportunity) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	idx := make(map[int64]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
		idx[it.ID] = i
	}
	rows, err := db.Query(ctx,
		`SELECT opportunity_id, factor, weight::float8, value, COALESCE(result, 0)::float8, COALESCE(confidence, 0)::float8
		 FROM launch.score_breakdowns WHERE opportunity_id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var oppID int64
		var f core.ScoreFactor
		var value *string
		if err := rows.Scan(&oppID, &f.Factor, &f.Weight, &value, &f.Normalized, &f.Confidence); err != nil {
			return err
		}
		if value != nil {
			f.Raw, _ = strconv.ParseFloat(*value, 64)
		}
		// screening stores result = normalized factor (0..1) and weight = weight
		// normalised by the sum of weights, so points = normalized × weight × 100.
		f.Points = f.Normalized * f.Weight * 100
		i := idx[oppID]
		items[i].Breakdown = append(items[i].Breakdown, f)
	}
	return rows.Err()
}

// ListOpportunities returns a page ordered by id descending.
func ListOpportunities(ctx context.Context, db core.DB, f OpportunityFilter) (OpportunityPage, error) {
	limit := clampLimit(f.Limit)
	var w where
	if f.Band != "" {
		w.add("o.band = ?", f.Band)
	}
	if f.Status != "" {
		w.add("o.status = ?", f.Status)
	}
	if f.BeforeID > 0 {
		w.add("o.id < ?", f.BeforeID)
	}
	w.args = append(w.args, limit+1)
	q := opportunitySelect + w.clause() + " ORDER BY o.id DESC LIMIT $" + strconv.Itoa(len(w.args))
	rows, err := db.Query(ctx, q, w.args...)
	if err != nil {
		return OpportunityPage{}, err
	}
	items, err := scanOpportunities(rows)
	if err != nil {
		return OpportunityPage{}, err
	}
	page := OpportunityPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		next := items[limit-1].ID
		page.NextBeforeID = &next
	}
	if err := attachBreakdowns(ctx, db, page.Items); err != nil {
		return OpportunityPage{}, err
	}
	return page, nil
}

// GetOpportunity returns one opportunity or ErrNotFound.
func GetOpportunity(ctx context.Context, db core.DB, id int64) (Opportunity, error) {
	rows, err := db.Query(ctx, opportunitySelect+" WHERE o.id = $1", id)
	if err != nil {
		return Opportunity{}, err
	}
	items, err := scanOpportunities(rows)
	if err != nil {
		return Opportunity{}, err
	}
	if len(items) == 0 {
		return Opportunity{}, ErrNotFound
	}
	if err := attachBreakdowns(ctx, db, items); err != nil {
		return Opportunity{}, err
	}
	return items[0], nil
}

// ListApprovals returns the live manual-approval queue (not expired), soonest
// expiry first.
func ListApprovals(ctx context.Context, db core.DB) ([]Opportunity, error) {
	rows, err := db.Query(ctx, opportunitySelect+
		" WHERE o.status = 'PENDING_APPROVAL' AND o.expires_at > now() ORDER BY o.expires_at ASC LIMIT 200")
	if err != nil {
		return nil, err
	}
	items, err := scanOpportunities(rows)
	if err != nil {
		return nil, err
	}
	if err := attachBreakdowns(ctx, db, items); err != nil {
		return nil, err
	}
	return items, nil
}

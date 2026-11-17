package paper

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// valued is a position with its real mark price. Mark is 0 when no recent
// price could be obtained (the position is then reported without valuation).
type valued struct {
	pos  *position
	mark float64
	upnl float64
}

// snapshot is a consistent read of one holder's account plus real prices.
type snapshot struct {
	acct      *account
	reserved  float64
	positions []*valued
	margin    float64 // margin locked in open futures positions
	upnl      float64
	spotValue float64
	equity    float64
	stale     bool // at least one mark price was unavailable
}

func (c *Client) snapshot(ctx context.Context, holder string) (*snapshot, error) {
	b := c.b
	s := &snapshot{}
	var poss []*position
	storeMu.Lock()
	err := b.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if s.acct, err = b.getOrCreateAccount(ctx, tx, holder); err != nil {
			return err
		}
		if s.reserved, err = b.reservedFor(ctx, tx, holder, ""); err != nil {
			return err
		}
		poss, err = b.openPositions(ctx, tx, holder)
		return err
	})
	storeMu.Unlock()
	if err != nil {
		return nil, err
	}
	s.equity = s.acct.balance
	for _, p := range poss {
		v := &valued{pos: p}
		px, perr := b.price(ctx, p.exchange, lookupKey(p.market, p.pair, p.symbol), p.market == marketFutures)
		if perr != nil {
			s.stale = true
		} else {
			v.mark = px
		}
		if p.market == marketFutures {
			s.margin += p.margin
			s.equity += p.margin
			if v.mark > 0 {
				v.upnl = unrealized(p, v.mark)
				s.upnl += v.upnl
				s.equity += v.upnl
			}
		} else {
			mark := v.mark
			if mark <= 0 {
				mark = p.entry
			}
			v.upnl = (mark - p.entry) * p.size
			s.upnl += v.upnl
			s.spotValue += p.size * mark
			s.equity += p.size * mark
		}
		s.positions = append(s.positions, v)
	}
	return s, nil
}

func (b *Broker) listOrders(ctx context.Context, holder string, onlyOpen bool) ([]*orderRow, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	q := `SELECT ` + orderCols + ` FROM paper_orders WHERE holder = ?`
	if onlyOpen {
		q += ` AND status = 'open'`
	}
	q += ` ORDER BY created_at DESC, rowid DESC`
	rows, err := b.db.QueryContext(ctx, q, holder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*orderRow
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (b *Broker) getOrder(ctx context.Context, holder, id string) (*orderRow, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	o, err := scanOrder(b.db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM paper_orders WHERE id = ? AND holder = ?`, id, holder))
	if err == sql.ErrNoRows {
		return nil, newErr(CodeNotFound, "no paper order %q", id)
	}
	return o, err
}

// FillRecord is a row of the fill history.
type FillRecord struct {
	OrderID       string    `json:"order_id"`
	Pair          string    `json:"pair"`
	Market        string    `json:"market"`
	Side          string    `json:"side"`
	Qty           float64   `json:"qty"`
	Price         float64   `json:"price"`
	Fee           float64   `json:"fee"`
	RealizedPnL   float64   `json:"realized_pnl"`
	Liquidation   bool      `json:"liquidation"`
	ClosedByReset bool      `json:"closed_by_reset"`
	CreatedAt     time.Time `json:"created_at"`
	Paper         bool      `json:"paper"`
}

// Fills returns the holder's fill history, newest first (liquidations are
// flagged, rows from before an account reset are flagged closed_by_reset).
func (c *Client) Fills(ctx context.Context) ([]FillRecord, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	rows, err := c.b.db.QueryContext(ctx, `SELECT order_id, pair, market, side, qty, price, fee, realized_pnl, liquidation,
		closed_by_reset, created_at FROM paper_fills WHERE holder = ? ORDER BY id DESC`, holder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FillRecord
	for rows.Next() {
		var f FillRecord
		var liq, cbr int
		if err := rows.Scan(&f.OrderID, &f.Pair, &f.Market, &f.Side, &f.Qty, &f.Price, &f.Fee, &f.RealizedPnL, &liq, &cbr, &f.CreatedAt); err != nil {
			return nil, err
		}
		f.Liquidation, f.ClosedByReset, f.Paper = liq != 0, cbr != 0, true
		out = append(out, f)
	}
	return out, rows.Err()
}

// matchesOrderFilter implements the GetOrders orderType filter: "" or "all"
// match everything, "open"/"active" match pending orders, anything else matches
// the order type or status.
func matchesOrderFilter(o *orderRow, orderType string) bool {
	f := strings.ToLower(orderType)
	switch f {
	case "", "all":
		return true
	case "open", "active":
		return o.status == "open"
	}
	return o.typ == f || o.status == f
}

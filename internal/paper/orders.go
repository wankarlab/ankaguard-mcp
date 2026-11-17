package paper

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
)

func newOrderID() string {
	var buf [10]byte
	_, _ = rand.Read(buf[:])
	return "paper-" + hex.EncodeToString(buf[:])
}

// splitSpot returns the base asset of a USD-quoted spot pair.
func splitSpot(pair string) (base, quote string, ok bool) {
	p := strings.ToUpper(strings.NewReplacer("_", "-", "/", "-").Replace(pair))
	for _, q := range []string{"USDT", "USDC", "USD"} {
		if strings.HasSuffix(p, "-"+q) {
			return strings.TrimSuffix(p, "-"+q), q, true
		}
		if !strings.Contains(p, "-") && strings.HasSuffix(p, q) && len(p) > len(q) {
			return strings.TrimSuffix(p, q), q, true
		}
	}
	return "", "", false
}

// spec is a validated, normalized order.
type spec struct {
	req           gct.SubmitOrderRequest
	holder        string
	exchange      string
	market        string
	symbol        string
	side, typ     string
	reduce        bool
	futures       bool
	mult, qty     float64
	refPrice      float64
	lev           float64
	curPrice      float64
	stopDir       string
	slip, feeRate float64
}

// submit places a paper order. forceFutures is set by the futures entry points
// (SubmitFuturesOrder), which always take the futures route.
func (c *Client) submit(ctx context.Context, in *gct.SubmitOrderRequest, forceFutures bool) (*gct.Order, error) {
	b := c.b
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, newErr(CodeRejected, "order request is nil")
	}
	s := &spec{req: *in, holder: holder}
	s.exchange = s.req.Exchange
	if s.exchange == "" {
		s.exchange = defaultExchange
	}
	s.side = strings.ToLower(s.req.Side)
	s.typ = strings.ToLower(s.req.Type)
	s.futures = forceFutures || s.req.Leverage > 0 || s.req.MarketType == gct.MarketTypeFutures
	if s.futures && s.req.Leverage <= 0 {
		s.req.Leverage = 1
	}
	s.reduce = s.req.ReduceOnly || s.req.CloseOrder

	// ── validation: anything the simulator cannot model is PAPER_UNSUPPORTED ──
	if s.req.MarketType == gct.MarketTypeMargin || s.req.MarketType == gct.MarketTypeOptions {
		return nil, newErr(CodeUnsupported, "market type %q is not supported by the paper broker", s.req.MarketType)
	}
	if s.side != "buy" && s.side != "sell" {
		return nil, newErr(CodeRejected, "invalid order side %q", s.req.Side)
	}
	switch s.typ {
	case "market", "limit", "stop":
	default:
		return nil, newErr(CodeUnsupported, "order type %q is not supported by the paper broker (market, limit and stop only)", s.req.Type)
	}
	if s.req.Amount <= 0 || math.IsNaN(s.req.Amount) {
		return nil, newErr(CodeRejected, "amount must be positive")
	}
	if s.typ == "limit" && s.req.Price <= 0 {
		return nil, newErr(CodeRejected, "limit order needs a positive price")
	}
	if s.typ == "stop" {
		if s.req.StopPrice <= 0 {
			return nil, newErr(CodeRejected, "stop order needs a positive stop price")
		}
		if s.req.Price > 0 {
			return nil, newErr(CodeUnsupported, "stop-limit orders are not supported by the paper broker")
		}
	}
	if s.futures {
		s.market = marketFutures
		s.symbol = gct.FuturesSymbol(s.req.Pair)
		if !strings.HasSuffix(s.symbol, "USDTM") {
			return nil, newErr(CodeUnsupported, "futures contract %q is not a USDT-margined contract", s.req.Pair)
		}
	} else {
		s.market = marketSpot
		if gct.IsFuturesPair(s.req.Pair) {
			return nil, newErr(CodeUnsupported, "%s looks like a futures pair on the spot path: quantity unit is undefined", s.req.Pair)
		}
		if _, _, ok := splitSpot(s.req.Pair); !ok {
			return nil, newErr(CodeUnsupported, "spot pair %q is not USD-quoted", s.req.Pair)
		}
		s.symbol = strings.ToUpper(s.req.Pair)
	}

	// ── same protections as real: the risk sentinel, evaluated first ──
	if err := b.riskEvaluator(s.exchange).Evaluate(ctx, &s.req, s.futures); err != nil {
		return nil, err
	}

	// ── real price and contract spec ──
	key := lookupKey(s.market, s.req.Pair, s.symbol)
	if s.curPrice, err = b.price(ctx, s.exchange, key, s.futures); err != nil {
		return nil, err
	}
	p := b.params()
	s.slip, s.feeRate = p.SlippagePct/100, p.TakerFeePct/100
	if s.futures {
		if s.mult, err = b.multiplier(ctx, s.symbol); err != nil {
			return nil, err
		}
		s.refPrice = s.curPrice
		if s.req.Price > 0 {
			s.refPrice = s.req.Price
		} else if s.typ == "stop" {
			s.refPrice = s.req.StopPrice
		}
		// Amount is a USD target on the futures route (same conversion as real).
		s.qty = gct.FuturesContracts(s.req.Amount, s.refPrice, s.mult) * s.mult
	} else {
		s.qty = s.req.Amount
	}
	if s.typ == "stop" {
		s.stopDir = "down"
		if s.req.StopPrice >= s.curPrice {
			s.stopDir = "up"
		}
	}

	var out *gct.Order
	storeMu.Lock()
	defer storeMu.Unlock()
	err = b.inTx(ctx, func(tx *sql.Tx) error {
		acct, err := b.getOrCreateAccount(ctx, tx, holder)
		if err != nil {
			return err
		}
		pos, err := b.openPosition(ctx, tx, holder, s.symbol, s.market)
		if err != nil {
			return err
		}
		// A position already past its margin is closed at the current price first.
		if pos != nil && pos.market == marketFutures && b.liquidationHit(pos, s.curPrice) {
			if err := b.liquidate(ctx, tx, pos, s.curPrice); err != nil {
				return err
			}
			pos = nil
		}
		o, err := b.buildOrder(ctx, tx, acct, pos, s)
		if err != nil {
			return err
		}
		if err := b.insertOrder(ctx, tx, o); err != nil {
			return err
		}
		if _, err := b.tryFill(ctx, tx, o, s.curPrice); err != nil {
			return err
		}
		out = c.toOrder(o)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (b *Broker) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// buildOrder classifies the order against the current position, clips reduce
// quantities, checks funds and fixes the funds to reserve while it is pending.
func (b *Broker) buildOrder(ctx context.Context, q dbq, acct *account, pos *position, s *spec) (*orderRow, error) {
	o := &orderRow{
		id: newOrderID(), holder: s.holder, exchange: s.exchange, pair: s.req.Pair, symbol: s.symbol,
		market: s.market, side: s.side, typ: s.typ, amount: s.req.Amount, qty: s.qty, price: s.req.Price,
		stopPrice: s.req.StopPrice, stopDir: s.stopDir, leverage: 1, reduceOnly: s.reduce, status: "open",
	}
	long := s.side == "buy"
	if s.futures {
		opposite := pos != nil && (pos.side == "long") != long
		if s.reduce && !opposite {
			return nil, newErr(CodeRejected, "reduce-only order has no opposite position to reduce on %s", s.symbol)
		}
		if opposite {
			if o.qty > pos.size+eps {
				if !s.reduce {
					return nil, newErr(CodeUnsupported, "order is larger than the open %s position on %s: flipping a position is not supported, close it first", pos.side, s.symbol)
				}
				o.qty = pos.size
			}
			o.reduceOnly = true // an opposite-side order reduces
			return o, nil
		}
		// increases exposure
		switch {
		case pos != nil:
			o.leverage = pos.leverage
		case s.req.Leverage > 0:
			o.leverage = s.req.Leverage
		}
		if l, ok := acct.leverage[s.symbol]; ok && pos == nil && s.req.Leverage <= 1 && l > 0 {
			o.leverage = l
		}
		if o.leverage < 1 {
			o.leverage = 1
		}
		px := b.pendingPrice(o, s.curPrice, s.slip)
		required := o.qty*px/o.leverage + o.qty*px*s.feeRate
		avail, err := b.available(ctx, q, acct, "")
		if err != nil {
			return nil, err
		}
		if required > avail+eps {
			return nil, insufficient(avail, required)
		}
		o.reserved = required
		return o, nil
	}
	// spot
	if !long {
		if pos == nil || pos.size+eps < o.qty {
			held := 0.0
			if pos != nil {
				held = pos.size
			}
			return nil, &Error{Code: CodeInsufficientFunds, Available: held, Required: o.qty,
				Message: fmt.Sprintf("insufficient base asset to sell on %s: available %.8f, required %.8f", s.symbol, held, o.qty)}
		}
		o.reduceOnly = true
		return o, nil
	}
	px := b.pendingPrice(o, s.curPrice, s.slip)
	required := o.qty*px + o.qty*px*s.feeRate
	avail, err := b.available(ctx, q, acct, "")
	if err != nil {
		return nil, err
	}
	if required > avail+eps {
		return nil, insufficient(avail, required)
	}
	o.reserved = required
	return o, nil
}

func insufficient(avail, required float64) *Error {
	return &Error{Code: CodeInsufficientFunds, Available: avail, Required: required,
		Message: fmt.Sprintf("insufficient balance: available %.4f USD, required %.4f USD", avail, required)}
}

// pendingPrice is the price used to estimate the funds an order needs.
func (b *Broker) pendingPrice(o *orderRow, cur, slip float64) float64 {
	switch o.typ {
	case "limit":
		return o.price
	case "stop":
		return o.stopPrice
	}
	if o.side == "buy" {
		return cur * (1 + slip)
	}
	return cur * (1 - slip)
}

func (b *Broker) available(ctx context.Context, q dbq, acct *account, exceptOrder string) (float64, error) {
	res, err := b.reservedFor(ctx, q, acct.holder, exceptOrder)
	if err != nil {
		return 0, err
	}
	return acct.balance - res, nil
}

func (b *Broker) insertOrder(ctx context.Context, q dbq, o *orderRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO paper_orders (id, holder, exchange, pair, symbol, market, side, type,
		amount, qty, price, stop_price, stop_dir, leverage, reduce_only, reserved, status)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.id, o.holder, o.exchange, o.pair, o.symbol, o.market, o.side, o.typ, o.amount, o.qty, o.price,
		o.stopPrice, o.stopDir, o.leverage, boolInt(o.reduceOnly), o.reserved, "open")
	if err != nil {
		return err
	}
	o.createdAt, o.updatedAt = time.Now().UTC(), time.Now().UTC()
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// tryFill fills the order when the real price allows it. It returns whether the
// order was filled. A funds failure is returned as an *Error without writing.
func (b *Broker) tryFill(ctx context.Context, q dbq, o *orderRow, price float64) (bool, error) {
	p := b.params()
	slip, feeRate := p.SlippagePct/100, p.TakerFeePct/100
	var exec float64
	switch o.typ {
	case "market":
		exec = price * (1 + slip)
		if o.side == "sell" {
			exec = price * (1 - slip)
		}
	case "limit":
		switch {
		case o.side == "buy" && price <= o.price+eps, o.side == "sell" && price >= o.price-eps:
			exec = o.price // fills at its own limit price
		default:
			return false, nil
		}
	case "stop":
		if (o.stopDir == "up" && price < o.stopPrice-eps) || (o.stopDir != "up" && price > o.stopPrice+eps) {
			return false, nil
		}
		exec = price * (1 + slip)
		if o.side == "sell" {
			exec = price * (1 - slip)
		}
	default:
		return false, newErr(CodeUnsupported, "order type %q is not supported", o.typ)
	}
	return true, b.applyFill(ctx, q, o, exec, feeRate)
}

// applyFill books a complete fill at exec: position, cash, fill row, order row.
// All checks run before the first write, so a rejection leaves no partial state.
func (b *Broker) applyFill(ctx context.Context, q dbq, o *orderRow, exec, feeRate float64) error {
	acct, err := b.getOrCreateAccount(ctx, q, o.holder)
	if err != nil {
		return err
	}
	pos, err := b.openPosition(ctx, q, o.holder, o.symbol, o.market)
	if err != nil {
		return err
	}
	long := o.side == "buy"
	qty := o.qty
	notional := qty * exec
	fee := notional * feeRate
	var realized float64
	var liquidation bool
	positionID := int64(0)

	switch {
	case o.market == marketFutures && pos != nil && (pos.side == "long") != long:
		// reduces / closes
		if qty > pos.size+eps {
			qty = pos.size
			notional = qty * exec
			fee = notional * feeRate
		}
		pnl := (exec - pos.entry) * qty
		if pos.side == "short" {
			pnl = (pos.entry - exec) * qty
		}
		released := pos.margin * qty / pos.size
		ret := math.Max(0, released+pnl) // isolated margin: a loss never exceeds it
		newBal := acct.balance + ret - fee
		realized = pnl
		positionID = pos.id
		if err := b.setBalance(ctx, q, o.holder, newBal); err != nil {
			return err
		}
		if err := b.shrinkPosition(ctx, q, pos, qty, released, pnl, exec); err != nil {
			return err
		}
	case o.market == marketFutures:
		// opens or adds
		margin := notional / o.leverage
		avail, err := b.available(ctx, q, acct, o.id)
		if err != nil {
			return err
		}
		if margin+fee > avail+eps {
			return insufficient(avail, margin+fee)
		}
		if err := b.setBalance(ctx, q, o.holder, acct.balance-margin-fee); err != nil {
			return err
		}
		positionID, err = b.growPosition(ctx, q, o, pos, qty, exec, o.leverage, margin)
		if err != nil {
			return err
		}
	case !long:
		// spot sell
		if pos == nil || pos.size+eps < qty {
			held := 0.0
			if pos != nil {
				held = pos.size
			}
			return &Error{Code: CodeInsufficientFunds, Available: held, Required: qty,
				Message: fmt.Sprintf("insufficient base asset to sell on %s: available %.8f, required %.8f", o.symbol, held, qty)}
		}
		pnl := (exec - pos.entry) * qty
		realized = pnl
		positionID = pos.id
		if err := b.setBalance(ctx, q, o.holder, acct.balance+notional-fee); err != nil {
			return err
		}
		if err := b.shrinkPosition(ctx, q, pos, qty, 0, pnl, exec); err != nil {
			return err
		}
	default:
		// spot buy
		avail, err := b.available(ctx, q, acct, o.id)
		if err != nil {
			return err
		}
		if notional+fee > avail+eps {
			return insufficient(avail, notional+fee)
		}
		if err := b.setBalance(ctx, q, o.holder, acct.balance-notional-fee); err != nil {
			return err
		}
		positionID, err = b.growPosition(ctx, q, o, pos, qty, exec, 1, 0)
		if err != nil {
			return err
		}
	}

	if _, err := q.ExecContext(ctx, `INSERT INTO paper_fills (holder, order_id, position_id, exchange, pair, market, side,
		qty, price, fee, realized_pnl, liquidation) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.holder, o.id, positionID, o.exchange, o.pair, o.market, o.side, qty, exec, fee, realized, boolInt(liquidation)); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE paper_orders SET status='filled', qty=?, fill_price=?, fee=?, reserved=0,
		updated_at=CURRENT_TIMESTAMP WHERE id=?`, qty, exec, fee, o.id); err != nil {
		return err
	}
	o.status, o.qty, o.fillPrice, o.fee, o.reserved = "filled", qty, exec, fee, 0
	o.updatedAt = time.Now().UTC()
	return nil
}

func (b *Broker) growPosition(ctx context.Context, q dbq, o *orderRow, pos *position, qty, exec, lev, margin float64) (int64, error) {
	side := "long"
	if o.side == "sell" {
		side = "short"
	}
	if pos == nil {
		res, err := q.ExecContext(ctx, `INSERT INTO paper_positions (holder, exchange, pair, symbol, market, side, size,
			entry_price, leverage, margin, status) VALUES (?,?,?,?,?,?,?,?,?,?, 'open')`,
			o.holder, o.exchange, o.pair, o.symbol, o.market, side, qty, exec, lev, margin)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	newSize := pos.size + qty
	entry := (pos.entry*pos.size + exec*qty) / newSize
	_, err := q.ExecContext(ctx, `UPDATE paper_positions SET size=?, entry_price=?, margin=margin+? WHERE id=?`,
		newSize, entry, margin, pos.id)
	return pos.id, err
}

func (b *Broker) shrinkPosition(ctx context.Context, q dbq, pos *position, qty, releasedMargin, pnl, exec float64) error {
	if qty >= pos.size-eps {
		_, err := q.ExecContext(ctx, `UPDATE paper_positions SET size=0, margin=0, realized_pnl=realized_pnl+?, status='closed',
			close_reason='closed', close_price=?, closed_at=CURRENT_TIMESTAMP WHERE id=?`, pnl, exec, pos.id)
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE paper_positions SET size=size-?, margin=margin-?, realized_pnl=realized_pnl+? WHERE id=?`,
		qty, releasedMargin, pnl, pos.id)
	return err
}

// liquidationHit reports whether the unrealized loss has consumed the margin.
func (b *Broker) liquidationHit(pos *position, price float64) bool {
	if pos.market != marketFutures || pos.margin <= 0 {
		return false
	}
	return unrealized(pos, price) <= -pos.margin+eps
}

func unrealized(pos *position, price float64) float64 {
	if pos.side == "short" {
		return (pos.entry - price) * pos.size
	}
	return (price - pos.entry) * pos.size
}

// liquidate force-closes a leveraged position: the margin is lost, the history
// marks the fill as a liquidation and the pending orders of the symbol are cancelled.
func (b *Broker) liquidate(ctx context.Context, q dbq, pos *position, price float64) error {
	side := "sell"
	if pos.side == "short" {
		side = "buy"
	}
	if _, err := q.ExecContext(ctx, `UPDATE paper_positions SET realized_pnl=realized_pnl-?, size=0, margin=0, status='closed',
		close_reason='liquidated', close_price=?, closed_at=CURRENT_TIMESTAMP WHERE id=?`, pos.margin, price, pos.id); err != nil {
		return err
	}
	holder, err := b.positionHolder(ctx, q, pos.id)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO paper_fills (holder, order_id, position_id, exchange, pair, market, side,
		qty, price, fee, realized_pnl, liquidation) VALUES (?,?,?,?,?,?,?,?,?,0,?,1)`,
		holder, "liquidation-"+newOrderID(), pos.id, pos.exchange, pos.pair, pos.market, side, pos.size, price, -pos.margin); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE paper_orders SET status='cancelled', reason='position liquidated', reserved=0,
		updated_at=CURRENT_TIMESTAMP WHERE holder=? AND symbol=? AND market=? AND status='open'`, holder, pos.symbol, pos.market)
	return err
}

func (b *Broker) positionHolder(ctx context.Context, q dbq, id int64) (string, error) {
	var h string
	err := q.QueryRowContext(ctx, `SELECT holder FROM paper_positions WHERE id = ?`, id).Scan(&h)
	return h, err
}

// cancel cancels an open order of the holder.
func (c *Client) cancel(ctx context.Context, orderID string) error {
	holder, err := holderOf(ctx)
	if err != nil {
		return err
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	return c.b.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE paper_orders SET status='cancelled', reserved=0, updated_at=CURRENT_TIMESTAMP
			WHERE id=? AND holder=? AND status='open'`, orderID, holder)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return newErr(CodeNotFound, "no open paper order %q", orderID)
		}
		return nil
	})
}

// toOrder converts a row to the shared gct.Order type, marked as paper.
func (c *Client) toOrder(o *orderRow) *gct.Order {
	p := c.b.params()
	price := o.price
	if o.status == "filled" {
		price = o.fillPrice
	} else if o.typ == "stop" {
		price = o.stopPrice
	}
	filled := 0.0
	if o.status == "filled" {
		filled = o.qty
	}
	base, quote := o.pair, ""
	if o.market == marketSpot {
		if bs, q, ok := splitSpot(o.pair); ok {
			base, quote = bs, q
		}
	}
	mt := gct.MarketTypeSpot
	if o.market == marketFutures {
		mt = gct.MarketTypeFutures
	}
	return &gct.Order{
		ID: o.id, Exchange: o.exchange, Pair: o.pair, BaseCurrency: base, QuoteCurrency: quote,
		Side: o.side, Type: o.typ, Price: price, Amount: o.qty, Filled: filled, Remaining: o.qty - filled,
		OpenVolume: o.qty - filled, Status: o.status, MarketType: mt,
		CreatedAt: o.createdAt, UpdatedAt: o.updatedAt, UpdateTime: o.updatedAt,
		Paper: true,
		PaperAssumptions: &gct.PaperAssumptions{
			SlippagePct: p.SlippagePct, TakerFeePct: p.TakerFeePct, FeeUSD: o.fee,
			FundingModeled: false, PartialFills: false,
			LiquidationNote: "leveraged positions are closed when unrealized loss equals margin",
		},
	}
}

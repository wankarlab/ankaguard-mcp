package paper

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// Client is the paper implementation of gct.GCTClient.
//
// Delegated to the real client (market data and connection plumbing only):
// HealthCheck, IsConnected, GetTicker, GetOrderbook, GetCandles,
// GetEnabledExchanges, GetTickerBySymbol, GetOrderBookBySymbol, GetTrades,
// GetOHLCV, CalculateRSI/MACD/BollingerBands, GetExchangeInfo, GetAllTickers,
// ConvertSymbol, SetKuCoinCredentials, Connect.
//
// Served from SQLite, never forwarded: GetAccountInfo, SubmitOrder, PlaceOrder,
// GetOrderInfo, CancelOrder, GetPortfolio, GetOrders, SimulateOrder, GetBalance,
// GetPositions, SubmitFuturesOrder, GetFuturesPositions, GetFuturesPosition,
// CloseFuturesPosition, SetLeverage, GetPortfolioOverview (plus the optional
// CancelFuturesOrder and GetOrderFillInfo the server reaches by type assertion).
//
// Neutralized: Disconnect is a no-op (the real connection is shared and must
// not be torn down by a paper session) and GetGRPCConn returns nil (a raw gRPC
// connection would allow order calls that bypass the broker).
type Client struct {
	b     *Broker
	inner gct.GCTClient
}

var _ gct.GCTClient = (*Client)(nil)

// NewClient builds a paper client and creates the paper tables if missing.
func NewClient(cfg Config) (*Client, error) {
	b, err := newBroker(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{b: b, inner: cfg.Inner}, nil
}

// Broker returns the execution engine (Run, Tick, ResetAccount, Assumptions).
func (c *Client) Broker() *Broker { return c.b }

// Assumptions returns the execution assumptions for tool responses.
func (c *Client) Assumptions() map[string]interface{} { return c.b.Assumptions() }

// ── delegated: market data ──────────────────────────────────────────────────

func (c *Client) Connect(ctx context.Context, address string) error {
	return c.inner.Connect(ctx, address)
}
func (c *Client) Disconnect() error                     { return nil }
func (c *Client) IsConnected() bool                     { return c.inner.IsConnected() }
func (c *Client) HealthCheck(ctx context.Context) error { return c.inner.HealthCheck(ctx) }
func (c *Client) GetGRPCConn() *grpc.ClientConn         { return nil }
func (c *Client) SetKuCoinCredentials(k, s, p string)   { c.inner.SetKuCoinCredentials(k, s, p) }
func (c *Client) GetTicker(ctx context.Context, exchange, pair string) (*gct.Ticker, error) {
	return c.inner.GetTicker(ctx, exchange, pair)
}
func (c *Client) GetOrderbook(ctx context.Context, exchange, pair string) (*gct.Orderbook, error) {
	return c.inner.GetOrderbook(ctx, exchange, pair)
}
func (c *Client) GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]*gct.Candle, error) {
	return c.inner.GetCandles(ctx, exchange, pair, interval, limit)
}
func (c *Client) GetEnabledExchanges(ctx context.Context) ([]string, error) {
	return c.inner.GetEnabledExchanges(ctx)
}
func (c *Client) GetTickerBySymbol(ctx context.Context, exchange, symbol, assetType string) (*gct.Ticker, error) {
	return c.inner.GetTickerBySymbol(ctx, exchange, symbol, assetType)
}
func (c *Client) GetOrderBookBySymbol(ctx context.Context, exchange, symbol, assetType string) (*gct.Orderbook, error) {
	return c.inner.GetOrderBookBySymbol(ctx, exchange, symbol, assetType)
}
func (c *Client) GetTrades(ctx context.Context, exchange, symbol string, limit int) ([]map[string]interface{}, error) {
	return c.inner.GetTrades(ctx, exchange, symbol, limit)
}
func (c *Client) GetOHLCV(ctx context.Context, exchange, symbol, interval string, limit int) ([]map[string]interface{}, error) {
	return c.inner.GetOHLCV(ctx, exchange, symbol, interval, limit)
}
func (c *Client) CalculateRSI(ctx context.Context, exchange, symbol string, period int) (float64, error) {
	return c.inner.CalculateRSI(ctx, exchange, symbol, period)
}
func (c *Client) CalculateMACD(ctx context.Context, exchange, symbol string, fast, slow, signal int) (map[string]float64, error) {
	return c.inner.CalculateMACD(ctx, exchange, symbol, fast, slow, signal)
}
func (c *Client) CalculateBollingerBands(ctx context.Context, exchange, symbol string, period int, stdDev float64) (map[string]float64, error) {
	return c.inner.CalculateBollingerBands(ctx, exchange, symbol, period, stdDev)
}
func (c *Client) GetExchangeInfo(ctx context.Context, exchange string) (map[string]interface{}, error) {
	return c.inner.GetExchangeInfo(ctx, exchange)
}
func (c *Client) GetAllTickers(ctx context.Context, exchange string) ([]map[string]interface{}, error) {
	return c.inner.GetAllTickers(ctx, exchange)
}
func (c *Client) ConvertSymbol(symbol string, marketType gct.MarketType, exchange string) (string, error) {
	return c.inner.ConvertSymbol(symbol, marketType, exchange)
}

// ── served in paper: orders ─────────────────────────────────────────────────

func (c *Client) SubmitOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	return c.submit(ctx, req, false)
}

func (c *Client) PlaceOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	return c.submit(ctx, req, false)
}

func (c *Client) SubmitFuturesOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	return c.submit(ctx, req, true)
}

func (c *Client) GetOrderInfo(ctx context.Context, exchange, orderID string) (*gct.Order, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	o, err := c.b.getOrder(ctx, holder, orderID)
	if err != nil {
		return nil, err
	}
	return c.toOrder(o), nil
}

func (c *Client) CancelOrder(ctx context.Context, exchange, orderID string) error {
	return c.cancel(ctx, orderID)
}

// CancelFuturesOrder mirrors the optional method of the real client.
func (c *Client) CancelFuturesOrder(ctx context.Context, exchange, orderID string) error {
	return c.cancel(ctx, orderID)
}

// GetOrderFillInfo mirrors the optional method of the real client.
func (c *Client) GetOrderFillInfo(ctx context.Context, exchange, orderID string) (size, filled float64, err error) {
	o, err := c.GetOrderInfo(ctx, exchange, orderID)
	if err != nil {
		return 0, 0, err
	}
	return o.Amount, o.Filled, nil
}

func (c *Client) GetOrders(ctx context.Context, exchange, pair, orderType, assetType string, start, end *time.Time) ([]*gct.Order, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := c.b.listOrders(ctx, holder, false)
	if err != nil {
		return nil, err
	}
	norm := func(s string) string {
		return strings.ToUpper(strings.NewReplacer("-", "", "_", "", "/", "").Replace(s))
	}
	var out []*gct.Order
	for _, o := range rows {
		if pair != "" && norm(o.pair) != norm(pair) && norm(o.symbol) != norm(pair) {
			continue
		}
		if assetType != "" && !strings.EqualFold(assetType, o.market) {
			continue
		}
		if !matchesOrderFilter(o, orderType) {
			continue
		}
		if start != nil && o.createdAt.Before(*start) || end != nil && o.createdAt.After(*end) {
			continue
		}
		out = append(out, c.toOrder(o))
	}
	return out, nil
}

// SimulateOrder estimates an execution against the paper account without
// placing anything. It never contacts the exchange for orders.
func (c *Client) SimulateOrder(ctx context.Context, req *gct.SubmitOrderRequest) (map[string]interface{}, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Amount <= 0 || req.Pair == "" {
		return nil, newErr(CodeRejected, "pair and a positive amount are required")
	}
	futures := req.Leverage > 0 || req.MarketType == gct.MarketTypeFutures
	exchange := req.Exchange
	if exchange == "" {
		exchange = defaultExchange
	}
	symbol := req.Pair
	if futures {
		symbol = gct.FuturesSymbol(req.Pair)
	}
	px, err := c.b.price(ctx, exchange, symbol, futures)
	if err != nil {
		return nil, err
	}
	p := c.b.params()
	exec := px * (1 + p.SlippagePct/100)
	if strings.EqualFold(req.Side, "sell") {
		exec = px * (1 - p.SlippagePct/100)
	}
	if req.Type == "limit" && req.Price > 0 {
		exec = req.Price
	}
	qty, lev := req.Amount, 1.0
	if futures {
		m, err := c.b.multiplier(ctx, symbol)
		if err != nil {
			return nil, err
		}
		qty = gct.FuturesContracts(req.Amount, exec, m) * m
		lev = math.Max(1, req.Leverage)
	}
	notional := qty * exec
	fee := notional * p.TakerFeePct / 100
	snap, err := c.snapshot(ctx, holder)
	if err != nil {
		return nil, err
	}
	avail := snap.acct.balance - snap.reserved
	required := notional/lev + fee
	return map[string]interface{}{
		"paper": true, "simulated": true, "pair": req.Pair, "side": req.Side, "type": req.Type,
		"market_price": px, "estimated_fill_price": exec, "quantity": qty, "notional_usd": notional,
		"margin_required_usd": notional / lev, "estimated_fee_usd": fee, "leverage": lev,
		"available_usd": avail, "sufficient_funds": required <= avail+eps,
		"assumptions": c.b.Assumptions(),
	}, nil
}

// ── served in paper: account, balance, positions ────────────────────────────

func (c *Client) GetBalance(ctx context.Context, exchange, currency string) (map[string]interface{}, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	s, err := c.snapshot(ctx, holder)
	if err != nil {
		return nil, err
	}
	cur := strings.ToUpper(currency)
	if cur == "" || cur == "USDT" || cur == "USD" || cur == "USDC" {
		total := s.acct.balance + s.margin
		avail := s.acct.balance - s.reserved
		return map[string]interface{}{
			"paper": true, "currency": "USDT", "total": total, "available": avail,
			"in_order": total - avail, "hold": total - avail,
			"equity": s.equity, "unrealized_pnl": s.upnl, "initial_balance": s.acct.initial,
			"prices_complete": !s.stale, "assumptions": c.b.Assumptions(),
		}, nil
	}
	held := 0.0
	for _, v := range s.positions {
		if v.pos.market == marketSpot {
			if base, _, ok := splitSpot(v.pos.pair); ok && base == cur {
				held += v.pos.size
			}
		}
	}
	return map[string]interface{}{
		"paper": true, "currency": cur, "total": held, "available": held, "in_order": 0.0,
		"assumptions": c.b.Assumptions(),
	}, nil
}

func (c *Client) GetAccountInfo(ctx context.Context, exchange string) (*gct.AccountInfo, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	s, err := c.snapshot(ctx, holder)
	if err != nil {
		return nil, err
	}
	total := s.acct.balance + s.margin
	avail := s.acct.balance - s.reserved
	bal := map[string]gct.Balance{"USDT": {Currency: "USDT", Total: total, Available: avail, Hold: total - avail, InOrder: total - avail}}
	for _, v := range s.positions {
		if v.pos.market != marketSpot {
			continue
		}
		if base, _, ok := splitSpot(v.pos.pair); ok {
			b := bal[base]
			b.Currency, b.Total, b.Available = base, b.Total+v.pos.size, b.Available+v.pos.size
			bal[base] = b
		}
	}
	if exchange == "" {
		exchange = defaultExchange
	}
	return &gct.AccountInfo{Exchange: exchange, Accounts: []gct.Account{{ID: "paper", Balances: bal}}, Paper: true}, nil
}

func (c *Client) GetPortfolio(ctx context.Context) (*gct.Portfolio, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	s, err := c.snapshot(ctx, holder)
	if err != nil {
		return nil, err
	}
	pnl := s.equity - s.acct.initial
	pct := 0.0
	if s.acct.initial > 0 {
		pct = pnl / s.acct.initial * 100
	}
	avail := s.acct.balance - s.reserved
	holdings := map[string]*gct.Holding{
		"USDT": {Currency: "USDT", Total: s.acct.balance + s.margin, Available: avail, InOrder: s.acct.balance + s.margin - avail, Value: s.acct.balance + s.margin + s.upnlFutures()},
	}
	alloc := map[string]float64{}
	for _, v := range s.positions {
		if v.pos.market != marketSpot {
			continue
		}
		if base, _, ok := splitSpot(v.pos.pair); ok {
			mark := v.mark
			if mark <= 0 {
				mark = v.pos.entry
			}
			holdings[base] = &gct.Holding{Currency: base, Total: v.pos.size, Available: v.pos.size, Value: v.pos.size * mark, PnL: v.upnl}
		}
	}
	for k, h := range holdings {
		if s.equity > 0 {
			alloc[k] = h.Value / s.equity * 100
		}
	}
	return &gct.Portfolio{TotalValue: s.equity, TotalPnL: pnl, PnL: pnl, PnLPercentage: pct, Holdings: holdings,
		Allocations: alloc, UpdatedAt: time.Now().UTC(), Paper: true}, nil
}

func (s *snapshot) upnlFutures() float64 {
	t := 0.0
	for _, v := range s.positions {
		if v.pos.market == marketFutures {
			t += v.upnl
		}
	}
	return t
}

func (c *Client) GetPositions(ctx context.Context, exchange string) ([]map[string]interface{}, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	s, err := c.snapshot(ctx, holder)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(s.positions))
	for _, v := range s.positions {
		out = append(out, map[string]interface{}{
			"paper": true, "symbol": v.pos.symbol, "pair": v.pos.pair, "market": v.pos.market, "side": v.pos.side,
			"size": v.pos.size, "entry_price": v.pos.entry, "current_price": v.mark, "unrealized_pnl": v.upnl,
			"leverage": v.pos.leverage, "margin": v.pos.margin, "liquidation_price": liquidationPrice(v.pos),
		})
	}
	return out, nil
}

// liquidationPrice is the price at which the unrealized loss equals the margin.
func liquidationPrice(p *position) float64 {
	if p.market != marketFutures || p.size <= 0 {
		return 0
	}
	if p.side == "short" {
		return p.entry + p.margin/p.size
	}
	return math.Max(0, p.entry-p.margin/p.size)
}

func (c *Client) toPosition(v *valued) *gct.Position {
	mt := gct.MarketTypeFutures
	if v.pos.market == marketSpot {
		mt = gct.MarketTypeSpot
	}
	return &gct.Position{
		Symbol: v.pos.symbol, Side: v.pos.side, Size: v.pos.size, EntryPrice: v.pos.entry, CurrentPrice: v.mark,
		UnrealizedPnL: v.upnl, RealizedPnL: v.pos.realized, Leverage: int(v.pos.leverage), MarginMode: "ISOLATED",
		LiquidationPrice: liquidationPrice(v.pos), Margin: v.pos.margin, UpdatedAt: time.Now().UTC(),
		MarketType: mt, Paper: true,
	}
}

func (c *Client) GetFuturesPositions(ctx context.Context, exchange string) ([]*gct.Position, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	s, err := c.snapshot(ctx, holder)
	if err != nil {
		return nil, err
	}
	out := []*gct.Position{}
	for _, v := range s.positions {
		if v.pos.market == marketFutures {
			out = append(out, c.toPosition(v))
		}
	}
	return out, nil
}

func (c *Client) GetFuturesPosition(ctx context.Context, exchange, symbol string) (*gct.Position, error) {
	list, err := c.GetFuturesPositions(ctx, exchange)
	if err != nil {
		return nil, err
	}
	want := gct.FuturesSymbol(symbol)
	for _, p := range list {
		if p.Symbol == want {
			return p, nil
		}
	}
	return nil, newErr(CodeNotFound, "no open paper position on %s", symbol)
}

// CloseFuturesPosition closes the whole position with a market order.
func (c *Client) CloseFuturesPosition(ctx context.Context, exchange, symbol string) error {
	if symbol == "" {
		return newErr(CodeRejected, "symbol is empty")
	}
	pos, err := c.GetFuturesPosition(ctx, exchange, symbol)
	if err != nil {
		return err
	}
	side := "sell"
	if pos.Side == "short" {
		side = "buy"
	}
	// The order Amount is a USD target on the futures route; the clip to the
	// position size makes any value at least as large as the position close it all.
	req := &gct.SubmitOrderRequest{
		Exchange: exchange, Pair: pos.Symbol, Side: side, Type: "market",
		Amount: math.Max(pos.Size*math.Max(pos.EntryPrice, pos.CurrentPrice)*2, 1), Leverage: float64(pos.Leverage),
		ReduceOnly: true, CloseOrder: true, MarketType: gct.MarketTypeFutures,
	}
	if req.Leverage <= 0 {
		req.Leverage = 1
	}
	_, err = c.submit(ctx, req, true)
	return err
}

func (c *Client) SetLeverage(ctx context.Context, exchange, symbol string, leverage int) error {
	holder, err := holderOf(ctx)
	if err != nil {
		return err
	}
	if leverage < 1 || leverage > 125 {
		return newErr(CodeRejected, "leverage %d out of range [1,125]", leverage)
	}
	return c.b.setLeverage(ctx, holder, gct.FuturesSymbol(symbol), float64(leverage))
}

func (c *Client) GetPortfolioOverview(ctx context.Context, exchange string) (*gct.PortfolioOverview, error) {
	holder, err := holderOf(ctx)
	if err != nil {
		return nil, err
	}
	s, err := c.snapshot(ctx, holder)
	if err != nil {
		return nil, err
	}
	avail := s.acct.balance - s.reserved
	var exposure float64
	ov := &gct.PortfolioOverview{Timestamp: time.Now().UTC(), Exchange: defaultExchange, Paper: true}
	for _, v := range s.positions {
		if v.pos.market != marketFutures {
			continue
		}
		mark := v.mark
		if mark <= 0 {
			mark = v.pos.entry
		}
		notional := v.pos.size * mark
		exposure += notional
		pct := 0.0
		if v.pos.margin > 0 {
			pct = v.upnl / v.pos.margin * 100
		}
		ov.Positions = append(ov.Positions, gct.PositionDetail{
			Symbol: v.pos.symbol, Side: v.pos.side, Size: v.pos.size, EntryPrice: v.pos.entry, CurrentPrice: v.mark,
			Leverage: v.pos.leverage, MarginMode: "ISOLATED", MarginUSD: v.pos.margin, NotionalUSD: notional,
			UnrealizedPnL: v.upnl, UnrealizedPnLPct: pct, RealizedPnL: v.pos.realized,
			LiquidationPrice: liquidationPrice(v.pos), ROE: pct,
		})
	}
	ov.Summary = gct.OverviewSummary{
		TotalEquityUSD: s.equity, SpotEquityUSD: s.spotValue, FuturesEquityUSD: s.margin + s.upnlFutures() + s.acct.balance,
		UnrealizedPnLUSD: s.upnl, AvailableForTrading: avail, TotalExposureUSD: exposure,
	}
	ov.Futures = gct.FuturesWallet{Currency: "USDT", AccountEquity: s.equity, AvailableBalance: avail, PositionMargin: s.margin,
		OrderMargin: s.reserved, UnrealizedPnL: s.upnl}
	ov.Risk = gct.RiskMetrics{MarginUsedUSD: s.margin, MarginAvailableUSD: avail, TotalExposureUSD: exposure,
		OpenPositions: len(ov.Positions), AccountHealth: "healthy", CanOpenPosition: avail > 0, MaxNewPositionUSD: avail}
	return ov, nil
}

func (b *Broker) setLeverage(ctx context.Context, holder, symbol string, lev float64) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	return b.inTx(ctx, func(tx *sql.Tx) error {
		acct, err := b.getOrCreateAccount(ctx, tx, holder)
		if err != nil {
			return err
		}
		acct.leverage[symbol] = lev
		return b.saveLeverage(ctx, tx, acct)
	})
}

package paper

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/risk"
)

// ── fakes ────────────────────────────────────────────────────────────────────

// realFake is the "real" client: market data works, every order/account method
// fails the test, so any leak to the exchange is caught.
type realFake struct {
	t     *testing.T
	price float64
	calls []string
}

func (f *realFake) forbid(name string) {
	f.t.Helper()
	f.t.Fatalf("real client received forbidden call %s", name)
}
func (f *realFake) Connect(context.Context, string) error { return nil }
func (f *realFake) Disconnect() error                     { f.forbid("Disconnect"); return nil }
func (f *realFake) IsConnected() bool                     { return true }
func (f *realFake) HealthCheck(context.Context) error     { return nil }
func (f *realFake) GetTicker(_ context.Context, ex, pair string) (*gct.Ticker, error) {
	f.calls = append(f.calls, "GetTicker")
	if f.price <= 0 {
		return nil, fmt.Errorf("no ticker")
	}
	return &gct.Ticker{Exchange: ex, Pair: pair, Last: f.price, Timestamp: time.Now()}, nil
}
func (f *realFake) GetOrderbook(context.Context, string, string) (*gct.Orderbook, error) {
	f.calls = append(f.calls, "GetOrderbook")
	return &gct.Orderbook{}, nil
}
func (f *realFake) GetCandles(context.Context, string, string, string, int) ([]*gct.Candle, error) {
	f.calls = append(f.calls, "GetCandles")
	return nil, nil
}
func (f *realFake) GetAccountInfo(context.Context, string) (*gct.AccountInfo, error) {
	f.forbid("GetAccountInfo")
	return nil, nil
}
func (f *realFake) SubmitOrder(context.Context, *gct.SubmitOrderRequest) (*gct.Order, error) {
	f.forbid("SubmitOrder")
	return nil, nil
}
func (f *realFake) GetOrderInfo(context.Context, string, string) (*gct.Order, error) {
	f.forbid("GetOrderInfo")
	return nil, nil
}
func (f *realFake) CancelOrder(context.Context, string, string) error {
	f.forbid("CancelOrder")
	return nil
}
func (f *realFake) GetPortfolio(context.Context) (*gct.Portfolio, error) {
	f.forbid("GetPortfolio")
	return nil, nil
}
func (f *realFake) GetEnabledExchanges(context.Context) ([]string, error) {
	f.calls = append(f.calls, "GetEnabledExchanges")
	return []string{"kucoin"}, nil
}
func (f *realFake) GetOrders(context.Context, string, string, string, string, *time.Time, *time.Time) ([]*gct.Order, error) {
	f.forbid("GetOrders")
	return nil, nil
}
func (f *realFake) GetTickerBySymbol(context.Context, string, string, string) (*gct.Ticker, error) {
	f.calls = append(f.calls, "GetTickerBySymbol")
	return &gct.Ticker{}, nil
}
func (f *realFake) GetOrderBookBySymbol(context.Context, string, string, string) (*gct.Orderbook, error) {
	f.calls = append(f.calls, "GetOrderBookBySymbol")
	return &gct.Orderbook{}, nil
}
func (f *realFake) GetTrades(context.Context, string, string, int) ([]map[string]interface{}, error) {
	f.calls = append(f.calls, "GetTrades")
	return nil, nil
}
func (f *realFake) GetOHLCV(context.Context, string, string, string, int) ([]map[string]interface{}, error) {
	f.calls = append(f.calls, "GetOHLCV")
	return nil, nil
}
func (f *realFake) CalculateRSI(context.Context, string, string, int) (float64, error) {
	f.calls = append(f.calls, "CalculateRSI")
	return 50, nil
}
func (f *realFake) CalculateMACD(context.Context, string, string, int, int, int) (map[string]float64, error) {
	f.calls = append(f.calls, "CalculateMACD")
	return nil, nil
}
func (f *realFake) CalculateBollingerBands(context.Context, string, string, int, float64) (map[string]float64, error) {
	f.calls = append(f.calls, "CalculateBollingerBands")
	return nil, nil
}
func (f *realFake) GetExchangeInfo(context.Context, string) (map[string]interface{}, error) {
	f.calls = append(f.calls, "GetExchangeInfo")
	return nil, nil
}
func (f *realFake) GetAllTickers(context.Context, string) ([]map[string]interface{}, error) {
	f.calls = append(f.calls, "GetAllTickers")
	return nil, nil
}
func (f *realFake) SimulateOrder(context.Context, *gct.SubmitOrderRequest) (map[string]interface{}, error) {
	f.forbid("SimulateOrder")
	return nil, nil
}
func (f *realFake) GetBalance(context.Context, string, string) (map[string]interface{}, error) {
	f.forbid("GetBalance")
	return nil, nil
}
func (f *realFake) GetPositions(context.Context, string) ([]map[string]interface{}, error) {
	f.forbid("GetPositions")
	return nil, nil
}
func (f *realFake) PlaceOrder(context.Context, *gct.SubmitOrderRequest) (*gct.Order, error) {
	f.forbid("PlaceOrder")
	return nil, nil
}
func (f *realFake) GetGRPCConn() *grpc.ClientConn { f.forbid("GetGRPCConn"); return nil }
func (f *realFake) SetKuCoinCredentials(string, string, string) {
	f.calls = append(f.calls, "SetKuCoinCredentials")
}
func (f *realFake) SubmitFuturesOrder(context.Context, *gct.SubmitOrderRequest) (*gct.Order, error) {
	f.forbid("SubmitFuturesOrder")
	return nil, nil
}
func (f *realFake) GetFuturesPositions(context.Context, string) ([]*gct.Position, error) {
	f.forbid("GetFuturesPositions")
	return nil, nil
}
func (f *realFake) GetFuturesPosition(context.Context, string, string) (*gct.Position, error) {
	f.forbid("GetFuturesPosition")
	return nil, nil
}
func (f *realFake) CloseFuturesPosition(context.Context, string, string) error {
	f.forbid("CloseFuturesPosition")
	return nil
}
func (f *realFake) SetLeverage(context.Context, string, string, int) error {
	f.forbid("SetLeverage")
	return nil
}
func (f *realFake) ConvertSymbol(s string, _ gct.MarketType, _ string) (string, error) { return s, nil }
func (f *realFake) GetPortfolioOverview(context.Context, string) (*gct.PortfolioOverview, error) {
	f.forbid("GetPortfolioOverview")
	return nil, nil
}

var _ gct.GCTClient = (*realFake)(nil)

// gateSentinel mimics the real sentinel: rejects orders above capUSD (when the
// notional is known) and everything that opens a position while killed.
type gateSentinel struct {
	risk.RiskSentinel
	capUSD float64
	killed bool
	last   *risk.TradingAction
}

func (g *gateSentinel) EvaluateAction(_ context.Context, a *risk.TradingAction) (*risk.RiskDecision, error) {
	g.last = a
	if g.killed && a.Type == risk.ActionOpenPosition {
		return &risk.RiskDecision{Allowed: false, Reason: "kill switch active"}, nil
	}
	if g.capUSD > 0 && a.Type == risk.ActionOpenPosition && a.NotionalUSD > g.capUSD {
		return &risk.RiskDecision{Allowed: false, Code: "ORDER_CAP_EXCEEDED", Reason: "order exceeds cap"}, nil
	}
	return &risk.RiskDecision{Allowed: true}, nil
}

// ── harness ──────────────────────────────────────────────────────────────────

type env struct {
	t      *testing.T
	db     *sql.DB
	real   *realFake
	sent   *gateSentinel
	c      *Client
	params Params
	price  float64
	mult   float64
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "paper.db")+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, db: openDB(t), price: 100, mult: 1, params: Params{InitialBalanceUSD: 1000, SlippagePct: 0.05, TakerFeePct: 0.06}}
	e.real = &realFake{t: t, price: 100}
	e.sent = &gateSentinel{}
	e.rebuild()
	return e
}

func (e *env) rebuild() {
	c, err := NewClient(Config{
		DB: e.db, Inner: e.real, Sentinel: e.sent,
		Params: func() Params { return e.params },
		Price: func(_ context.Context, _, _ string, _ bool) (float64, time.Time, error) {
			if e.price <= 0 {
				return 0, time.Time{}, fmt.Errorf("no price")
			}
			return e.price, time.Now(), nil
		},
		ContractMultiplier: func(context.Context, string) (float64, error) { return e.mult, nil },
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.c = c
}

func ctxFor(holder string) context.Context { return WithHolder(context.Background(), holder) }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var pe *Error
	if err == nil || CodeOf(err) != code {
		t.Fatalf("want error code %s, got %v", code, err)
	}
	pe, _ = err.(*Error)
	return pe
}

func balanceOf(t *testing.T, e *env, holder string) map[string]interface{} {
	t.Helper()
	m, err := e.c.GetBalance(ctxFor(holder), "kucoin", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// ── isolation (5.5) ──────────────────────────────────────────────────────────

func TestNoOrderOrAccountMethodReachesTheRealClient(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("agent-1")
	req := func() *gct.SubmitOrderRequest {
		return &gct.SubmitOrderRequest{Exchange: "kucoin", Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 0.1}
	}
	futReq := &gct.SubmitOrderRequest{Exchange: "kucoin", Pair: "NEAR-USDTM", Side: "buy", Type: "market", Amount: 20, Leverage: 5}
	now := time.Now()
	// Each call may fail or succeed in paper; the real fake t.Fatals on any leak.
	_, _ = e.c.SubmitOrder(ctx, req())
	_, _ = e.c.PlaceOrder(ctx, req())
	_, _ = e.c.SubmitFuturesOrder(ctx, futReq)
	_, _ = e.c.GetOrderInfo(ctx, "kucoin", "nope")
	_ = e.c.CancelOrder(ctx, "kucoin", "nope")
	_ = e.c.CancelFuturesOrder(ctx, "kucoin", "nope")
	_, _, _ = e.c.GetOrderFillInfo(ctx, "kucoin", "nope")
	_, _ = e.c.GetOrders(ctx, "kucoin", "", "", "", &now, &now)
	_, _ = e.c.SimulateOrder(ctx, req())
	_, _ = e.c.GetBalance(ctx, "kucoin", "USDT")
	_, _ = e.c.GetAccountInfo(ctx, "kucoin")
	_, _ = e.c.GetPortfolio(ctx)
	_, _ = e.c.GetPortfolioOverview(ctx, "kucoin")
	_, _ = e.c.GetPositions(ctx, "kucoin")
	_, _ = e.c.GetFuturesPositions(ctx, "kucoin")
	_, _ = e.c.GetFuturesPosition(ctx, "kucoin", "NEARUSDTM")
	_ = e.c.CloseFuturesPosition(ctx, "kucoin", "NEARUSDTM")
	_ = e.c.SetLeverage(ctx, "kucoin", "NEARUSDTM", 3)
	_ = e.c.Disconnect()
	if e.c.GetGRPCConn() != nil {
		t.Fatal("GetGRPCConn must not expose the real connection")
	}
	// Market data is delegated.
	if tk, err := e.c.GetTicker(ctx, "kucoin", "BTC-USDT"); err != nil || tk.Last != 100 {
		t.Fatalf("ticker delegation: %v %v", tk, err)
	}
}

func TestUnsupportedOrdersNeverReachTheRealClient(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("agent-1")
	cases := map[string]*gct.SubmitOrderRequest{
		"stop_limit":   {Pair: "BTC-USDT", Side: "buy", Type: "stop", Amount: 1, StopPrice: 90, Price: 91},
		"trailing":     {Pair: "BTC-USDT", Side: "buy", Type: "trailing_stop", Amount: 1},
		"ioc":          {Pair: "BTC-USDT", Side: "buy", Type: "ioc", Amount: 1},
		"margin":       {Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1, MarketType: gct.MarketTypeMargin},
		"options":      {Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1, MarketType: gct.MarketTypeOptions},
		"spot_futures": {Pair: "BTC-USDTM", Side: "buy", Type: "market", Amount: 1},
		"non_usd_spot": {Pair: "ETH-BTC", Side: "buy", Type: "market", Amount: 1},
	}
	for name, r := range cases {
		_, err := e.c.SubmitOrder(ctx, r)
		wantCode(t, err, CodeUnsupported)
		if t.Failed() {
			t.Fatalf("case %s", name)
		}
	}
}

func TestNoHolderIsAnErrorNeverASharedAccount(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	_, err := e.c.SubmitOrder(bg, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1})
	wantCode(t, err, CodeNoHolder)
	_, err = e.c.GetBalance(bg, "kucoin", "USDT")
	wantCode(t, err, CodeNoHolder)
	_, err = e.c.GetFuturesPositions(bg, "kucoin")
	wantCode(t, err, CodeNoHolder)
	wantCode(t, e.c.CancelOrder(bg, "kucoin", "x"), CodeNoHolder)
}

// ── execution (5.2) ──────────────────────────────────────────────────────────

func TestMarketBuyAtRealPricePlusSlippageAndFee(t *testing.T) {
	e := newEnv(t)
	o, err := e.c.SubmitOrder(ctxFor("a"), &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 2})
	if err != nil {
		t.Fatal(err)
	}
	wantPx := 100 * 1.0005
	if o.Status != "filled" || !near(o.Price, wantPx) || !o.Paper || o.PaperAssumptions == nil ||
		o.PaperAssumptions.SlippagePct != 0.05 || o.PaperAssumptions.TakerFeePct != 0.06 {
		t.Fatalf("order = %+v assumptions=%+v", o, o.PaperAssumptions)
	}
	fee := 2 * wantPx * 0.0006
	if !near(o.PaperAssumptions.FeeUSD, fee) {
		t.Fatalf("fee = %v want %v", o.PaperAssumptions.FeeUSD, fee)
	}
	bal := balanceOf(t, e, "a")
	if !near(bal["available"].(float64), 1000-2*wantPx-fee) || bal["paper"] != true {
		t.Fatalf("balance = %v", bal)
	}
	pos, _ := e.c.GetPositions(ctxFor("a"), "kucoin")
	if len(pos) != 1 || pos[0]["paper"] != true || !near(pos[0]["size"].(float64), 2) {
		t.Fatalf("positions = %v", pos)
	}
}

func TestLimitBuyThatDoesNotCrossStaysOpenThenFillsAtLimit(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	o, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "limit", Amount: 1, Price: 90})
	if err != nil || o.Status != "open" {
		t.Fatalf("limit order = %+v err=%v", o, err)
	}
	open, _ := e.c.GetOrders(ctx, "kucoin", "", "open", "", nil, nil)
	if len(open) != 1 || open[0].ID != o.ID {
		t.Fatalf("open orders = %v", open)
	}
	if bal := balanceOf(t, e, "a"); bal["available"].(float64) >= 1000-90 { // funds reserved
		t.Fatalf("limit order did not reserve funds: %v", bal)
	}
	// price does not reach the limit: still open
	e.price = 95
	if r, err := e.c.b.Tick(ctx); err != nil || r.Filled != 0 {
		t.Fatalf("tick at 95: %+v %v", r, err)
	}
	// price reaches the limit: filled at the limit price, position updated
	e.price = 89
	r, err := e.c.b.Tick(ctx)
	if err != nil || r.Filled != 1 {
		t.Fatalf("tick at 89: %+v %v", r, err)
	}
	got, err := e.c.GetOrderInfo(ctx, "kucoin", o.ID)
	if err != nil || got.Status != "filled" || !near(got.Price, 90) {
		t.Fatalf("filled order = %+v err=%v", got, err)
	}
	pos, _ := e.c.GetPositions(ctx, "kucoin")
	if len(pos) != 1 || !near(pos[0]["entry_price"].(float64), 90) {
		t.Fatalf("positions = %v", pos)
	}
	if open, _ := e.c.GetOrders(ctx, "kucoin", "", "open", "", nil, nil); len(open) != 0 {
		t.Fatalf("open orders after fill = %v", open)
	}
}

func TestStopOrderActivatesOnTrigger(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	if _, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1}); err != nil {
		t.Fatal(err)
	}
	stop, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "sell", Type: "stop", Amount: 1, StopPrice: 95})
	if err != nil || stop.Status != "open" {
		t.Fatalf("stop = %+v %v", stop, err)
	}
	e.price = 97
	if r, _ := e.c.b.Tick(ctx); r.Filled != 0 {
		t.Fatal("stop triggered too early")
	}
	e.price = 94
	if r, _ := e.c.b.Tick(ctx); r.Filled != 1 {
		t.Fatal("stop did not trigger")
	}
	if pos, _ := e.c.GetPositions(ctx, "kucoin"); len(pos) != 0 {
		t.Fatalf("position should be closed: %v", pos)
	}
	fills, _ := e.c.Fills(ctx)
	if len(fills) != 2 || fills[0].Side != "sell" || fills[0].Price >= 94 { // slippage is adverse
		t.Fatalf("fills = %+v", fills)
	}
}

func TestPriceUnavailableRejectsWithDataUnavailable(t *testing.T) {
	e := newEnv(t)
	e.price = 0
	_, err := e.c.SubmitOrder(ctxFor("a"), &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1})
	wantCode(t, err, CodeDataUnavailable)
	if orders, _ := e.c.GetOrders(ctxFor("a"), "", "", "", "", nil, nil); len(orders) != 0 {
		t.Fatalf("rejected order must leave no trace: %v", orders)
	}
}

func TestStalePriceIsDataUnavailable(t *testing.T) {
	e := newEnv(t)
	e.c.b.cfg.Price = func(context.Context, string, string, bool) (float64, time.Time, error) {
		return 100, time.Now().Add(-time.Minute), nil
	}
	_, err := e.c.SubmitOrder(ctxFor("a"), &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1})
	wantCode(t, err, CodeDataUnavailable)
}

func TestInsufficientBalanceReportsAvailableAndRequired(t *testing.T) {
	e := newEnv(t)
	_, err := e.c.SubmitOrder(ctxFor("a"), &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 20}) // 2000 USD
	pe := wantCode(t, err, CodeInsufficientFunds)
	if !near(pe.Available, 1000) || pe.Required < 2000 || !strings.Contains(pe.Message, "available") || !strings.Contains(pe.Message, "required") {
		t.Fatalf("error = %+v", pe)
	}
	// spot sell without holdings
	_, err = e.c.SubmitOrder(ctxFor("a"), &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "sell", Type: "market", Amount: 1})
	wantCode(t, err, CodeInsufficientFunds)
}

// ── persistence and isolation between holders (5.4, 5.12) ────────────────────

func TestStateSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	if _, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1}); err != nil {
		t.Fatal(err)
	}
	pending, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "limit", Amount: 1, Price: 80})
	if err != nil {
		t.Fatal(err)
	}
	before := balanceOf(t, e, "a")
	e.rebuild() // "restart": a brand new client over the same database
	after := balanceOf(t, e, "a")
	if before["available"] != after["available"] || before["total"] != after["total"] {
		t.Fatalf("balance changed across restart: %v vs %v", before, after)
	}
	if pos, _ := e.c.GetPositions(ctx, "kucoin"); len(pos) != 1 {
		t.Fatalf("positions after restart = %v", pos)
	}
	if o, err := e.c.GetOrderInfo(ctx, "kucoin", pending.ID); err != nil || o.Status != "open" {
		t.Fatalf("pending order after restart = %+v %v", o, err)
	}
	// and the periodic process resumes it
	e.price = 79
	if r, _ := e.c.b.Tick(ctx); r.Filled != 1 {
		t.Fatalf("tick after restart: %+v", r)
	}
}

func TestTwoHoldersAreIsolated(t *testing.T) {
	e := newEnv(t)
	a, b := ctxFor("a"), ctxFor("b")
	oa, err := e.c.SubmitOrder(a, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "limit", Amount: 1, Price: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.SubmitOrder(a, &gct.SubmitOrderRequest{Pair: "ETH-USDT", Side: "buy", Type: "market", Amount: 1}); err != nil {
		t.Fatal(err)
	}
	if pos, _ := e.c.GetPositions(b, "kucoin"); len(pos) != 0 {
		t.Fatalf("b sees a's positions: %v", pos)
	}
	if orders, _ := e.c.GetOrders(b, "", "", "", "", nil, nil); len(orders) != 0 {
		t.Fatalf("b sees a's orders: %v", orders)
	}
	if bal := balanceOf(t, e, "b"); bal["available"].(float64) != 1000 {
		t.Fatalf("b balance affected: %v", bal)
	}
	if _, err := e.c.GetOrderInfo(b, "kucoin", oa.ID); err == nil {
		t.Fatal("b can read a's order")
	}
	wantCode(t, e.c.CancelOrder(b, "kucoin", oa.ID), CodeNotFound)
	if o, _ := e.c.GetOrderInfo(a, "kucoin", oa.ID); o.Status != "open" {
		t.Fatalf("a's order was modified by b: %+v", o)
	}
}

// ── same protections as real (5.5) ───────────────────────────────────────────

func TestSentinelAppliesCapAndKillSwitchInPaper(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	e.sent.capUSD = 50
	// cap: 1 BTC at 100 = 100 USD notional, computed like real (spot: amount*price)
	_, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1})
	if err == nil || !strings.Contains(err.Error(), "order rejected by risk sentinel") {
		t.Fatalf("cap error = %v", err)
	}
	if !near(e.sent.last.NotionalUSD, 100) || e.sent.last.NotionalUnknown {
		t.Fatalf("sentinel saw notional %+v", e.sent.last)
	}
	// within cap passes
	if _, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 0.4}); err != nil {
		t.Fatalf("within cap: %v", err)
	}
	// futures: USD target converted with the multiplier, like real
	e.mult = 1
	e.price = 10
	e.sent.capUSD = 0
	_, _ = e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "NEAR-USDTM", Side: "buy", Type: "market", Amount: 20, Leverage: 5})
	if !near(e.sent.last.NotionalUSD, 20) {
		t.Fatalf("futures notional = %v", e.sent.last.NotionalUSD)
	}
	// kill switch: opens rejected, closes allowed
	e.sent.killed = true
	_, err = e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 0.1})
	if err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("kill switch error = %v", err)
	}
	if err := e.c.CloseFuturesPosition(ctx, "kucoin", "NEARUSDTM"); err != nil {
		t.Fatalf("closing must stay possible under the kill switch: %v", err)
	}
	if pos, _ := e.c.GetFuturesPositions(ctx, "kucoin"); len(pos) != 0 {
		t.Fatalf("position not closed: %v", pos)
	}
}

// ── futures, margin and liquidation (5.3) ────────────────────────────────────

func TestFuturesMarginAndLiquidation(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	e.price, e.mult = 10, 1
	o, err := e.c.SubmitFuturesOrder(ctx, &gct.SubmitOrderRequest{Pair: "NEAR-USDTM", Side: "buy", Type: "market", Amount: 50, Leverage: 10})
	if err != nil {
		t.Fatal(err)
	}
	// 50 USD target / (10*1) = 5 contracts -> qty 5; margin = 5*10.005/10
	entry := 10 * 1.0005
	if !near(o.Amount, 5) || !near(o.Price, entry) {
		t.Fatalf("order = %+v", o)
	}
	margin := 5 * entry / 10
	pos, _ := e.c.GetFuturesPositions(ctx, "kucoin")
	if len(pos) != 1 || !near(pos[0].Margin, margin) || pos[0].Leverage != 10 || !pos[0].Paper {
		t.Fatalf("position = %+v", pos)
	}
	fee := 5 * entry * 0.0006
	if bal := balanceOf(t, e, "a"); !near(bal["available"].(float64), 1000-margin-fee) {
		t.Fatalf("balance = %v", bal)
	}
	// unrealized loss below margin: not liquidated
	e.price = 9.6
	if r, _ := e.c.b.Tick(ctx); r.Liquidated != 0 {
		t.Fatal("liquidated too early")
	}
	// loss equals margin: entry - margin/size = entry*(1-1/10)
	e.price = entry * 0.9
	r, err := e.c.b.Tick(ctx)
	if err != nil || r.Liquidated != 1 {
		t.Fatalf("tick: %+v %v", r, err)
	}
	if pos, _ := e.c.GetFuturesPositions(ctx, "kucoin"); len(pos) != 0 {
		t.Fatalf("position should be gone: %v", pos)
	}
	fills, _ := e.c.Fills(ctx)
	if len(fills) != 2 || !fills[0].Liquidation || !near(fills[0].RealizedPnL, -margin) {
		t.Fatalf("fills = %+v", fills)
	}
	if bal := balanceOf(t, e, "a"); !near(bal["available"].(float64), 1000-margin-fee) {
		t.Fatalf("margin must be lost, nothing returned: %v", bal)
	}
}

func TestFuturesShortReduceAndNoFlip(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	e.price, e.mult = 10, 1
	if _, err := e.c.SubmitFuturesOrder(ctx, &gct.SubmitOrderRequest{Pair: "NEAR-USDTM", Side: "sell", Type: "market", Amount: 100, Leverage: 5}); err != nil {
		t.Fatal(err)
	}
	// opposite order larger than the position without reduce-only: flipping unsupported
	_, err := e.c.SubmitFuturesOrder(ctx, &gct.SubmitOrderRequest{Pair: "NEAR-USDTM", Side: "buy", Type: "market", Amount: 1000, Leverage: 5})
	wantCode(t, err, CodeUnsupported)
	// price drops: the short wins on close
	e.price = 9
	if err := e.c.CloseFuturesPosition(ctx, "kucoin", "NEAR-USDTM"); err != nil {
		t.Fatal(err)
	}
	bal := balanceOf(t, e, "a")
	if bal["available"].(float64) <= 1000 {
		t.Fatalf("short should have profited: %v", bal)
	}
}

func TestSetLeverageAppliesToNextOrder(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	e.price, e.mult = 10, 1
	if err := e.c.SetLeverage(ctx, "kucoin", "NEARUSDTM", 4); err != nil {
		t.Fatal(err)
	}
	e.rebuild() // persisted
	if _, err := e.c.SubmitFuturesOrder(ctx, &gct.SubmitOrderRequest{Pair: "NEAR-USDTM", Side: "buy", Type: "market", Amount: 40, Leverage: 1}); err != nil {
		t.Fatal(err)
	}
	pos, _ := e.c.GetFuturesPositions(ctx, "kucoin")
	if len(pos) != 1 || pos[0].Leverage != 4 {
		t.Fatalf("position = %+v", pos)
	}
}

// ── reset (5.12) ─────────────────────────────────────────────────────────────

func TestResetClosesPositionsCancelsOrdersKeepsHistory(t *testing.T) {
	e := newEnv(t)
	ctx := ctxFor("a")
	other := ctxFor("b")
	if _, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "limit", Amount: 1, Price: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.SubmitOrder(other, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1}); err != nil {
		t.Fatal(err)
	}
	e.params.InitialBalanceUSD = 2500
	if err := e.c.b.ResetAccount(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	bal := balanceOf(t, e, "a")
	if !near(bal["available"].(float64), 2500) || !near(bal["total"].(float64), 2500) {
		t.Fatalf("balance after reset = %v", bal)
	}
	if pos, _ := e.c.GetPositions(ctx, "kucoin"); len(pos) != 0 {
		t.Fatalf("positions after reset = %v", pos)
	}
	if open, _ := e.c.GetOrders(ctx, "", "", "open", "", nil, nil); len(open) != 0 {
		t.Fatalf("open orders after reset = %v", open)
	}
	fills, _ := e.c.Fills(ctx)
	if len(fills) != 1 || !fills[0].ClosedByReset {
		t.Fatalf("history must be kept and flagged: %+v", fills)
	}
	if all, _ := e.c.GetOrders(ctx, "", "", "", "", nil, nil); len(all) != 2 {
		t.Fatalf("order history lost: %v", all)
	}
	// the other holder is untouched
	if pos, _ := e.c.GetPositions(other, "kucoin"); len(pos) != 1 {
		t.Fatal("reset affected another holder")
	}
	// the account keeps working afterwards
	if _, err := e.c.SubmitOrder(ctx, &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestSimulateOrderIsMarkedAndDoesNotPersist(t *testing.T) {
	e := newEnv(t)
	m, err := e.c.SimulateOrder(ctxFor("a"), &gct.SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Type: "market", Amount: 1})
	if err != nil || m["paper"] != true || m["assumptions"] == nil || m["sufficient_funds"] != true {
		t.Fatalf("simulate = %v %v", m, err)
	}
	if orders, _ := e.c.GetOrders(ctxFor("a"), "", "", "", "", nil, nil); len(orders) != 0 {
		t.Fatal("simulation persisted an order")
	}
}

func TestSchemaMatchesMigration(t *testing.T) {
	mig, err := os.ReadFile(filepath.Join("..", "storage", "migrations", "031_paper_broker.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mig) != schemaSQL {
		t.Fatal("internal/paper/schema.sql differs from storage/migrations/031_paper_broker.sql; keep them identical")
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	e := newEnv(t)
	e.c.b.cfg.PollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.c.b.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

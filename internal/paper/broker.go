package paper

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/risk"
)

//go:embed schema.sql
var schemaSQL string

const (
	defaultExchange    = "kucoin"
	defaultPriceMaxAge = 10 * time.Second
	defaultPollEvery   = 5 * time.Second
	eps                = 1e-9
)

// Params are the runtime-tunable paper parameters (dynamic config keys
// paper.initial_balance_usd, paper.slippage_pct and paper.taker_fee_pct).
type Params struct {
	InitialBalanceUSD float64
	SlippagePct       float64 // adverse slippage applied to market and triggered-stop fills
	TakerFeePct       float64 // commission charged on every fill
}

// DefaultParams mirrors the manifest defaults.
func DefaultParams() Params {
	return Params{InitialBalanceUSD: 1000, SlippagePct: 0.05, TakerFeePct: 0.06}
}

// PriceFunc returns the last real price of a symbol and the snapshot timestamp
// (zero when unknown). symbol is the futures symbol (NEARUSDTM) when futures is
// true, otherwise the spot pair as supplied by the caller.
type PriceFunc func(ctx context.Context, exchange, symbol string, futures bool) (float64, time.Time, error)

// MultiplierFunc returns the base units per contract of a futures symbol.
type MultiplierFunc func(ctx context.Context, symbol string) (float64, error)

// Config configures a paper broker.
type Config struct {
	// DB is the SQLite database. The paper tables are created if missing.
	DB *sql.DB
	// Inner is the real client. Only market data methods are ever forwarded to it.
	Inner gct.GCTClient
	// Sentinel is the same risk gate the real client uses; nil disables it,
	// exactly like a real client without a sentinel.
	Sentinel risk.RiskSentinel
	// Params returns the current paper parameters (hot-reloadable). nil = DefaultParams.
	Params func() Params
	// Price overrides the price source (default: Inner.GetTicker).
	Price PriceFunc
	// ContractMultiplier is required for futures orders.
	ContractMultiplier MultiplierFunc
	// PriceMaxAge is the oldest price snapshot accepted (default 10s).
	PriceMaxAge time.Duration
	// PollInterval is the Run period (default 5s).
	PollInterval time.Duration
}

// storeMu serializes every paper write in the process (one database per process).
var storeMu sync.Mutex

// Broker is the paper execution engine and its SQLite state.
type Broker struct {
	cfg Config
	db  *sql.DB
}

func newBroker(cfg Config) (*Broker, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("paper: DB is required")
	}
	if cfg.Inner == nil {
		return nil, fmt.Errorf("paper: Inner client is required")
	}
	if cfg.PriceMaxAge <= 0 {
		cfg.PriceMaxAge = defaultPriceMaxAge
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollEvery
	}
	if _, err := cfg.DB.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("paper: applying schema: %w", err)
	}
	return &Broker{cfg: cfg, db: cfg.DB}, nil
}

func (b *Broker) params() Params {
	p := DefaultParams()
	if b.cfg.Params != nil {
		p = b.cfg.Params()
	}
	if p.InitialBalanceUSD <= 0 {
		p.InitialBalanceUSD = DefaultParams().InitialBalanceUSD
	}
	if p.SlippagePct < 0 {
		p.SlippagePct = 0
	}
	if p.TakerFeePct < 0 {
		p.TakerFeePct = 0
	}
	return p
}

// Assumptions returns the execution assumptions as a map, for tool responses.
func (b *Broker) Assumptions() map[string]interface{} {
	p := b.params()
	return map[string]interface{}{
		"slippage_pct":    p.SlippagePct,
		"taker_fee_pct":   p.TakerFeePct,
		"funding_modeled": false,
		"partial_fills":   false,
		"liquidation":     "a leveraged position is closed when its unrealized loss equals its margin",
		"price_source":    "real last price from the exchange",
	}
}

// price fetches a recent real price; any failure is DATA_UNAVAILABLE.
func (b *Broker) price(ctx context.Context, exchange, symbol string, futures bool) (float64, error) {
	if exchange == "" {
		exchange = defaultExchange
	}
	fetch := b.cfg.Price
	if fetch == nil {
		fetch = func(ctx context.Context, exchange, symbol string, _ bool) (float64, time.Time, error) {
			t, err := b.cfg.Inner.GetTicker(ctx, exchange, symbol)
			if err != nil {
				return 0, time.Time{}, err
			}
			if t == nil {
				return 0, time.Time{}, fmt.Errorf("nil ticker")
			}
			return t.Last, t.Timestamp, nil
		}
	}
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	px, ts, err := fetch(lookup, exchange, symbol, futures)
	if err != nil {
		return 0, newErr(CodeDataUnavailable, "no recent price for %s: %v", symbol, err)
	}
	if px <= 0 {
		return 0, newErr(CodeDataUnavailable, "no recent price for %s: non-positive price", symbol)
	}
	if ts.Year() >= 2020 && time.Since(ts) > b.cfg.PriceMaxAge {
		return 0, newErr(CodeDataUnavailable, "price of %s is %s old (max %s)", symbol, time.Since(ts).Round(time.Second), b.cfg.PriceMaxAge)
	}
	return px, nil
}

func (b *Broker) multiplier(ctx context.Context, symbol string) (float64, error) {
	if b.cfg.ContractMultiplier == nil {
		return 0, newErr(CodeDataUnavailable, "no contract specification source for %s", symbol)
	}
	m, err := b.cfg.ContractMultiplier(ctx, symbol)
	if err != nil || m <= 0 {
		return 0, newErr(CodeDataUnavailable, "contract specification of %s unavailable: %v", symbol, err)
	}
	return m, nil
}

// riskEvaluator builds the real-client risk evaluation for one order, backed by
// the paper price and contract-spec sources.
func (b *Broker) riskEvaluator(exchange string) *gct.RiskEvaluator {
	return gct.NewRiskEvaluator(b.cfg.Sentinel,
		func(ctx context.Context, symbol string, futures bool) (float64, time.Time, error) {
			px, err := b.price(ctx, exchange, symbol, futures)
			return px, time.Time{}, err
		},
		b.multiplier)
}

// ── row types ────────────────────────────────────────────────────────────────

type dbq interface {
	ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row
}

type account struct {
	holder   string
	balance  float64
	initial  float64
	leverage map[string]float64
}

type position struct {
	id                                      int64
	exchange, pair, symbol, market, side    string
	size, entry, leverage, margin, realized float64
	status, closeReason                     string
	closePrice                              float64
	closedByReset                           bool
	openedAt                                time.Time
}

type orderRow struct {
	id, holder, exchange, pair, symbol, market string
	side, typ                                  string
	amount, qty, price, stopPrice              float64
	stopDir                                    string
	leverage                                   float64
	reduceOnly                                 bool
	reserved                                   float64
	status                                     string
	fillPrice, fee                             float64
	reason                                     string
	createdAt, updatedAt                       time.Time
}

const orderCols = `id, holder, exchange, pair, symbol, market, side, type, amount, qty, price, stop_price,
	stop_dir, leverage, reduce_only, reserved, status, fill_price, fee, reason, created_at, updated_at`

func scanOrder(sc interface{ Scan(...interface{}) error }) (*orderRow, error) {
	o := &orderRow{}
	var ro int
	err := sc.Scan(&o.id, &o.holder, &o.exchange, &o.pair, &o.symbol, &o.market, &o.side, &o.typ, &o.amount, &o.qty,
		&o.price, &o.stopPrice, &o.stopDir, &o.leverage, &ro, &o.reserved, &o.status, &o.fillPrice, &o.fee, &o.reason,
		&o.createdAt, &o.updatedAt)
	o.reduceOnly = ro != 0
	return o, err
}

const posCols = `id, exchange, pair, symbol, market, side, size, entry_price, leverage, margin, realized_pnl,
	status, close_reason, close_price, closed_by_reset, opened_at`

func scanPos(sc interface{ Scan(...interface{}) error }) (*position, error) {
	p := &position{}
	var cbr int
	err := sc.Scan(&p.id, &p.exchange, &p.pair, &p.symbol, &p.market, &p.side, &p.size, &p.entry, &p.leverage,
		&p.margin, &p.realized, &p.status, &p.closeReason, &p.closePrice, &cbr, &p.openedAt)
	p.closedByReset = cbr != 0
	return p, err
}

func (b *Broker) getOrCreateAccount(ctx context.Context, q dbq, holder string) (*account, error) {
	a := &account{holder: holder}
	var lev string
	err := q.QueryRowContext(ctx, `SELECT balance, initial_balance, leverage_json FROM paper_accounts WHERE holder = ?`, holder).
		Scan(&a.balance, &a.initial, &lev)
	if err == sql.ErrNoRows {
		init := b.params().InitialBalanceUSD
		if _, err := q.ExecContext(ctx, `INSERT INTO paper_accounts (holder, balance, initial_balance) VALUES (?, ?, ?)`, holder, init, init); err != nil {
			return nil, err
		}
		a.balance, a.initial, a.leverage = init, init, map[string]float64{}
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	a.leverage = map[string]float64{}
	_ = json.Unmarshal([]byte(lev), &a.leverage)
	return a, nil
}

func (b *Broker) setBalance(ctx context.Context, q dbq, holder string, balance float64) error {
	_, err := q.ExecContext(ctx, `UPDATE paper_accounts SET balance = ?, updated_at = CURRENT_TIMESTAMP WHERE holder = ?`, balance, holder)
	return err
}

// reservedFor sums the funds held by the holder's open orders, optionally
// excluding one order.
func (b *Broker) reservedFor(ctx context.Context, q dbq, holder, exceptOrder string) (float64, error) {
	var r sql.NullFloat64
	err := q.QueryRowContext(ctx, `SELECT SUM(reserved) FROM paper_orders WHERE holder = ? AND status = 'open' AND id != ?`, holder, exceptOrder).Scan(&r)
	return r.Float64, err
}

func (b *Broker) openPosition(ctx context.Context, q dbq, holder, symbol, market string) (*position, error) {
	p, err := scanPos(q.QueryRowContext(ctx, `SELECT `+posCols+` FROM paper_positions
		WHERE holder = ? AND symbol = ? AND market = ? AND status = 'open'`, holder, symbol, market))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (b *Broker) openPositions(ctx context.Context, q dbq, holder string) ([]*position, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+posCols+` FROM paper_positions WHERE holder = ? AND status = 'open' ORDER BY id`, holder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*position
	for rows.Next() {
		p, err := scanPos(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// lookupKey is the string used to ask the price source about a position/order.
func lookupKey(market, pair, symbol string) string {
	if market == marketFutures {
		return symbol
	}
	return pair
}

const (
	marketSpot    = "spot"
	marketFutures = "futures"
)

func isLong(side string) bool { return strings.EqualFold(side, "buy") || side == "long" }

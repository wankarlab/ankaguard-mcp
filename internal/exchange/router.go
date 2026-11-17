// Package exchange provides read-only multi-exchange observability primitives:
// a price matrix across active venues, best-venue selection, and a consolidated
// (synthetic) order book. It performs NO order placement.
//
// The package is decoupled from concrete clients via small local interfaces so
// it can be unit-tested with fakes. *gctclient.Client satisfies TickerSource and
// SymbolConverter; *server.ExchangeManager satisfies ExchangeSet.
package exchange

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// TickerSource fetches a ticker for a given exchange/pair.
// *gctclient.Client satisfies this.
type TickerSource interface {
	GetTicker(ctx context.Context, exchange, pair string) (*models.Ticker, error)
}

// SymbolConverter normalizes a generic symbol into an exchange-specific symbol.
// *gctclient.Client satisfies this.
type SymbolConverter interface {
	ConvertSymbol(symbol, marketType, exchange string) (string, error)
}

// ExchangeSet exposes the set of configured/enabled exchanges.
// *server.ExchangeManager satisfies this.
type ExchangeSet interface {
	GetEnabledExchanges() []string
	IsEnabled(exchange string) bool
}

// PriceQuote is a top-of-book snapshot for one exchange.
type PriceQuote struct {
	Exchange  string    `json:"exchange"`
	Bid       float64   `json:"bid"`
	Ask       float64   `json:"ask"`
	Last      float64   `json:"last"`
	Volume    float64   `json:"volume"`
	Symbol    string    `json:"symbol"`
	Timestamp time.Time `json:"timestamp"`
}

// Venue is the result of best-venue selection for a side.
type Venue struct {
	Exchange       string  `json:"exchange"`
	Symbol         string  `json:"symbol"`
	Price          float64 `json:"price"`
	EffectivePrice float64 `json:"effective_price"`
	Reason         string  `json:"reason"`
}

// ExchangeRouter is the read-only multi-exchange routing surface.
type ExchangeRouter interface {
	ActiveExchanges(ctx context.Context) ([]string, error)
	PriceMatrix(ctx context.Context, pair string) (map[string]PriceQuote, error)
	BestVenue(ctx context.Context, pair, side string, size float64) (Venue, error)
	ConsolidatedBook(ctx context.Context, pair string, depth int) (*ConsolidatedBook, error)
}

// Router is the concrete ExchangeRouter implementation.
type Router struct {
	tickers    TickerSource
	conv       SymbolConverter
	set        ExchangeSet
	marketType string // e.g. "futures" or "spot", used for ConvertSymbol

	// credsValid, when non-nil, filters ActiveExchanges to exchanges with valid
	// credentials. When nil, all enabled exchanges are considered active.
	credsValid func(exchange string) bool

	// books, when non-nil, supplies real order book depth for ConsolidatedBook.
	books OrderBookSource

	// fees maps lowercase exchange -> fee schedule, used by BestVenue/SplitOrder
	// to compute effective (fee+slippage-adjusted) prices. Defaults to
	// DefaultFeeSchedules(); override via SetFeeSchedules.
	fees map[string]FeeSchedule

	// sorEnabled gates smart-order-routing refinement. The package never places
	// orders; this flag only signals consumers that EV-based routing is desired.
	// Default false — no behavior change unless explicitly enabled.
	sorEnabled bool

	// submitter and execEnabled are used by the execution surface (execution.go).
	// Wired via SetOrderSubmitter and SetExecutionEnabled.
	submitter   OrderSubmitter
	execEnabled bool
}

var _ ExchangeRouter = (*Router)(nil)

// NewRouter builds a Router. marketType is forwarded to ConvertSymbol.
func NewRouter(tickers TickerSource, conv SymbolConverter, set ExchangeSet, marketType string) *Router {
	return &Router{
		tickers:    tickers,
		conv:       conv,
		set:        set,
		marketType: marketType,
		fees:       DefaultFeeSchedules(),
	}
}

// SetFeeSchedules overrides the per-exchange fee schedules used for effective
// price computation. Keys should be lowercase exchange names.
func (r *Router) SetFeeSchedules(fees map[string]FeeSchedule) {
	r.fees = fees
}

// SetSOREnabled toggles smart-order-routing refinement. Default is off.
func (r *Router) SetSOREnabled(enabled bool) {
	r.sorEnabled = enabled
}

// feeFor returns the fee schedule for an exchange, falling back to a zero
// schedule (no fees) when unknown.
func (r *Router) feeFor(exchange string) FeeSchedule {
	if r.fees == nil {
		return FeeSchedule{}
	}
	return r.fees[exchange]
}

// SetCredsValidator installs an optional credential filter for ActiveExchanges.
func (r *Router) SetCredsValidator(f func(exchange string) bool) {
	r.credsValid = f
}

// SetOrderBookSource installs an optional real depth feed for ConsolidatedBook.
func (r *Router) SetOrderBookSource(s OrderBookSource) {
	r.books = s
}

// ActiveExchanges returns enabled exchanges, optionally filtered by credsValid.
// It never crashes on a missing exchange; it simply returns the resolved set.
func (r *Router) ActiveExchanges(_ context.Context) ([]string, error) {
	if r.set == nil {
		return nil, nil
	}
	enabled := r.set.GetEnabledExchanges()
	if r.credsValid == nil {
		return enabled, nil
	}
	active := make([]string, 0, len(enabled))
	for _, ex := range enabled {
		if r.credsValid(ex) {
			active = append(active, ex)
		}
	}
	return active, nil
}

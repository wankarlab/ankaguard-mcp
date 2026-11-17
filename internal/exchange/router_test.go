package exchange

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// --- fakes ---

type fakeTickers struct {
	// keyed by "exchange|pair"
	data map[string]*models.Ticker
}

func (f *fakeTickers) GetTicker(_ context.Context, exchange, pair string) (*models.Ticker, error) {
	t, ok := f.data[exchange+"|"+pair]
	if !ok {
		return nil, fmt.Errorf("no ticker for %s %s", exchange, pair)
	}
	return t, nil
}

type identityConv struct{}

func (identityConv) ConvertSymbol(symbol, _ string, _ string) (string, error) { return symbol, nil }

type fakeSet struct{ exchanges []string }

func (f fakeSet) GetEnabledExchanges() []string { return f.exchanges }
func (f fakeSet) IsEnabled(ex string) bool {
	for _, e := range f.exchanges {
		if e == ex {
			return true
		}
	}
	return false
}

func newTestRouter(data map[string]*models.Ticker) *Router {
	return NewRouter(
		&fakeTickers{data: data},
		identityConv{},
		fakeSet{exchanges: []string{"kucoin", "binance"}},
		"futures",
	)
}

// --- tests ---

func TestPriceMatrixReturnsBothVenues(t *testing.T) {
	r := newTestRouter(map[string]*models.Ticker{
		"kucoin|BTC-USDT":  {Exchange: "kucoin", Bid: 100, Ask: 101, Last: 100.5, Volume: 5},
		"binance|BTC-USDT": {Exchange: "binance", Bid: 99, Ask: 100, Last: 99.5, Volume: 8},
	})

	m, err := r.PriceMatrix(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 venues, got %d (%v)", len(m), m)
	}
	if m["kucoin"].Ask != 101 || m["binance"].Bid != 99 {
		t.Fatalf("unexpected quotes: %+v", m)
	}
}

func TestPriceMatrixSkipsErroringExchange(t *testing.T) {
	r := newTestRouter(map[string]*models.Ticker{
		// only kucoin has data; binance fetch will error and be skipped
		"kucoin|BTC-USDT": {Exchange: "kucoin", Bid: 100, Ask: 101, Last: 100.5},
	})

	m, err := r.PriceMatrix(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("want 1 venue, got %d (%v)", len(m), m)
	}
	if _, ok := m["kucoin"]; !ok {
		t.Fatalf("expected kucoin to survive, got %v", m)
	}
}

func TestPriceMatrixAllFailReturnsError(t *testing.T) {
	r := newTestRouter(map[string]*models.Ticker{})
	if _, err := r.PriceMatrix(context.Background(), "BTC-USDT"); err == nil {
		t.Fatal("expected error when all exchanges fail")
	}
}

func TestBestVenueBuyPicksLowestAsk(t *testing.T) {
	r := newTestRouter(map[string]*models.Ticker{
		"kucoin|BTC-USDT":  {Exchange: "kucoin", Bid: 100, Ask: 101},
		"binance|BTC-USDT": {Exchange: "binance", Bid: 99, Ask: 100},
	})

	v, err := r.BestVenue(context.Background(), "BTC-USDT", "buy", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With default fees binance (taker 4bps) effective cost stays below kucoin's
	// ask; volume=0 → slippage=0, so effective price = 100*(1+0.0004)=100.04.
	if v.Exchange != "binance" || v.Price != 100 {
		t.Fatalf("buy should pick binance@100, got %+v", v)
	}
	if v.EffectivePrice <= v.Price {
		t.Fatalf("buy effective price should exceed raw price (fees), got %+v", v)
	}
}

func TestBestVenueSellPicksHighestBid(t *testing.T) {
	r := newTestRouter(map[string]*models.Ticker{
		"kucoin|BTC-USDT":  {Exchange: "kucoin", Bid: 100, Ask: 101},
		"binance|BTC-USDT": {Exchange: "binance", Bid: 99, Ask: 100},
	})

	v, err := r.BestVenue(context.Background(), "BTC-USDT", "sell", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Exchange != "kucoin" || v.Price != 100 {
		t.Fatalf("sell should pick kucoin@100, got %+v", v)
	}
}

func TestBestVenueFeeFlipsChoice(t *testing.T) {
	// binance has the lower RAW ask (100) but a punitive taker fee that makes its
	// effective cost worse than kucoin (raw ask 101, cheap fee) → pick kucoin.
	r := newTestRouter(map[string]*models.Ticker{
		"kucoin|BTC-USDT":  {Exchange: "kucoin", Bid: 100, Ask: 101},
		"binance|BTC-USDT": {Exchange: "binance", Bid: 99, Ask: 100},
	})
	r.SetFeeSchedules(map[string]FeeSchedule{
		"kucoin":  {MakerBps: 1, TakerBps: 5},   // 101*1.0005 = 101.0505
		"binance": {MakerBps: 50, TakerBps: 200}, // 100*1.02   = 102.0
	})

	v, err := r.BestVenue(context.Background(), "BTC-USDT", "buy", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Exchange != "kucoin" {
		t.Fatalf("high binance fee should flip choice to kucoin, got %+v", v)
	}
	if v.Price != 101 {
		t.Fatalf("kucoin raw ask should be 101, got %+v", v)
	}
}

func TestSplitOrderSmallSizeSingleAllocation(t *testing.T) {
	// Deep top-of-book (Volume large) → negligible slippage → no incentive to
	// split; expect a single allocation equal to the full size.
	r := newTestRouter(map[string]*models.Ticker{
		"kucoin|BTC-USDT":  {Exchange: "kucoin", Bid: 100, Ask: 101, Volume: 1_000_000},
		"binance|BTC-USDT": {Exchange: "binance", Bid: 99, Ask: 100, Volume: 1_000_000},
	})

	allocs, err := r.SplitOrder(context.Background(), "BTC-USDT", "buy", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(allocs) != 1 {
		t.Fatalf("small size should yield single allocation, got %d (%+v)", len(allocs), allocs)
	}
	if allocs[0].SizeBase != 1 {
		t.Fatalf("allocation size must equal requested size, got %v", allocs[0].SizeBase)
	}
}

func TestSplitOrderOversizedSplits(t *testing.T) {
	// Thin top-of-book on both venues → big order incurs heavy slippage; splitting
	// across the two venues lowers total cost, so expect 2 allocations summing to size.
	r := newTestRouter(map[string]*models.Ticker{
		"kucoin|BTC-USDT":  {Exchange: "kucoin", Bid: 100, Ask: 101, Volume: 10},
		"binance|BTC-USDT": {Exchange: "binance", Bid: 99, Ask: 100, Volume: 10},
	})

	size := 8.0
	allocs, err := r.SplitOrder(context.Background(), "BTC-USDT", "buy", size)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(allocs) != 2 {
		t.Fatalf("oversized order should split across 2 venues, got %d (%+v)", len(allocs), allocs)
	}
	var sum float64
	exch := map[string]bool{}
	for _, a := range allocs {
		sum += a.SizeBase
		exch[a.Exchange] = true
	}
	if math.Abs(sum-size) > 1e-9 {
		t.Fatalf("allocations must sum to size %v, got %v", size, sum)
	}
	if len(exch) != 2 {
		t.Fatalf("split should use two distinct venues, got %v", exch)
	}
}

func TestConsolidatedBookDetectsCrossedBook(t *testing.T) {
	// binance bid (102) > kucoin ask (101) => crossed arbitrage signal.
	r := newTestRouter(map[string]*models.Ticker{
		"kucoin|BTC-USDT":  {Exchange: "kucoin", Bid: 100, Ask: 101},
		"binance|BTC-USDT": {Exchange: "binance", Bid: 102, Ask: 103},
	})

	book, err := r.ConsolidatedBook(context.Background(), "BTC-USDT", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !book.Crossed {
		t.Fatalf("expected crossed book, got %+v", book)
	}
	if book.BestBid.Exchange != "binance" || book.BestBid.Price != 102 {
		t.Fatalf("best bid should be binance@102, got %+v", book.BestBid)
	}
	if book.BestAsk.Exchange != "kucoin" || book.BestAsk.Price != 101 {
		t.Fatalf("best ask should be kucoin@101, got %+v", book.BestAsk)
	}
}

func TestDetectCrossedBookSameExchangeNotCrossed(t *testing.T) {
	// Same exchange on both sides with bid>ask must NOT be flagged as arbitrage.
	bids := []ConsolidatedLevel{{Exchange: "kucoin", Price: 102}}
	asks := []ConsolidatedLevel{{Exchange: "kucoin", Price: 101}}
	_, _, crossed := detectCrossedBook(bids, asks)
	if crossed {
		t.Fatal("same-exchange inverted quotes must not be crossed")
	}
}

func TestActiveExchangesCredsFilter(t *testing.T) {
	r := newTestRouter(map[string]*models.Ticker{})
	r.SetCredsValidator(func(ex string) bool { return ex == "kucoin" })
	got, err := r.ActiveExchanges(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "kucoin" {
		t.Fatalf("creds filter should yield [kucoin], got %v", got)
	}
}

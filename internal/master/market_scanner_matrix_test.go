package master

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/regime"
	"github.com/sirupsen/logrus"
)

// --- mocks ------------------------------------------------------------------

type mockGCT struct {
	// pairsByExchange maps an exchange name to its top tickers.
	pairsByExchange map[string][]TickerSummary
	// failExchanges marks exchanges whose GetTopPairsByVolume should error.
	failExchanges map[string]bool
}

func (m *mockGCT) GetTopPairsByVolume(_ context.Context, exchange string, _ int) ([]TickerSummary, error) {
	if m.failExchanges[exchange] {
		return nil, fmt.Errorf("mock: %s unavailable", exchange)
	}
	return m.pairsByExchange[exchange], nil
}

func (m *mockGCT) GetCandles(_ context.Context, _, _, _ string, limit int) ([]ai.Candle, error) {
	// Return enough flat candles to clear the >=50 gate. Values are arbitrary;
	// the mock regime analyzer ignores them.
	out := make([]ai.Candle, limit)
	for i := range out {
		out[i] = ai.Candle{Open: 100, High: 101, Low: 99, Close: 100, Volume: 10}
	}
	return out, nil
}

type mockRegime struct{}

func (mockRegime) Analyze(_ []ai.Candle) (*regime.RegimeAnalysis, error) {
	return &regime.RegimeAnalysis{
		ATRPercent:              0.01,
		ADX:                     25,
		SuggestedSizeMultiplier: 0.5,
	}, nil
}

func newTestScanner(gct *mockGCT) *MarketScanner {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel) // silence test logs
	return NewMarketScanner(log, gct, mockRegime{}, nil, nil, nil)
}

// --- ScanMatrix tests -------------------------------------------------------

func TestScanMatrix_NoExchanges(t *testing.T) {
	ms := newTestScanner(&mockGCT{})
	if _, err := ms.ScanMatrix(context.Background(), nil, 5); err == nil {
		t.Fatal("expected error for empty exchanges, got nil")
	}
}

func TestScanMatrix_SingleExchangeEqualsScan(t *testing.T) {
	gct := &mockGCT{pairsByExchange: map[string][]TickerSummary{
		"kucoin": {
			{Pair: "BTC-USDT", Volume24h: 1_000_000},
			{Pair: "ETH-USDT", Volume24h: 2_000_000},
			{Pair: "SOL-USDT", Volume24h: 500_000},
		},
	}}
	ms := newTestScanner(gct)

	scan, err := ms.Scan(context.Background(), "kucoin", 5)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	matrix, err := ms.ScanMatrix(context.Background(), []string{"kucoin"}, 5)
	if err != nil {
		t.Fatalf("ScanMatrix: %v", err)
	}
	if len(scan) != len(matrix) {
		t.Fatalf("len mismatch: scan=%d matrix=%d", len(scan), len(matrix))
	}
	for i := range scan {
		if scan[i].Pair != matrix[i].Pair {
			t.Errorf("order mismatch at %d: scan=%s matrix=%s", i, scan[i].Pair, matrix[i].Pair)
		}
		if scan[i].OpportunityScore != matrix[i].OpportunityScore {
			t.Errorf("score mismatch at %d (%s)", i, scan[i].Pair)
		}
		if matrix[i].Exchange != "kucoin" {
			t.Errorf("expected Exchange=kucoin, got %q", matrix[i].Exchange)
		}
	}
}

func TestScanMatrix_MultiExchangeSortedAndTagged(t *testing.T) {
	gct := &mockGCT{pairsByExchange: map[string][]TickerSummary{
		"kucoin":  {{Pair: "BTC-USDT", Volume24h: 1_000_000}},
		"binance": {{Pair: "ETH-USDT", Volume24h: 5_000_000}},
	}}
	ms := newTestScanner(gct)

	out, err := ms.ScanMatrix(context.Background(), []string{"kucoin", "binance"}, 5)
	if err != nil {
		t.Fatalf("ScanMatrix: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 opportunities, got %d", len(out))
	}
	// Combined slice must be sorted by score desc.
	if out[0].OpportunityScore < out[1].OpportunityScore {
		t.Errorf("not sorted desc: %f < %f", out[0].OpportunityScore, out[1].OpportunityScore)
	}
	// Each opportunity carries its surfacing exchange.
	seen := map[string]bool{}
	for _, o := range out {
		if o.Exchange == "" {
			t.Errorf("pair %s missing exchange tag", o.Pair)
		}
		seen[o.Exchange] = true
	}
	if !seen["kucoin"] || !seen["binance"] {
		t.Errorf("expected both exchanges tagged, got %v", seen)
	}
}

func TestScanMatrix_PartialFailureContinues(t *testing.T) {
	gct := &mockGCT{
		pairsByExchange: map[string][]TickerSummary{
			"kucoin": {{Pair: "BTC-USDT", Volume24h: 1_000_000}},
		},
		failExchanges: map[string]bool{"binance": true},
	}
	ms := newTestScanner(gct)

	out, err := ms.ScanMatrix(context.Background(), []string{"kucoin", "binance"}, 5)
	if err != nil {
		t.Fatalf("partial failure should not error: %v", err)
	}
	if len(out) != 1 || out[0].Exchange != "kucoin" {
		t.Fatalf("expected 1 kucoin opportunity, got %+v", out)
	}
}

func TestScanMatrix_AllFailReturnsError(t *testing.T) {
	gct := &mockGCT{failExchanges: map[string]bool{"kucoin": true, "binance": true}}
	ms := newTestScanner(gct)

	if _, err := ms.ScanMatrix(context.Background(), []string{"kucoin", "binance"}, 5); err == nil {
		t.Fatal("expected error when all exchanges fail, got nil")
	}
}

// --- DetectCrossExchangeArb tests -------------------------------------------

func TestDetectCrossExchangeArb(t *testing.T) {
	opps := []*MarketOpportunity{
		{Pair: "BTC-USDT", Exchange: "kucoin", OpportunityScore: 72.0},
		{Pair: "BTC-USDT", Exchange: "binance", OpportunityScore: 65.0}, // diff=7 >= 5 → signal
		{Pair: "ETH-USDT", Exchange: "kucoin", OpportunityScore: 60.0},
		{Pair: "ETH-USDT", Exchange: "binance", OpportunityScore: 58.0}, // diff=2 < 5 → no signal
		{Pair: "SOL-USDT", Exchange: "kucoin", OpportunityScore: 90.0},  // only one exchange → no signal
	}

	signals := DetectCrossExchangeArb(opps, 5.0)
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d: %v", len(signals), signals)
	}
	s := signals[0]
	if !strings.HasPrefix(s, "BTC-USDT:") {
		t.Errorf("signal should be for BTC-USDT, got %q", s)
	}
	for _, want := range []string{"kucoin=72.0", "binance=65.0", "diff=7.0"} {
		if !strings.Contains(s, want) {
			t.Errorf("signal %q missing %q", s, want)
		}
	}
}

func TestDetectCrossExchangeArb_Empty(t *testing.T) {
	if got := DetectCrossExchangeArb(nil, 5.0); len(got) != 0 {
		t.Errorf("expected no signals for nil input, got %v", got)
	}
	// Single-exchange-per-pair input yields nothing.
	one := []*MarketOpportunity{{Pair: "BTC-USDT", Exchange: "kucoin", OpportunityScore: 80}}
	if got := DetectCrossExchangeArb(one, 0.0); len(got) != 0 {
		t.Errorf("expected no signals for single-exchange pairs, got %v", got)
	}
}

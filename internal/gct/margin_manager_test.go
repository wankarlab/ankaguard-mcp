package gct

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func newTestLogger() *logrus.Logger {
	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	return l
}

func newTestMarginManager(t *testing.T) *MarginManager {
	t.Helper()
	// KuCoinRESTClient with no credentials — sufficient for unit tests because
	// PlaceMarginOrder returns ErrMarginNotImplemented before any HTTP call.
	kc := NewKuCoinRESTClient()
	mm, err := NewMarginManager(kc, newTestLogger())
	if err != nil {
		t.Fatalf("NewMarginManager: %v", err)
	}
	return mm
}

// ─── constructor tests ────────────────────────────────────────────────────────

func TestNewMarginManager_NilClient(t *testing.T) {
	_, err := NewMarginManager(nil, newTestLogger())
	if err == nil {
		t.Fatal("expected error for nil kucoinClient, got nil")
	}
}

func TestNewMarginManager_NilLogger(t *testing.T) {
	kc := NewKuCoinRESTClient()
	_, err := NewMarginManager(kc, nil)
	if err == nil {
		t.Fatal("expected error for nil logger, got nil")
	}
}

func TestNewMarginManager_OK(t *testing.T) {
	mm := newTestMarginManager(t)
	if mm == nil {
		t.Fatal("expected non-nil MarginManager")
	}
}

// ─── gate tests (via client struct) ──────────────────────────────────────────

// buildTestClientForMargin returns a minimal *client with a real KuCoinRESTClient
// and a logrus logger so ensureMarginManager succeeds.
func buildTestClientForMargin(t *testing.T, enabled bool) *client {
	t.Helper()
	kc := NewKuCoinRESTClient()
	c := &client{
		kucoinClient:  kc,
		marginEnabled: enabled,
		logger:        newTestLogger(),
		restOnlyPairs: make(map[string]bool),
		restCB:        newRestCircuitBreaker(5, 0),
	}
	return c
}

func TestSubmitOrder_MarginDisabled_ReturnsErrMarginDisabled(t *testing.T) {
	c := buildTestClientForMargin(t, false /* disabled */)

	req := &SubmitOrderRequest{
		Exchange:   "kucoin",
		Pair:       "BTC-USDT",
		Side:       "buy",
		Type:       "market",
		Amount:     0.001,
		MarketType: MarketTypeMargin,
	}

	// SubmitOrder requires a live gRPC connection for non-margin paths, but the
	// margin gate fires before any gRPC call, so no connection is needed here.
	_, err := c.SubmitOrder(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when margin is disabled, got nil")
	}
	if !errors.Is(err, ErrMarginDisabled) {
		t.Fatalf("expected ErrMarginDisabled, got: %v", err)
	}
}

func TestSubmitOrder_MarginEnabled_ReturnsErrMarginNotImplemented(t *testing.T) {
	c := buildTestClientForMargin(t, true /* enabled */)

	req := &SubmitOrderRequest{
		Exchange:   "kucoin",
		Pair:       "BTC-USDT",
		Side:       "buy",
		Type:       "market",
		Amount:     0.001,
		MarketType: MarketTypeMargin,
	}

	_, err := c.SubmitOrder(context.Background(), req)
	if err == nil {
		t.Fatal("expected error (endpoint not wired), got nil")
	}
	if !errors.Is(err, ErrMarginNotImplemented) {
		t.Fatalf("expected ErrMarginNotImplemented, got: %v", err)
	}
}

// ─── PlaceMarginOrder direct tests ───────────────────────────────────────────

func TestPlaceMarginOrder_NilRequest(t *testing.T) {
	mm := newTestMarginManager(t)
	_, err := mm.PlaceMarginOrder(context.Background(), nil)
	if !errors.Is(err, ErrNilRequest) {
		t.Fatalf("expected ErrNilRequest, got: %v", err)
	}
}

func TestPlaceMarginOrder_MissingExchange(t *testing.T) {
	mm := newTestMarginManager(t)
	req := &SubmitOrderRequest{Pair: "BTC-USDT", Side: "buy", Amount: 0.001, Type: "market"}
	_, err := mm.PlaceMarginOrder(context.Background(), req)
	if err == nil || errors.Is(err, ErrMarginNotImplemented) {
		t.Fatalf("expected validation error for missing exchange, got: %v", err)
	}
}

func TestPlaceMarginOrder_MissingPair(t *testing.T) {
	mm := newTestMarginManager(t)
	req := &SubmitOrderRequest{Exchange: "kucoin", Side: "buy", Amount: 0.001, Type: "market"}
	_, err := mm.PlaceMarginOrder(context.Background(), req)
	if err == nil || errors.Is(err, ErrMarginNotImplemented) {
		t.Fatalf("expected validation error for missing pair, got: %v", err)
	}
}

func TestPlaceMarginOrder_InvalidSide(t *testing.T) {
	mm := newTestMarginManager(t)
	req := &SubmitOrderRequest{Exchange: "kucoin", Pair: "BTC-USDT", Side: "long", Amount: 0.001, Type: "market"}
	_, err := mm.PlaceMarginOrder(context.Background(), req)
	if err == nil || errors.Is(err, ErrMarginNotImplemented) {
		t.Fatalf("expected validation error for invalid side, got: %v", err)
	}
}

func TestPlaceMarginOrder_ZeroAmount(t *testing.T) {
	mm := newTestMarginManager(t)
	req := &SubmitOrderRequest{Exchange: "kucoin", Pair: "BTC-USDT", Side: "buy", Amount: 0, Type: "market"}
	_, err := mm.PlaceMarginOrder(context.Background(), req)
	if err == nil || errors.Is(err, ErrMarginNotImplemented) {
		t.Fatalf("expected validation error for zero amount, got: %v", err)
	}
}

func TestPlaceMarginOrder_LimitMissingPrice(t *testing.T) {
	mm := newTestMarginManager(t)
	req := &SubmitOrderRequest{Exchange: "kucoin", Pair: "BTC-USDT", Side: "buy", Amount: 0.001, Type: "limit", Price: 0}
	_, err := mm.PlaceMarginOrder(context.Background(), req)
	if err == nil || errors.Is(err, ErrMarginNotImplemented) {
		t.Fatalf("expected validation error for limit with no price, got: %v", err)
	}
}

// Valid requests reach the dispatch stub and return ErrMarginNotImplemented.
func TestPlaceMarginOrder_ValidRequest_ReturnsNotImplemented(t *testing.T) {
	mm := newTestMarginManager(t)
	req := &SubmitOrderRequest{
		Exchange: "kucoin",
		Pair:     "BTC-USDT",
		Side:     "buy",
		Type:     "market",
		Amount:   0.001,
	}
	_, err := mm.PlaceMarginOrder(context.Background(), req)
	if !errors.Is(err, ErrMarginNotImplemented) {
		t.Fatalf("expected ErrMarginNotImplemented for valid request, got: %v", err)
	}
}

// ─── symbol conversion tests ──────────────────────────────────────────────────

var symbolConversionCases = []struct {
	input    string
	expected string
}{
	{"BTC-USDT", "BTC-USDT"},
	{"btc-usdt", "BTC-USDT"},
	{"BTC_USDT", "BTC-USDT"},
	{"BTCUSDTM", "BTC-USDT"}, // futures symbol stripped
	{"ETH-USDT", "ETH-USDT"},
	{"ETHUSDTM", "ETH-USDT"},
	{"DOGE-USDT", "DOGE-USDT"},
	{"DOGEUSDTM", "DOGE-USDT"},
	{"", ""},
}

func TestConvertPairToMarginSymbol(t *testing.T) {
	for _, tc := range symbolConversionCases {
		got := convertPairToMarginSymbol(tc.input)
		if got != tc.expected {
			t.Errorf("convertPairToMarginSymbol(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// ─── SetMarginEnabled race-safety smoke test ──────────────────────────────────

func TestSetMarginEnabled_Toggle(t *testing.T) {
	c := buildTestClientForMargin(t, false)

	// Starts disabled.
	if c.marginEnabled {
		t.Fatal("expected marginEnabled=false initially")
	}

	c.SetMarginEnabled(true)
	if !c.marginEnabled {
		t.Fatal("expected marginEnabled=true after SetMarginEnabled(true)")
	}

	c.SetMarginEnabled(false)
	if c.marginEnabled {
		t.Fatal("expected marginEnabled=false after SetMarginEnabled(false)")
	}
}

package exchange

import (
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
)

func TestNormalizeBinanceSymbol(t *testing.T) {
	cases := []struct {
		input string
		mt    gct.MarketType
		want  string
	}{
		{"BTC-USDT", gct.MarketTypeSpot, "BTCUSDT"},
		{"ETH-USDT", gct.MarketTypeFutures, "ETHUSDT"},
		{"SOL-USDT", gct.MarketTypeSpot, "SOLUSDT"},
		{"btc-usdt", gct.MarketTypeSpot, "BTCUSDT"},
	}
	for _, c := range cases {
		got := normalizeBinanceSymbol(c.input, c.mt)
		if got != c.want {
			t.Errorf("normalizeBinanceSymbol(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestToBinanceFuturesSymbol(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"BTCUSDT", "BTCUSDTPERP"},
		{"BTCUSDTPERP", "BTCUSDTPERP"}, // idempotent
		{"BTCUSDT_240329", "BTCUSDT_240329"}, // dated contract: unchanged
	}
	for _, c := range cases {
		got := toBinanceFuturesSymbol(c.input)
		if got != c.want {
			t.Errorf("toBinanceFuturesSymbol(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestGetBinanceContractSpec_Known(t *testing.T) {
	spec := GetBinanceContractSpec("BTCUSDTPERP")
	if spec.ContractMult != 1.0 {
		t.Errorf("expected ContractMult=1.0, got %v", spec.ContractMult)
	}
	if spec.MaxLeverage != 125 {
		t.Errorf("expected MaxLeverage=125, got %v", spec.MaxLeverage)
	}
}

func TestGetBinanceContractSpec_Unknown(t *testing.T) {
	spec := GetBinanceContractSpec("UNKNOWNUSDTPERP")
	if spec.ContractMult != 1.0 {
		t.Errorf("expected ContractMult=1.0 default, got %v", spec.ContractMult)
	}
	if spec.MaxLeverage != 50 {
		t.Errorf("expected MaxLeverage=50 default, got %v", spec.MaxLeverage)
	}
}

func TestBinanceVenueAdapter_LeverageCap(t *testing.T) {
	req := &gct.SubmitOrderRequest{
		Exchange:   "binance",
		Pair:       "BTC-USDT",
		MarketType: gct.MarketTypeFutures,
		Leverage:   200, // exceeds max 125
	}
	err := validateBinanceMarginMode(req)
	if err == nil {
		t.Error("expected error for leverage > 125, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds max 125") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBinanceVenueAdapter_LeverageOK(t *testing.T) {
	req := &gct.SubmitOrderRequest{
		Exchange:   "binance",
		Pair:       "BTC-USDT",
		MarketType: gct.MarketTypeFutures,
		Leverage:   50,
	}
	if err := validateBinanceMarginMode(req); err != nil {
		t.Errorf("unexpected error for leverage=50: %v", err)
	}
}

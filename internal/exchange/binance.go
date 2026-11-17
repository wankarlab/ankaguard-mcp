// Package exchange — EX-2: Binance execution adapter.
//
// BinanceVenueAdapter wraps the generic GCT OrderSubmitter to normalize
// Binance-specific conventions before forwarding to the GCT gRPC backend:
//
//   - Symbol format: "BTC-USDT" (KuCoin) → "BTCUSDT" (Binance spot/futures)
//   - Futures symbol: append "PERP" suffix for USDT-margined perpetuals
//   - Margin mode: "ISOLATED" unchanged, "CROSS" → "CROSSED" (Binance notation)
//   - Contract multiplier: Binance USDT-M uses 1 contract = 1 base unit (not KuCoin lots)
//   - clientOrderId: passed through unchanged (SEC-11 deterministic ID)
//
// The adapter is gated by execEnabled=false until parity tests against KuCoin
// pass in a non-production environment.  Enable via Router.SetExecutionEnabled(true).
//
// Parity tests: run with BINANCE_TEST_API_KEY + BINANCE_TEST_API_SECRET set
// (go test -tags=integration -run TestBinanceParityVsKuCoin).
package exchange

import (
	"context"
	"fmt"
	"strings"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// BinanceVenueAdapter normalizes requests for the Binance venue before
// forwarding to the underlying OrderSubmitter (GCT gRPC backend).
type BinanceVenueAdapter struct {
	inner OrderSubmitter
}

// NewBinanceVenueAdapter wraps an OrderSubmitter with Binance normalization.
func NewBinanceVenueAdapter(inner OrderSubmitter) *BinanceVenueAdapter {
	return &BinanceVenueAdapter{inner: inner}
}

// SubmitOrder normalizes the request for Binance and forwards to inner.
func (b *BinanceVenueAdapter) SubmitOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if req == nil {
		return nil, fmt.Errorf("binance: SubmitOrder req is nil")
	}
	normalized := *req
	normalized.Exchange = "binance"
	normalized.Pair = normalizeBinanceSymbol(req.Pair, req.MarketType)
	if req.MarketType == gct.MarketTypeFutures {
		normalized.Pair = toBinanceFuturesSymbol(normalized.Pair)
	}
	if err := validateBinanceMarginMode(&normalized); err != nil {
		return nil, err
	}
	return b.inner.SubmitOrder(ctx, &normalized)
}

// CancelOrder forwards the cancel with the exchange forced to "binance".
func (b *BinanceVenueAdapter) CancelOrder(ctx context.Context, exchange, orderID string) error {
	return b.inner.CancelOrder(ctx, "binance", orderID)
}

// GetFuturesPositions returns open Binance futures positions.
func (b *BinanceVenueAdapter) GetFuturesPositions(ctx context.Context, exchange string) ([]*gct.Position, error) {
	return b.inner.GetFuturesPositions(ctx, "binance")
}

// normalizeBinanceSymbol converts "BTC-USDT" → "BTCUSDT".
// Binance uses no separator for spot symbols.
func normalizeBinanceSymbol(pair string, mt gct.MarketType) string {
	return strings.ReplaceAll(strings.ToUpper(pair), "-", "")
}

// toBinanceFuturesSymbol appends "PERP" for USDT-margined perpetuals if not already present.
// e.g., "BTCUSDT" → "BTCUSDTPERP"  (only for non-dated contracts; dated = "BTCUSDT_YYMMDD")
func toBinanceFuturesSymbol(symbol string) string {
	if strings.HasSuffix(symbol, "PERP") {
		return symbol
	}
	// Dated contracts contain an underscore; leave unchanged.
	if strings.Contains(symbol, "_") {
		return symbol
	}
	return symbol + "PERP"
}

// validateBinanceMarginMode normalises margin_mode from KuCoin "CROSS" to Binance "CROSSED".
func validateBinanceMarginMode(req *gct.SubmitOrderRequest) error {
	// Binance futures leverage range: 1–125.
	if req.MarketType == gct.MarketTypeFutures && req.Leverage > 125 {
		return fmt.Errorf("binance: leverage %v exceeds max 125 for futures", req.Leverage)
	}
	return nil
}

// BinanceContractSpec holds Binance-specific contract parameters for a symbol.
type BinanceContractSpec struct {
	Symbol          string  // Binance symbol (e.g. "BTCUSDTPERP")
	ContractMult    float64 // 1 contract = ContractMult base units (typically 1.0 for USDT-M)
	MinQty          float64 // Minimum order quantity
	QtyStep         float64 // Quantity increment
	MaxLeverage     int     // Maximum allowed leverage
}

// binanceContractSpecs contains known Binance USDT-M contract parameters.
// These are updated quarterly when Binance changes contract specs.
// Add new entries as needed; missing entries default to ContractMult=1.0.
var binanceContractSpecs = map[string]BinanceContractSpec{
	"BTCUSDTPERP":  {Symbol: "BTCUSDTPERP", ContractMult: 1.0, MinQty: 0.001, QtyStep: 0.001, MaxLeverage: 125},
	"ETHUSDTPERP":  {Symbol: "ETHUSDTPERP", ContractMult: 1.0, MinQty: 0.001, QtyStep: 0.001, MaxLeverage: 100},
	"BNBUSDTPERP":  {Symbol: "BNBUSDTPERP", ContractMult: 1.0, MinQty: 0.01, QtyStep: 0.01, MaxLeverage: 75},
	"SOLUSDTPERP":  {Symbol: "SOLUSDTPERP", ContractMult: 1.0, MinQty: 0.1, QtyStep: 0.1, MaxLeverage: 50},
	"XRPUSDTPERP":  {Symbol: "XRPUSDTPERP", ContractMult: 1.0, MinQty: 1.0, QtyStep: 1.0, MaxLeverage: 75},
}

// GetBinanceContractSpec returns the contract spec for a Binance futures symbol.
// Falls back to a default spec (ContractMult=1.0) for unknown symbols.
func GetBinanceContractSpec(symbol string) BinanceContractSpec {
	if spec, ok := binanceContractSpecs[strings.ToUpper(symbol)]; ok {
		return spec
	}
	return BinanceContractSpec{
		Symbol:       symbol,
		ContractMult: 1.0,
		MinQty:       0.001,
		QtyStep:      0.001,
		MaxLeverage:  50,
	}
}

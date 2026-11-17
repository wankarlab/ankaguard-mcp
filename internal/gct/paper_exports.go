package gct

import (
	"context"
	"time"

	"github.com/rantipay/trading-mcp/internal/risk"
)

// Minimal exports for the paper broker (internal/paper). They expose the exact
// same risk evaluation and unit conversions the real client uses, so a paper
// order is judged with the same notional as a real one (OpenSpec
// guarded-mcp-product, D3/D4). Nothing here changes the real client behaviour.

// FuturesContracts is the USD -> whole contracts conversion used by the real
// futures route: max(1, round(usd / (price*multiplier))).
func FuturesContracts(usdAmount, price, multiplier float64) float64 {
	return futuresContracts(usdAmount, price, multiplier)
}

// FuturesSymbol converts a pair (NEAR-USDT, NEAR-USDTM) to the KuCoin futures
// symbol (NEARUSDTM), as the real futures route does.
func FuturesSymbol(pair string) string { return convertPairToFuturesSymbol(pair) }

// IsFuturesPair reports whether a pair name looks like a futures contract
// (same detection the real client applies on the spot path).
func IsFuturesPair(pair string) bool { return detectAssetType(pair) == "futures" }

// RiskEvaluator runs the real client's order risk evaluation (evaluateOrderRisk)
// against a sentinel using caller-supplied price and contract-spec sources.
type RiskEvaluator struct {
	c *client
}

// NewRiskEvaluator builds an evaluator. ticker returns the last price and the
// snapshot timestamp (zero when unknown) of a symbol; multiplier returns base
// units per contract of a futures symbol. A nil sentinel makes Evaluate a no-op,
// exactly like a real client without a sentinel.
func NewRiskEvaluator(
	sentinel risk.RiskSentinel,
	ticker func(ctx context.Context, symbol string, futures bool) (float64, time.Time, error),
	multiplier func(ctx context.Context, symbol string) (float64, error),
) *RiskEvaluator {
	c := &client{riskSentinel: sentinel}
	c.notional.src = &notionalSources{ticker: ticker, multiplier: multiplier}
	return &RiskEvaluator{c: c}
}

// Evaluate consults the sentinel for req. futures says which route the order
// takes (the unit of req.Amount depends on it).
func (e *RiskEvaluator) Evaluate(ctx context.Context, req *SubmitOrderRequest, futures bool) error {
	return e.c.evaluateOrderRisk(ctx, req, futures)
}

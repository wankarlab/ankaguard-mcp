package core

import (
	"context"
	"math"
	"time"
)

// Market-data field names used in snapshots, data-quality records and score
// breakdowns.
const (
	FieldPriceUSD       = "price_usd"      // reserves/mid price
	FieldExecPriceUSD   = "exec_price_usd" // executable price from a sell quote
	FieldLiquidityUSD   = "liquidity_usd"
	FieldMarketCapUSD   = "market_cap_usd"
	FieldVolume1hUSD    = "volume_1h_usd"
	FieldPriceChange5m  = "price_change_5m_pct"
	FieldBuys5m         = "buys_5m"
	FieldSells5m        = "sells_5m"
	FieldTop10Pct       = "top10_pct" // grouped by bundle, excluding pool and burn
	FieldAgeSeconds     = "age_s"
	FieldTaxPct         = "tax_pct"
	FieldLPLockedPct    = "lp_locked_pct"
	FieldReserveSOL     = "reserve_sol"
	FieldReserveToken   = "reserve_token"
	FieldSolUSD         = "sol_usd"
	FieldPriorityFeeLam = "priority_fee_lamports"
)

// CriticalFields must be fresh for an opportunity to be scored.
var CriticalFields = []string{FieldPriceUSD, FieldLiquidityUSD, FieldExecPriceUSD}

// DataPoint is one market value with its provenance (FRD FR-DATA-01).
type DataPoint struct {
	Field      string    `json:"field"`
	Value      float64   `json:"value"`
	Text       string    `json:"text,omitempty"`
	Source     string    `json:"source"`
	TS         time.Time `json:"ts"`
	ExpiresAt  time.Time `json:"expires_at"`
	Confidence float64   `json:"confidence"` // 0..1
	Method     string    `json:"method,omitempty"`
}

// Valid reports a finite, strictly positive value (prices, liquidity).
func (d DataPoint) Valid() bool {
	return !math.IsNaN(d.Value) && !math.IsInf(d.Value, 0) && d.Value > 0
}

// Stale reports whether the value expired at now.
func (d DataPoint) Stale(now time.Time) bool { return !d.ExpiresAt.IsZero() && now.After(d.ExpiresAt) }

// Snapshot is the set of data points used for one evaluation.
type Snapshot map[string]DataPoint

// Get returns a field and whether it exists.
func (s Snapshot) Get(field string) (DataPoint, bool) { d, ok := s[field]; return d, ok }

// MinExpiry is the earliest expiry among the given fields (zero if none set).
func (s Snapshot) MinExpiry(fields ...string) time.Time {
	var min time.Time
	for _, f := range fields {
		d, ok := s[f]
		if !ok || d.ExpiresAt.IsZero() {
			continue
		}
		if min.IsZero() || d.ExpiresAt.Before(min) {
			min = d.ExpiresAt
		}
	}
	return min
}

// Quote is an executable quote for a given size. Amounts are in UI units:
// tokens for the token side, USD for the quote side.
type Quote struct {
	InAmount       float64   `json:"in_amount"`
	OutAmount      float64   `json:"out_amount"`
	PriceImpactPct float64   `json:"price_impact_pct"`
	RouteFound     bool      `json:"route_found"`
	FeePct         float64   `json:"fee_pct"` // venue fee already included in OutAmount, if known
	Source         string    `json:"source"`
	TS             time.Time `json:"ts"`
}

// Quoter returns executable quotes. QuoteBuy spends usdIn and returns tokens;
// QuoteSell sells tokens and returns USD. RouteFound=false means no exit route.
type Quoter interface {
	QuoteBuy(ctx context.Context, mint string, usdIn float64) (Quote, error)
	QuoteSell(ctx context.Context, mint string, tokens float64) (Quote, error)
}

// PriorityFeeSource returns the recent median priority fee per transaction in lamports.
type PriorityFeeSource interface {
	PriorityFeeLamports(ctx context.Context) (int64, error)
}

// SolPriceSource returns the SOL/USD price.
type SolPriceSource interface {
	SolUSD(ctx context.Context) (float64, error)
}

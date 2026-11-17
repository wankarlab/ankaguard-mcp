package exchange

// ev_engine.go — fee + funding + slippage aware expected-value engine.
//
// Everything here is PURE (no I/O, no clients) and unit-testable. Costs are
// expressed in USD and combined into a single ExpectedValueUSD figure that
// callers can compare across venues or against a minimum-EV threshold.

// FeeSchedule holds maker/taker fees for a venue/tier, expressed in basis
// points (1 bp = 0.01% = 0.0001 of notional).
type FeeSchedule struct {
	MakerBps float64
	TakerBps float64
}

// DefaultFeeSchedules returns sensible defaults for futures takers, keyed by
// lowercase exchange name. Values are (maker, taker) in basis points. These are
// conservative ballpark numbers; override per exchange/tier via SetFeeSchedules.
func DefaultFeeSchedules() map[string]FeeSchedule {
	return map[string]FeeSchedule{
		"kucoin":  {MakerBps: 2, TakerBps: 6},
		"binance": {MakerBps: 2, TakerBps: 4},
	}
}

// EVInput describes a single hypothetical execution on one venue.
type EVInput struct {
	Side            string  // "buy"|"sell"
	GrossPrice      float64 // venue price before costs (ask for buy, bid for sell)
	SizeBase        float64 // size in base units
	NotionalUSD     float64 // SizeBase*price (caller-provided to avoid recompute)
	FeeBps          float64 // taker bps for the venue/tier
	SlippageBps     float64 // estimated from depth (caller may pass 0)
	ExpectedFunding float64 // expected funding USD over hold (signed; + = received)
}

// EVResult breaks down the cost components of an execution, all in USD.
type EVResult struct {
	NetPriceUSD      float64 // notional adjusted for fees+slippage (signed by side, see below)
	FeesUSD          float64 // taker fee cost (always >= 0)
	SlippageUSD      float64 // estimated slippage cost (always >= 0)
	FundingUSD       float64 // expected funding (signed; + = received)
	ExpectedValueUSD float64 // see sign convention below
}

// ComputeEV computes the expected value of an execution relative to its gross
// (frictionless) execution price.
//
// SIGN CONVENTION:
//   - Fees and slippage are always *costs* (>= 0) and reduce EV.
//   - Funding is signed and added directly (+ received improves EV).
//   - ExpectedValueUSD is the *advantage relative to gross execution*, higher is
//     better for BOTH sides. Concretely:
//       ExpectedValueUSD = -FeesUSD - SlippageUSD + FundingUSD
//     i.e. a frictionless fill with zero funding has EV = 0; any real fill is
//     penalized by its frictional costs. Because every venue is measured against
//     its own gross price, ComputeEV alone does NOT capture the cross-venue price
//     spread — the caller compares effective prices for that (see BestVenue).
//
//   - NetPriceUSD is the size-weighted effective notional INCLUDING friction,
//     signed by side so it composes as a cash flow:
//       buy  -> negative (cash paid out):  -(notional + fees + slippage) + funding
//       sell -> positive (cash received):  +(notional - fees - slippage) + funding
func ComputeEV(in EVInput) EVResult {
	notional := in.NotionalUSD
	if notional == 0 {
		notional = in.SizeBase * in.GrossPrice
	}

	// bps → fraction: 1 bp = 0.0001.
	feesUSD := notional * (in.FeeBps * 0.0001)
	slipUSD := notional * (in.SlippageBps * 0.0001)
	fundingUSD := in.ExpectedFunding

	// EV relative to gross: only the frictional terms matter here.
	ev := -feesUSD - slipUSD + fundingUSD

	var netPrice float64
	if in.Side == "sell" {
		// Cash received: proceeds minus costs, plus funding.
		netPrice = notional - feesUSD - slipUSD + fundingUSD
	} else {
		// buy (default): cash paid out is negative.
		netPrice = -(notional + feesUSD + slipUSD) + fundingUSD
	}

	return EVResult{
		NetPriceUSD:      netPrice,
		FeesUSD:          feesUSD,
		SlippageUSD:      slipUSD,
		FundingUSD:       fundingUSD,
		ExpectedValueUSD: ev,
	}
}

// MeetsThreshold reports whether an execution's expected value clears the
// minimum required EV (in USD). minEVUSD may be negative if the caller accepts
// paying some friction; a clean spread should be folded into EV upstream.
func MeetsThreshold(ev EVResult, minEVUSD float64) bool {
	return ev.ExpectedValueUSD >= minEVUSD
}

// EstimateSlippageBps estimates slippage in basis points using a simple linear
// price-impact model.
//
// ASSUMPTIONS:
//   - topOfBookSize is a rough proxy for available liquidity at the touch (e.g.
//     ticker volume or best-level size). If <= 0, we cannot estimate and return 0.
//   - Impact grows linearly with the fraction of top-of-book liquidity consumed:
//       slippageBps = k * (sizeBase / topOfBookSize)
//     where k is a tunable impact coefficient expressed in bps per 1x of
//     top-of-book consumed (e.g. k=10 → consuming the whole top level costs
//     ~10 bps). This is intentionally crude; replace with a depth-walk when real
//     order-book levels are available.
//   - Result is clamped to >= 0.
func EstimateSlippageBps(sizeBase, topOfBookSize, k float64) float64 {
	if topOfBookSize <= 0 || sizeBase <= 0 || k <= 0 {
		return 0
	}
	bps := k * (sizeBase / topOfBookSize)
	if bps < 0 {
		return 0
	}
	return bps
}

package launch

import (
	"context"
	"time"
)

// Venue is an on-chain trading venue (Jupiter aggregator, a direct pool
// program, later EVM routers). It deliberately shares no types with the CEX
// GCTClient (srs.md §2.3): amounts are integer base units and every price
// carries its source and timestamp so DataQuality can judge it.
type Venue interface {
	Name() string
	Network() string
	// Quote prices a swap without side effects.
	Quote(ctx context.Context, req QuoteRequest) (Quote, error)
	// BuildSwap returns the unsigned transaction for a quote, plus the program
	// ids and native outflow the signer must verify.
	BuildSwap(ctx context.Context, q Quote, wallet string) (SignRequest, error)
	// Submit sends a signed transaction and waits for the requested commitment.
	Submit(ctx context.Context, tx SignedTx, commitment string) (TxResult, error)
	// SimulateSell dry-runs selling amount of token; used to detect honeypots
	// before any buy.
	SimulateSell(ctx context.Context, token string, amount uint64, wallet string) (SimulationResult, error)
}

// QuoteRequest asks for a swap price. Amounts are in base units of InputMint.
type QuoteRequest struct {
	InputMint   string
	OutputMint  string
	Amount      uint64
	SlippageBps uint32
}

// Quote is a priced route. OutAmount is the expected output in base units of
// OutputMint; MinOutAmount already accounts for slippage.
type Quote struct {
	Venue          string
	InputMint      string
	OutputMint     string
	InAmount       uint64
	OutAmount      uint64
	MinOutAmount   uint64
	PriceImpactPct float64
	Route          []byte // venue-specific opaque route, passed back to BuildSwap
	Source         string
	FetchedAt      time.Time
}

// TxResult is the on-chain outcome of a submitted transaction.
type TxResult struct {
	Signature  string
	Slot       uint64
	Commitment string
	Succeeded  bool
	Error      string
	FeeLamport uint64
}

// SimulationResult is the outcome of a simulated sell.
type SimulationResult struct {
	Succeeded bool
	OutAmount uint64
	Error     string
	Logs      []string
}

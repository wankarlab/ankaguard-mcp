package core

import (
	"context"
	"time"
)

// MintInfo is the on-chain state of a token mint (SPL or Token-2022).
type MintInfo struct {
	Program         string   `json:"program"` // "spl" | "token2022"
	Decimals        int      `json:"decimals"`
	Supply          float64  `json:"supply"` // UI units
	MintAuthority   string   `json:"mint_authority,omitempty"`
	FreezeAuthority string   `json:"freeze_authority,omitempty"`
	Extensions      []string `json:"extensions,omitempty"` // Token-2022 extension names, e.g. "transferHook"
	TransferFeeBps  int      `json:"transfer_fee_bps,omitempty"`
}

// MintReader reads mint state (implemented by the Solana RPC client).
type MintReader interface {
	MintInfo(ctx context.Context, mint string) (MintInfo, error)
}

// LiquidityReader returns the current pool liquidity (USD) of a token's main
// pool; used by screening and by the position manager's emergency exits.
type LiquidityReader interface {
	LiquidityUSD(ctx context.Context, mint string) (DataPoint, error)
}

// BlockTimeResolver resolves the on-chain block time of a transaction.
type BlockTimeResolver interface {
	BlockTime(ctx context.Context, txSignature string) (time.Time, error)
}

// EventRecorder persists a discovery event in the corpus with deduplication.
// duplicate=true means the dedup key already existed (the raw event is still
// stored, flagged duplicate).
type EventRecorder interface {
	RecordEvent(ctx context.Context, ev Event) (duplicate bool, err error)
}

// RPCBudget is the shared per-minute budget of Solana RPC calls
// (launch.sources.max_rpc_calls_per_min). Allow consumes one call or reports
// that the budget is exhausted.
type RPCBudget interface {
	Allow() bool
}

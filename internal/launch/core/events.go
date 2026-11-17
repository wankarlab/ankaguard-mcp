package core

import (
	"encoding/json"
	"time"
)

// EventKind is the type of a discovery event (FRD §2).
type EventKind string

const (
	EventCurveCreated     EventKind = "CurveCreated"
	EventCurveGraduated   EventKind = "CurveGraduated"
	EventPoolCreated      EventKind = "PoolCreated"
	EventFirstSwap        EventKind = "FirstSwap"
	EventLiquidityRemoved EventKind = "LiquidityRemoved"
	EventAuthorityChanged EventKind = "AuthorityChanged"
)

// SourceKind says where a token trades.
type SourceKind string

const (
	SourceBondingCurve SourceKind = "bonding_curve"
	SourceAMM          SourceKind = "amm"
)

// Event is one normalised discovery event. Subject is the dedup subject: the
// pool address when known, otherwise the mint.
type Event struct {
	Network     string          `json:"network"`
	Kind        EventKind       `json:"kind"`
	Source      string          `json:"source"` // adapter name, e.g. "pumpportal", "dexscreener"
	Subject     string          `json:"subject"`
	Mint        string          `json:"mint"`
	Pool        string          `json:"pool,omitempty"`
	Dex         string          `json:"dex,omitempty"` // "pumpfun", "pumpswap", "raydium", ...
	SourceKind  SourceKind      `json:"source_kind,omitempty"`
	Symbol      string          `json:"symbol,omitempty"`
	Name        string          `json:"name,omitempty"`
	Creator     string          `json:"creator,omitempty"`
	TxSignature string          `json:"tx_signature"`
	Slot        int64           `json:"slot,omitempty"`
	ChainTime   time.Time       `json:"chain_time"`  // on-chain timestamp; zero if the source has none
	ReceivedAt  time.Time       `json:"received_at"` // local receive time
	Payload     json.RawMessage `json:"payload"`     // original source payload, verbatim
}

// DedupKey identifies an event across sources.
func (e Event) DedupKey() string {
	return e.Network + "|" + e.Subject + "|" + e.TxSignature + "|" + string(e.Kind)
}

// Candidate is a token handed from discovery to screening: a new curve, a new
// pool, or a graduation (re-evaluated as a new opportunity with SourceAMM).
type Candidate struct {
	TokenID    int64      `json:"token_id"`
	PoolID     int64      `json:"pool_id,omitempty"`
	Mint       string     `json:"mint"`
	Pool       string     `json:"pool,omitempty"`
	Dex        string     `json:"dex,omitempty"`
	SourceKind SourceKind `json:"source_kind"`
	Graduated  bool       `json:"graduated"`
	Symbol     string     `json:"symbol,omitempty"`
	Creator    string     `json:"creator,omitempty"`
	CreatedAt  time.Time  `json:"created_at"` // on-chain creation time; token age is measured from here
	// GraduatedAt is the on-chain time the AMM pool appeared after the bonding
	// curve completed (zero when not graduated). Strategies that require a
	// graduation measure their age window from here.
	GraduatedAt time.Time `json:"graduated_at,omitempty"`
	EventKind   EventKind `json:"event_kind"`
}

// AgeRef returns the instant the token's tradable window starts and how it
// was chosen: the graduation time for graduated tokens (the AMM pool's birth;
// the bonding-curve phase may last hours), otherwise the on-chain creation.
func (c Candidate) AgeRef() (time.Time, string) {
	if c.Graduated && !c.GraduatedAt.IsZero() {
		return c.GraduatedAt, "candidate.graduated_at"
	}
	return c.CreatedAt, "candidate.created_at"
}

// GraduationDexes are AMM venues whose pools are only created by a bonding-curve
// migration, so a pool there implies a graduated token.
var GraduationDexes = map[string]bool{"pumpswap": true, "pump-amm": true}

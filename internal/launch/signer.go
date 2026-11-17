package launch

import (
	"context"
	"errors"
	"fmt"
)

// SignRequest is a transaction submitted to the signer. The signer re-derives
// ProgramIDs and NativeOutflow from Tx before signing; the declared values are
// what the caller expects and must match (docs/bot/tech-stack.md §3).
type SignRequest struct {
	Network       string
	Tx            []byte   // serialized, unsigned transaction message
	ProgramIDs    []string // every program the transaction invokes
	NativeOutflow uint64   // lamports leaving the signing wallet (amount + fees + tips)
	Reason        string   // audit trail: why this transaction is being signed
}

// SignedTx is a signed, serialized transaction ready for submission.
type SignedTx struct {
	Tx        []byte
	Signature string
}

// Signer holds the hot-wallet key. Implementations live outside the bot
// process (cmd/launch-signer behind a Unix socket) so the key never shares
// memory with network-facing code, and enforce SignerPolicy independently of
// any database value.
type Signer interface {
	PublicKey(ctx context.Context) (string, error)
	Sign(ctx context.Context, req SignRequest) (SignedTx, error)
}

// SignerPolicy is the signer-side guard: only allowlisted programs and a hard
// per-transaction cap, configured from LAUNCH_SIGNER_* at signer startup.
type SignerPolicy struct {
	MaxTxLamports    uint64
	ProgramAllowlist map[string]bool
}

// ErrPolicyViolation is returned when a request breaks the signer policy.
var ErrPolicyViolation = errors.New("signer policy violation")

// NewSignerPolicy builds the policy from the validated environment.
func NewSignerPolicy(env Env) SignerPolicy {
	allow := make(map[string]bool, len(env.SignerProgramAllowlist))
	for _, p := range env.SignerProgramAllowlist {
		allow[p] = true
	}
	return SignerPolicy{MaxTxLamports: env.SignerMaxTxLamports, ProgramAllowlist: allow}
}

// Check rejects a request that is malformed, invokes a non-allowlisted
// program or moves more native value than the per-transaction cap. A policy
// with no cap or an empty allowlist rejects everything (fail-closed).
func (p SignerPolicy) Check(req SignRequest) error {
	if p.MaxTxLamports == 0 || len(p.ProgramAllowlist) == 0 {
		return fmt.Errorf("%w: policy not configured", ErrPolicyViolation)
	}
	if len(req.Tx) == 0 {
		return fmt.Errorf("%w: empty transaction", ErrPolicyViolation)
	}
	if req.Reason == "" {
		return fmt.Errorf("%w: missing reason", ErrPolicyViolation)
	}
	if len(req.ProgramIDs) == 0 {
		return fmt.Errorf("%w: transaction declares no programs", ErrPolicyViolation)
	}
	for _, id := range req.ProgramIDs {
		if !p.ProgramAllowlist[id] {
			return fmt.Errorf("%w: program %s is not allowlisted", ErrPolicyViolation, id)
		}
	}
	if req.NativeOutflow > p.MaxTxLamports {
		return fmt.Errorf("%w: outflow %d lamports exceeds cap %d", ErrPolicyViolation, req.NativeOutflow, p.MaxTxLamports)
	}
	return nil
}

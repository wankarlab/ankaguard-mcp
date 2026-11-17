// Package paper implements the paper trading broker (OpenSpec change
// guarded-mcp-product, decision D3): a gct.GCTClient that delegates market
// data to the real client and serves orders, positions, balance and account
// from SQLite, so an agent can practice with real prices and no real money.
//
// Isolation rule: no order, cancellation, modification, close, leverage change
// or account query ever reaches the wrapped real client. A method the broker
// cannot serve returns PAPER_UNSUPPORTED; it is never forwarded.
//
// How the server marks responses as paper: every gct.Order, gct.Position,
// gct.Portfolio, gct.PortfolioOverview and gct.AccountInfo returned by this
// package has Paper=true (JSON "paper": true), and orders also carry
// PaperAssumptions (slippage, taker fee, fee charged, "no funding", "no partial
// fills"). Map results (GetBalance, GetPositions, SimulateOrder) contain
// "paper": true and an "assumptions" map. Tools that build their own JSON from
// these values must copy those fields; Client.Assumptions() returns the same
// map for tools that need it standalone.
package paper

import (
	"context"
	"errors"
	"fmt"
)

// Stable error codes (also the JSON-RPC data.code a server should surface).
const (
	CodeUnsupported       = "PAPER_UNSUPPORTED"
	CodeDataUnavailable   = "DATA_UNAVAILABLE"
	CodeInsufficientFunds = "INSUFFICIENT_FUNDS"
	CodeNoHolder          = "PAPER_NO_HOLDER"
	CodeRejected          = "PAPER_ORDER_REJECTED"
	CodeNotFound          = "PAPER_NOT_FOUND"
)

// Error is a paper broker rejection with a stable machine-readable code.
type Error struct {
	Code    string
	Message string
	// Available and Required are filled for INSUFFICIENT_FUNDS.
	Available float64
	Required  float64
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newErr(code, format string, args ...interface{}) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the paper error code of err, or "" when it is not a paper error.
func CodeOf(err error) string {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

type holderKey struct{}

// WithHolder returns a context carrying the paper account holder (the token
// subject). The server sets it for every call made with a paper profile.
func WithHolder(ctx context.Context, holder string) context.Context {
	return context.WithValue(ctx, holderKey{}, holder)
}

// HolderFromContext returns the holder stored by WithHolder.
func HolderFromContext(ctx context.Context) (string, bool) {
	h, ok := ctx.Value(holderKey{}).(string)
	return h, ok && h != ""
}

func holderOf(ctx context.Context) (string, error) {
	h, ok := HolderFromContext(ctx)
	if !ok {
		return "", newErr(CodeNoHolder, "no paper account holder in context; a shared paper account is never used")
	}
	return h, nil
}

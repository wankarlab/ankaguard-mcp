// EX-1: execution-capable ExchangeRouter extension.
//
// This file adds write capability (Place/Cancel/GetPositions) to the existing
// read-only Router without breaking the read surface.  A VenueExecutor is
// obtained via Router.For(exchange) and routes orders through the GCT client
// matching that exchange — no hardcoded KuCoin path.
//
// Deterministic clientOrderId is set by the caller (SEC-11); this layer
// passes it through unchanged via SubmitOrderRequest.ClientID.
//
// Parity tests must pass before any write path goes to real money; the
// interface is gated by execEnabled (default false, toggle via SetExecutionEnabled).
package exchange

import (
	"context"
	"fmt"
	"strings"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// OrderSubmitter submits and cancels orders on a specific exchange.
// *gct.client satisfies this.
type OrderSubmitter interface {
	SubmitOrder(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error)
	CancelOrder(ctx context.Context, exchange, orderID string) error
	GetFuturesPositions(ctx context.Context, exchange string) ([]*gct.Position, error)
}

// VenueExecutor is an exchange-specific order surface.
// Obtain one via Router.For(exchange).
type VenueExecutor struct {
	exchange    string
	submitter   OrderSubmitter
	execEnabled bool
}

// Place submits an order to the venue.
// Returns an error when execution is disabled (execEnabled=false).
func (v *VenueExecutor) Place(ctx context.Context, req *gct.SubmitOrderRequest) (*gct.Order, error) {
	if !v.execEnabled {
		return nil, fmt.Errorf("EX-1: execution disabled for %s — enable via Router.SetExecutionEnabled(true)", v.exchange)
	}
	if req == nil {
		return nil, fmt.Errorf("Place: req is nil")
	}
	// Always force the exchange field to match the venue.
	req.Exchange = v.exchange
	return v.submitter.SubmitOrder(ctx, req)
}

// Cancel cancels an open order on the venue.
func (v *VenueExecutor) Cancel(ctx context.Context, orderID string) error {
	if !v.execEnabled {
		return fmt.Errorf("EX-1: execution disabled for %s", v.exchange)
	}
	return v.submitter.CancelOrder(ctx, v.exchange, orderID)
}

// GetPositions returns open futures positions for this venue.
func (v *VenueExecutor) GetPositions(ctx context.Context) ([]*gct.Position, error) {
	return v.submitter.GetFuturesPositions(ctx, v.exchange)
}

// IsExecutionEnabled returns true when Place/Cancel are allowed.
func (v *VenueExecutor) IsExecutionEnabled() bool {
	return v.execEnabled
}

// ---- Router execution surface ----

// SetOrderSubmitter installs the concrete GCT client used by For().
// Until this is called, For() returns a read-only VenueExecutor with execution disabled.
func (r *Router) SetOrderSubmitter(s OrderSubmitter) {
	r.submitter = s
}

// SetExecutionEnabled globally enables or disables order placement via For().
// Default is false (read-only router). Set true only after parity tests pass
// in a paper/sandbox environment (EX-1 acceptance criterion).
func (r *Router) SetExecutionEnabled(enabled bool) {
	r.execEnabled = enabled
}

// For returns a VenueExecutor for the given exchange.
// Exchange name matching is case-insensitive.
// Returns a no-op executor (execEnabled=false) when the exchange is not
// active or the submitter is not configured.
func (r *Router) For(exchange string) *VenueExecutor {
	if exchange == "" {
		return &VenueExecutor{execEnabled: false}
	}

	// Verify the exchange is active.
	if r.set != nil {
		active := r.set.GetEnabledExchanges()
		found := false
		for _, ex := range active {
			if strings.EqualFold(ex, exchange) {
				found = true
				break
			}
		}
		if !found {
			return &VenueExecutor{exchange: exchange, execEnabled: false}
		}
	}

	return &VenueExecutor{
		exchange:    strings.ToLower(exchange),
		submitter:   r.submitter,
		execEnabled: r.execEnabled && r.submitter != nil,
	}
}

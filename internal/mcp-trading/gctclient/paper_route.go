package gctclient

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/paper"
)

// PaperUnavailableCode is the error code of a paper call made while no paper
// broker is wired in. The call is rejected; it is never served by the real
// exchange client.
const PaperUnavailableCode = "PAPER_UNAVAILABLE"

// SetPaperClient wires the paper broker client. From then on every call whose
// context carries a paper account holder (paper.WithHolder, set by the MCP
// server for tokens with profile paper) is served by it.
func (c *Client) SetPaperClient(p gct.GCTClient) {
	c.paperMu.Lock()
	c.paperClient = p
	c.paperMu.Unlock()
}

// HasPaperClient reports whether a paper broker client is wired in.
func (c *Client) HasPaperClient() bool {
	c.paperMu.RLock()
	defer c.paperMu.RUnlock()
	return c.paperClient != nil
}

// PaperRequested reports whether the call belongs to a paper profile.
func PaperRequested(ctx context.Context) bool {
	_, ok := paper.HolderFromContext(ctx)
	return ok
}

// route picks the client for order and account operations of one call.
//
// Isolation rule (guarded-mcp-product, D3): a call that belongs to a paper
// profile NEVER reaches the real client. If the paper broker is missing the
// call fails with PAPER_UNAVAILABLE instead of falling back to the exchange.
// Market data methods do not use route: they are read-only and always real.
func (c *Client) route(ctx context.Context) (client gct.GCTClient, isPaper bool, err error) {
	if !PaperRequested(ctx) {
		return c.gctClient, false, nil
	}
	c.paperMu.RLock()
	p := c.paperClient
	c.paperMu.RUnlock()
	if p == nil {
		return nil, true, &paper.Error{Code: PaperUnavailableCode, Message: "the paper broker is not available; the call was not executed"}
	}
	return p, true, nil
}

// ClientFor returns the gct.GCTClient that serves order and account operations
// for the call carried by ctx: the paper broker for a paper profile, the real
// client otherwise. Tools that need the full gct.GCTClient interface (the
// "direct" tools, the trailing stop, the scalping tools) must use this instead
// of GetBaseClient, which always returns the real client.
func (c *Client) ClientFor(ctx context.Context) (gct.GCTClient, error) {
	cl, _, err := c.route(ctx)
	return cl, err
}

// paperUnsupportedForUser is returned by the per-user (multi-user session)
// variants when the call belongs to a paper profile: those methods sign with
// real user credentials and have no paper equivalent.
func paperUnsupportedForUser(ctx context.Context) error {
	if PaperRequested(ctx) {
		return &paper.Error{Code: paper.CodeUnsupported, Message: "per-user exchange sessions are not available with a paper profile"}
	}
	return nil
}

package gctclient

import (
	"context"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// gctClientAdapter adapts gct.GCTClient to work with HFT system
type gctClientAdapter struct {
	client gct.GCTClient
}

// newGCTClientAdapter creates a new adapter
func newGCTClientAdapter(client gct.GCTClient) *gctClientAdapter {
	return &gctClientAdapter{client: client}
}

// SubmitOrder implements HFT interface
func (a *gctClientAdapter) SubmitOrder(ctx context.Context, req interface{}) (interface{}, error) {
	gctReq, ok := req.(*gct.SubmitOrderRequest)
	if !ok {
		return nil, nil
	}
	return a.client.SubmitOrder(ctx, gctReq)
}

// SubmitFuturesOrder implements HFT interface
func (a *gctClientAdapter) SubmitFuturesOrder(ctx context.Context, req interface{}) (interface{}, error) {
	gctReq, ok := req.(*gct.SubmitOrderRequest)
	if !ok {
		return nil, nil
	}
	return a.client.SubmitFuturesOrder(ctx, gctReq)
}

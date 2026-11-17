package gctclient

import (
	"context"
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/paper"
)

// The per-user session methods talk to the user's own exchange credentials
// (KuCoin REST through the session). A paper profile must never reach them:
// every one answers PAPER_UNSUPPORTED before touching the session (the nil
// session would panic if it did).
func TestForUserMethodsRefuseAPaperProfile(t *testing.T) {
	c := &Client{}
	ctx := paper.WithHolder(context.Background(), "agent:x")

	calls := map[string]func() error{
		"SubmitOrderForUser": func() error {
			_, err := c.SubmitOrderForUser(ctx, nil, &models.OrderRequest{})
			return err
		},
		"SubmitFuturesOrderForUser": func() error {
			_, err := c.SubmitFuturesOrderForUser(ctx, nil, &gct.SubmitOrderRequest{})
			return err
		},
		"GetFuturesPositionsForUser": func() error {
			_, err := c.GetFuturesPositionsForUser(ctx, nil)
			return err
		},
		"GetBalanceForUser": func() error {
			_, err := c.GetBalanceForUser(ctx, nil, "USDT")
			return err
		},
		"SetMarginModeForUser": func() error { return c.SetMarginModeForUser(ctx, nil, "XBTUSDTM", "ISOLATED") },
		"SetLeverageForUser":   func() error { return c.SetLeverageForUser(ctx, nil, "XBTUSDTM", 3) },
		"CancelOrderForUser":   func() error { return c.CancelOrderForUser(ctx, nil, "kucoin", "1") },
		"CancelFuturesOrderForUser": func() error {
			return c.CancelFuturesOrderForUser(ctx, nil, "kucoin", "1")
		},
		"CloseFuturesPositionForUser": func() error { return c.CloseFuturesPositionForUser(ctx, nil, "XBTUSDTM") },
		"SubmitStopLossForUser": func() error {
			_, err := c.SubmitStopLossForUser(ctx, nil, &gct.SubmitOrderRequest{})
			return err
		},
		"SubmitTakeProfitForUser": func() error {
			_, err := c.SubmitTakeProfitForUser(ctx, nil, &gct.SubmitOrderRequest{})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if got := paper.CodeOf(err); got != paper.CodeUnsupported {
				t.Fatalf("%s with a paper profile: code %q (err %v), want %q", name, got, err, paper.CodeUnsupported)
			}
		})
	}
}

// Without a paper client the route fails closed: the real client is never used.
func TestRouteFailsClosedWithoutPaperClient(t *testing.T) {
	c := &Client{}
	ctx := paper.WithHolder(context.Background(), "agent:x")
	if _, err := c.ClientFor(ctx); paper.CodeOf(err) != PaperUnavailableCode {
		t.Fatalf("ClientFor without a broker = %v, want %s", err, PaperUnavailableCode)
	}
	if !PaperRequested(ctx) || PaperRequested(context.Background()) {
		t.Fatal("PaperRequested must follow the holder in the context")
	}
}

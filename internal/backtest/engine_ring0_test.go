package backtest

import (
	"context"
	"errors"
	"testing"

	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/killswitch"
)

// While ring 0 is active, historical replays must refuse to run.
func TestEngineReplay_BlockedByRing0(t *testing.T) {
	killswitch.SetRing0Active(true)
	t.Cleanup(func() { killswitch.SetRing0Active(false) })

	e := &Engine{}
	_, err := e.Replay(context.Background(), domain.BacktestRequest{}, nil, []*gct.Candle{{}}, nil)
	if !errors.Is(err, killswitch.ErrActive) {
		t.Fatalf("expected ErrActive, got %v", err)
	}
}

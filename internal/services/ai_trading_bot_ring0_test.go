package services

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/killswitch"
)

func newRing0TestBot() *AITradingBot {
	return &AITradingBot{
		log:        logrus.New(),
		resultChan: make(chan *ExecutionResult, 4),
		stopChan:   make(chan struct{}),
	}
}

// executeTrade must abort before touching any exchange client while ring 0 is
// active (gctClient is nil here: reaching it would panic).
func TestExecuteTrade_BlockedByRing0(t *testing.T) {
	killswitch.SetRing0Active(true)
	t.Cleanup(func() { killswitch.SetRing0Active(false) })

	bot := newRing0TestBot()
	bot.executeTrade(context.Background(), &ai.Decision{Action: "BUY", Confidence: 0.9}, &ai.MarketContext{})

	select {
	case r := <-bot.resultChan:
		if r.Success || !errors.Is(r.Error, killswitch.ErrActive) {
			t.Fatalf("unexpected result: %+v", r)
		}
	default:
		t.Fatal("expected a blocked result on resultChan")
	}
}

// StopWithContext on a bot with running=false must still signal stopChan so
// loops launched through another path terminate.
func TestStopWithContext_SignalsLoopsWhenNotRunning(t *testing.T) {
	bot := newRing0TestBot()
	if err := bot.StopWithContext(context.Background()); err == nil {
		t.Fatal("expected 'not running' error")
	}
	select {
	case <-bot.stopChan:
	default:
		t.Fatal("stopChan must be closed even when running=false")
	}
}

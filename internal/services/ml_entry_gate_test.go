package services

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/featureeng"
)

// fakeMLPredictor lets tests control readiness without a live ml-server.
type fakeMLPredictor struct{ ready bool }

func (f *fakeMLPredictor) IsReady() bool { return f.ready }
func (f *fakeMLPredictor) Predict(_ context.Context, _ *featureeng.Features, _ string) (*mlPrediction, error) {
	return &mlPrediction{Action: "BUY", Probability: 0.7}, nil
}

// TestMLEntryVote_FailsOpen is the safety-critical test: when the ML predictor is
// unavailable (nil, not ready) or there is no market context, mlEntryVote MUST
// report evaluated=false so the caller never blocks a trade on a down ml-server.
func TestMLEntryVote_FailsOpen(t *testing.T) {
	log := logrus.New()
	bot := &AITradingBot{log: log}

	// nil predictor → not evaluated.
	if _, _, _, evaluated := bot.mlEntryVote(&ai.MarketContext{}, "SELL"); evaluated {
		t.Error("nil predictor must yield evaluated=false (fail open)")
	}

	// predictor present but not ready → not evaluated.
	bot.mlPredictor = &fakeMLPredictor{ready: false}
	if _, _, _, evaluated := bot.mlEntryVote(&ai.MarketContext{}, "SELL"); evaluated {
		t.Error("not-ready predictor must yield evaluated=false (fail open)")
	}

	// ready predictor but nil market context → not evaluated.
	bot.mlPredictor = &fakeMLPredictor{ready: true}
	if _, _, _, evaluated := bot.mlEntryVote(nil, "SELL"); evaluated {
		t.Error("nil marketCtx must yield evaluated=false (fail open)")
	}

	// ready predictor but a context with too few candles → not evaluated.
	if _, _, _, evaluated := bot.mlEntryVote(&ai.MarketContext{}, "SELL"); evaluated {
		t.Error("context without primary candles must yield evaluated=false (fail open)")
	}
}

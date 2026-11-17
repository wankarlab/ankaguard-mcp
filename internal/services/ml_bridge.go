package services

// ml_bridge.go — factory functions that let cmd/main.go wire the ML pipeline
// without exposing private interface types. Go allows returning unexported
// interface values from exported functions; callers hold them opaquely.

import (
	"context"
	"database/sql"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai/feedback"
	"github.com/rantipay/trading-mcp/internal/ai/featureeng"
	"github.com/rantipay/trading-mcp/internal/ai/predictor"
	"github.com/sirupsen/logrus"
)

// NewHTTPMLPredictor returns an mlPredictor backed by the ml-server HTTP API.
// Use from cmd/main.go — set ML_SERVER_URL env var to activate.
func NewHTTPMLPredictor(serverURL string, timeout time.Duration) mlPredictor {
	return &httpPredictorBridge{inner: predictor.NewHTTPPredictor(serverURL, timeout)}
}

// NewNoopMLPredictor returns an mlPredictor that always abstains (safe default).
func NewNoopMLPredictor() mlPredictor {
	return &noopPredictorBridge{}
}

// NewFeedbackLabelerBridge returns a tradeLabeler backed by feedback.Labeler.
// The bridge converts the private feedbackOutcome to feedback.TradeOutcome.
func NewFeedbackLabelerBridge(db *sql.DB, log *logrus.Logger) tradeLabeler {
	return &feedbackLabelerBridge{inner: feedback.NewLabeler(db, log)}
}

// --- httpPredictorBridge ---

type httpPredictorBridge struct {
	inner *predictor.HTTPPredictor
}

func (b *httpPredictorBridge) IsReady() bool { return b.inner.IsReady() }

func (b *httpPredictorBridge) Predict(ctx context.Context, features *featureeng.Features, regime string) (*mlPrediction, error) {
	pred, err := b.inner.Predict(ctx, features, regime)
	if err != nil || pred == nil {
		return nil, err
	}
	return &mlPrediction{
		Action:         pred.Action,
		Probability:    pred.Probability,
		ExpectedReturn: pred.ExpectedReturn,
		Sigma:          pred.Sigma,
		HorizonMinutes: pred.HorizonMinutes,
		ModelVersion:   pred.ModelVersion,
	}, nil
}

// --- noopPredictorBridge ---

type noopPredictorBridge struct{}

func (b *noopPredictorBridge) IsReady() bool { return false }

func (b *noopPredictorBridge) Predict(_ context.Context, _ *featureeng.Features, _ string) (*mlPrediction, error) {
	return nil, nil
}

// --- feedbackLabelerBridge ---

type feedbackLabelerBridge struct {
	inner *feedback.Labeler
}

func (b *feedbackLabelerBridge) Label(ctx context.Context, o *feedbackOutcome) {
	if o == nil {
		return
	}
	b.inner.Label(ctx, &feedback.TradeOutcome{
		Pair:          o.Pair,
		Exchange:      o.Exchange,
		Side:          o.Side,
		EntryPrice:    o.EntryPrice,
		ExitPrice:     o.ExitPrice,
		EntryTime:     o.EntryTime,
		ExitTime:      o.ExitTime,
		PnLPct:        o.PnLPct,
		ExitReason:    o.ExitReason,
		Confidence:    o.Confidence,
		Regime:        o.Regime,
		MLProb:        o.MLProb,
		TechnicalVote: o.TechnicalVote,
		LLMVote:       o.LLMVote,
		TechnicalConf: o.TechnicalConf,
		LLMConf:       o.LLMConf,
	})
}

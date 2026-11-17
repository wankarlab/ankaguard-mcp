package predictor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rantipay/trading-mcp/internal/ai/featureeng"
)

// TestNoopPredictor verifies that the noop never errors and always abstains.
func TestNoopPredictor(t *testing.T) {
	p := NoopPredictor{}
	if p.IsReady() {
		t.Error("NoopPredictor.IsReady() must return false")
	}
	pred, err := p.Predict(context.Background(), &featureeng.Features{RSI: 55}, "")
	if err != nil {
		t.Fatalf("NoopPredictor.Predict returned error: %v", err)
	}
	if pred != nil {
		t.Errorf("NoopPredictor.Predict should return nil prediction, got %+v", pred)
	}
}

// TestHTTPPredictor_HealthDown verifies that an unreachable server → IsReady=false.
func TestHTTPPredictor_HealthDown(t *testing.T) {
	p := NewHTTPPredictor("http://127.0.0.1:19999", 100*1e6) // nothing listening there
	if p.IsReady() {
		t.Error("expected IsReady=false when server is down")
	}
}

// TestHTTPPredictor_Predict exercises the happy-path against a fake ml-server.
func TestHTTPPredictor_Predict(t *testing.T) {
	// Fake ml-server that always returns a BUY signal.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":        "ok",
				"ready":         true,
				"model_version": "test-v1",
			})
		case "/predict":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"action":          "BUY",
				"probability":     0.72,
				"expected_return": 0.5,
				"sigma":           0.3,
				"horizon_minutes": 60,
				"model_version":   "test-v1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewHTTPPredictor(srv.URL, 5*1e9)
	if !p.IsReady() {
		t.Fatal("expected IsReady=true after successful health-check")
	}

	feat := &featureeng.Features{RSI: 42, MACDHistogram: 0.1, BBPosition: 0.3}
	pred, err := p.Predict(context.Background(), feat, "RANGING")
	if err != nil {
		t.Fatalf("Predict error: %v", err)
	}
	if pred == nil {
		t.Fatal("expected non-nil prediction")
	}
	if pred.Action != "BUY" {
		t.Errorf("expected action=BUY, got %q", pred.Action)
	}
	if pred.Probability != 0.72 {
		t.Errorf("expected probability=0.72, got %v", pred.Probability)
	}
}

// TestPredictionConfidence verifies the confidence mapping.
func TestPredictionConfidence(t *testing.T) {
	cases := []struct {
		prob float64
		want float64
	}{
		{0.5, 0.0},  // total uncertainty
		{1.0, 1.0},  // maximum certainty (TP)
		{0.0, 1.0},  // maximum certainty (SL)
		{0.75, 0.5}, // 75% → 0.5 confidence
		{0.25, 0.5}, // 25% → 0.5 confidence (symmetric)
	}
	for _, c := range cases {
		p := &Prediction{Probability: c.prob}
		got := p.Confidence()
		if abs(got-c.want) > 1e-9 {
			t.Errorf("prob=%.2f: Confidence()=%.4f, want %.4f", c.prob, got, c.want)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// Package predictor provides the ML prediction voter interface.
// The predictor runs as an optional third signal source alongside the
// deterministic technical engine and the LLM consensus layer.
//
// Architecture:
//   - Predictor interface — implemented by HTTPPredictor (live) or NoopPredictor (absent)
//   - HTTPPredictor — calls the ml-server microservice (cmd/ml-server/server.py)
//   - NoopPredictor — always returns WAIT / confidence=0 (used when no model is deployed)
//
// Degradation guarantee: if the predictor is unavailable or returns an error,
// the bot continues with technical+LLM analysis unchanged.
package predictor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai/featureeng"
)

// Prediction is the output emitted by the ML predictor for one snapshot.
type Prediction struct {
	// Action is the recommended trade direction: "BUY", "SELL", or "WAIT".
	Action string `json:"action"`

	// Probability is P(TP_first) — probability that TP is hit before SL [0,1].
	// Values near 0.5 → model is uncertain → low confidence vote.
	Probability float64 `json:"probability"`

	// ExpectedReturn is the model's estimate of forward return % (signed, net of costs).
	ExpectedReturn float64 `json:"expected_return"`

	// Sigma is the model's uncertainty (std dev of expected return).
	// Low sigma → tight estimate; high sigma → model is uncertain.
	Sigma float64 `json:"sigma"`

	// HorizonMinutes is the prediction horizon the model was trained on.
	HorizonMinutes int `json:"horizon_minutes"`

	// ModelVersion identifies the deployed model artifact.
	ModelVersion string `json:"model_version"`
}

// Confidence converts the raw probability into a [0,1] confidence score
// suitable for voting: maps [0,1] probability through a symmetric calibration
// so that 0.5 → 0.0 (total uncertainty) and 1.0 or 0.0 → 1.0 (certainty).
func (p *Prediction) Confidence() float64 {
	if p == nil {
		return 0
	}
	// |prob - 0.5| * 2 maps [0,1] → [0,1] centered at 0.5.
	c := (p.Probability - 0.5) * 2
	if c < 0 {
		c = -c
	}
	return c
}

// Predictor is the interface the bot uses for ML-based signal generation.
// All implementations must be safe for concurrent use.
type Predictor interface {
	// Predict extracts a prediction for the given feature snapshot.
	// regime is the current market regime (e.g. "TRENDING_UP") for model selection;
	// pass "" to use the default horizon model.
	// Returns nil prediction (not an error) when the model abstains.
	Predict(ctx context.Context, features *featureeng.Features, regime string) (*Prediction, error)

	// IsReady returns true if the predictor has a loaded model and can serve.
	// When false, the bot skips the ML vote entirely.
	IsReady() bool
}

// --- NoopPredictor ---

// NoopPredictor satisfies the Predictor interface but always abstains.
// Used when no model has been trained or the ml-server is not configured.
type NoopPredictor struct{}

func (NoopPredictor) Predict(_ context.Context, _ *featureeng.Features, _ string) (*Prediction, error) {
	return nil, nil
}
func (NoopPredictor) IsReady() bool { return false }

// --- HTTPPredictor ---

// HTTPPredictor calls the ml-server microservice over HTTP.
// The server is expected to run at the configured address
// (default: http://localhost:10601) and expose:
//
//	POST /predict   body: FeaturesJSON   → PredictionJSON
//	GET  /health    → {"status":"ok","model_version":"...","ready":true}
type HTTPPredictor struct {
	baseURL string
	client  *http.Client

	// mu guards ready, lastCheck and recheckInterval, which are read/written from
	// multiple goroutines (the bot calls IsReady/Predict concurrently).
	mu              sync.Mutex
	ready           bool
	lastCheck       time.Time     // timestamp of the most recent health-check attempt
	recheckInterval time.Duration // throttle for re-probing while not-ready (default 20s)
}

// defaultRecheckInterval throttles automatic re-probing of the ml-server while the
// predictor is in a not-ready state, so a transient outage does not turn into
// a permanent dead predictor (and a flapping server is not spammed).
const defaultRecheckInterval = 20 * time.Second

// NewHTTPPredictor creates a predictor that calls the ml-server at baseURL.
// It performs a health-check immediately; if the server is down, IsReady()
// will automatically re-probe (throttled) and recover once it comes back.
func NewHTTPPredictor(baseURL string, timeout time.Duration) *HTTPPredictor {
	p := &HTTPPredictor{
		baseURL:         baseURL,
		client:          &http.Client{Timeout: timeout},
		recheckInterval: defaultRecheckInterval,
	}
	p.mu.Lock()
	p.ready = p.healthCheck()
	p.lastCheck = time.Now()
	p.mu.Unlock()
	return p
}

// SetRecheckInterval overrides the not-ready re-probe throttle. Non-positive values
// are ignored so a missing dynamic config keeps the 20s default.
// Dynamic config: ai.predictor.recheck_interval (audit P1).
func (p *HTTPPredictor) SetRecheckInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	p.mu.Lock()
	p.recheckInterval = d
	p.mu.Unlock()
}

// healthCheck pings /health and sets ready=true if the server responds with ready=true.
func (p *HTTPPredictor) healthCheck() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := p.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return false
	}
	defer resp.Body.Close()

	var body struct {
		Ready bool `json:"ready"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	return body.Ready
}

// IsReady returns true if the predictor can serve.
//
// When the predictor is currently not ready (initial startup race or a server
// outage detected by Predict), IsReady automatically re-runs the health-check,
// throttled to at most once per recheckInterval. This lets the predictor
// recover on its own once the ml-server comes back, without a bot restart.
// The bot calls this before every Predict to avoid blocking on a dead server.
func (p *HTTPPredictor) IsReady() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ready {
		return true
	}
	// Not ready — re-probe, but no more often than once per recheckInterval.
	interval := p.recheckInterval
	if interval <= 0 {
		interval = defaultRecheckInterval
	}
	if time.Since(p.lastCheck) < interval {
		return false
	}
	p.lastCheck = time.Now()
	p.ready = p.healthCheck()
	return p.ready
}

// predictRequest is the payload sent to POST /predict.
// Regime is passed so the ml-server can select the best-fit horizon model (P4.4).
type predictRequest struct {
	Features *featureeng.Features `json:"features"`
	Regime   string               `json:"regime,omitempty"` // P4.2/P4.4: TRENDING_UP|DOWN|RANGING|...
}

// Predict sends the feature snapshot to the ml-server and returns the prediction.
// Returns (nil, nil) if the server is unreachable (treated as abstain, not error).
func (p *HTTPPredictor) Predict(ctx context.Context, features *featureeng.Features, regime string) (*Prediction, error) {
	// IsReady() may re-probe and recover a previously-dead predictor.
	if features == nil || !p.IsReady() {
		return nil, nil
	}

	body, err := json.Marshal(predictRequest{Features: features, Regime: regime})
	if err != nil {
		return nil, fmt.Errorf("predictor: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/predict",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("predictor: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		// Server unreachable — mark not ready and record the moment so the
		// IsReady() re-check throttle starts from now. IsReady() will probe
		// again after recheckInterval and recover automatically.
		p.mu.Lock()
		p.ready = false
		p.lastCheck = time.Now()
		p.mu.Unlock()
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var pred Prediction
	if err := json.NewDecoder(resp.Body).Decode(&pred); err != nil {
		return nil, fmt.Errorf("predictor: decode: %w", err)
	}
	return &pred, nil
}

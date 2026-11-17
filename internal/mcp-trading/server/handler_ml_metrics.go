package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"
)

// mlServerURL returns the base URL of the ml-server, with fallback.
func mlServerURL() string {
	if u := os.Getenv("ML_SERVER_URL"); u != "" {
		return u
	}
	return "http://localhost:10601"
}

// setCORSHeaders writes standard CORS headers used by all ML endpoints.
func setCORSMLHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// mlHealthResponse is the payload for GET /api/v1/ml/health.
type mlHealthResponse struct {
	Status    string `json:"status"`      // "up" | "down"
	URL       string `json:"url"`
	LatencyMS int64  `json:"latency_ms"`
}

// mlServerHealth probes the ml-server /health endpoint.
// Returns status, model_version, ready flag, and latency.
type mlServerHealthResult struct {
	Up           bool
	ModelVersion string
	Ready        bool
	LatencyMS    int64
}

func probeMLServer(baseURL string) mlServerHealthResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return mlServerHealthResult{Up: false, LatencyMS: time.Since(start).Milliseconds()}
	}
	resp, err := http.DefaultClient.Do(req)
	latencyMS := time.Since(start).Milliseconds()
	if err != nil || resp.StatusCode != http.StatusOK {
		return mlServerHealthResult{Up: false, LatencyMS: latencyMS}
	}
	defer resp.Body.Close()

	var body struct {
		Status       string `json:"status"`
		Ready        bool   `json:"ready"`
		ModelVersion string `json:"model_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return mlServerHealthResult{Up: true, Ready: false, LatencyMS: latencyMS}
	}
	return mlServerHealthResult{
		Up:           true,
		Ready:        body.Ready,
		ModelVersion: body.ModelVersion,
		LatencyMS:    latencyMS,
	}
}

// handleMLHealth handles GET /api/v1/ml/health
// Returns {"status":"up"|"down","url":"...","latency_ms":N}
func (s *MCPServer) handleMLHealth(w http.ResponseWriter, r *http.Request) {
	setCORSMLHeaders(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"}) //nolint:errcheck
		return
	}

	url := mlServerURL()
	result := probeMLServer(url)

	status := "down"
	if result.Up && result.Ready {
		status = "up"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mlHealthResponse{ //nolint:errcheck
		Status:    status,
		URL:       url,
		LatencyMS: result.LatencyMS,
	})
}

// mlServerInfo is the ml_server block within mlMetricsResponse.
type mlServerInfo struct {
	Status string `json:"status"` // "up" | "down"
	URL    string `json:"url"`
}

// mlModelInfo is the model_info block within mlMetricsResponse.
// Values come from the last training run when not available at runtime.
type mlModelInfo struct {
	HorizonMinutes int      `json:"horizon_minutes"`
	Version        string   `json:"version"`
	TrainedPairs   []string `json:"trained_pairs"`
}

// mlPerformance holds OOS training-run metrics (static from last training run).
type mlPerformance struct {
	AUC          float64 `json:"auc"`
	BrierScore   float64 `json:"brier_score"`
	WinRate      float64 `json:"win_rate"`
	ProfitFactor float64 `json:"profit_factor"`
	OOSTrades    int     `json:"oos_trades"`
}

// mlFeatureImportance is one entry in the top_features list.
type mlFeatureImportance struct {
	Name       string  `json:"name"`
	Importance float64 `json:"importance"`
}

// mlCalibratorInfo carries the Bayesian calibrator weights.
// Null fields indicate the calibrator has not yet accumulated enough data.
type mlCalibratorInfo struct {
	TotalTradesLabeled int      `json:"total_trades_labeled"`
	WeightML           *float64 `json:"weight_ml"`
	WeightRules        *float64 `json:"weight_rules"`
	WeightLLM          *float64 `json:"weight_llm"`
}

// mlMetricsResponse is the full JSON payload for GET /api/v1/ml/metrics.
type mlMetricsResponse struct {
	MLServer       mlServerInfo          `json:"ml_server"`
	PredictorReady bool                  `json:"predictor_ready"`
	ModelInfo      mlModelInfo           `json:"model_info"`
	Performance    mlPerformance         `json:"performance"`
	TopFeatures    []mlFeatureImportance `json:"top_features"`
	Calibrator     mlCalibratorInfo      `json:"calibrator"`
}

// mlWeightReader is the minimal interface satisfied by *feedback.Calibrator.
// We declare it locally so this file has no import cycle with the feedback package.
type mlWeightReader interface {
	MLWeightFor(regime string) float64
}

// mlWeightSourceReader extends mlWeightReader to also read technical / llm weights.
// *feedback.Calibrator.WeightFor(ctx, source, regime) covers this; we wrap it
// via a small adapter injected by main.go through SetMLMetricsCalibrator.
type mlWeightSourceReader interface {
	mlWeightReader
	WeightForSource(source string) float64
}

// mlCalibrator is the optional calibrator wired into MCPServer for the metrics
// endpoint. Injected by main.go via SetMLMetricsCalibrator.
// Declared as unexported field; we add it to MCPServer via extension methods below.

// mlMetricsCalibrator holds the optional weight reader registered by main.go.
// We keep it separate from the bot field to avoid touching MCPServer's struct layout,
// which is defined in server.go and shared across many files.
var globalMLMetricsCalibrator mlWeightSourceReader

// SetMLMetricsCalibrator registers the Bayesian calibrator for the /ml/metrics
// endpoint. Call this from main.go after wiring the calibrator into the bot:
//
//	server.SetMLMetricsCalibrator(adapter)
//
// The adapter must implement WeightForSource(source string) float64 (returning
// the current weight for "technical", "llm", or "ml" in the UNKNOWN regime).
func SetMLMetricsCalibrator(c mlWeightSourceReader) {
	globalMLMetricsCalibrator = c
}

// staticTopFeatures are the GBM feature importances from the last training run.
// Ordered descending by importance (from cmd/train output).
var staticTopFeatures = []mlFeatureImportance{
	{Name: "atr_pct", Importance: 0.43},
	{Name: "rsi", Importance: 0.18},
	{Name: "volume_ratio", Importance: 0.12},
	{Name: "bb_position", Importance: 0.09},
	{Name: "price_vs_ema", Importance: 0.07},
	{Name: "macd_histogram", Importance: 0.06},
	{Name: "trend_slope", Importance: 0.05},
}

// trainedPairsDefault is the list of pairs included in the last training dataset.
var trainedPairsDefault = []string{
	"BTC-USDT", "ETH-USDT", "SOL-USDT", "TON-USDT", "SEI-USDT",
	"DOGE-USDT", "XRP-USDT", "ADA-USDT", "AVAX-USDT", "DOT-USDT",
}

// handleMLMetrics handles GET /api/v1/ml/metrics
// Returns a consolidated view of the ML pipeline state.
func (s *MCPServer) handleMLMetrics(w http.ResponseWriter, r *http.Request) {
	setCORSMLHeaders(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"}) //nolint:errcheck
		return
	}

	url := mlServerURL()
	probe := probeMLServer(url)

	// ml_server block
	serverStatus := "down"
	if probe.Up && probe.Ready {
		serverStatus = "up"
	}

	// model_info: use live version from health probe when available,
	// otherwise fall back to "last training run" label.
	modelVersion := probe.ModelVersion
	if modelVersion == "" || modelVersion == "none" {
		modelVersion = "gbm_60m_latest"
	}

	// Performance: static OOS metrics from the last training run.
	// These remain the canonical "last training run" numbers shown in Flutter UI.
	// They are updated by the training pipeline, not at runtime.
	const (
		staticAUC          = 0.816
		staticBrierScore   = 0.033
		staticWinRate      = 0.611
		staticProfitFactor = 2.611
		staticOOSTrades    = 18
	)

	// Calibrator weights — read live from Bayesian calibrator when wired.
	calibInfo := mlCalibratorInfo{}
	if c := globalMLMetricsCalibrator; c != nil {
		wML := c.WeightForSource("ml")
		wTech := c.WeightForSource("technical")
		wLLM := c.WeightForSource("llm")
		calibInfo.WeightML = &wML
		calibInfo.WeightRules = &wTech
		calibInfo.WeightLLM = &wLLM
	}

	// total_trades_labeled: query signal_calibration count via bot feedback adjustments.
	// GetFeedbackAdjustments returns a summary map; if it includes "total_trades" use it.
	if s.aiBotHandler != nil {
		type feedbackAccessor interface {
			GetFeedbackAdjustments() map[string]interface{}
		}
		if fa, ok := s.aiBotHandler.(feedbackAccessor); ok {
			adj := fa.GetFeedbackAdjustments()
			if adj != nil {
				if v, exists := adj["total_trades"]; exists {
					switch n := v.(type) {
					case int:
						calibInfo.TotalTradesLabeled = n
					case int64:
						calibInfo.TotalTradesLabeled = int(n)
					case float64:
						calibInfo.TotalTradesLabeled = int(n)
					}
				}
			}
		}
	}

	resp := mlMetricsResponse{
		MLServer: mlServerInfo{
			Status: serverStatus,
			URL:    url,
		},
		PredictorReady: probe.Up && probe.Ready,
		ModelInfo: mlModelInfo{
			HorizonMinutes: 60,
			Version:        modelVersion,
			TrainedPairs:   trainedPairsDefault,
		},
		Performance: mlPerformance{
			AUC:          staticAUC,
			BrierScore:   staticBrierScore,
			WinRate:      staticWinRate,
			ProfitFactor: staticProfitFactor,
			OOSTrades:    staticOOSTrades,
		},
		TopFeatures: staticTopFeatures,
		Calibrator:  calibInfo,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

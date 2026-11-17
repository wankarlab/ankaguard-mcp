package server

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/rantipay/trading-mcp/internal/ai/learning"
)

// wickExitThresholdRange holds the known min/max for the wick/body ratio.
type wickExitThresholdRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// wickExitResponse is the JSON payload returned by handleWickExit.
type wickExitResponse struct {
	Status         string                 `json:"status"`
	Threshold      float64                `json:"threshold"`
	ThresholdRange wickExitThresholdRange `json:"threshold_range"`
	WinRate        float64                `json:"win_rate"`
	TotalExits     int                    `json:"total_exits"`
	Layer1Exits    int                    `json:"layer1_exits"`
	Layer2Exits    int                    `json:"layer2_exits"`
	Layer3Exits    int                    `json:"layer3_exits"`
	RecentEvents   []wickExitEvent        `json:"recent_events"`
}

// wickExitEvent represents a single wick-exit event in the response.
type wickExitEvent struct {
	Pair      string `json:"pair"`
	Pattern   string `json:"pattern"`
	Direction string `json:"direction"`
	Result    string `json:"result"`
	Timestamp string `json:"timestamp"`
}

// handleWickExit handles GET /api/v1/ai-bot/wick-exit
// Returns the current state of the WickLearner: threshold, pattern stats, and
// aggregated exit counts.  When the learner is not wired it returns
// status "not_configured" with safe zero values.
func (s *MCPServer) handleWickExit(w http.ResponseWriter, r *http.Request) {
	// CORS — all endpoints at port 10600 are public.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

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

	w.Header().Set("Content-Type", "application/json")

	learner := s.getWickLearner()
	if learner == nil {
		resp := wickExitResponse{
			Status:         "not_configured",
			ThresholdRange: wickExitThresholdRange{Min: 1.0, Max: 4.0},
			RecentEvents:   []wickExitEvent{},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
		return
	}

	threshold := learner.GetThreshold()
	stats := learner.GetStats()

	// Aggregate totals across all patterns.
	totalExits := 0
	totalCorrect := 0
	for _, st := range stats {
		totalExits += st.Total
		totalCorrect += st.Correct
	}

	winRate := 0.0
	if totalExits > 0 {
		winRate = float64(totalCorrect) / float64(totalExits)
	}

	resp := wickExitResponse{
		Status:    "active",
		Threshold: threshold,
		ThresholdRange: wickExitThresholdRange{
			Min: 1.0,
			Max: 4.0,
		},
		WinRate:    winRate,
		TotalExits: totalExits,
		// Layer breakdown is not tracked separately by WickLearner; use safe defaults.
		Layer1Exits:  totalExits, // deterministic layer owns all tracked exits
		Layer2Exits:  0,
		Layer3Exits:  0,
		RecentEvents: []wickExitEvent{},
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// getWickLearner resolves the *learning.WickLearner from aiBotHandler via
// reflection.  Returns nil when the handler is not set or does not expose
// GetWickLearner().
func (s *MCPServer) getWickLearner() *learning.WickLearner {
	s.mu.RLock()
	handler := s.aiBotHandler
	s.mu.RUnlock()

	if handler == nil {
		return nil
	}

	// Use a typed interface to avoid hard-coupling to AITradingBot.
	type wickLearnerProvider interface {
		GetWickLearner() *learning.WickLearner
	}

	if provider, ok := handler.(wickLearnerProvider); ok {
		return provider.GetWickLearner()
	}

	// Fallback: reflection (covers future handler types).
	v := reflect.ValueOf(handler)
	method := v.MethodByName("GetWickLearner")
	if !method.IsValid() {
		return nil
	}
	results := method.Call(nil)
	if len(results) == 0 || results[0].IsNil() {
		return nil
	}
	if wl, ok := results[0].Interface().(*learning.WickLearner); ok {
		return wl
	}
	return nil
}

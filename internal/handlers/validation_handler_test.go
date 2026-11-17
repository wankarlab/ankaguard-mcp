package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai/validation"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockValidationService implements the validation service interface for testing
type MockValidationService struct {
	rules              []*validation.Rule
	validateTradeErr   error
	circuitBreakerErr  error
	validateResult     *validation.ValidationResult
	circuitBreakerStat *validation.CircuitBreakerStatus
}

func NewMockValidationService() *MockValidationService {
	return &MockValidationService{
		rules: []*validation.Rule{
			{
				ID:       "rule-1",
				Name:     "Min Risk Reward",
				Category: "risk",
				Severity: "critical",
				Enabled:  true,
			},
			{
				ID:       "rule-2",
				Name:     "Max Leverage",
				Category: "risk",
				Severity: "warning",
				Enabled:  true,
			},
		},
		validateResult: &validation.ValidationResult{
			Passed:         true,
			RulesEvaluated: 2,
			Timestamp:      time.Now(),
		},
		circuitBreakerStat: &validation.CircuitBreakerStatus{
			TradingHalted:     false,
			DailyLossLimit:    false,
			DrawdownLimit:     false,
			ConsecutiveLosses: 0,
			CurrentDrawdown:   0.0,
		},
	}
}

func (m *MockValidationService) GetRules() []*validation.Rule {
	return m.rules
}

func (m *MockValidationService) ValidateTrade(ctx context.Context, trade *validation.TradingDecision, portfolio *validation.PortfolioState) (*validation.ValidationResult, error) {
	if m.validateTradeErr != nil {
		return nil, m.validateTradeErr
	}
	return m.validateResult, nil
}

func (m *MockValidationService) CheckCircuitBreakers(ctx context.Context, portfolio *validation.PortfolioState) (*validation.CircuitBreakerStatus, error) {
	if m.circuitBreakerErr != nil {
		return nil, m.circuitBreakerErr
	}
	return m.circuitBreakerStat, nil
}

// testValidationHandler is a simplified handler for testing
type testValidationHandler struct {
	svc *MockValidationService
	log *logrus.Logger
}

func (h *testValidationHandler) GetRules(w http.ResponseWriter, r *http.Request) {
	rules := h.svc.GetRules()

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"total": len(rules),
			"rules": rules,
		},
	})
}

func (h *testValidationHandler) GetCircuitBreakers(w http.ResponseWriter, r *http.Request) {
	portfolio := &validation.PortfolioState{
		TotalCapital:      10000.0,
		AvailableCapital:  9000.0,
		PeakCapital:       10000.0,
		DailyLoss:         0.0,
		CurrentDrawdown:   0.0,
		ConsecutiveLosses: 0,
		OpenPositions:     0,
	}

	status, err := h.svc.CheckCircuitBreakers(r.Context(), portfolio)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "failed to check circuit breakers")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    status,
	})
}

func (h *testValidationHandler) TestTrade(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Trade     *validation.TradingDecision `json:"trade"`
		Portfolio *validation.PortfolioState  `json:"portfolio"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Trade == nil {
		h.respondError(w, http.StatusBadRequest, "trade is required")
		return
	}

	if req.Portfolio == nil {
		req.Portfolio = &validation.PortfolioState{
			TotalCapital:      10000.0,
			AvailableCapital:  9000.0,
			PeakCapital:       10000.0,
			DailyLoss:         0.0,
			CurrentDrawdown:   0.0,
			ConsecutiveLosses: 0,
			OpenPositions:     0,
		}
	}

	result, err := h.svc.ValidateTrade(r.Context(), req.Trade, req.Portfolio)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "failed to validate trade")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    result,
	})
}

func (h *testValidationHandler) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *testValidationHandler) respondError(w http.ResponseWriter, status int, message string) {
	h.respondJSON(w, status, map[string]interface{}{
		"success": false,
		"error":   message,
	})
}

func TestValidationHandler_GetRules(t *testing.T) {
	tests := []struct {
		name           string
		rules          []*validation.Rule
		expectedStatus int
		expectedCount  int
	}{
		{
			name:           "returns all rules",
			rules:          nil, // Will use default mock rules
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:           "empty rules",
			rules:          []*validation.Rule{},
			expectedStatus: http.StatusOK,
			expectedCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := NewMockValidationService()
			if tt.rules != nil {
				mockSvc.rules = tt.rules
			}

			log := logrus.New()
			log.SetLevel(logrus.PanicLevel)

			handler := &testValidationHandler{
				svc: mockSvc,
				log: log,
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/validation/rules", nil)
			rec := httptest.NewRecorder()

			handler.GetRules(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)

			var response map[string]interface{}
			err := json.NewDecoder(rec.Body).Decode(&response)
			require.NoError(t, err)
			assert.True(t, response["success"].(bool))

			data := response["data"].(map[string]interface{})
			assert.Equal(t, float64(tt.expectedCount), data["total"].(float64))
		})
	}
}

func TestValidationHandler_GetCircuitBreakers(t *testing.T) {
	tests := []struct {
		name           string
		status         *validation.CircuitBreakerStatus
		err            error
		expectedStatus int
	}{
		{
			name: "trading not halted",
			status: &validation.CircuitBreakerStatus{
				TradingHalted:     false,
				DailyLossLimit:    false,
				DrawdownLimit:     false,
				ConsecutiveLosses: 0,
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "trading halted - daily loss",
			status: &validation.CircuitBreakerStatus{
				TradingHalted:  true,
				DailyLossLimit: true,
				DailyLoss:      500.0,
				Reason:         "Daily loss limit exceeded",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "error checking circuit breakers",
			err:            assert.AnError,
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := NewMockValidationService()
			if tt.status != nil {
				mockSvc.circuitBreakerStat = tt.status
			}
			mockSvc.circuitBreakerErr = tt.err

			log := logrus.New()
			log.SetLevel(logrus.PanicLevel)

			handler := &testValidationHandler{
				svc: mockSvc,
				log: log,
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/validation/circuit-breakers", nil)
			rec := httptest.NewRecorder()

			handler.GetCircuitBreakers(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.NewDecoder(rec.Body).Decode(&response)
				require.NoError(t, err)
				assert.True(t, response["success"].(bool))

				data := response["data"].(map[string]interface{})
				assert.Equal(t, tt.status.TradingHalted, data["TradingHalted"].(bool))
			}
		})
	}
}

func TestValidationHandler_TestTrade(t *testing.T) {
	tests := []struct {
		name           string
		body           interface{}
		validateResult *validation.ValidationResult
		validateErr    error
		expectedStatus int
	}{
		{
			name: "valid trade passes",
			body: map[string]interface{}{
				"trade": map[string]interface{}{
					"pair":             "BTC/USDT",
					"direction":        "LONG",
					"entry_price":      50000.0,
					"stop_loss":        49000.0,
					"take_profit":      52000.0,
					"position_size":    0.1,
					"risk_reward_ratio": 2.0,
				},
			},
			validateResult: &validation.ValidationResult{
				Passed:         true,
				RulesEvaluated: 5,
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "trade with portfolio",
			body: map[string]interface{}{
				"trade": map[string]interface{}{
					"pair":        "ETH/USDT",
					"direction":   "SHORT",
					"entry_price": 3000.0,
					"stop_loss":   3100.0,
					"take_profit": 2800.0,
				},
				"portfolio": map[string]interface{}{
					"total_capital":     20000.0,
					"available_capital": 15000.0,
					"peak_capital":      22000.0,
					"daily_loss":        200.0,
					"current_drawdown":  0.1,
				},
			},
			validateResult: &validation.ValidationResult{
				Passed:         true,
				RulesEvaluated: 5,
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "trade fails validation",
			body: map[string]interface{}{
				"trade": map[string]interface{}{
					"pair":             "BTC/USDT",
					"direction":        "LONG",
					"risk_reward_ratio": 0.5,
				},
			},
			validateResult: &validation.ValidationResult{
				Passed: false,
				CriticalFailures: []validation.RuleViolation{
					{
						RuleID:   "min-rr",
						RuleName: "Minimum Risk Reward",
						Severity: "critical",
						Message:  "Risk reward ratio 0.5 is below minimum 1.5",
					},
				},
				RulesEvaluated: 5,
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "missing trade",
			body:           map[string]interface{}{},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid JSON",
			body:           "invalid",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "validation error",
			body: map[string]interface{}{
				"trade": map[string]interface{}{
					"pair":      "BTC/USDT",
					"direction": "LONG",
				},
			},
			validateErr:    assert.AnError,
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := NewMockValidationService()
			if tt.validateResult != nil {
				mockSvc.validateResult = tt.validateResult
			}
			mockSvc.validateTradeErr = tt.validateErr

			log := logrus.New()
			log.SetLevel(logrus.PanicLevel)

			handler := &testValidationHandler{
				svc: mockSvc,
				log: log,
			}

			var body []byte
			var err error
			if str, ok := tt.body.(string); ok {
				body = []byte(str)
			} else {
				body, err = json.Marshal(tt.body)
				require.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/validation/test", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.TestTrade(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.NewDecoder(rec.Body).Decode(&response)
				require.NoError(t, err)
				assert.True(t, response["success"].(bool))
			}
		})
	}
}

func TestValidationHandler_ResponseFormat(t *testing.T) {
	mockSvc := NewMockValidationService()
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	handler := &testValidationHandler{
		svc: mockSvc,
		log: log,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/validation/rules", nil)
	rec := httptest.NewRecorder()

	handler.GetRules(rec, req)

	// Check Content-Type header
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	// Check response structure
	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	// Verify standard response fields
	_, hasSuccess := response["success"]
	_, hasData := response["data"]
	assert.True(t, hasSuccess, "response should have 'success' field")
	assert.True(t, hasData, "response should have 'data' field")
}

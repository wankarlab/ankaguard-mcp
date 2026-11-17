package llm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMarketAnalysis(t *testing.T) {
	tests := []struct {
		name     string
		response *LLMResponse
		wantErr  bool
		check    func(t *testing.T, analysis *LLMAnalysis)
	}{
		{
			name:     "nil response",
			response: nil,
			wantErr:  true,
		},
		{
			name: "valid JSON response",
			response: &LLMResponse{
				Content: `{
					"action": "BUY",
					"confidence": 0.75,
					"reasoning": "Strong bullish momentum",
					"entry_price": 100.50,
					"stop_loss": 95.00,
					"take_profit": 110.00,
					"risk_assessment": "Medium risk",
					"market_condition": "Bullish trend",
					"key_factors": ["RSI oversold", "MACD crossover"],
					"warnings": ["High volatility"]
				}`,
				Provider:  "test",
				Model:     "test-model",
				Timestamp: time.Now(),
			},
			wantErr: false,
			check: func(t *testing.T, analysis *LLMAnalysis) {
				assert.Equal(t, "BUY", analysis.Action)
				assert.Equal(t, 0.75, analysis.Confidence)
				assert.Equal(t, "Strong bullish momentum", analysis.Reasoning)
				assert.Equal(t, 100.50, analysis.EntryPrice)
				assert.Equal(t, 95.00, analysis.StopLoss)
				assert.Equal(t, 110.00, analysis.TakeProfit)
				assert.Equal(t, "Medium risk", analysis.RiskAssessment)
				assert.Equal(t, "Bullish trend", analysis.MarketCondition)
				assert.Len(t, analysis.KeyFactors, 2)
				assert.Contains(t, analysis.KeyFactors, "RSI oversold")
				assert.Len(t, analysis.Warnings, 1)
				assert.Contains(t, analysis.Warnings, "High volatility")
			},
		},
		{
			name: "JSON wrapped in markdown code blocks",
			response: &LLMResponse{
				Content: "```json\n" + `{
					"action": "SELL",
					"confidence": 0.80,
					"reasoning": "Bearish divergence detected",
					"entry_price": 50.25,
					"stop_loss": 52.00,
					"take_profit": 45.00,
					"risk_assessment": "Low risk",
					"market_condition": "Bearish trend"
				}` + "\n```",
				Provider:  "test",
				Model:     "test-model",
				Timestamp: time.Now(),
			},
			wantErr: false,
			check: func(t *testing.T, analysis *LLMAnalysis) {
				assert.Equal(t, "SELL", analysis.Action)
				assert.Equal(t, 0.80, analysis.Confidence)
				assert.Equal(t, "Bearish divergence detected", analysis.Reasoning)
			},
		},
		{
			name: "JSON wrapped in plain code blocks",
			response: &LLMResponse{
				Content: "```\n" + `{
					"action": "WAIT",
					"confidence": 0.50,
					"reasoning": "Unclear market direction",
					"entry_price": 75.00,
					"stop_loss": 70.00,
					"take_profit": 80.00,
					"risk_assessment": "High risk",
					"market_condition": "Sideways"
				}` + "\n```",
				Provider:  "test",
				Model:     "test-model",
				Timestamp: time.Now(),
			},
			wantErr: false,
			check: func(t *testing.T, analysis *LLMAnalysis) {
				assert.Equal(t, "WAIT", analysis.Action)
				assert.Equal(t, 0.50, analysis.Confidence)
			},
		},
		{
			name: "invalid JSON",
			response: &LLMResponse{
				Content:   "This is not JSON",
				Provider:  "test",
				Model:     "test-model",
				Timestamp: time.Now(),
			},
			wantErr: true,
		},
		{
			name: "partial JSON with missing fields",
			response: &LLMResponse{
				Content: `{
					"action": "BUY",
					"confidence": 0.65
				}`,
				Provider:  "test",
				Model:     "test-model",
				Timestamp: time.Now(),
			},
			wantErr: false,
			check: func(t *testing.T, analysis *LLMAnalysis) {
				assert.Equal(t, "BUY", analysis.Action)
				assert.Equal(t, 0.65, analysis.Confidence)
				assert.Empty(t, analysis.Reasoning)
				assert.Equal(t, 0.0, analysis.EntryPrice)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis, err := ParseMarketAnalysis(tt.response)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, analysis)

			if tt.check != nil {
				tt.check(t, analysis)
			}
		})
	}
}

func TestGetString(t *testing.T) {
	tests := []struct {
		name string
		data map[string]interface{}
		key  string
		want string
	}{
		{
			name: "existing string",
			data: map[string]interface{}{"key": "value"},
			key:  "key",
			want: "value",
		},
		{
			name: "missing key",
			data: map[string]interface{}{"other": "value"},
			key:  "key",
			want: "",
		},
		{
			name: "non-string value",
			data: map[string]interface{}{"key": 123},
			key:  "key",
			want: "",
		},
		{
			name: "empty map",
			data: map[string]interface{}{},
			key:  "key",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getString(tt.data, tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetFloat(t *testing.T) {
	tests := []struct {
		name string
		data map[string]interface{}
		key  string
		want float64
	}{
		{
			name: "existing float",
			data: map[string]interface{}{"key": 123.45},
			key:  "key",
			want: 123.45,
		},
		{
			name: "integer value",
			data: map[string]interface{}{"key": 100},
			key:  "key",
			want: 100.0,
		},
		{
			name: "string number",
			data: map[string]interface{}{"key": "75.50"},
			key:  "key",
			want: 75.50,
		},
		{
			name: "missing key",
			data: map[string]interface{}{"other": 123.45},
			key:  "key",
			want: 0.0,
		},
		{
			name: "non-numeric value",
			data: map[string]interface{}{"key": "not a number"},
			key:  "key",
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getFloat(tt.data, tt.key)
			assert.InDelta(t, tt.want, got, 0.001)
		})
	}
}

func TestGetStringArray(t *testing.T) {
	tests := []struct {
		name string
		data map[string]interface{}
		key  string
		want []string
	}{
		{
			name: "existing string array",
			data: map[string]interface{}{
				"key": []interface{}{"item1", "item2", "item3"},
			},
			key:  "key",
			want: []string{"item1", "item2", "item3"},
		},
		{
			name: "empty array",
			data: map[string]interface{}{
				"key": []interface{}{},
			},
			key:  "key",
			want: []string{},
		},
		{
			name: "missing key",
			data: map[string]interface{}{"other": []interface{}{"item"}},
			key:  "key",
			want: []string{},
		},
		{
			name: "mixed types in array",
			data: map[string]interface{}{
				"key": []interface{}{"string", 123, "another"},
			},
			key:  "key",
			want: []string{"string", "another"},
		},
		{
			name: "non-array value",
			data: map[string]interface{}{"key": "not an array"},
			key:  "key",
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getStringArray(tt.data, tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildMarketAnalysisPrompt(t *testing.T) {
	// This would test the prompt building function
	// For now, we'll add a placeholder
	t.Skip("Prompt building function to be tested when implemented")
}

package technical

import (
	"math"
	"testing"
)

func TestPearsonCorrelation(t *testing.T) {
	tests := []struct {
		name     string
		x        []float64
		y        []float64
		expected float64
		delta    float64 // allowed error margin
	}{
		{
			name:     "Perfect positive correlation",
			x:        []float64{1, 2, 3, 4, 5},
			y:        []float64{2, 4, 6, 8, 10},
			expected: 1.0,
			delta:    0.0001,
		},
		{
			name:     "Perfect negative correlation",
			x:        []float64{1, 2, 3, 4, 5},
			y:        []float64{10, 8, 6, 4, 2},
			expected: -1.0,
			delta:    0.0001,
		},
		{
			name:     "No correlation (zero)",
			x:        []float64{1, 2, 3, 4, 5},
			y:        []float64{5, 5, 5, 5, 5},
			expected: 0.0,
			delta:    0.0001,
		},
		{
			name:     "Moderate positive correlation",
			x:        []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			y:        []float64{1.1, 2.2, 2.8, 4.1, 5.0, 5.9, 7.2, 7.8, 9.1, 10.0},
			expected: 0.9973,
			delta:    0.01,
		},
		{
			name:     "Empty series",
			x:        []float64{},
			y:        []float64{},
			expected: 0.0,
			delta:    0.0001,
		},
		{
			name:     "Mismatched length",
			x:        []float64{1, 2, 3},
			y:        []float64{1, 2},
			expected: 0.0,
			delta:    0.0001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pearsonCorrelation(tt.x, tt.y)
			if math.Abs(result-tt.expected) > tt.delta {
				t.Errorf("pearsonCorrelation() = %v, expected %v (delta %v)", result, tt.expected, tt.delta)
			}
		})
	}
}

func TestSortCorrelationsByAbsValue(t *testing.T) {
	correlations := []CorrelationPair{
		{Pair1: "BTC-USDT", Pair2: "ETH-USDT", Correlation: 0.5},
		{Pair1: "BTC-USDT", Pair2: "SOL-USDT", Correlation: -0.9},
		{Pair1: "ETH-USDT", Pair2: "SOL-USDT", Correlation: 0.3},
		{Pair1: "BTC-USDT", Pair2: "DOGE-USDT", Correlation: -0.1},
	}

	sortCorrelationsByAbsValue(correlations)

	// Check order is by absolute value descending
	expectedOrder := []float64{-0.9, 0.5, 0.3, -0.1}
	for i, cp := range correlations {
		if cp.Correlation != expectedOrder[i] {
			t.Errorf("Position %d: got %v, expected %v", i, cp.Correlation, expectedOrder[i])
		}
	}
}

func TestExtractPairs(t *testing.T) {
	tool := &GetCorrelationTool{}

	tests := []struct {
		name        string
		input       interface{}
		expected    []string
		expectError bool
	}{
		{
			name:        "Array of interfaces",
			input:       []interface{}{"BTC-USDT", "ETH-USDT", "SOL-USDT"},
			expected:    []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"},
			expectError: false,
		},
		{
			name:        "Array of strings",
			input:       []string{"BTC-USDT", "ETH-USDT"},
			expected:    []string{"BTC-USDT", "ETH-USDT"},
			expectError: false,
		},
		{
			name:        "Invalid type",
			input:       "not-an-array",
			expected:    nil,
			expectError: true,
		},
		{
			name:        "Mixed array with empty strings (filtered)",
			input:       []interface{}{"BTC-USDT", "", "ETH-USDT"},
			expected:    []string{"BTC-USDT", "ETH-USDT"},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.extractPairs(tt.input)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if len(result) != len(tt.expected) {
					t.Errorf("Length mismatch: got %d, expected %d", len(result), len(tt.expected))
				}
				for i, v := range result {
					if v != tt.expected[i] {
						t.Errorf("Position %d: got %s, expected %s", i, v, tt.expected[i])
					}
				}
			}
		})
	}
}

func TestGetCorrelationToolSchema(t *testing.T) {
	tool := NewGetCorrelationTool(nil)

	// Check name
	if tool.Name() != "get_correlation" {
		t.Errorf("Name() = %s, expected get_correlation", tool.Name())
	}

	// Check schema has required fields
	schema := tool.InputSchema()
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Schema missing properties")
	}

	requiredFields := []string{"exchange", "pairs", "interval", "period", "market_type"}
	for _, field := range requiredFields {
		if _, ok := properties[field]; !ok {
			t.Errorf("Schema missing field: %s", field)
		}
	}

	// Check required array
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("Schema missing required array")
	}

	expectedRequired := map[string]bool{"exchange": true, "pairs": true}
	for _, r := range required {
		if !expectedRequired[r] {
			t.Errorf("Unexpected required field: %s", r)
		}
	}
}

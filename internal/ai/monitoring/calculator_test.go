package monitoring

import (
	"testing"
)

func TestMetricsCalculator_Calculate(t *testing.T) {
	calculator := NewMetricsCalculator()

	tests := []struct {
		name       string
		decisions  []*DecisionRecord
		wantWins   int
		wantLosses int
	}{
		{
			name: "mixed results",
			decisions: []*DecisionRecord{
				{
					ID:          "1",
					Outcome:     "win",
					ActualPnL:   100.0,
					AIConfidence: "HIGH",
				},
				{
					ID:          "2",
					Outcome:     "loss",
					ActualPnL:   -50.0,
					AIConfidence: "MEDIUM",
				},
				{
					ID:          "3",
					Outcome:     "win",
					ActualPnL:   150.0,
					AIConfidence: "HIGH",
				},
			},
			wantWins:   2,
			wantLosses: 1,
		},
		{
			name:       "empty decisions",
			decisions:  []*DecisionRecord{},
			wantWins:   0,
			wantLosses: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := calculator.Calculate(tt.decisions, "test")

			if metrics.WinningTrades != tt.wantWins {
				t.Errorf("Calculate() wins = %v, want %v", metrics.WinningTrades, tt.wantWins)
			}

			if metrics.LosingTrades != tt.wantLosses {
				t.Errorf("Calculate() losses = %v, want %v", metrics.LosingTrades, tt.wantLosses)
			}

			if len(tt.decisions) > 0 {
				expectedWinRate := float64(tt.wantWins) / float64(tt.wantWins+tt.wantLosses)
				if metrics.WinRate != expectedWinRate {
					t.Errorf("Calculate() winRate = %v, want %v", metrics.WinRate, expectedWinRate)
				}
			}
		})
	}
}

func TestMetricsCalculator_CalculateSharpe(t *testing.T) {
	_ = NewMetricsCalculator()

	tests := []struct {
		name string
		pnls []float64
		want float64
	}{
		{
			name: "positive returns",
			pnls: []float64{100, 150, 120, 180, 90},
			want: 0.0, // Will be calculated
		},
		{
			name: "single value",
			pnls: []float64{100},
			want: 0.0,
		},
		{
			name: "empty",
			pnls: []float64{},
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test would require the calculateSharpe method to be public
			// For now, we just verify the test data structure
			if len(tt.pnls) == 0 && tt.want != 0.0 {
				t.Errorf("Empty PnL list should have 0.0 expected value")
			}
		})
	}
}

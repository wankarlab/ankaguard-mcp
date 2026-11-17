package services

import "testing"

// shouldAutoPause is the pure decision helper extracted for testability.
// It mirrors the condition evaluated inside closePosition.
func shouldAutoPause(consecutive, threshold int) bool {
	return threshold > 0 && consecutive >= threshold
}

func TestShouldAutoPause(t *testing.T) {
	cases := []struct {
		consecutive int
		threshold   int
		want        bool
	}{
		{0, 0, false},  // disabled
		{5, 0, false},  // disabled regardless of count
		{0, 3, false},  // below threshold
		{2, 3, false},  // below threshold
		{3, 3, true},   // exactly at threshold
		{10, 3, true},  // above threshold
		{1, 1, true},   // threshold of 1
		{0, -1, false}, // negative threshold = disabled
	}
	for _, tc := range cases {
		got := shouldAutoPause(tc.consecutive, tc.threshold)
		if got != tc.want {
			t.Errorf("shouldAutoPause(%d, %d) = %v; want %v", tc.consecutive, tc.threshold, got, tc.want)
		}
	}
}

package services

import (
	"testing"
	"time"
)

// streakPauseVerdict drives the global loss-streak entry throttle
// (bot.streak_pause.*). These cases pin the probe semantics: the pause holds
// only while the cooldown since the streak's latest close hasn't elapsed —
// afterwards entries reopen so the next trade can seal or re-arm the streak.
func TestStreakPauseVerdict(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	cooldown := 60 * time.Minute

	cases := []struct {
		name      string
		streak    int
		maxLosses int
		lastExit  time.Time
		now       time.Time
		want      bool
	}{
		{"below threshold not paused", 1, 2, base, base.Add(time.Minute), false},
		{"at threshold inside cooldown paused", 2, 2, base, base.Add(30 * time.Minute), true},
		{"above threshold inside cooldown paused", 5, 2, base, base.Add(59 * time.Minute), true},
		{"cooldown elapsed reopens (probe)", 5, 2, base, base.Add(61 * time.Minute), false},
		{"exactly at cooldown boundary reopens", 2, 2, base, base.Add(cooldown), false},
		{"disabled via maxLosses zero", 10, 0, base, base.Add(time.Minute), false},
		{"zero lastExit never pauses", 10, 2, time.Time{}, base, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := streakPauseVerdict(tc.streak, tc.maxLosses, tc.lastExit, cooldown, tc.now)
			if got != tc.want {
				t.Errorf("streakPauseVerdict(streak=%d, max=%d) = %v, want %v",
					tc.streak, tc.maxLosses, got, tc.want)
			}
		})
	}
}

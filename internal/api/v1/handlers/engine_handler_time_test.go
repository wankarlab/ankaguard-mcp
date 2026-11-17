package handlers

import (
	"testing"
	"time"
)

func TestParseTradeHistoryTime(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		// go-sqlite3 default format, as stored in trade_history.
		{"2026-10-03 11:18:14.821220083-05:00", time.Date(2026, 10, 3, 16, 18, 14, 821220083, time.UTC)},
		{"2026-10-03 11:18:14", time.Date(2026, 10, 3, 11, 18, 14, 0, time.UTC)},
		{"2026-10-03T11:18:14Z", time.Date(2026, 10, 3, 11, 18, 14, 0, time.UTC)},
		{"2026-10-03T11:18:14.5-05:00", time.Date(2026, 10, 3, 16, 18, 14, 500000000, time.UTC)},
	}
	for _, c := range cases {
		got, ok := parseTradeHistoryTime(c.in)
		if !ok {
			t.Fatalf("parseTradeHistoryTime(%q) failed", c.in)
		}
		if !got.Equal(c.want) {
			t.Errorf("parseTradeHistoryTime(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, ok := parseTradeHistoryTime("not a time"); ok {
		t.Error("expected failure for invalid input")
	}
}

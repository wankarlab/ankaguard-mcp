package ai

import (
	"sync"
	"testing"
	"time"
)

// TestBeginCloseCAS verifies the atomic double-close guard: the first caller
// wins, concurrent callers are rejected, and EndClose releases for a retry.
func TestBeginCloseCAS(t *testing.T) {
	pos := &ManagedPosition{ID: "t1"}

	if !pos.BeginClose() {
		t.Fatal("first BeginClose must succeed")
	}
	if pos.BeginClose() {
		t.Fatal("second BeginClose must fail while close is in progress")
	}

	// Release (simulating a failed close attempt) and confirm retry is allowed.
	pos.EndClose()
	if !pos.BeginClose() {
		t.Fatal("BeginClose must succeed again after EndClose")
	}
}

// TestBeginCloseCAS_Concurrent confirms exactly one of N concurrent callers
// wins the guard.
func TestBeginCloseCAS_Concurrent(t *testing.T) {
	pos := &ManagedPosition{ID: "t2"}
	const goroutines = 50

	var wins int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if pos.BeginClose() {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("expected exactly 1 winner of the close guard, got %d", wins)
	}
}

// TestGetTTL_NewTimeframes verifies the timeframes added in F0 no longer fall
// through to the 1-minute default (which would hammer the REST API on high TFs).
func TestGetTTL_NewTimeframes(t *testing.T) {
	dc := &DataCollector{}
	cases := []struct {
		tf   string
		want time.Duration
	}{
		{"30m", 8 * time.Minute},
		{"2h", 30 * time.Minute},
		{"8h", 2 * time.Hour},
		{"12h", 3 * time.Hour},
		// existing cases still hold
		{"1m", 30 * time.Second},
		{"5m", 2 * time.Minute},
		{"1h", 15 * time.Minute},
		// unknown still falls back to 1m
		{"99x", 1 * time.Minute},
	}
	for _, c := range cases {
		if got := dc.getTTL(c.tf); got != c.want {
			t.Errorf("getTTL(%q)=%v, want %v", c.tf, got, c.want)
		}
	}
}

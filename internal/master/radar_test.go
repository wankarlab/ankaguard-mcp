package master

import (
	"testing"
	"time"
)

func baseOpps() []*MarketOpportunity {
	return []*MarketOpportunity{
		{Pair: "BTC-USDT", Exchange: "kucoin", OpportunityScore: 0.85},
		{Pair: "ETH-USDT", Exchange: "kucoin", OpportunityScore: 0.75},
		{Pair: "SOL-USDT", Exchange: "kucoin", OpportunityScore: 0.60},
		{Pair: "XRP-USDT", Exchange: "kucoin", OpportunityScore: 0.50},
	}
}

func TestRadar_CandidatesHighScoreUnselected(t *testing.T) {
	r := NewRadar(true, 0.7, 30*time.Minute)
	now := time.Now()

	candidates := r.Candidates(baseOpps(), nil, now)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates (score>=0.7), got %d", len(candidates))
	}
}

func TestRadar_CandidatesSelectedExcluded(t *testing.T) {
	r := NewRadar(true, 0.7, 30*time.Minute)
	now := time.Now()
	selected := []string{"BTC-USDT"}

	candidates := r.Candidates(baseOpps(), selected, now)
	for _, c := range candidates {
		if c.Pair == "BTC-USDT" {
			t.Fatal("BTC-USDT is selected; should not appear in candidates")
		}
	}
	// ETH-USDT (0.75) should still appear.
	found := false
	for _, c := range candidates {
		if c.Pair == "ETH-USDT" {
			found = true
		}
	}
	if !found {
		t.Fatal("ETH-USDT should be a candidate but was not returned")
	}
}

func TestRadar_CandidatesBelowMinScoreExcluded(t *testing.T) {
	r := NewRadar(true, 0.8, 30*time.Minute)
	now := time.Now()

	candidates := r.Candidates(baseOpps(), nil, now)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (score>=0.8), got %d", len(candidates))
	}
	if candidates[0].Pair != "BTC-USDT" {
		t.Fatalf("expected BTC-USDT, got %s", candidates[0].Pair)
	}
}

func TestRadar_CandidatesCooldownSuppressesRepeat(t *testing.T) {
	r := NewRadar(true, 0.7, 30*time.Minute)
	now := time.Now()

	// First call — should return candidates.
	first := r.Candidates(baseOpps(), nil, now)
	if len(first) == 0 {
		t.Fatal("expected candidates on first call")
	}

	// Second call immediately after — all within cooldown, none returned.
	second := r.Candidates(baseOpps(), nil, now.Add(1*time.Minute))
	if len(second) != 0 {
		t.Fatalf("expected 0 candidates within cooldown, got %d", len(second))
	}

	// After cooldown expires — candidates should reappear.
	third := r.Candidates(baseOpps(), nil, now.Add(31*time.Minute))
	if len(third) == 0 {
		t.Fatal("expected candidates after cooldown expiry")
	}
}

func TestRadar_CandidatesDisabledReturnsNil(t *testing.T) {
	r := NewRadar(false, 0.7, 30*time.Minute)
	now := time.Now()

	candidates := r.Candidates(baseOpps(), nil, now)
	if candidates != nil {
		t.Fatalf("disabled radar should return nil, got %v", candidates)
	}
}

func TestRadar_CandidatesZeroMinScoreReturnsNil(t *testing.T) {
	r := NewRadar(true, 0, 30*time.Minute)
	now := time.Now()

	candidates := r.Candidates(baseOpps(), nil, now)
	if candidates != nil {
		t.Fatalf("minScore=0 radar should return nil, got %v", candidates)
	}
}

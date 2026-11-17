package services

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/ai/regime"
)

func defThresholds() fusionThresholds {
	return fusionThresholds{closeScore: -0.6, tightenScore: -0.25, extendScore: 0.6, minSources: 2}
}

func TestFuseVotes_HardVeto(t *testing.T) {
	votes := []exitVote{
		{source: "rule", value: -1, conf: 1, weight: 0.4, hardVeto: true},
		{source: "regime", value: 0.4, conf: 0.7, weight: 0.15}, // would say hold
	}
	d := fuseVotes(votes, defThresholds())
	if d.Action != mgmtClose {
		t.Fatalf("hard veto must force CLOSE, got %s (score %.2f)", d.Action, d.Score)
	}
}

func TestFuseVotes_CloseNeedsMinSources(t *testing.T) {
	// One strong negative source → too few for CLOSE → degrades to TIGHTEN.
	one := []exitVote{{source: "regime", value: -1, conf: 1, weight: 0.5}}
	if d := fuseVotes(one, defThresholds()); d.Action != mgmtTighten {
		t.Errorf("single strong-negative source should TIGHTEN, got %s", d.Action)
	}
	// Two strong negative sources → CLOSE.
	two := []exitVote{
		{source: "regime", value: -1, conf: 1, weight: 0.5},
		{source: "ml", value: -1, conf: 1, weight: 0.5},
	}
	if d := fuseVotes(two, defThresholds()); d.Action != mgmtClose {
		t.Errorf("two strong-negative sources should CLOSE, got %s (score %.2f)", d.Action, d.Score)
	}
}

func TestFuseVotes_MildNegativeTightens(t *testing.T) {
	votes := []exitVote{
		{source: "regime", value: -0.3, conf: 1, weight: 0.5},
		{source: "rule", value: -0.3, conf: 1, weight: 0.5},
	}
	if d := fuseVotes(votes, defThresholds()); d.Action != mgmtTighten {
		t.Errorf("mild negative should TIGHTEN, got %s (score %.2f)", d.Action, d.Score)
	}
}

func TestFuseVotes_ExtendAndHold(t *testing.T) {
	extend := []exitVote{{source: "regime", value: 0.7, conf: 1, weight: 0.5}, {source: "rule", value: 0.7, conf: 1, weight: 0.5}}
	if d := fuseVotes(extend, defThresholds()); d.Action != mgmtExtend {
		t.Errorf("strong positive should EXTEND, got %s (score %.2f)", d.Action, d.Score)
	}
	hold := []exitVote{{source: "regime", value: 0.1, conf: 1, weight: 0.5}}
	if d := fuseVotes(hold, defThresholds()); d.Action != mgmtHold {
		t.Errorf("weak positive should HOLD, got %s (score %.2f)", d.Action, d.Score)
	}
}

func TestFuseVotes_AllAbstainHolds(t *testing.T) {
	votes := []exitVote{
		{source: "ml", abstain: true, weight: 0.2},
		{source: "llm", abstain: true, weight: 0.25},
	}
	d := fuseVotes(votes, defThresholds())
	if d.Action != mgmtHold || d.NonAbstain != 0 {
		t.Errorf("all-abstain must HOLD with 0 sources, got %s n=%d", d.Action, d.NonAbstain)
	}
}

func TestRegimeExitVote(t *testing.T) {
	cases := []struct {
		regime   string
		isLong   bool
		wantSign int // -1, 0, +1
	}{
		{string(regime.RegimeTrendingUp), true, +1},
		{string(regime.RegimeTrendingUp), false, -1},
		{string(regime.RegimeTrendingDown), false, +1},
		{string(regime.RegimeTrendingDown), true, -1},
		{string(regime.RegimeChaos), true, -1},
		{string(regime.RegimeRanging), true, -1},
		{string(regime.RegimeBreakout), true, +1},
	}
	for _, c := range cases {
		v, _ := regimeExitVote(c.regime, c.isLong)
		sign := 0
		if v > 0 {
			sign = 1
		} else if v < 0 {
			sign = -1
		}
		if sign != c.wantSign {
			t.Errorf("regimeExitVote(%s, long=%v)=%.2f, want sign %d", c.regime, c.isLong, v, c.wantSign)
		}
	}
}

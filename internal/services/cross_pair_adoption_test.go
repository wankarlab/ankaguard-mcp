package services

import (
	"io"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai"
)

// TestNormalizeSymbolForMatch verifies that exchange-format futures symbols, dash
// pairs, and slash pairs all collapse to the same canonical key so cross-pair
// adoption and the pair-switch gate match positions regardless of source format.
func TestNormalizeSymbolForMatch(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"DOTUSDTM", "DOTUSDT"},   // exchange futures format
		{"DOT-USDT", "DOTUSDT"},   // dash pair
		{"DOT/USDT", "DOTUSDT"},   // slash pair
		{"dotusdtm", "DOTUSDT"},   // lowercase
		{" SOL-USDT ", "SOLUSDT"}, // whitespace
		{"XMR-USDT", "XMRUSDT"},   // base asset ending in M must keep its M
		{"XMRUSDTM", "XMRUSDT"},   // ...but the futures M suffix is still stripped
		{"", ""},                  // empty
	}
	for _, c := range cases {
		if got := normalizeSymbolForMatch(c.in); got != c.want {
			t.Errorf("normalizeSymbolForMatch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNormalizeSymbolForMatch_CrossFormatEquality is the property the fix relies on:
// an adopted exchange symbol and the bot's dash pair must compare equal.
func TestNormalizeSymbolForMatch_CrossFormatEquality(t *testing.T) {
	if normalizeSymbolForMatch("DOTUSDTM") != normalizeSymbolForMatch("DOT-USDT") {
		t.Fatal("exchange futures symbol must match dash pair after normalization")
	}
	if normalizeSymbolForMatch("SOL-USDT") == normalizeSymbolForMatch("DOT-USDT") {
		t.Fatal("different pairs must NOT collapse to the same key")
	}
}

// TestOpenPositionsForPair verifies the per-pair counter used by the switch gate:
// it counts only positions whose symbol matches the target pair (normalized), so
// positions on other adopted pairs do not block rotating away from the current one.
func TestOpenPositionsForPair(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	pm := ai.NewPositionManager(nil, log)
	pm.AddPosition(&ai.ManagedPosition{ID: "1", Symbol: "DOTUSDTM", Side: "sell", Status: "open"})
	pm.AddPosition(&ai.ManagedPosition{ID: "2", Symbol: "SOL-USDT", Side: "buy", Status: "open"})
	pm.AddPosition(&ai.ManagedPosition{ID: "3", Symbol: "SOLUSDTM", Side: "buy", Status: "open"})

	bot := &AITradingBot{positionManager: pm}

	if got := bot.openPositionsForPair("DOT-USDT"); got != 1 {
		t.Errorf("openPositionsForPair(DOT-USDT) = %d, want 1", got)
	}
	if got := bot.openPositionsForPair("SOL-USDT"); got != 2 {
		t.Errorf("openPositionsForPair(SOL-USDT) = %d, want 2 (dash + exchange format)", got)
	}
	if got := bot.openPositionsForPair("BTC-USDT"); got != 0 {
		t.Errorf("openPositionsForPair(BTC-USDT) = %d, want 0", got)
	}
	if got := bot.openPositionsForPair(""); got != 0 {
		t.Errorf("openPositionsForPair(\"\") = %d, want 0", got)
	}
}

// TestPairRecentlyOpened_RaceGuard verifies the open-vs-adopt race guard: a pair
// the bot just started opening is reported as recently-opened (so adoption skips
// it), in any symbol format, while an unmarked pair is not.
func TestPairRecentlyOpened_RaceGuard(t *testing.T) {
	bot := &AITradingBot{}

	// Unmarked pair: not recently opened.
	if bot.pairRecentlyOpened("DOT-USDT") {
		t.Fatal("unmarked pair must not be reported as recently opened")
	}

	// Mark in dash format, query in exchange format — must match via normalization.
	bot.markPairOpening("DOT-USDT")
	if !bot.pairRecentlyOpened("DOTUSDTM") {
		t.Fatal("pair opened as DOT-USDT must be recently-opened when queried as DOTUSDTM")
	}
	if !bot.pairRecentlyOpened("DOT-USDT") {
		t.Fatal("pair opened as DOT-USDT must be recently-opened when queried as DOT-USDT")
	}

	// A different pair stays unguarded.
	if bot.pairRecentlyOpened("SOL-USDT") {
		t.Fatal("a pair that was never opened must not be guarded")
	}

	// Empty symbol is safe (no panic, not guarded).
	if bot.pairRecentlyOpened("") {
		t.Fatal("empty symbol must not be reported as recently opened")
	}
}

// TestDropDuplicateManagedPositions verifies the single-position-per-(symbol,side)
// invariant: opening a position drops any prior managed copy (e.g. an adopted
// orphan) for the same symbol+side, in any format, while leaving the new position
// and unrelated positions intact.
func TestDropDuplicateManagedPositions(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	pm := ai.NewPositionManager(nil, log)
	// A prior sync adopted the SOL short in exchange format.
	pm.AddPosition(&ai.ManagedPosition{ID: "ADOPTED-SOLUSDTM-sell-1", Symbol: "SOLUSDTM", Side: "sell", Status: "open"})
	// An unrelated position that must survive.
	pm.AddPosition(&ai.ManagedPosition{ID: "DOT-long", Symbol: "DOT-USDT", Side: "buy", Status: "open"})
	// The fresh bot-opened SOL short (dash format, exchange order id).
	pm.AddPosition(&ai.ManagedPosition{ID: "451449610431500288", Symbol: "SOL-USDT", Side: "sell", Status: "open"})

	bot := &AITradingBot{positionManager: pm, log: log}
	bot.dropDuplicateManagedPositions("SOL-USDT", "sell", "451449610431500288")

	ids := map[string]bool{}
	for _, p := range pm.GetAllPositions() {
		ids[p.ID] = true
	}
	if ids["ADOPTED-SOLUSDTM-sell-1"] {
		t.Error("adopted SOL duplicate should have been dropped")
	}
	if !ids["451449610431500288"] {
		t.Error("fresh bot-opened SOL (keepID) must be retained")
	}
	if !ids["DOT-long"] {
		t.Error("unrelated DOT position must be retained")
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 positions after dedup, got %d", len(ids))
	}
}

// TestRaceGuardHelpers_NoBotMuDeadlock is a regression test for the startup
// deadlock: StartWithContext holds bot.mu.Lock() while the initial sync runs, and
// the race-guard/anti-flapping helpers must NOT take bot.mu (a non-reentrant
// RWMutex) or they self-deadlock and freeze the whole bot at boot. Holding bot.mu
// here and calling the helpers must complete; if any helper grabbed bot.mu the
// test would hang and the -timeout would fail it.
func TestRaceGuardHelpers_NoBotMuDeadlock(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	bot := &AITradingBot{positionManager: ai.NewPositionManager(nil, log), log: log}

	bot.mu.Lock()
	defer bot.mu.Unlock()

	// All of these run inside syncOpenPositionsFromExchange, which executes while
	// bot.mu is held during startup. None may touch bot.mu.
	bot.markPairOpening("SOL-USDT")
	_ = bot.pairRecentlyOpened("SOLUSDTM")
	_, _ = bot.recordGhostMiss("pos-1")
	bot.clearGhostMiss("pos-1")
	_ = bot.openPositionsForPair("SOL-USDT")
	bot.dropDuplicateManagedPositions("SOL-USDT", "sell", "keep")
}

// TestSideIsLong checks the side-vocabulary normalization used by the invariant.
func TestSideIsLong(t *testing.T) {
	for _, s := range []string{"buy", "BUY", "long", "LONG", " Long "} {
		if !sideIsLong(s) {
			t.Errorf("sideIsLong(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"sell", "SELL", "short", "SHORT", ""} {
		if sideIsLong(s) {
			t.Errorf("sideIsLong(%q) = true, want false", s)
		}
	}
}

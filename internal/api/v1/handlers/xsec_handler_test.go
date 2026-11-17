package handlers

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// Regression test for 2026-07-23: GetFuturesPositions returns pos.Symbol
// already normalized to "BASE-USDT" when the client's symbolConverter is
// active, but the lookup used to try only the raw KuCoin futures form
// ("BASEUSDTM") — a mismatch that silently zeroed PnL enrichment for every
// leg. matchLivePosition must succeed against BOTH symbol shapes.
func TestMatchLivePositionStandardSymbol(t *testing.T) {
	bySymbol := map[string]*gct.Position{
		"BCH-USDT": {Symbol: "BCH-USDT", UnrealizedPnL: 0.0306},
	}
	live, ok := matchLivePosition("BCH-USDT", bySymbol)
	if !ok {
		t.Fatal("want match on standard \"BASE-USDT\" symbol, got none")
	}
	if live.UnrealizedPnL != 0.0306 {
		t.Errorf("unrealized pnl: want 0.0306, got %v", live.UnrealizedPnL)
	}
}

func TestMatchLivePositionRawFuturesSymbol(t *testing.T) {
	bySymbol := map[string]*gct.Position{
		"BCHUSDTM": {Symbol: "BCHUSDTM", UnrealizedPnL: 0.0306},
	}
	live, ok := matchLivePosition("BCH-USDT", bySymbol)
	if !ok {
		t.Fatal("want match on raw KuCoin futures symbol, got none")
	}
	if live.UnrealizedPnL != 0.0306 {
		t.Errorf("unrealized pnl: want 0.0306, got %v", live.UnrealizedPnL)
	}
}

func TestMatchLivePositionBTCUsesXBT(t *testing.T) {
	bySymbol := map[string]*gct.Position{
		"XBTUSDTM": {Symbol: "XBTUSDTM", UnrealizedPnL: 1.5},
	}
	if _, ok := matchLivePosition("BTC-USDT", bySymbol); !ok {
		t.Fatal("want BTC-USDT to match the XBT-prefixed raw futures symbol")
	}
}

func TestMatchLivePositionNoMatch(t *testing.T) {
	bySymbol := map[string]*gct.Position{
		"ETH-USDT": {Symbol: "ETH-USDT"},
	}
	if _, ok := matchLivePosition("SOL-USDT", bySymbol); ok {
		t.Fatal("want no match for a pair absent from bySymbol under either form")
	}
}

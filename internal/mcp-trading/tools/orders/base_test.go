package orders

import (
	"strings"
	"testing"
)

// TestDeterministicClientOrderID_StableForIdenticalIntent verifies that two
// calls with identical order content within the same minute bucket produce the
// SAME id (audit P0a — idempotent retries dedup at the exchange).
func TestDeterministicClientOrderID_StableForIdenticalIntent(t *testing.T) {
	a := deterministicClientOrderID("kucoin", "BTC-USDT", "buy", "limit", 0.01, 65000)
	b := deterministicClientOrderID("kucoin", "BTC-USDT", "buy", "limit", 0.01, 65000)
	if a != b {
		t.Fatalf("expected identical ids for identical intent, got %q and %q", a, b)
	}
}

// TestDeterministicClientOrderID_DiffersForDistinctIntent verifies that
// genuinely different orders produce different ids so they are not collapsed.
func TestDeterministicClientOrderID_DiffersForDistinctIntent(t *testing.T) {
	base := deterministicClientOrderID("kucoin", "BTC-USDT", "buy", "limit", 0.01, 65000)

	cases := map[string]string{
		"side":     deterministicClientOrderID("kucoin", "BTC-USDT", "sell", "limit", 0.01, 65000),
		"pair":     deterministicClientOrderID("kucoin", "ETH-USDT", "buy", "limit", 0.01, 65000),
		"exchange": deterministicClientOrderID("binance", "BTC-USDT", "buy", "limit", 0.01, 65000),
		"type":     deterministicClientOrderID("kucoin", "BTC-USDT", "buy", "market", 0.01, 65000),
		"amount":   deterministicClientOrderID("kucoin", "BTC-USDT", "buy", "limit", 0.02, 65000),
		"price":    deterministicClientOrderID("kucoin", "BTC-USDT", "buy", "limit", 0.01, 65500),
	}
	for field, id := range cases {
		if id == base {
			t.Errorf("expected distinct id when %s differs, got collision %q", field, id)
		}
	}
}

// TestDeterministicClientOrderID_FormatAndLength verifies the prefix and that
// the id stays within exchange clientOrderId length limits (KuCoin: 40 chars).
func TestDeterministicClientOrderID_FormatAndLength(t *testing.T) {
	id := deterministicClientOrderID("kucoin", "BTC-USDT", "buy", "limit", 0.01, 65000)
	if !strings.HasPrefix(id, "mcp-") {
		t.Errorf("expected %q to have prefix %q", id, "mcp-")
	}
	if len(id) > 40 {
		t.Errorf("id length %d exceeds 40-char clientOid limit: %q", len(id), id)
	}
	if len(id) != len("mcp-")+16 {
		t.Errorf("expected 20-char id, got %d: %q", len(id), id)
	}
}

// TestResolveClientOrderID_RespectsSupplied verifies a caller-supplied id is
// passed through unchanged and only generated when empty.
func TestResolveClientOrderID_RespectsSupplied(t *testing.T) {
	supplied := resolveClientOrderID("caller-123", "kucoin", "BTC-USDT", "buy", "limit", 0.01, 65000)
	if supplied != "caller-123" {
		t.Errorf("expected supplied id to be respected, got %q", supplied)
	}

	generated := resolveClientOrderID("", "kucoin", "BTC-USDT", "buy", "limit", 0.01, 65000)
	if !strings.HasPrefix(generated, "mcp-") {
		t.Errorf("expected generated id when supplied is empty, got %q", generated)
	}
}

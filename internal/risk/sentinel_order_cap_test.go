package risk

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/config/dynamic"
)

func TestEffectiveOrderCap(t *testing.T) {
	cases := []struct {
		name    string
		values  map[string]string
		capital float64
		want    float64
	}{
		{"absolute only", map[string]string{"risk.max_order_size_usd": "2"}, 27, 2},
		{"relative tighter", map[string]string{"risk.max_order_size_usd": "50", "risk.max_order_size_pct": "20"}, 27, 5.4},
		{"absolute tighter", map[string]string{"risk.max_order_size_usd": "50", "risk.max_order_size_pct": "20"}, 500, 50},
		{"relative only", map[string]string{"risk.max_order_size_pct": "20"}, 500, 100},
		{"capital unknown", map[string]string{"risk.max_order_size_usd": "2", "risk.max_order_size_pct": "20"}, 0, 2},
		{"no caps", map[string]string{}, 500, 0},
	}
	for _, c := range cases {
		got := effectiveOrderCap(dynamic.NewStaticSnapshot(c.values), c.capital)
		if got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("%s: cap = %.4f, want %.4f", c.name, got, c.want)
		}
	}
}

package tracker

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
)

func TestEquityValuesEveryHoldingInUSDT(t *testing.T) {
	tr := &PortfolioTracker{priceUSDT: func(_ context.Context, cur string) (float64, error) {
		switch cur {
		case "SHIB":
			return 0.000012, nil
		case "BTC":
			return 100000, nil
		case "DEAD":
			return 0, nil // ticker.Last=0 must never value a holding
		}
		return 0, errors.New("no market")
	}}
	holdings := map[string]*gct.Holding{
		"USDT-FUTURES": {Currency: "USDT", Total: 25.36},
		"USDT":         {Currency: "USDT", Total: 1},
		"SHIB":         {Currency: "SHIB", Total: 270693.9586},
		"BTC":          {Currency: "BTC", Total: 0.00000719},
		"DEAD":         {Currency: "DEAD", Total: 5},
		"NOMKT":        {Currency: "NOMKT", Total: 3},
		"ZERO":         {Currency: "ZERO", Total: 0},
	}
	total, unpriced := tr.equityUSDT(context.Background(), holdings)
	want := 25.36 + 1 + 270693.9586*0.000012 + 0.00000719*100000
	if math.Abs(total-want) > 1e-9 {
		t.Fatalf("total %.9f, want %.9f", total, want)
	}
	if len(unpriced) != 2 {
		t.Fatalf("unpriced %v, want DEAD and NOMKT", unpriced)
	}
}

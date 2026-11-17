package universe

import "testing"

func TestPassesVolume(t *testing.T) {
	f := &DiscoveryFilters{MinVolume24hUSD: 100_000}

	if f.PassesVolume(50_000) {
		t.Error("expected volume 50k to fail when min is 100k")
	}
	if !f.PassesVolume(100_000) {
		t.Error("expected volume equal to min to pass")
	}
	if !f.PassesVolume(200_000) {
		t.Error("expected volume above min to pass")
	}

	// Zero min means no restriction.
	fNoMin := &DiscoveryFilters{}
	if !fNoMin.PassesVolume(0) {
		t.Error("expected zero-min to pass any volume")
	}
}

func TestIsExcluded_StablecoinDash(t *testing.T) {
	f := &DiscoveryFilters{ExcludeStablecoins: true}

	stablePairs := []string{"USDC-USDT", "DAI-USDT", "TUSD-USDT", "FDUSD-USDT"}
	for _, p := range stablePairs {
		if !f.IsExcluded(p) {
			t.Errorf("expected stablecoin pair %q to be excluded", p)
		}
	}

	// Non-stablecoin base must not be excluded.
	if f.IsExcluded("BTC-USDT") {
		t.Error("BTC-USDT should not be excluded as stablecoin pair")
	}
}

func TestIsExcluded_StablecoinNoSeparator(t *testing.T) {
	f := &DiscoveryFilters{ExcludeStablecoins: true}

	if !f.IsExcluded("USDCUSDT") {
		t.Error("expected USDCUSDT to be excluded (USDC base is stablecoin)")
	}
	if f.IsExcluded("BTCUSDT") {
		t.Error("BTCUSDT should not be excluded")
	}
}

func TestIsExcluded_ExplicitList(t *testing.T) {
	f := &DiscoveryFilters{
		Exclude: []string{"ETH-BTC", "LTC-USDT"},
	}

	if !f.IsExcluded("eth-btc") {
		t.Error("expected case-insensitive match for eth-btc")
	}
	if !f.IsExcluded("LTC-USDT") {
		t.Error("expected LTC-USDT to be excluded")
	}
	if f.IsExcluded("BNB-USDT") {
		t.Error("BNB-USDT should not be excluded")
	}
}

func TestPassesVolatility(t *testing.T) {
	f := &DiscoveryFilters{
		MinVolatilityATRPct: 1.0,
		MaxVolatilityATRPct: 5.0,
	}

	// Below min.
	if f.PassesVolatility(0.5) {
		t.Error("0.5 should fail below min 1.0")
	}
	// Exactly at min edge.
	if !f.PassesVolatility(1.0) {
		t.Error("1.0 should pass at min edge")
	}
	// Within band.
	if !f.PassesVolatility(3.0) {
		t.Error("3.0 should pass within [1,5]")
	}
	// Exactly at max edge.
	if !f.PassesVolatility(5.0) {
		t.Error("5.0 should pass at max edge")
	}
	// Above max.
	if f.PassesVolatility(5.1) {
		t.Error("5.1 should fail above max 5.0")
	}

	// Zero min = unbounded low.
	fNoMin := &DiscoveryFilters{MaxVolatilityATRPct: 5.0}
	if !fNoMin.PassesVolatility(0.0) {
		t.Error("0.0 should pass when min is 0 (unbounded)")
	}

	// Zero max = unbounded high.
	fNoMax := &DiscoveryFilters{MinVolatilityATRPct: 1.0}
	if !fNoMax.PassesVolatility(1000.0) {
		t.Error("1000.0 should pass when max is 0 (unbounded)")
	}
}

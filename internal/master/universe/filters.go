package universe

import "strings"

// knownStablecoins is the set of well-known stablecoin base symbols (upper-case).
var knownStablecoins = map[string]struct{}{
	"USDT":  {},
	"USDC":  {},
	"BUSD":  {},
	"DAI":   {},
	"TUSD":  {},
	"FDUSD": {},
	"USDP":  {},
	"GUSD":  {},
	"FRAX":  {},
	"LUSD":  {},
	"SUSD":  {},
	"EURC":  {},
	"EURT":  {},
	"EURS":  {},
}

// DiscoveryFilters holds the criteria used to screen pairs during universe discovery.
//
// Filtering is applied in two stages:
//   - Stage 1 (ticker): volume filter via PassesVolume.
//   - Stage 2 (post-candles/ATR): volatility and correlation filters via
//     PassesVolatility and PassesCorrelation.
//
// IsExcluded and PassesVolume can be applied at either stage.
type DiscoveryFilters struct {
	MinVolume24hUSD     float64
	MinVolatilityATRPct float64
	MaxVolatilityATRPct float64
	MaxBTCCorrelation   float64
	ExcludeStablecoins  bool
	Exclude             []string // exact pair symbols to drop (case-insensitive)
}

// PassesVolume returns true when the pair's 24-h USD volume meets the minimum.
// A MinVolume24hUSD of 0 (or negative) disables the check.
func (f *DiscoveryFilters) PassesVolume(volume24hUSD float64) bool {
	return f.MinVolume24hUSD <= 0 || volume24hUSD >= f.MinVolume24hUSD
}

// PassesVolatility returns true when atrPct falls within
// [MinVolatilityATRPct, MaxVolatilityATRPct].
// A zero value on either bound means that side is unbounded.
func (f *DiscoveryFilters) PassesVolatility(atrPct float64) bool {
	if f.MinVolatilityATRPct > 0 && atrPct < f.MinVolatilityATRPct {
		return false
	}
	if f.MaxVolatilityATRPct > 0 && atrPct > f.MaxVolatilityATRPct {
		return false
	}
	return true
}

// PassesCorrelation returns true when the pair's BTC correlation is at or below
// the maximum. A MaxBTCCorrelation of 0 (or negative) disables the check.
func (f *DiscoveryFilters) PassesCorrelation(btcCorr float64) bool {
	return f.MaxBTCCorrelation <= 0 || btcCorr <= f.MaxBTCCorrelation
}

// IsExcluded returns true when the pair should be dropped because it appears in
// the explicit Exclude list (case-insensitive), or because ExcludeStablecoins is
// true and the pair's base asset is a known stablecoin.
func (f *DiscoveryFilters) IsExcluded(pair string) bool {
	upper := strings.ToUpper(pair)

	for _, ex := range f.Exclude {
		if strings.EqualFold(ex, pair) {
			return true
		}
	}

	if f.ExcludeStablecoins {
		base := baseSymbol(upper)
		if _, ok := knownStablecoins[base]; ok {
			return true
		}
	}

	return false
}

// baseSymbol extracts the base from a pair like "USDC-USDT" or "USDCUSDT".
// It checks the known-stablecoin set for common separators first, then falls back
// to a prefix scan.
func baseSymbol(upper string) string {
	// Handle dash / underscore / slash separators.
	for _, sep := range []string{"-", "_", "/"} {
		if idx := strings.Index(upper, sep); idx > 0 {
			return upper[:idx]
		}
	}
	// No separator: try to find a known stablecoin quote suffix and strip it.
	for quote := range knownStablecoins {
		if strings.HasSuffix(upper, quote) && len(upper) > len(quote) {
			return upper[:len(upper)-len(quote)]
		}
	}
	return upper
}

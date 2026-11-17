package gctclient

import (
	"context"
	"fmt"
	"strings"
)

// GetTradablePairs returns the live tradable pairs for an exchange and asset
// type, sourced from GCT's GetExchangeInfo (enabled/available pairs).
func (c *Client) GetTradablePairs(ctx context.Context, exchange, assetType string) ([]string, error) {
	info, err := c.gctClient.GetExchangeInfo(ctx, exchange)
	if err != nil {
		return nil, fmt.Errorf("GetExchangeInfo for %s: %w", exchange, err)
	}

	supportedAssets, ok := info["supported_assets"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("supported_assets missing or malformed for %s", exchange)
	}

	assetData, ok := supportedAssets[assetType].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("asset type %q not found for %s", assetType, exchange)
	}

	// Prefer the full available universe — enabled_pairs is a small subset GCT
	// auto-syncs/streams, while available_pairs lists everything the exchange
	// offers. The downstream scanner fetches candles via the spot endpoint
	// (which serves any valid pair, enabled or not), so available is the right
	// universe for opportunity discovery.
	raw := toStringSlice(assetData["available_pairs"])
	if len(raw) == 0 {
		raw = toStringSlice(assetData["enabled_pairs"])
	}

	// Futures pairs come in GCT contract format (e.g. XBT_USDTM, SOL_USDTM).
	// The rest of the pipeline (regime analysis, indicators, order routing via
	// ConvertSymbol) expects canonical spot-style BASE-QUOTE pairs, and candle
	// fetches only succeed in that format. Normalize here so discovery is a
	// drop-in replacement for the previous hardcoded BASE-QUOTE list.
	if assetType == "futures" {
		for i, p := range raw {
			raw[i] = normalizeFuturesSymbol(p)
		}
	}

	return normalizePairs(raw), nil
}

// normalizeFuturesSymbol converts a GCT KuCoin futures contract symbol
// (BASE_QUOTEM, e.g. "XBT_USDTM", "ETH_USDCM", "1000PEPE_USDTM") into the
// canonical spot-style "BASE-QUOTE" form used across the trading pipeline.
// It maps KuCoin's XBT back to BTC and drops the trailing perpetual "M" marker.
// Inputs without an underscore are returned uppercased unchanged (best effort).
func normalizeFuturesSymbol(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	i := strings.Index(s, "_")
	if i < 0 {
		return s
	}
	base, quote := s[:i], s[i+1:]
	quote = strings.TrimSuffix(quote, "M") // USDTM -> USDT, USDCM -> USDC
	if base == "XBT" {
		base = "BTC"
	}
	if quote == "" {
		return base
	}
	return base + "-" + quote
}

// toStringSlice converts a value to []string. GCT's gRPC GetExchangeInfo
// returns AvailablePairs/EnabledPairs as a single comma-separated string
// (currency.Pairs.Join() uses ","), so the string case must be split — not
// treated as empty. Also handles []string / []interface{} defensively.
func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case string:
		if strings.TrimSpace(val) == "" {
			return nil
		}
		// GCT joins pairs with ",". Tolerate stray whitespace too.
		fields := strings.FieldsFunc(val, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		})
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			if f = strings.TrimSpace(f); f != "" {
				out = append(out, f)
			}
		}
		return out
	case []string:
		return val
	case []interface{}:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// normalizePairs deduplicates pairs while preserving order.
func normalizePairs(pairs []string) []string {
	seen := make(map[string]struct{}, len(pairs))
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

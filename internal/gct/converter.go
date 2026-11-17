package gct

import (
	"fmt"
	"strings"
)

// MarketType represents the type of market
type MarketType string

const (
	MarketTypeSpot    MarketType = "spot"
	MarketTypeFutures MarketType = "futures"
	MarketTypeMargin  MarketType = "margin"
	MarketTypeOptions MarketType = "options"
)

// SymbolConverter handles conversion between standard and exchange-specific symbol formats
type SymbolConverter struct{}

// NewSymbolConverter creates a new SymbolConverter
func NewSymbolConverter() *SymbolConverter {
	return &SymbolConverter{}
}

// ToExchangeFormat converts a standard symbol to exchange-specific format
// Example: "BTC-USDT" + futures + kucoin → "BTCUSDTM"
func (c *SymbolConverter) ToExchangeFormat(symbol string, marketType MarketType, exchange string) (string, error) {
	if symbol == "" {
		return "", fmt.Errorf("symbol cannot be empty")
	}

	exchange = strings.ToLower(exchange)

	// KuCoin specific conversions
	if exchange == "kucoin" {
		if marketType == MarketTypeFutures {
			// BTC-USDT → BTCUSDTM (KuCoin futures: strip dash, append M)
			converted := strings.ReplaceAll(symbol, "-", "") + "M"
			return converted, nil
		}
	}

	// For spot or other exchanges, return as-is
	return symbol, nil
}

// FromExchangeFormat converts exchange-specific format to standard symbol
// Example: "BTCUSDTM" + futures + kucoin → "BTC-USDT"
func (c *SymbolConverter) FromExchangeFormat(symbol string, marketType MarketType, exchange string) (string, error) {
	if symbol == "" {
		return "", fmt.Errorf("symbol cannot be empty")
	}

	exchange = strings.ToLower(exchange)

	// KuCoin specific conversions
	if exchange == "kucoin" {
		if marketType == MarketTypeFutures && strings.HasSuffix(symbol, "M") {
			// BTCUSDTM → BTC-USDT  (strip M suffix, then split on known quote)
			base := strings.TrimSuffix(symbol, "M")
			if len(base) < 4 {
				return "", fmt.Errorf("invalid futures symbol: %s", symbol)
			}

			// Known quote currencies ordered longest-first to avoid prefix collisions.
			quoteCurrencies := []string{"USDT", "USDC", "USD", "BTC", "ETH"}
			for _, quote := range quoteCurrencies {
				if strings.HasSuffix(base, quote) {
					baseCurrency := strings.TrimSuffix(base, quote)
					if baseCurrency != "" {
						return baseCurrency + "-" + quote, nil
					}
				}
			}

			return "", fmt.Errorf("unable to parse futures symbol: %s", symbol)
		}
	}

	// For spot or other exchanges, return as-is
	return symbol, nil
}

// ValidateMarketType validates if the market type is supported
func ValidateMarketType(marketType string) error {
	switch MarketType(marketType) {
	case MarketTypeSpot, MarketTypeFutures, MarketTypeMargin, MarketTypeOptions:
		return nil
	default:
		return fmt.Errorf("invalid market type: %s (must be one of: spot, futures, margin, options)", marketType)
	}
}

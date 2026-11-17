package pairs

import (
	"fmt"
	"regexp"
	"strings"
)

// PairConverter handles conversion between different trading pair formats
type PairConverter struct {
	// Standard format is BASE-QUOTE (e.g., BTC-USDT)
	standardFormat string
}

// NewPairConverter creates a new pair converter instance
func NewPairConverter() *PairConverter {
	return &PairConverter{
		standardFormat: "BASE-QUOTE",
	}
}

// ExchangeFormat represents different exchange formats
type ExchangeFormat string

const (
	FormatStandard      ExchangeFormat = "standard"       // BTC-USDT
	FormatKuCoinSpot    ExchangeFormat = "kucoin_spot"    // BTC-USDT
	FormatKuCoinFutures ExchangeFormat = "kucoin_futures" // BTCUSDTM
	FormatBinance       ExchangeFormat = "binance"        // BTCUSDT
	FormatBybit         ExchangeFormat = "bybit"          // BTCUSDT
	FormatOKX           ExchangeFormat = "okx"            // BTC-USDT-SWAP
)

// MarketType represents different market types
type MarketType string

const (
	MarketSpot    MarketType = "spot"
	MarketFutures MarketType = "futures"
	MarketMargin  MarketType = "margin"
	MarketOptions MarketType = "options"
	MarketSwap    MarketType = "swap"
)

// TradingPair represents a standardized trading pair
type TradingPair struct {
	Standard     string     `json:"standard"`      // BTC-USDT
	Base         string     `json:"base"`          // BTC
	Quote        string     `json:"quote"`         // USDT
	MarketType   MarketType `json:"market_type"`   // spot, futures, etc.
	Exchange     string     `json:"exchange"`      // kucoin, binance, etc.
	ExchangePair string     `json:"exchange_pair"` // BTCUSDTM, BTCUSDT, etc.
}

// ConvertPair converts a trading pair from one format to another
func (pc *PairConverter) ConvertPair(pair string, fromFormat, toFormat ExchangeFormat, marketType MarketType) (string, error) {
	// First normalize to standard format
	standardPair, err := pc.NormalizeToStandard(pair, fromFormat, marketType)
	if err != nil {
		return "", fmt.Errorf("failed to normalize pair %s: %w", pair, err)
	}

	// Then convert to target format
	targetPair, err := pc.ConvertFromStandard(standardPair, toFormat, marketType)
	if err != nil {
		return "", fmt.Errorf("failed to convert to format %s: %w", toFormat, err)
	}

	return targetPair, nil
}

// NormalizeToStandard converts any format to standard BASE-QUOTE format
func (pc *PairConverter) NormalizeToStandard(pair string, fromFormat ExchangeFormat, marketType MarketType) (string, error) {
	pair = strings.ToUpper(strings.TrimSpace(pair))

	switch fromFormat {
	case FormatStandard:
		return pair, nil

	case FormatKuCoinSpot, FormatKuCoinFutures:
		return pc.normalizeKuCoin(pair, marketType)

	case FormatBinance, FormatBybit:
		return pc.normalizeBinanceStyle(pair)

	case FormatOKX:
		return pc.normalizeOKX(pair, marketType)

	default:
		return "", fmt.Errorf("unsupported format: %s", fromFormat)
	}
}

// ConvertFromStandard converts from standard format to target format
func (pc *PairConverter) ConvertFromStandard(standardPair string, toFormat ExchangeFormat, marketType MarketType) (string, error) {
	if !pc.IsValidStandardPair(standardPair) {
		return "", fmt.Errorf("invalid standard pair format: %s", standardPair)
	}

	parts := strings.Split(standardPair, "-")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid standard pair: %s", standardPair)
	}

	base, quote := parts[0], parts[1]

	switch toFormat {
	case FormatStandard:
		return standardPair, nil

	case FormatKuCoinSpot:
		return fmt.Sprintf("%s-%s", base, quote), nil

	case FormatKuCoinFutures:
		if marketType == MarketFutures {
			return fmt.Sprintf("%s%sM", base, quote), nil
		}
		return fmt.Sprintf("%s-%s", base, quote), nil

	case FormatBinance, FormatBybit:
		return fmt.Sprintf("%s%s", base, quote), nil

	case FormatOKX:
		if marketType == MarketFutures || marketType == MarketSwap {
			return fmt.Sprintf("%s-%s-SWAP", base, quote), nil
		}
		return fmt.Sprintf("%s-%s", base, quote), nil

	default:
		return "", fmt.Errorf("unsupported target format: %s", toFormat)
	}
}

// normalizeKuCoin handles KuCoin specific formats
func (pc *PairConverter) normalizeKuCoin(pair string, marketType MarketType) (string, error) {
	// Handle futures format: BTCUSDTM -> BTC-USDT
	if marketType == MarketFutures && strings.HasSuffix(pair, "M") {
		withoutM := strings.TrimSuffix(pair, "M")
		return pc.addHyphenToPair(withoutM)
	}

	// Handle spot format: BTC-USDT -> BTC-USDT (already standard)
	if strings.Contains(pair, "-") {
		return pair, nil
	}

	// Handle concatenated format: BTCUSDT -> BTC-USDT
	return pc.addHyphenToPair(pair)
}

// normalizeBinanceStyle handles Binance/Bybit style formats
func (pc *PairConverter) normalizeBinanceStyle(pair string) (string, error) {
	// BTCUSDT -> BTC-USDT
	return pc.addHyphenToPair(pair)
}

// normalizeOKX handles OKX specific formats
func (pc *PairConverter) normalizeOKX(pair string, marketType MarketType) (string, error) {
	// Handle swap format: BTC-USDT-SWAP -> BTC-USDT
	if strings.HasSuffix(pair, "-SWAP") {
		return strings.TrimSuffix(pair, "-SWAP"), nil
	}

	// Handle standard format: BTC-USDT -> BTC-USDT
	if strings.Contains(pair, "-") {
		return pair, nil
	}

	// Handle concatenated format: BTCUSDT -> BTC-USDT
	return pc.addHyphenToPair(pair)
}

// addHyphenToPair adds hyphen to concatenated pairs like BTCUSDT -> BTC-USDT
func (pc *PairConverter) addHyphenToPair(pair string) (string, error) {
	// Common quote currencies (ordered by length, longest first)
	quotes := []string{"USDT", "USDC", "BUSD", "DAI", "USD", "EUR", "GBP", "JPY", "BTC", "ETH", "BNB"}

	for _, quote := range quotes {
		if strings.HasSuffix(pair, quote) {
			base := strings.TrimSuffix(pair, quote)
			if len(base) > 0 {
				return fmt.Sprintf("%s-%s", base, quote), nil
			}
		}
	}

	return "", fmt.Errorf("unable to parse pair: %s", pair)
}

// IsValidStandardPair validates if a pair is in standard format
func (pc *PairConverter) IsValidStandardPair(pair string) bool {
	// Format: BASE-QUOTE (e.g., BTC-USDT, 1INCH-USDT)
	// Base can be 2-10 characters (letters and numbers)
	// Quote can be 3-6 characters (letters)
	regex := regexp.MustCompile(`^[A-Z0-9]{2,10}-[A-Z]{3,6}$`)
	return regex.MatchString(pair)
}

// ParsePair extracts base and quote from a standard pair
func (pc *PairConverter) ParsePair(standardPair string) (base, quote string, err error) {
	if !pc.IsValidStandardPair(standardPair) {
		return "", "", fmt.Errorf("invalid standard pair: %s", standardPair)
	}

	parts := strings.Split(standardPair, "-")
	return parts[0], parts[1], nil
}

// GetSupportedFormats returns all supported exchange formats
func (pc *PairConverter) GetSupportedFormats() []ExchangeFormat {
	return []ExchangeFormat{
		FormatStandard,
		FormatKuCoinSpot,
		FormatKuCoinFutures,
		FormatBinance,
		FormatBybit,
		FormatOKX,
	}
}

// GetSupportedMarketTypes returns all supported market types
func (pc *PairConverter) GetSupportedMarketTypes() []MarketType {
	return []MarketType{
		MarketSpot,
		MarketFutures,
		MarketMargin,
		MarketOptions,
		MarketSwap,
	}
}

// CreateTradingPair creates a TradingPair struct with all formats
func (pc *PairConverter) CreateTradingPair(standardPair string, exchange string, marketType MarketType) (*TradingPair, error) {
	if !pc.IsValidStandardPair(standardPair) {
		return nil, fmt.Errorf("invalid standard pair: %s", standardPair)
	}

	base, quote, err := pc.ParsePair(standardPair)
	if err != nil {
		return nil, err
	}

	// Determine exchange format
	var exchangeFormat ExchangeFormat
	switch strings.ToLower(exchange) {
	case "kucoin":
		if marketType == MarketFutures {
			exchangeFormat = FormatKuCoinFutures
		} else {
			exchangeFormat = FormatKuCoinSpot
		}
	case "binance":
		exchangeFormat = FormatBinance
	case "bybit":
		exchangeFormat = FormatBybit
	case "okx":
		exchangeFormat = FormatOKX
	default:
		exchangeFormat = FormatStandard
	}

	// Convert to exchange format
	exchangePair, err := pc.ConvertFromStandard(standardPair, exchangeFormat, marketType)
	if err != nil {
		return nil, err
	}

	return &TradingPair{
		Standard:     standardPair,
		Base:         base,
		Quote:        quote,
		MarketType:   marketType,
		Exchange:     exchange,
		ExchangePair: exchangePair,
	}, nil
}

// ConvertPairBatch converts multiple pairs at once
func (pc *PairConverter) ConvertPairBatch(pairs []string, fromFormat, toFormat ExchangeFormat, marketType MarketType) (map[string]string, error) {
	result := make(map[string]string)

	for _, pair := range pairs {
		converted, err := pc.ConvertPair(pair, fromFormat, toFormat, marketType)
		if err != nil {
			return nil, fmt.Errorf("failed to convert pair %s: %w", pair, err)
		}
		result[pair] = converted
	}

	return result, nil
}

// GetExamplePairs returns example pairs for testing
func (pc *PairConverter) GetExamplePairs() map[string]map[ExchangeFormat]string {
	return map[string]map[ExchangeFormat]string{
		"BTC-USDT": {
			FormatStandard:      "BTC-USDT",
			FormatKuCoinSpot:    "BTC-USDT",
			FormatKuCoinFutures: "BTCUSDTM",
			FormatBinance:       "BTCUSDT",
			FormatBybit:         "BTCUSDT",
			FormatOKX:           "BTC-USDT-SWAP",
		},
		"DOGE-USDT": {
			FormatStandard:      "DOGE-USDT",
			FormatKuCoinSpot:    "DOGE-USDT",
			FormatKuCoinFutures: "DOGEUSDTM",
			FormatBinance:       "DOGEUSDT",
			FormatBybit:         "DOGEUSDT",
			FormatOKX:           "DOGE-USDT-SWAP",
		},
		"ETH-USDT": {
			FormatStandard:      "ETH-USDT",
			FormatKuCoinSpot:    "ETH-USDT",
			FormatKuCoinFutures: "ETHUSDTM",
			FormatBinance:       "ETHUSDT",
			FormatBybit:         "ETHUSDT",
			FormatOKX:           "ETH-USDT-SWAP",
		},
	}
}

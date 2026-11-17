package pairs

import (
	"testing"
)

func TestPairConverter_NormalizeToStandard(t *testing.T) {
	converter := NewPairConverter()

	tests := []struct {
		name       string
		pair       string
		format     ExchangeFormat
		marketType MarketType
		expected   string
		shouldErr  bool
	}{
		// KuCoin Futures
		{"KuCoin Futures BTC", "BTCUSDTM", FormatKuCoinFutures, MarketFutures, "BTC-USDT", false},
		{"KuCoin Futures DOGE", "DOGEUSDTM", FormatKuCoinFutures, MarketFutures, "DOGE-USDT", false},
		{"KuCoin Futures ETH", "ETHUSDTM", FormatKuCoinFutures, MarketFutures, "ETH-USDT", false},

		// KuCoin Spot
		{"KuCoin Spot BTC", "BTC-USDT", FormatKuCoinSpot, MarketSpot, "BTC-USDT", false},
		{"KuCoin Spot DOGE", "DOGE-USDT", FormatKuCoinSpot, MarketSpot, "DOGE-USDT", false},

		// Binance
		{"Binance BTC", "BTCUSDT", FormatBinance, MarketSpot, "BTC-USDT", false},
		{"Binance DOGE", "DOGEUSDT", FormatBinance, MarketSpot, "DOGE-USDT", false},
		{"Binance ETH", "ETHUSDT", FormatBinance, MarketSpot, "ETH-USDT", false},

		// Bybit
		{"Bybit BTC", "BTCUSDT", FormatBybit, MarketSpot, "BTC-USDT", false},
		{"Bybit DOGE", "DOGEUSDT", FormatBybit, MarketSpot, "DOGE-USDT", false},

		// OKX
		{"OKX Spot", "BTC-USDT", FormatOKX, MarketSpot, "BTC-USDT", false},
		{"OKX Swap", "BTC-USDT-SWAP", FormatOKX, MarketSwap, "BTC-USDT", false},

		// Standard
		{"Standard BTC", "BTC-USDT", FormatStandard, MarketSpot, "BTC-USDT", false},
		{"Standard DOGE", "DOGE-USDT", FormatStandard, MarketSpot, "DOGE-USDT", false},

		// Edge cases
		{"Long token name", "1INCHUSDTM", FormatKuCoinFutures, MarketFutures, "1INCH-USDT", false},
		{"SHIB token", "SHIBUSDTM", FormatKuCoinFutures, MarketFutures, "SHIB-USDT", false},

		// Error cases
		{"Invalid pair", "INVALID", FormatBinance, MarketSpot, "", true},
		{"Empty pair", "", FormatBinance, MarketSpot, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.NormalizeToStandard(tt.pair, tt.format, tt.marketType)

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestPairConverter_ConvertFromStandard(t *testing.T) {
	converter := NewPairConverter()

	tests := []struct {
		name         string
		standardPair string
		toFormat     ExchangeFormat
		marketType   MarketType
		expected     string
		shouldErr    bool
	}{
		// To KuCoin Futures
		{"To KuCoin Futures BTC", "BTC-USDT", FormatKuCoinFutures, MarketFutures, "BTCUSDTM", false},
		{"To KuCoin Futures DOGE", "DOGE-USDT", FormatKuCoinFutures, MarketFutures, "DOGEUSDTM", false},

		// To KuCoin Spot
		{"To KuCoin Spot BTC", "BTC-USDT", FormatKuCoinSpot, MarketSpot, "BTC-USDT", false},
		{"To KuCoin Spot DOGE", "DOGE-USDT", FormatKuCoinSpot, MarketSpot, "DOGE-USDT", false},

		// To Binance
		{"To Binance BTC", "BTC-USDT", FormatBinance, MarketSpot, "BTCUSDT", false},
		{"To Binance DOGE", "DOGE-USDT", FormatBinance, MarketSpot, "DOGEUSDT", false},

		// To Bybit
		{"To Bybit BTC", "BTC-USDT", FormatBybit, MarketSpot, "BTCUSDT", false},
		{"To Bybit DOGE", "DOGE-USDT", FormatBybit, MarketSpot, "DOGEUSDT", false},

		// To OKX
		{"To OKX Spot", "BTC-USDT", FormatOKX, MarketSpot, "BTC-USDT", false},
		{"To OKX Swap", "BTC-USDT", FormatOKX, MarketSwap, "BTC-USDT-SWAP", false},

		// To Standard
		{"To Standard", "BTC-USDT", FormatStandard, MarketSpot, "BTC-USDT", false},

		// Error cases
		{"Invalid standard pair", "BTCUSDT", FormatBinance, MarketSpot, "", true},
		{"Empty pair", "", FormatBinance, MarketSpot, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.ConvertFromStandard(tt.standardPair, tt.toFormat, tt.marketType)

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestPairConverter_ConvertPair(t *testing.T) {
	converter := NewPairConverter()

	tests := []struct {
		name       string
		pair       string
		fromFormat ExchangeFormat
		toFormat   ExchangeFormat
		marketType MarketType
		expected   string
		shouldErr  bool
	}{
		// KuCoin Futures to Standard
		{"KuCoin Futures to Standard", "DOGEUSDTM", FormatKuCoinFutures, FormatStandard, MarketFutures, "DOGE-USDT", false},
		{"KuCoin Futures to Binance", "BTCUSDTM", FormatKuCoinFutures, FormatBinance, MarketFutures, "BTCUSDT", false},

		// Standard to KuCoin Futures
		{"Standard to KuCoin Futures", "DOGE-USDT", FormatStandard, FormatKuCoinFutures, MarketFutures, "DOGEUSDTM", false},

		// Binance to Standard
		{"Binance to Standard", "DOGEUSDT", FormatBinance, FormatStandard, MarketSpot, "DOGE-USDT", false},

		// Cross-exchange conversion
		{"Binance to KuCoin Futures", "BTCUSDT", FormatBinance, FormatKuCoinFutures, MarketFutures, "BTCUSDTM", false},
		{"KuCoin Spot to Binance", "BTC-USDT", FormatKuCoinSpot, FormatBinance, MarketSpot, "BTCUSDT", false},

		// Same format conversion
		{"Same format", "BTC-USDT", FormatStandard, FormatStandard, MarketSpot, "BTC-USDT", false},

		// Error cases
		{"Invalid input", "INVALID", FormatBinance, FormatStandard, MarketSpot, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.ConvertPair(tt.pair, tt.fromFormat, tt.toFormat, tt.marketType)

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestPairConverter_IsValidStandardPair(t *testing.T) {
	converter := NewPairConverter()

	tests := []struct {
		name     string
		pair     string
		expected bool
	}{
		{"Valid BTC-USDT", "BTC-USDT", true},
		{"Valid DOGE-USDT", "DOGE-USDT", true},
		{"Valid ETH-USDT", "ETH-USDT", true},
		{"Valid 1INCH-USDT", "1INCH-USDT", true},
		{"Valid SHIB-USDT", "SHIB-USDT", true},

		{"Invalid no hyphen", "BTCUSDT", false},
		{"Invalid lowercase", "btc-usdt", false},
		{"Invalid underscore", "BTC_USDT", false},
		{"Invalid empty", "", false},
		{"Invalid single token", "BTC", false},
		{"Invalid too many parts", "BTC-USDT-EXTRA", false},
		{"Invalid short base", "B-USDT", false},
		{"Invalid short quote", "BTC-US", false},
		{"Invalid long quote", "BTC-TOOLONG", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := converter.IsValidStandardPair(tt.pair)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v for pair %s", tt.expected, result, tt.pair)
			}
		})
	}
}

func TestPairConverter_ParsePair(t *testing.T) {
	converter := NewPairConverter()

	tests := []struct {
		name          string
		standardPair  string
		expectedBase  string
		expectedQuote string
		shouldErr     bool
	}{
		{"Valid BTC-USDT", "BTC-USDT", "BTC", "USDT", false},
		{"Valid DOGE-USDT", "DOGE-USDT", "DOGE", "USDT", false},
		{"Valid ETH-USDC", "ETH-USDC", "ETH", "USDC", false},
		{"Valid 1INCH-USDT", "1INCH-USDT", "1INCH", "USDT", false},

		{"Invalid format", "BTCUSDT", "", "", true},
		{"Invalid empty", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, quote, err := converter.ParsePair(tt.standardPair)

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if base != tt.expectedBase {
				t.Errorf("Expected base %s, got %s", tt.expectedBase, base)
			}

			if quote != tt.expectedQuote {
				t.Errorf("Expected quote %s, got %s", tt.expectedQuote, quote)
			}
		})
	}
}

func TestPairConverter_CreateTradingPair(t *testing.T) {
	converter := NewPairConverter()

	tests := []struct {
		name         string
		standardPair string
		exchange     string
		marketType   MarketType
		expectedPair string
		shouldErr    bool
	}{
		{"KuCoin Spot", "BTC-USDT", "kucoin", MarketSpot, "BTC-USDT", false},
		{"KuCoin Futures", "BTC-USDT", "kucoin", MarketFutures, "BTCUSDTM", false},
		{"Binance Spot", "DOGE-USDT", "binance", MarketSpot, "DOGEUSDT", false},
		{"Bybit Spot", "ETH-USDT", "bybit", MarketSpot, "ETHUSDT", false},
		{"OKX Spot", "BTC-USDT", "okx", MarketSpot, "BTC-USDT", false},
		{"OKX Swap", "BTC-USDT", "okx", MarketSwap, "BTC-USDT-SWAP", false},

		{"Invalid standard pair", "BTCUSDT", "kucoin", MarketSpot, "", true},
		{"Empty pair", "", "kucoin", MarketSpot, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tradingPair, err := converter.CreateTradingPair(tt.standardPair, tt.exchange, tt.marketType)

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tradingPair.ExchangePair != tt.expectedPair {
				t.Errorf("Expected exchange pair %s, got %s", tt.expectedPair, tradingPair.ExchangePair)
			}

			if tradingPair.Standard != tt.standardPair {
				t.Errorf("Expected standard %s, got %s", tt.standardPair, tradingPair.Standard)
			}

			if tradingPair.Exchange != tt.exchange {
				t.Errorf("Expected exchange %s, got %s", tt.exchange, tradingPair.Exchange)
			}

			if tradingPair.MarketType != tt.marketType {
				t.Errorf("Expected market type %s, got %s", tt.marketType, tradingPair.MarketType)
			}
		})
	}
}

func TestPairConverter_ConvertPairBatch(t *testing.T) {
	converter := NewPairConverter()

	pairs := []string{"BTCUSDTM", "DOGEUSDTM", "ETHUSDTM"}
	expected := map[string]string{
		"BTCUSDTM":  "BTC-USDT",
		"DOGEUSDTM": "DOGE-USDT",
		"ETHUSDTM":  "ETH-USDT",
	}

	result, err := converter.ConvertPairBatch(pairs, FormatKuCoinFutures, FormatStandard, MarketFutures)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	for pair, expectedResult := range expected {
		if result[pair] != expectedResult {
			t.Errorf("Expected %s for pair %s, got %s", expectedResult, pair, result[pair])
		}
	}
}

func TestPairConverter_GetExamplePairs(t *testing.T) {
	converter := NewPairConverter()
	examples := converter.GetExamplePairs()

	// Test that we have examples
	if len(examples) == 0 {
		t.Error("Expected example pairs but got none")
	}

	// Test BTC-USDT examples
	btcExamples, exists := examples["BTC-USDT"]
	if !exists {
		t.Error("Expected BTC-USDT examples but not found")
		return
	}

	expectedBTCFormats := map[ExchangeFormat]string{
		FormatStandard:      "BTC-USDT",
		FormatKuCoinSpot:    "BTC-USDT",
		FormatKuCoinFutures: "BTCUSDTM",
		FormatBinance:       "BTCUSDT",
		FormatBybit:         "BTCUSDT",
		FormatOKX:           "BTC-USDT-SWAP",
	}

	for format, expected := range expectedBTCFormats {
		if btcExamples[format] != expected {
			t.Errorf("Expected %s for format %s, got %s", expected, format, btcExamples[format])
		}
	}
}

// Benchmark tests
func BenchmarkPairConverter_NormalizeToStandard(b *testing.B) {
	converter := NewPairConverter()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = converter.NormalizeToStandard("BTCUSDTM", FormatKuCoinFutures, MarketFutures)
	}
}

func BenchmarkPairConverter_ConvertPair(b *testing.B) {
	converter := NewPairConverter()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = converter.ConvertPair("DOGEUSDTM", FormatKuCoinFutures, FormatStandard, MarketFutures)
	}
}

func BenchmarkPairConverter_ConvertPairBatch(b *testing.B) {
	converter := NewPairConverter()
	pairs := []string{"BTCUSDTM", "DOGEUSDTM", "ETHUSDTM", "ADAUSDTM", "SOLUSDTM"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = converter.ConvertPairBatch(pairs, FormatKuCoinFutures, FormatStandard, MarketFutures)
	}
}

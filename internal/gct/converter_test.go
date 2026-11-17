package gct

import (
	"testing"
)

func TestSymbolConverter_ToExchangeFormat(t *testing.T) {
	converter := NewSymbolConverter()

	tests := []struct {
		name       string
		symbol     string
		marketType MarketType
		exchange   string
		want       string
		wantErr    bool
	}{
		{
			name:       "KuCoin spot - no conversion",
			symbol:     "BTC-USDT",
			marketType: MarketTypeSpot,
			exchange:   "kucoin",
			want:       "BTC-USDT",
			wantErr:    false,
		},
		{
			name:       "KuCoin futures - add M suffix",
			symbol:     "BTC-USDT",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
			want:       "BTCUSDTM",
			wantErr:    false,
		},
		{
			name:       "KuCoin futures ETH",
			symbol:     "ETH-USDT",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
			want:       "ETHUSDTM",
			wantErr:    false,
		},
		{
			name:       "KuCoin futures DOGE",
			symbol:     "DOGE-USDT",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
			want:       "DOGEUSDTM",
			wantErr:    false,
		},
		{
			name:       "Other exchange - no conversion",
			symbol:     "BTC-USDT",
			marketType: MarketTypeFutures,
			exchange:   "binance",
			want:       "BTC-USDT",
			wantErr:    false,
		},
		{
			name:       "Empty symbol - error",
			symbol:     "",
			marketType: MarketTypeSpot,
			exchange:   "kucoin",
			want:       "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := converter.ToExchangeFormat(tt.symbol, tt.marketType, tt.exchange)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToExchangeFormat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToExchangeFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSymbolConverter_FromExchangeFormat(t *testing.T) {
	converter := NewSymbolConverter()

	tests := []struct {
		name       string
		symbol     string
		marketType MarketType
		exchange   string
		want       string
		wantErr    bool
	}{
		{
			name:       "KuCoin futures - remove M suffix",
			symbol:     "BTCUSDTM",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
			want:       "BTC-USDT",
			wantErr:    false,
		},
		{
			name:       "KuCoin futures ETH",
			symbol:     "ETHUSDTM",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
			want:       "ETH-USDT",
			wantErr:    false,
		},
		{
			name:       "KuCoin futures DOGE",
			symbol:     "DOGEUSDTM",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
			want:       "DOGE-USDT",
			wantErr:    false,
		},
		{
			name:       "KuCoin spot - no conversion",
			symbol:     "BTC-USDT",
			marketType: MarketTypeSpot,
			exchange:   "kucoin",
			want:       "BTC-USDT",
			wantErr:    false,
		},
		{
			name:       "Other exchange - no conversion",
			symbol:     "BTCUSDTM",
			marketType: MarketTypeFutures,
			exchange:   "binance",
			want:       "BTCUSDTM",
			wantErr:    false,
		},
		{
			name:       "Invalid futures symbol - too short",
			symbol:     "BTCM",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
			want:       "",
			wantErr:    true,
		},
		{
			name:       "Empty symbol - error",
			symbol:     "",
			marketType: MarketTypeSpot,
			exchange:   "kucoin",
			want:       "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := converter.FromExchangeFormat(tt.symbol, tt.marketType, tt.exchange)
			if (err != nil) != tt.wantErr {
				t.Errorf("FromExchangeFormat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FromExchangeFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateMarketType(t *testing.T) {
	tests := []struct {
		name       string
		marketType string
		wantErr    bool
	}{
		{
			name:       "Valid spot",
			marketType: "spot",
			wantErr:    false,
		},
		{
			name:       "Valid futures",
			marketType: "futures",
			wantErr:    false,
		},
		{
			name:       "Valid margin",
			marketType: "margin",
			wantErr:    false,
		},
		{
			name:       "Valid options",
			marketType: "options",
			wantErr:    false,
		},
		{
			name:       "Invalid type",
			marketType: "invalid",
			wantErr:    true,
		},
		{
			name:       "Empty type",
			marketType: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMarketType(tt.marketType)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMarketType() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	converter := NewSymbolConverter()

	tests := []struct {
		name       string
		symbol     string
		marketType MarketType
		exchange   string
	}{
		{
			name:       "BTC futures round trip",
			symbol:     "BTC-USDT",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
		},
		{
			name:       "ETH futures round trip",
			symbol:     "ETH-USDT",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
		},
		{
			name:       "DOGE futures round trip",
			symbol:     "DOGE-USDT",
			marketType: MarketTypeFutures,
			exchange:   "kucoin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert to exchange format
			exchangeFormat, err := converter.ToExchangeFormat(tt.symbol, tt.marketType, tt.exchange)
			if err != nil {
				t.Fatalf("ToExchangeFormat() error = %v", err)
			}

			// Convert back to standard format
			standardFormat, err := converter.FromExchangeFormat(exchangeFormat, tt.marketType, tt.exchange)
			if err != nil {
				t.Fatalf("FromExchangeFormat() error = %v", err)
			}

			// Should match original
			if standardFormat != tt.symbol {
				t.Errorf("Round trip failed: got %v, want %v", standardFormat, tt.symbol)
			}
		})
	}
}

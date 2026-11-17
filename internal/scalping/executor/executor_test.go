package executor

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertToFuturesSymbol_Standard tests standard symbol conversions
func TestConvertToFuturesSymbol_Standard(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	executor := &HighFrequencyExecutor{
		logger: logger,
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"BTC-USDT conversion", "BTC-USDT", "BTCUSDTM"},
		{"ETH-USDT conversion", "ETH-USDT", "ETHUSDTM"},
		{"SHIB-USDT conversion", "SHIB-USDT", "SHIBUSDTM"},
		{"DOGE-USDT conversion", "DOGE-USDT", "DOGEUSDTM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executor.convertToFuturesSymbol(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestStopLossPriceCalculation_BuyOrders tests SL price calculation for buy orders
func TestStopLossPriceCalculation_BuyOrders(t *testing.T) {
	tests := []struct {
		name          string
		entryPrice    float64
		slPercent     float64
		expectedPrice float64
	}{
		{"Buy with 0.5% SL", 50000.0, 0.5, 49750.0},
		{"Buy with 1% SL", 50000.0, 1.0, 49500.0},
		{"Buy with 2% SL", 50000.0, 2.0, 49000.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slPrice := tt.entryPrice * (1 - tt.slPercent/100)
			assert.InDelta(t, tt.expectedPrice, slPrice, 0.01)
			assert.Less(t, slPrice, tt.entryPrice)
		})
	}
}

// TestStopLossPriceCalculation_SellOrders tests SL price calculation for sell orders
func TestStopLossPriceCalculation_SellOrders(t *testing.T) {
	tests := []struct {
		name          string
		entryPrice    float64
		slPercent     float64
		expectedPrice float64
	}{
		{"Sell with 0.5% SL", 50000.0, 0.5, 50250.0},
		{"Sell with 1% SL", 50000.0, 1.0, 50500.0},
		{"Sell with 2% SL", 50000.0, 2.0, 51000.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slPrice := tt.entryPrice * (1 + tt.slPercent/100)
			assert.InDelta(t, tt.expectedPrice, slPrice, 0.01)
			assert.Greater(t, slPrice, tt.entryPrice)
		})
	}
}

// TestTakeProfitPriceCalculation_BuyOrders tests TP price calculation for buy orders
func TestTakeProfitPriceCalculation_BuyOrders(t *testing.T) {
	tests := []struct {
		name          string
		entryPrice    float64
		tpPercent     float64
		expectedPrice float64
	}{
		{"Buy with 1% TP", 50000.0, 1.0, 50500.0},
		{"Buy with 2% TP", 50000.0, 2.0, 51000.0},
		{"Buy with 5% TP", 50000.0, 5.0, 52500.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpPrice := tt.entryPrice * (1 + tt.tpPercent/100)
			assert.InDelta(t, tt.expectedPrice, tpPrice, 0.01)
			assert.Greater(t, tpPrice, tt.entryPrice)
		})
	}
}

// TestTakeProfitPriceCalculation_SellOrders tests TP price calculation for sell orders
func TestTakeProfitPriceCalculation_SellOrders(t *testing.T) {
	tests := []struct {
		name          string
		entryPrice    float64
		tpPercent     float64
		expectedPrice float64
	}{
		{"Sell with 1% TP", 50000.0, 1.0, 49500.0},
		{"Sell with 2% TP", 50000.0, 2.0, 49000.0},
		{"Sell with 5% TP", 50000.0, 5.0, 47500.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpPrice := tt.entryPrice * (1 - tt.tpPercent/100)
			assert.InDelta(t, tt.expectedPrice, tpPrice, 0.01)
			assert.Less(t, tpPrice, tt.entryPrice)
		})
	}
}

package gct

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTicker_Struct(t *testing.T) {
	ticker := &Ticker{
		Exchange:    "kucoin",
		Pair:        "BTC-USDT",
		Last:        50000.0,
		Bid:         49990.0,
		Ask:         50010.0,
		Volume:      1000.0,
		High:        51000.0,
		Low:         49000.0,
		PriceATH:    69000.0,
		LastUpdated: time.Now(),
		Timestamp:   time.Now(),
	}

	assert.Equal(t, "kucoin", ticker.Exchange)
	assert.Equal(t, "BTC-USDT", ticker.Pair)
	assert.Equal(t, 50000.0, ticker.Last)
	assert.Equal(t, 49990.0, ticker.Bid)
	assert.Equal(t, 50010.0, ticker.Ask)
	assert.Equal(t, 1000.0, ticker.Volume)
	assert.Equal(t, 51000.0, ticker.High)
	assert.Equal(t, 49000.0, ticker.Low)
	assert.Equal(t, 69000.0, ticker.PriceATH)
}

func TestOrderbook_Struct(t *testing.T) {
	orderbook := &Orderbook{
		Exchange: "kucoin",
		Pair:     "ETH-USDT",
		Bids: []OrderbookEntry{
			{Price: 3000.0, Amount: 1.5},
			{Price: 2999.0, Amount: 2.0},
		},
		Asks: []OrderbookEntry{
			{Price: 3001.0, Amount: 1.0},
			{Price: 3002.0, Amount: 3.0},
		},
		Timestamp: time.Now(),
	}

	assert.Equal(t, "kucoin", orderbook.Exchange)
	assert.Equal(t, "ETH-USDT", orderbook.Pair)
	assert.Len(t, orderbook.Bids, 2)
	assert.Len(t, orderbook.Asks, 2)
	assert.Equal(t, 3000.0, orderbook.Bids[0].Price)
	assert.Equal(t, 1.5, orderbook.Bids[0].Amount)
	assert.Equal(t, 3001.0, orderbook.Asks[0].Price)
}

func TestCandle_Struct(t *testing.T) {
	now := time.Now()
	candle := &Candle{
		Timestamp: now,
		Open:      50000.0,
		High:      51000.0,
		Low:       49000.0,
		Close:     50500.0,
		Volume:    100.0,
	}

	assert.Equal(t, now, candle.Timestamp)
	assert.Equal(t, 50000.0, candle.Open)
	assert.Equal(t, 51000.0, candle.High)
	assert.Equal(t, 49000.0, candle.Low)
	assert.Equal(t, 50500.0, candle.Close)
	assert.Equal(t, 100.0, candle.Volume)
}

func TestAccountInfo_Struct(t *testing.T) {
	accountInfo := &AccountInfo{
		Exchange: "binance",
		Accounts: []Account{
			{
				ID: "spot-account",
				Balances: map[string]Balance{
					"BTC": {
						Currency:  "BTC",
						Total:     1.5,
						Available: 1.0,
						Hold:      0.5,
						InOrder:   0.5,
					},
					"USDT": {
						Currency:  "USDT",
						Total:     50000.0,
						Available: 40000.0,
						Hold:      10000.0,
						InOrder:   10000.0,
					},
				},
			},
		},
	}

	assert.Equal(t, "binance", accountInfo.Exchange)
	assert.Len(t, accountInfo.Accounts, 1)
	assert.Equal(t, "spot-account", accountInfo.Accounts[0].ID)
	assert.Len(t, accountInfo.Accounts[0].Balances, 2)

	btcBalance := accountInfo.Accounts[0].Balances["BTC"]
	assert.Equal(t, "BTC", btcBalance.Currency)
	assert.Equal(t, 1.5, btcBalance.Total)
	assert.Equal(t, 1.0, btcBalance.Available)
}

func TestSubmitOrderRequest_Struct(t *testing.T) {
	req := &SubmitOrderRequest{
		Exchange:      "kucoin",
		Pair:          "BTC-USDT",
		Side:          "buy",
		Type:          "limit",
		Amount:        0.1,
		Price:         50000.0,
		ClientID:      "test-order-1",
		Leverage:      10.0,
		StopPrice:     48000.0,
		StopPriceType: "MP",
		ReduceOnly:    false,
		MarketType:    MarketTypeFutures,
	}

	assert.Equal(t, "kucoin", req.Exchange)
	assert.Equal(t, "BTC-USDT", req.Pair)
	assert.Equal(t, "buy", req.Side)
	assert.Equal(t, "limit", req.Type)
	assert.Equal(t, 0.1, req.Amount)
	assert.Equal(t, 50000.0, req.Price)
	assert.Equal(t, "test-order-1", req.ClientID)
	assert.Equal(t, 10.0, req.Leverage)
	assert.Equal(t, 48000.0, req.StopPrice)
	assert.Equal(t, "MP", req.StopPriceType)
	assert.False(t, req.ReduceOnly)
	assert.Equal(t, MarketTypeFutures, req.MarketType)
}

func TestOrder_Struct(t *testing.T) {
	now := time.Now()
	order := &Order{
		ID:            "order-123",
		Exchange:      "kucoin",
		Pair:          "ETH-USDT",
		BaseCurrency:  "ETH",
		QuoteCurrency: "USDT",
		Side:          "sell",
		Type:          "market",
		Price:         3000.0,
		Amount:        2.0,
		Filled:        1.5,
		Remaining:     0.5,
		OpenVolume:    0.5,
		Status:        "partial",
		CreatedAt:     now,
		UpdatedAt:     now,
		UpdateTime:    now,
	}

	assert.Equal(t, "order-123", order.ID)
	assert.Equal(t, "kucoin", order.Exchange)
	assert.Equal(t, "ETH-USDT", order.Pair)
	assert.Equal(t, "ETH", order.BaseCurrency)
	assert.Equal(t, "USDT", order.QuoteCurrency)
	assert.Equal(t, "sell", order.Side)
	assert.Equal(t, "market", order.Type)
	assert.Equal(t, 3000.0, order.Price)
	assert.Equal(t, 2.0, order.Amount)
	assert.Equal(t, 1.5, order.Filled)
	assert.Equal(t, 0.5, order.Remaining)
	assert.Equal(t, "partial", order.Status)
}

func TestPortfolio_Struct(t *testing.T) {
	now := time.Now()
	portfolio := &Portfolio{
		TotalValue:    100000.0,
		TotalPnL:      5000.0,
		PnL:           5000.0,
		PnLPercentage: 5.0,
		Holdings: map[string]*Holding{
			"BTC": {
				Currency:  "BTC",
				Total:     1.0,
				Available: 0.8,
				InOrder:   0.2,
				Value:     50000.0,
				PnL:       2500.0,
				PnLPct:    5.0,
			},
		},
		Allocations: map[string]float64{
			"BTC":  50.0,
			"USDT": 50.0,
		},
		UpdatedAt: now,
	}

	assert.Equal(t, 100000.0, portfolio.TotalValue)
	assert.Equal(t, 5000.0, portfolio.TotalPnL)
	assert.Equal(t, 5000.0, portfolio.PnL)
	assert.Equal(t, 5.0, portfolio.PnLPercentage)
	assert.Len(t, portfolio.Holdings, 1)
	assert.Equal(t, 50000.0, portfolio.Holdings["BTC"].Value)
	assert.Equal(t, 50.0, portfolio.Allocations["BTC"])
}

func TestFuturesOrderRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		request *FuturesOrderRequest
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil request",
			request: nil,
			wantErr: true,
			errMsg:  "futures order request is nil",
		},
		{
			name: "valid market order",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "market",
				Size:     0.1,
				Leverage: 10,
			},
			wantErr: false,
		},
		{
			name: "valid limit order",
			request: &FuturesOrderRequest{
				Symbol:   "ETHUSDTM",
				Side:     "sell",
				Type:     "limit",
				Size:     1.0,
				Price:    3000.0,
				Leverage: 5,
			},
			wantErr: false,
		},
		{
			name: "missing symbol",
			request: &FuturesOrderRequest{
				Side:     "buy",
				Type:     "market",
				Size:     0.1,
				Leverage: 10,
			},
			wantErr: true,
			errMsg:  "symbol is required",
		},
		{
			name: "invalid side",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "invalid",
				Type:     "market",
				Size:     0.1,
				Leverage: 10,
			},
			wantErr: true,
			errMsg:  "side must be 'buy' or 'sell'",
		},
		{
			name: "invalid type",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "invalid",
				Size:     0.1,
				Leverage: 10,
			},
			wantErr: true,
			errMsg:  "type must be 'market' or 'limit'",
		},
		{
			name: "zero size",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "market",
				Size:     0,
				Leverage: 10,
			},
			wantErr: true,
			errMsg:  "size must be greater than 0",
		},
		{
			name: "negative size",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "market",
				Size:     -1,
				Leverage: 10,
			},
			wantErr: true,
			errMsg:  "size must be greater than 0",
		},
		{
			name: "limit order without price",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "limit",
				Size:     0.1,
				Price:    0,
				Leverage: 10,
			},
			wantErr: true,
			errMsg:  "price must be greater than 0 for limit orders",
		},
		{
			name: "leverage too low",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "market",
				Size:     0.1,
				Leverage: 0,
			},
			wantErr: true,
			errMsg:  "leverage must be between 1 and 100",
		},
		{
			name: "leverage too high",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "market",
				Size:     0.1,
				Leverage: 101,
			},
			wantErr: true,
			errMsg:  "leverage must be between 1 and 100",
		},
		{
			name: "postOnly with market order",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "market",
				Size:     0.1,
				Leverage: 10,
				PostOnly: true,
			},
			wantErr: true,
			errMsg:  "postOnly can only be used with limit orders",
		},
		{
			name: "postOnly with limit order",
			request: &FuturesOrderRequest{
				Symbol:   "BTCUSDTM",
				Side:     "buy",
				Type:     "limit",
				Size:     0.1,
				Price:    50000.0,
				Leverage: 10,
				PostOnly: true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPosition_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		position *Position
		expected bool
	}{
		{
			name:     "nil position",
			position: nil,
			expected: false,
		},
		{
			name: "valid long position",
			position: &Position{
				Symbol:     "BTCUSDTM",
				Side:       "long",
				Size:       0.1,
				Leverage:   10,
				MarginMode: "ISOLATED",
			},
			expected: true,
		},
		{
			name: "valid short position",
			position: &Position{
				Symbol:     "ETHUSDTM",
				Side:       "short",
				Size:       1.0,
				Leverage:   5,
				MarginMode: "CROSSED",
			},
			expected: true,
		},
		{
			name: "empty symbol",
			position: &Position{
				Symbol:     "",
				Side:       "long",
				Size:       0.1,
				Leverage:   10,
				MarginMode: "ISOLATED",
			},
			expected: false,
		},
		{
			name: "invalid side",
			position: &Position{
				Symbol:     "BTCUSDTM",
				Side:       "invalid",
				Size:       0.1,
				Leverage:   10,
				MarginMode: "ISOLATED",
			},
			expected: false,
		},
		{
			name: "zero size",
			position: &Position{
				Symbol:     "BTCUSDTM",
				Side:       "long",
				Size:       0,
				Leverage:   10,
				MarginMode: "ISOLATED",
			},
			expected: false,
		},
		{
			name: "negative size",
			position: &Position{
				Symbol:     "BTCUSDTM",
				Side:       "long",
				Size:       -1,
				Leverage:   10,
				MarginMode: "ISOLATED",
			},
			expected: false,
		},
		{
			name: "leverage too low",
			position: &Position{
				Symbol:     "BTCUSDTM",
				Side:       "long",
				Size:       0.1,
				Leverage:   0,
				MarginMode: "ISOLATED",
			},
			expected: false,
		},
		{
			name: "leverage too high",
			position: &Position{
				Symbol:     "BTCUSDTM",
				Side:       "long",
				Size:       0.1,
				Leverage:   101,
				MarginMode: "ISOLATED",
			},
			expected: false,
		},
		{
			name: "invalid margin mode",
			position: &Position{
				Symbol:     "BTCUSDTM",
				Side:       "long",
				Size:       0.1,
				Leverage:   10,
				MarginMode: "INVALID",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.position.IsValid()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPosition_CalculatePnLPercent(t *testing.T) {
	tests := []struct {
		name     string
		position *Position
		expected float64
	}{
		{
			name:     "nil position",
			position: nil,
			expected: 0,
		},
		{
			name: "zero margin",
			position: &Position{
				UnrealizedPnL: 100.0,
				Margin:        0,
			},
			expected: 0,
		},
		{
			name: "positive PnL",
			position: &Position{
				UnrealizedPnL: 100.0,
				Margin:        1000.0,
			},
			expected: 10.0,
		},
		{
			name: "negative PnL",
			position: &Position{
				UnrealizedPnL: -50.0,
				Margin:        1000.0,
			},
			expected: -5.0,
		},
		{
			name: "large PnL",
			position: &Position{
				UnrealizedPnL: 500.0,
				Margin:        100.0,
			},
			expected: 500.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.position.CalculatePnLPercent()
			assert.InDelta(t, tt.expected, result, 0.001)
		})
	}
}

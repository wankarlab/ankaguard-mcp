package gct

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestNewClient tests the creation of a new GCT client
func TestNewClient(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config)
	assert.NotNil(t, client)
	assert.False(t, client.IsConnected())
}

// TestClientConfig tests configuration validation
func TestClientConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name: "valid config",
			config: &Config{
				Address:  "localhost:9052",
				Username: "admin",
				Password: "Password",
				Timeout:  30 * time.Second,
			},
		},
		{
			name: "empty address",
			config: &Config{
				Address:  "",
				Username: "admin",
				Password: "Password",
				Timeout:  30 * time.Second,
			},
		},
		{
			name: "zero timeout",
			config: &Config{
				Address:  "localhost:9052",
				Username: "admin",
				Password: "Password",
				Timeout:  0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.config)
			assert.NotNil(t, client)
		})
	}
}

// TestIsConnected tests the connection status check
func TestIsConnected(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config)
	assert.False(t, client.IsConnected())
}

// TestDisconnectWithoutConnect tests disconnecting without connecting
func TestDisconnectWithoutConnect(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config)
	err := client.Disconnect()
	assert.NoError(t, err)
}

// TestGetGRPCConn tests getting the gRPC connection
func TestGetGRPCConn(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config)
	conn := client.GetGRPCConn()
	assert.Nil(t, conn)
}

// TestIndicatorIntervalDefault verifies the fallback interval when no dynamic
// config snapshot is present in the context.
func TestIndicatorIntervalDefault(t *testing.T) {
	assert.Equal(t, "1h", indicatorInterval(context.Background()))
}

// TestSubmitOrderRequest tests the SubmitOrderRequest structure
func TestSubmitOrderRequest(t *testing.T) {
	req := &SubmitOrderRequest{
		Exchange: "kucoin",
		Pair:     "BTC-USDT",
		Side:     "buy",
		Type:     "limit",
		Amount:   0.001,
		Price:    50000.0,
	}

	assert.Equal(t, "kucoin", req.Exchange)
	assert.Equal(t, "BTC-USDT", req.Pair)
	assert.Equal(t, "buy", req.Side)
	assert.Equal(t, "limit", req.Type)
	assert.Equal(t, 0.001, req.Amount)
	assert.Equal(t, 50000.0, req.Price)
}

// TestTickerStructure tests the Ticker structure
func TestTickerStructure(t *testing.T) {
	now := time.Now()
	ticker := &Ticker{
		Exchange:    "kucoin",
		Pair:        "BTC-USDT",
		Last:        50000.0,
		Bid:         49999.0,
		Ask:         50001.0,
		Volume:      100.5,
		High:        51000.0,
		Low:         49000.0,
		PriceATH:    69000.0,
		LastUpdated: now,
		Timestamp:   now,
	}

	assert.Equal(t, "kucoin", ticker.Exchange)
	assert.Equal(t, "BTC-USDT", ticker.Pair)
	assert.Equal(t, 50000.0, ticker.Last)
	assert.Equal(t, 49999.0, ticker.Bid)
	assert.Equal(t, 50001.0, ticker.Ask)
	assert.Equal(t, 100.5, ticker.Volume)
	assert.Equal(t, 51000.0, ticker.High)
	assert.Equal(t, 49000.0, ticker.Low)
	assert.Equal(t, 69000.0, ticker.PriceATH)
}

// TestOrderStructure tests the Order structure
func TestOrderStructure(t *testing.T) {
	now := time.Now()
	order := &Order{
		ID:            "order123",
		Exchange:      "kucoin",
		Pair:          "BTC-USDT",
		Side:          "buy",
		Type:          "limit",
		Amount:        0.001,
		Price:         50000.0,
		Status:        "filled",
		Filled:        0.001,
		Remaining:     0,
		BaseCurrency:  "BTC",
		QuoteCurrency: "USDT",
		OpenVolume:    0,
		CreatedAt:     now,
		UpdateTime:    now,
	}

	assert.Equal(t, "order123", order.ID)
	assert.Equal(t, "kucoin", order.Exchange)
	assert.Equal(t, "BTC-USDT", order.Pair)
	assert.Equal(t, "buy", order.Side)
	assert.Equal(t, "limit", order.Type)
	assert.Equal(t, 0.001, order.Amount)
	assert.Equal(t, 50000.0, order.Price)
	assert.Equal(t, "filled", order.Status)
	assert.Equal(t, 0.001, order.Filled)
	assert.Equal(t, 0.0, order.Remaining)
}

// TestPortfolioStructure tests the Portfolio structure
func TestPortfolioStructure(t *testing.T) {
	portfolio := &Portfolio{
		TotalValue:    10000.0,
		PnL:           500.0,
		PnLPercentage: 5.0,
		Holdings: map[string]*Holding{
			"BTC": {
				Currency:  "BTC",
				Total:     0.1,
				Value:     5000.0,
				Available: 0.1,
				InOrder:   0,
			},
			"USDT": {
				Currency:  "USDT",
				Total:     5000.0,
				Value:     5000.0,
				Available: 5000.0,
				InOrder:   0,
			},
		},
		Allocations: map[string]float64{
			"BTC":  50.0,
			"USDT": 50.0,
		},
	}

	assert.Equal(t, 10000.0, portfolio.TotalValue)
	assert.Equal(t, 500.0, portfolio.PnL)
	assert.Equal(t, 5.0, portfolio.PnLPercentage)
	assert.Len(t, portfolio.Holdings, 2)
	assert.Len(t, portfolio.Allocations, 2)
	assert.Equal(t, 50.0, portfolio.Allocations["BTC"])
	assert.Equal(t, 50.0, portfolio.Allocations["USDT"])
}

// TestCandleStructure tests the Candle structure
func TestCandleStructure(t *testing.T) {
	now := time.Now()
	candle := &Candle{
		Timestamp: now,
		Open:      50000.0,
		High:      51000.0,
		Low:       49000.0,
		Close:     50500.0,
		Volume:    100.5,
	}

	assert.Equal(t, now, candle.Timestamp)
	assert.Equal(t, 50000.0, candle.Open)
	assert.Equal(t, 51000.0, candle.High)
	assert.Equal(t, 49000.0, candle.Low)
	assert.Equal(t, 50500.0, candle.Close)
	assert.Equal(t, 100.5, candle.Volume)
}

// TestOrderbookStructure tests the Orderbook structure
func TestOrderbookStructure(t *testing.T) {
	orderbook := &Orderbook{
		Exchange: "kucoin",
		Pair:     "BTC-USDT",
		Bids: []OrderbookEntry{
			{Price: 50000.0, Amount: 0.1},
			{Price: 49999.0, Amount: 0.2},
		},
		Asks: []OrderbookEntry{
			{Price: 50001.0, Amount: 0.1},
			{Price: 50002.0, Amount: 0.2},
		},
		Timestamp: time.Now(),
	}

	assert.Equal(t, "kucoin", orderbook.Exchange)
	assert.Equal(t, "BTC-USDT", orderbook.Pair)
	assert.Len(t, orderbook.Bids, 2)
	assert.Len(t, orderbook.Asks, 2)
	assert.Equal(t, 50000.0, orderbook.Bids[0].Price)
	assert.Equal(t, 0.1, orderbook.Bids[0].Amount)
	assert.Equal(t, 50001.0, orderbook.Asks[0].Price)
	assert.Equal(t, 0.1, orderbook.Asks[0].Amount)
}

// TestAccountInfoStructure tests the AccountInfo structure
func TestAccountInfoStructure(t *testing.T) {
	accountInfo := &AccountInfo{
		Exchange: "kucoin",
		Accounts: []Account{
			{
				ID: "main",
				Balances: map[string]Balance{
					"BTC": {
						Currency:  "BTC",
						Total:     0.1,
						Available: 0.1,
						InOrder:   0,
					},
				},
			},
		},
	}

	assert.Equal(t, "kucoin", accountInfo.Exchange)
	assert.Len(t, accountInfo.Accounts, 1)
	assert.Equal(t, "main", accountInfo.Accounts[0].ID)
	assert.Len(t, accountInfo.Accounts[0].Balances, 1)
	assert.Equal(t, "BTC", accountInfo.Accounts[0].Balances["BTC"].Currency)
}

// Integration tests (require running GoCryptoTrader)
func TestConnectIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  5 * time.Second,
	}

	client := NewClient(config)
	ctx := context.Background()

	err := client.Connect(ctx, config.Address)
	if err != nil {
		t.Skipf("Skipping integration test: GoCryptoTrader not available: %v", err)
		return
	}
	defer client.Disconnect()

	assert.True(t, client.IsConnected())
}

func TestHealthCheckIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  5 * time.Second,
	}

	client := NewClient(config)
	ctx := context.Background()

	err := client.Connect(ctx, config.Address)
	if err != nil {
		t.Skipf("Skipping integration test: GoCryptoTrader not available: %v", err)
		return
	}
	defer client.Disconnect()

	err = client.HealthCheck(ctx)
	// May fail due to authentication, but connection should work
	if err != nil {
		t.Logf("Health check failed (expected if auth not configured): %v", err)
	}
}

// TestValidateKuCoinFuturesAccess_MissingClient tests validation with nil KuCoin client
func TestValidateKuCoinFuturesAccess_MissingClient(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config).(*client)
	// Manually set kucoinClient to nil to test this scenario
	client.kucoinClient = nil
	ctx := context.Background()

	err := client.ValidateKuCoinFuturesAccess(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kucoin client not initialized")
}

// TestValidateKuCoinFuturesAccess_MissingCredentials tests validation with missing credentials
func TestValidateKuCoinFuturesAccess_MissingCredentials(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config).(*client)
	client.kucoinClient = NewKuCoinRESTClient()
	// Don't set credentials
	ctx := context.Background()

	err := client.ValidateKuCoinFuturesAccess(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kucoin credentials not configured")
}

// TestValidateKuCoinFuturesAccess_WithCredentials tests validation with credentials set
func TestValidateKuCoinFuturesAccess_WithCredentials(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config).(*client)
	client.kucoinClient = NewKuCoinRESTClient()
	client.kucoinClient.SetCredentials("test-key", "test-secret", "test-password")

	ctx := context.Background()

	// This will fail because we don't have a real futures manager or mock server
	// but it should not return the "credentials not configured" error
	err := client.ValidateKuCoinFuturesAccess(ctx)
	// The error should be nil (network error is ignored) or about futures manager
	if err != nil {
		assert.NotContains(t, err.Error(), "kucoin credentials not configured")
		assert.NotContains(t, err.Error(), "kucoin client not initialized")
	}
}

// TestKuCoinRESTClient_HasCredentials tests the HasCredentials method
func TestKuCoinRESTClient_HasCredentials(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		apiSecret   string
		apiPassword string
		expected    bool
	}{
		{
			name:        "all credentials set",
			apiKey:      "test-key",
			apiSecret:   "test-secret",
			apiPassword: "test-password",
			expected:    true,
		},
		{
			name:        "missing api key",
			apiKey:      "",
			apiSecret:   "test-secret",
			apiPassword: "test-password",
			expected:    false,
		},
		{
			name:        "missing api secret",
			apiKey:      "test-key",
			apiSecret:   "",
			apiPassword: "test-password",
			expected:    false,
		},
		{
			name:        "missing api password",
			apiKey:      "test-key",
			apiSecret:   "test-secret",
			apiPassword: "",
			expected:    false,
		},
		{
			name:        "all credentials missing",
			apiKey:      "",
			apiSecret:   "",
			apiPassword: "",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewKuCoinRESTClient()
			client.SetCredentials(tt.apiKey, tt.apiSecret, tt.apiPassword)

			result := client.HasCredentials()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestValidateKuCoinFuturesAccess_NilContext tests validation with nil context
func TestValidateKuCoinFuturesAccess_NilContext(t *testing.T) {
	config := &Config{
		Address:  "localhost:9052",
		Username: "admin",
		Password: "Password",
		Timeout:  30 * time.Second,
	}

	client := NewClient(config).(*client)
	client.kucoinClient = NewKuCoinRESTClient()
	client.kucoinClient.SetCredentials("test-key", "test-secret", "test-password")

	// Should not panic with nil context (it creates a background context)
	err := client.ValidateKuCoinFuturesAccess(nil)
	// Error is expected (no real connection), but should not panic
	if err != nil {
		assert.NotContains(t, err.Error(), "panic")
	}
}

// TestValidateKuCoinFuturesAccess_NilClient tests validation with nil client
func TestValidateKuCoinFuturesAccess_NilClient(t *testing.T) {
	var client *client = nil
	ctx := context.Background()

	err := client.ValidateKuCoinFuturesAccess(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client is nil")
}

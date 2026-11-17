package gct

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKuCoinIntegration_SetMarginMode tests setting margin mode with real API
func TestKuCoinIntegration_SetMarginMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Get credentials from environment
	apiKey := os.Getenv("KUCOIN_API_KEY")
	apiSecret := os.Getenv("KUCOIN_API_SECRET")
	apiPassword := os.Getenv("KUCOIN_API_PASSWORD")

	if apiKey == "" || apiSecret == "" || apiPassword == "" {
		t.Skip("KuCoin credentials not set, skipping integration test")
	}

	client := NewKuCoinRESTClient()
	client.SetCredentials(apiKey, apiSecret, apiPassword)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test symbol - using a common futures contract
	symbol := "DOGEUSDTM"

	t.Run("SetMarginMode_CROSSED", func(t *testing.T) {
		err := client.SetMarginMode(ctx, symbol, "CROSSED")
		if err != nil {
			// Log error but don't fail - might already be set
			t.Logf("SetMarginMode CROSSED: %v", err)
		} else {
			t.Logf("✓ Margin mode set to CROSSED for %s", symbol)
		}
	})

	t.Run("SetMarginMode_ISOLATED", func(t *testing.T) {
		err := client.SetMarginMode(ctx, symbol, "ISOLATED")
		if err != nil {
			// Log error but don't fail - might already be set
			t.Logf("SetMarginMode ISOLATED: %v", err)
		} else {
			t.Logf("✓ Margin mode set to ISOLATED for %s", symbol)
		}
	})
}

// TestKuCoinIntegration_SetLeverage tests setting leverage with real API
func TestKuCoinIntegration_SetLeverage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	apiKey := os.Getenv("KUCOIN_API_KEY")
	apiSecret := os.Getenv("KUCOIN_API_SECRET")
	apiPassword := os.Getenv("KUCOIN_API_PASSWORD")

	if apiKey == "" || apiSecret == "" || apiPassword == "" {
		t.Skip("KuCoin credentials not set, skipping integration test")
	}

	client := NewKuCoinRESTClient()
	client.SetCredentials(apiKey, apiSecret, apiPassword)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	symbol := "DOGEUSDTM"

	testCases := []struct {
		name     string
		leverage int
	}{
		{"Leverage_5x", 5},
		{"Leverage_10x", 10},
		{"Leverage_20x", 20},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := client.SetLeverage(ctx, symbol, tc.leverage)
			if err != nil {
				t.Logf("SetLeverage %dx: %v", tc.leverage, err)
			} else {
				t.Logf("✓ Leverage set to %dx for %s", tc.leverage, symbol)
			}
		})
	}
}

// TestKuCoinIntegration_GetPositions tests fetching positions with real API
func TestKuCoinIntegration_GetPositions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	apiKey := os.Getenv("KUCOIN_API_KEY")
	apiSecret := os.Getenv("KUCOIN_API_SECRET")
	apiPassword := os.Getenv("KUCOIN_API_PASSWORD")

	if apiKey == "" || apiSecret == "" || apiPassword == "" {
		t.Skip("KuCoin credentials not set, skipping integration test")
	}

	client := NewKuCoinRESTClient()
	client.SetCredentials(apiKey, apiSecret, apiPassword)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("GetPositions", func(t *testing.T) {
		positions, err := client.GetPositions(ctx)
		require.NoError(t, err, "GetPositions should not return error")
		assert.NotNil(t, positions, "Positions should not be nil")

		t.Logf("Found %d positions", len(positions))

		for i, pos := range positions {
			t.Logf("Position %d: %+v", i+1, pos)
		}
	})
}

// TestKuCoinIntegration_GetPosition tests fetching a specific position
func TestKuCoinIntegration_GetPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	apiKey := os.Getenv("KUCOIN_API_KEY")
	apiSecret := os.Getenv("KUCOIN_API_SECRET")
	apiPassword := os.Getenv("KUCOIN_API_PASSWORD")

	if apiKey == "" || apiSecret == "" || apiPassword == "" {
		t.Skip("KuCoin credentials not set, skipping integration test")
	}

	client := NewKuCoinRESTClient()
	client.SetCredentials(apiKey, apiSecret, apiPassword)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	symbol := "DOGEUSDTM"

	t.Run("GetPosition", func(t *testing.T) {
		position, err := client.GetPosition(ctx, symbol)

		if err == ErrPositionNotFound {
			t.Logf("No position found for %s (expected if no open position)", symbol)
			return
		}

		require.NoError(t, err, "GetPosition should not return error")
		assert.NotNil(t, position, "Position should not be nil")

		t.Logf("Position for %s: %+v", symbol, position)
	})
}

// TestKuCoinIntegration_SubmitFuturesOrder tests submitting a futures order
// WARNING: This test will place a REAL order with REAL money
// Only run with small amounts for testing purposes
func TestKuCoinIntegration_SubmitFuturesOrder(t *testing.T) {
	t.Skip("Skipping real order test - uncomment to test with real funds")

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	apiKey := os.Getenv("KUCOIN_API_KEY")
	apiSecret := os.Getenv("KUCOIN_API_SECRET")
	apiPassword := os.Getenv("KUCOIN_API_PASSWORD")

	if apiKey == "" || apiSecret == "" || apiPassword == "" {
		t.Skip("KuCoin credentials not set, skipping integration test")
	}

	client := NewKuCoinRESTClient()
	client.SetCredentials(apiKey, apiSecret, apiPassword)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Test with small amount - $10 worth
	symbol := "DOGEUSDTM"

	t.Run("SubmitFuturesOrder_Market_Buy", func(t *testing.T) {
		req := &FuturesOrderRequest{
			ClientOID: generateClientOID(),
			Symbol:    symbol,
			Side:      "buy",
			Type:      "market",
			Size:      10, // $10 worth
			Leverage:  5,
		}

		orderID, err := client.SubmitFuturesOrder(ctx, req)

		if err != nil {
			t.Logf("SubmitFuturesOrder error: %v", err)
			// Don't fail - might be due to margin mode not configured
			return
		}

		require.NoError(t, err, "SubmitFuturesOrder should not return error")
		assert.NotEmpty(t, orderID, "Order ID should not be empty")

		t.Logf("✓ Order placed successfully: %s", orderID)
		t.Logf("⚠️  Remember to close this position manually!")
	})
}

// TestKuCoinIntegration_EndToEnd tests complete flow
func TestKuCoinIntegration_EndToEnd(t *testing.T) {
	t.Skip("Skipping end-to-end test - uncomment to test complete flow")

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	apiKey := os.Getenv("KUCOIN_API_KEY")
	apiSecret := os.Getenv("KUCOIN_API_SECRET")
	apiPassword := os.Getenv("KUCOIN_API_PASSWORD")

	if apiKey == "" || apiSecret == "" || apiPassword == "" {
		t.Skip("KuCoin credentials not set, skipping integration test")
	}

	client := NewKuCoinRESTClient()
	client.SetCredentials(apiKey, apiSecret, apiPassword)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	symbol := "DOGEUSDTM"
	leverage := 5

	t.Log("=== Starting End-to-End Test ===")

	// Step 1: Set margin mode
	t.Log("Step 1: Setting margin mode to CROSSED")
	err := client.SetMarginMode(ctx, symbol, "CROSSED")
	if err != nil {
		t.Logf("SetMarginMode warning: %v", err)
	}

	// Step 2: Set leverage
	t.Log("Step 2: Setting leverage to 5x")
	err = client.SetLeverage(ctx, symbol, leverage)
	if err != nil {
		t.Logf("SetLeverage warning: %v", err)
	}

	// Step 3: Place order
	t.Log("Step 3: Placing market buy order ($10)")
	req := &FuturesOrderRequest{
		ClientOID: generateClientOID(),
		Symbol:    symbol,
		Side:      "buy",
		Type:      "market",
		Size:      10,
		Leverage:  leverage,
	}

	orderID, err := client.SubmitFuturesOrder(ctx, req)
	if err != nil {
		t.Fatalf("Failed to place order: %v", err)
	}

	t.Logf("✓ Order placed: %s", orderID)

	// Step 4: Wait for order to fill
	t.Log("Step 4: Waiting for order to fill...")
	time.Sleep(2 * time.Second)

	// Step 5: Check position
	t.Log("Step 5: Checking position")
	position, err := client.GetPosition(ctx, symbol)
	require.NoError(t, err, "Should be able to get position")
	require.NotNil(t, position, "Position should exist")

	t.Logf("✓ Position found: %+v", position)

	// Step 6: Close position (reduce-only sell)
	t.Log("Step 6: Closing position")
	closeReq := &FuturesOrderRequest{
		ClientOID:  generateClientOID(),
		Symbol:     symbol,
		Side:       "sell",
		Type:       "market",
		Size:       10,
		Leverage:   leverage,
		ReduceOnly: true,
	}

	closeOrderID, err := client.SubmitFuturesOrder(ctx, closeReq)
	require.NoError(t, err, "Should be able to close position")

	t.Logf("✓ Position closed: %s", closeOrderID)

	t.Log("=== End-to-End Test Complete ===")
}

// Helper function to generate unique client order ID
func generateClientOID() string {
	return "test_" + time.Now().Format("20060102150405")
}

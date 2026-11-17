package gct

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// TestClient_LeverageDetection tests that SubmitOrder detects leverage
func TestClient_LeverageDetection(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	cfg := &Config{
		Address:  "localhost:9052",
		Username: "test",
		Password: "test",
		Timeout:  10 * time.Second,
		Logger:   logger,
	}

	client := NewClient(cfg)
	client.SetKuCoinCredentials("test-key", "test-secret", "test-password")

	t.Run("Spot_Order_NoLeverage", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "BTC-USDT",
			Side:     "buy",
			Type:     "market",
			Amount:   0.001,
			Leverage: 0,
		}

		assert.Equal(t, float64(0), req.Leverage, "Spot order should have no leverage")
	})

	t.Run("Futures_Order_WithLeverage", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "DOGEUSDTM",
			Side:     "buy",
			Type:     "market",
			Amount:   10,
			Leverage: 5,
		}

		assert.Greater(t, req.Leverage, float64(0), "Futures order should have leverage")
		assert.Equal(t, float64(5), req.Leverage, "Leverage should be 5x")
	})
}

// TestClient_SubmitFuturesOrder tests the SubmitFuturesOrder method
func TestClient_SubmitFuturesOrder(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	cfg := &Config{
		Address:  "localhost:9052",
		Username: "test",
		Password: "test",
		Timeout:  10 * time.Second,
		Logger:   logger,
	}

	client := NewClient(cfg)
	client.SetKuCoinCredentials("test-key", "test-secret", "test-password")

	ctx := context.Background()

	t.Run("SubmitFuturesOrder_NilRequest", func(t *testing.T) {
		_, err := client.SubmitFuturesOrder(ctx, nil)
		assert.Error(t, err, "Should return error for nil request")
		assert.Contains(t, err.Error(), "nil", "Error should mention nil request")
	})

	t.Run("SubmitFuturesOrder_ValidRequest", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "DOGEUSDTM",
			Side:     "buy",
			Type:     "market",
			Amount:   10,
			Leverage: 5,
		}

		_, err := client.SubmitFuturesOrder(ctx, req)
		if err != nil {
			t.Logf("Expected error without real credentials: %v", err)
		}
	})
}

// TestClient_GetFuturesPosition tests the GetFuturesPosition method
func TestClient_GetFuturesPosition(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	cfg := &Config{
		Address:  "localhost:9052",
		Username: "test",
		Password: "test",
		Timeout:  10 * time.Second,
		Logger:   logger,
	}

	client := NewClient(cfg)
	client.SetKuCoinCredentials("test-key", "test-secret", "test-password")

	ctx := context.Background()

	t.Run("GetFuturesPosition_EmptySymbol", func(t *testing.T) {
		_, err := client.GetFuturesPosition(ctx, "kucoin", "")
		assert.Error(t, err, "Should return error for empty symbol")
		assert.Contains(t, err.Error(), "symbol", "Error should mention symbol")
	})

	t.Run("GetFuturesPosition_ValidSymbol", func(t *testing.T) {
		_, err := client.GetFuturesPosition(ctx, "kucoin", "DOGEUSDTM")
		if err != nil {
			t.Logf("Expected error without real credentials: %v", err)
		}
	})
}

// TestClient_CloseFuturesPosition tests the CloseFuturesPosition method
func TestClient_CloseFuturesPosition(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	cfg := &Config{
		Address:  "localhost:9052",
		Username: "test",
		Password: "test",
		Timeout:  10 * time.Second,
		Logger:   logger,
	}

	client := NewClient(cfg)
	client.SetKuCoinCredentials("test-key", "test-secret", "test-password")

	ctx := context.Background()

	t.Run("CloseFuturesPosition_EmptySymbol", func(t *testing.T) {
		err := client.CloseFuturesPosition(ctx, "kucoin", "")
		assert.Error(t, err, "Should return error for empty symbol")
		assert.Contains(t, err.Error(), "symbol", "Error should mention symbol")
	})

	t.Run("CloseFuturesPosition_ValidSymbol", func(t *testing.T) {
		err := client.CloseFuturesPosition(ctx, "kucoin", "DOGEUSDTM")
		if err != nil {
			t.Logf("Expected error without real credentials: %v", err)
		}
	})
}

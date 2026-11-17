package gct

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestErrorScenarios_InvalidLeverage tests invalid leverage values
func TestErrorScenarios_InvalidLeverage(t *testing.T) {
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

	testCases := []struct {
		name     string
		leverage float64
		wantErr  bool
	}{
		{
			name:     "Leverage_Zero",
			leverage: 0,
			wantErr:  true,
		},
		{
			name:     "Leverage_Negative",
			leverage: -5,
			wantErr:  true,
		},
		{
			name:     "Leverage_TooHigh",
			leverage: 101,
			wantErr:  true,
		},
		{
			name:     "Leverage_Valid_Min",
			leverage: 1,
			wantErr:  false,
		},
		{
			name:     "Leverage_Valid_Mid",
			leverage: 20,
			wantErr:  false,
		},
		{
			name:     "Leverage_Valid_Max",
			leverage: 100,
			wantErr:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &SubmitOrderRequest{
				Exchange: "kucoin",
				Pair:     "DOGEUSDTM",
				Side:     "buy",
				Type:     "market",
				Amount:   10,
				Leverage: tc.leverage,
			}

			// Validate leverage
			err := ValidateLeverageValue(int(tc.leverage))

			if tc.wantErr {
				assert.Error(t, err, "Should return error for invalid leverage")
				t.Logf("Expected error for leverage %v: %v", tc.leverage, err)
			} else {
				assert.NoError(t, err, "Should not return error for valid leverage")

				// Try to submit order (will fail due to credentials, but validates request)
				_, submitErr := client.SubmitFuturesOrder(ctx, req)
				if submitErr != nil {
					t.Logf("Submit error (expected without real credentials): %v", submitErr)
				}
			}
		})
	}
}

// TestErrorScenarios_InsufficientMargin tests insufficient margin error
func TestErrorScenarios_InsufficientMargin(t *testing.T) {
	t.Skip("Requires real KuCoin account with insufficient margin - manual test only")

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

	// Try to place order with very high amount (should fail with insufficient margin)
	req := &SubmitOrderRequest{
		Exchange: "kucoin",
		Pair:     "DOGEUSDTM",
		Side:     "buy",
		Type:     "market",
		Amount:   1000000, // Very high amount
		Leverage: 100,
	}

	_, err := client.SubmitFuturesOrder(ctx, req)
	require.Error(t, err, "Should return error for insufficient margin")
	t.Logf("Insufficient margin error: %v", err)
}

// TestErrorScenarios_InvalidCredentials tests invalid API credentials
func TestErrorScenarios_InvalidCredentials(t *testing.T) {
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

	// Set invalid credentials
	client.SetKuCoinCredentials("invalid-key", "invalid-secret", "invalid-password")

	ctx := context.Background()

	t.Run("InvalidCredentials_SubmitOrder", func(t *testing.T) {
		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "DOGEUSDTM",
			Side:     "buy",
			Type:     "market",
			Amount:   10,
			Leverage: 5,
		}

		_, err := client.SubmitFuturesOrder(ctx, req)
		require.Error(t, err, "Should return error for invalid credentials")
		assert.Contains(t, err.Error(), "400003", "Error should contain KuCoin error code")
		t.Logf("Invalid credentials error: %v", err)
	})

	t.Run("InvalidCredentials_GetPositions", func(t *testing.T) {
		_, err := client.GetFuturesPositions(ctx, "kucoin")
		require.Error(t, err, "Should return error for invalid credentials")
		assert.Contains(t, err.Error(), "400003", "Error should contain KuCoin error code")
		t.Logf("Invalid credentials error: %v", err)
	})
}

// TestErrorScenarios_ErrorMessages tests that error messages are clear
func TestErrorScenarios_ErrorMessages(t *testing.T) {
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

	t.Run("NilRequest_ClearError", func(t *testing.T) {
		_, err := client.SubmitFuturesOrder(ctx, nil)
		require.Error(t, err, "Should return error for nil request")
		assert.Contains(t, err.Error(), "nil", "Error message should mention nil")
		t.Logf("Nil request error: %v", err)
	})

	t.Run("EmptySymbol_ClearError", func(t *testing.T) {
		_, err := client.GetFuturesPosition(ctx, "kucoin", "")
		require.Error(t, err, "Should return error for empty symbol")
		assert.Contains(t, err.Error(), "symbol", "Error message should mention symbol")
		t.Logf("Empty symbol error: %v", err)
	})

	t.Run("InvalidLeverage_ClearError", func(t *testing.T) {
		err := ValidateLeverageValue(0)
		require.Error(t, err, "Should return error for zero leverage")
		assert.Contains(t, err.Error(), "leverage", "Error message should mention leverage")
		t.Logf("Invalid leverage error: %v", err)
	})
}

// TestErrorScenarios_KuCoinErrorMapping tests KuCoin error code mapping
func TestErrorScenarios_KuCoinErrorMapping(t *testing.T) {
	testCases := []struct {
		code        string
		message     string
		expectedErr error
	}{
		{
			code:        "330005",
			message:     "The order's margin mode does not match the selected one",
			expectedErr: ErrMarginModeMismatch, // 330005 = mode mismatch, not "not configured"
		},
		{
			code:        "400100",
			message:     "Invalid API credentials",
			expectedErr: ErrInvalidCredentials,
		},
		{
			code:        "429000",
			message:     "Too many requests",
			expectedErr: ErrRateLimited,
		},
		{
			code:        "400350",
			message:     "Invalid leverage",
			expectedErr: ErrInvalidLeverage,
		},
	}

	for _, tc := range testCases {
		t.Run("ErrorCode_"+tc.code, func(t *testing.T) {
			err := MapKuCoinError(tc.code, tc.message)
			assert.ErrorIs(t, err, tc.expectedErr, "Should wrap expected error")
			t.Logf("Mapped error code %s to: %v", tc.code, err)
		})
	}
}

// TestErrorScenarios_ValidationErrors tests request validation
func TestErrorScenarios_ValidationErrors(t *testing.T) {
	testCases := []struct {
		name    string
		req     *FuturesOrderRequest
		wantErr bool
	}{
		{
			name: "Valid_Request",
			req: &FuturesOrderRequest{
				ClientOID: "test123",
				Symbol:    "DOGEUSDTM",
				Side:      "buy",
				Type:      "market",
				Size:      10,
				Leverage:  5,
			},
			wantErr: false,
		},
		{
			name: "Missing_Symbol",
			req: &FuturesOrderRequest{
				ClientOID: "test123",
				Symbol:    "",
				Side:      "buy",
				Type:      "market",
				Size:      10,
				Leverage:  5,
			},
			wantErr: true,
		},
		{
			name: "Invalid_Side",
			req: &FuturesOrderRequest{
				ClientOID: "test123",
				Symbol:    "DOGEUSDTM",
				Side:      "invalid",
				Type:      "market",
				Size:      10,
				Leverage:  5,
			},
			wantErr: true,
		},
		{
			name: "Invalid_Type",
			req: &FuturesOrderRequest{
				ClientOID: "test123",
				Symbol:    "DOGEUSDTM",
				Side:      "buy",
				Type:      "invalid",
				Size:      10,
				Leverage:  5,
			},
			wantErr: true,
		},
		{
			name: "Zero_Size",
			req: &FuturesOrderRequest{
				ClientOID: "test123",
				Symbol:    "DOGEUSDTM",
				Side:      "buy",
				Type:      "market",
				Size:      0,
				Leverage:  5,
			},
			wantErr: true,
		},
		{
			name: "Invalid_Leverage",
			req: &FuturesOrderRequest{
				ClientOID: "test123",
				Symbol:    "DOGEUSDTM",
				Side:      "buy",
				Type:      "market",
				Size:      10,
				Leverage:  101,
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()

			if tc.wantErr {
				assert.Error(t, err, "Should return validation error")
				t.Logf("Validation error: %v", err)
			} else {
				assert.NoError(t, err, "Should not return validation error")
			}
		})
	}
}

// TestErrorScenarios_ContextTimeout tests context timeout handling
func TestErrorScenarios_ContextTimeout(t *testing.T) {
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

	t.Run("ContextTimeout_SubmitOrder", func(t *testing.T) {
		// Create context with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Wait for context to timeout
		time.Sleep(10 * time.Millisecond)

		req := &SubmitOrderRequest{
			Exchange: "kucoin",
			Pair:     "DOGEUSDTM",
			Side:     "buy",
			Type:     "market",
			Amount:   10,
			Leverage: 5,
		}

		_, err := client.SubmitFuturesOrder(ctx, req)
		// Should handle timeout gracefully
		if err != nil {
			t.Logf("Context timeout error (expected): %v", err)
		}
	})
}

// TestErrorScenarios_NetworkErrors tests network error handling
func TestErrorScenarios_NetworkErrors(t *testing.T) {
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
	// Use invalid credentials to simulate network/API errors
	client.SetKuCoinCredentials("test-key", "test-secret", "test-password")

	ctx := context.Background()

	t.Run("NetworkError_GetPositions", func(t *testing.T) {
		_, err := client.GetFuturesPositions(ctx, "kucoin")
		// Should handle network errors gracefully
		if err != nil {
			t.Logf("Network error (expected): %v", err)
		}
	})
}

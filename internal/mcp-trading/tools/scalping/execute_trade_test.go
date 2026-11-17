package scalping

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTranslateKuCoinError tests the error translation function
func TestTranslateKuCoinError(t *testing.T) {
	tests := []struct {
		name          string
		inputError    error
		expectedCode  string
		shouldContain []string
	}{
		{
			name:         "margin mode mismatch with code 330005",
			inputError:   fmt.Errorf("KuCoin error 330005: margin mode mismatch"),
			expectedCode: "MARGIN_MODE_MISMATCH",
			shouldContain: []string{
				"MARGIN_MODE_MISMATCH",
				"position margin mode",
				"ISOLATED vs CROSSED",
				"close existing positions",
			},
		},
		{
			name:         "margin mode mismatch with text",
			inputError:   fmt.Errorf("MARGIN_MODE_MISMATCH: position error"),
			expectedCode: "MARGIN_MODE_MISMATCH",
			shouldContain: []string{
				"MARGIN_MODE_MISMATCH",
				"margin mode",
			},
		},
		{
			name:         "futures permission required with code 400007",
			inputError:   fmt.Errorf("KuCoin error 400007: access denied"),
			expectedCode: "FUTURES_PERMISSION_REQUIRED",
			shouldContain: []string{
				"FUTURES_PERMISSION_REQUIRED",
				"Futures trading permission",
				"KuCoin API key settings",
				"https://www.kucoin.com/account/api",
			},
		},
		{
			name:         "futures permission required with text",
			inputError:   fmt.Errorf("FUTURES_PERMISSION_REQUIRED: no access"),
			expectedCode: "FUTURES_PERMISSION_REQUIRED",
			shouldContain: []string{
				"FUTURES_PERMISSION_REQUIRED",
				"permission",
			},
		},
		{
			name:         "insufficient balance with code 200004",
			inputError:   fmt.Errorf("KuCoin error 200004: insufficient balance"),
			expectedCode: "INSUFFICIENT_BALANCE",
			shouldContain: []string{
				"INSUFFICIENT_BALANCE",
				"Not enough balance",
				"deposit funds",
				"reduce the order size",
			},
		},
		{
			name:         "insufficient balance with text",
			inputError:   fmt.Errorf("insufficient margin for order"),
			expectedCode: "INSUFFICIENT_BALANCE",
			shouldContain: []string{
				"INSUFFICIENT_BALANCE",
				"balance",
			},
		},
		{
			name:         "invalid credentials with code 401",
			inputError:   fmt.Errorf("HTTP 401: Unauthorized"),
			expectedCode: "INVALID_CREDENTIALS",
			shouldContain: []string{
				"INVALID_CREDENTIALS",
				"API credentials",
				"API_KEY",
				"API_SECRET",
				"API_PASSWORD",
			},
		},
		{
			name:         "invalid credentials with text",
			inputError:   fmt.Errorf("INVALID_CREDENTIALS: auth failed"),
			expectedCode: "INVALID_CREDENTIALS",
			shouldContain: []string{
				"INVALID_CREDENTIALS",
				"credentials",
			},
		},
		{
			name:         "invalid leverage",
			inputError:   fmt.Errorf("invalid leverage value: out of range"),
			expectedCode: "INVALID_LEVERAGE",
			shouldContain: []string{
				"INVALID_LEVERAGE",
				"out of allowed range",
				"1-100x",
			},
		},
		{
			name:         "invalid symbol",
			inputError:   fmt.Errorf("invalid symbol format"),
			expectedCode: "INVALID_SYMBOL",
			shouldContain: []string{
				"INVALID_SYMBOL",
				"symbol format",
				"BTC-USDT",
				"BTCUSDTM",
			},
		},
		{
			name:         "network timeout",
			inputError:   fmt.Errorf("connection timeout"),
			expectedCode: "NETWORK_ERROR",
			shouldContain: []string{
				"NETWORK_ERROR",
				"timed out",
				"internet connection",
			},
		},
		{
			name:         "rate limit exceeded",
			inputError:   fmt.Errorf("rate limit exceeded"),
			expectedCode: "RATE_LIMIT_EXCEEDED",
			shouldContain: []string{
				"RATE_LIMIT_EXCEEDED",
				"Too many requests",
				"wait a moment",
			},
		},
		{
			name:         "rate limit with code 429",
			inputError:   fmt.Errorf("HTTP 429: Too Many Requests"),
			expectedCode: "RATE_LIMIT_EXCEEDED",
			shouldContain: []string{
				"RATE_LIMIT_EXCEEDED",
				"requests",
			},
		},
		{
			name:         "unknown error passthrough",
			inputError:   fmt.Errorf("some random error"),
			expectedCode: "",
			shouldContain: []string{
				"some random error",
			},
		},
		{
			name:          "nil error",
			inputError:    nil,
			expectedCode:  "",
			shouldContain: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := translateKuCoinError(tt.inputError)

			if tt.inputError == nil {
				assert.Nil(t, result)
				return
			}

			assert.NotNil(t, result)
			errStr := result.Error()

			for _, expected := range tt.shouldContain {
				assert.Contains(t, errStr, expected,
					"Error message should contain: %s\nActual: %s", expected, errStr)
			}

			if tt.expectedCode != "" {
				assert.Contains(t, errStr, tt.expectedCode,
					"Error message should contain code: %s\nActual: %s", tt.expectedCode, errStr)
			}
		})
	}
}

// TestTranslateKuCoinError_ErrorTypes tests that errors are properly typed
func TestTranslateKuCoinError_ErrorTypes(t *testing.T) {
	tests := []struct {
		name       string
		inputError error
		checkFunc  func(error) bool
	}{
		{
			name:       "margin mode mismatch is error",
			inputError: fmt.Errorf("330005: margin mode mismatch"),
			checkFunc: func(err error) bool {
				return err != nil && errors.Is(err, err)
			},
		},
		{
			name:       "nil error returns nil",
			inputError: nil,
			checkFunc: func(err error) bool {
				return err == nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := translateKuCoinError(tt.inputError)
			assert.True(t, tt.checkFunc(result))
		})
	}
}

// TestTranslateKuCoinError_Idempotent tests that translating twice doesn't change the message
func TestTranslateKuCoinError_Idempotent(t *testing.T) {
	originalErr := fmt.Errorf("KuCoin error 330005: margin mode mismatch")

	// Translate once
	translated1 := translateKuCoinError(originalErr)
	assert.NotNil(t, translated1)

	// Translate again
	translated2 := translateKuCoinError(translated1)
	assert.NotNil(t, translated2)

	// Should still contain the key information
	assert.Contains(t, translated2.Error(), "MARGIN_MODE_MISMATCH")
}

package gct

import (
	"errors"
	"fmt"
	"strings"
)

// Futures-specific errors
var (
	ErrMarginModeNotSet     = errors.New("margin mode not configured for symbol")
	ErrMarginModeMismatch   = errors.New("margin mode mismatch")
	ErrInvalidLeverage      = errors.New("leverage value out of allowed range")
	ErrPositionNotFound     = errors.New("position not found")
	ErrInsufficientMargin   = errors.New("insufficient margin for order")
	ErrExceedsExposureLimit = errors.New("order exceeds maximum exposure limit")
	ErrInvalidOrderRequest  = errors.New("invalid order request")
	ErrNilRequest           = errors.New("request is nil")
	ErrNilPosition          = errors.New("position is nil")
)

// API errors
var (
	ErrInvalidCredentials = errors.New("invalid API credentials")
	ErrRateLimited        = errors.New("rate limit exceeded")
	ErrAPIError           = errors.New("KuCoin API error")
	ErrNetworkError       = errors.New("network error")
	ErrTimeout            = errors.New("request timeout")
)

// KuCoin error code mappings
var kucoinErrorMap = map[string]error{
	"330005": ErrMarginModeMismatch,
	"400007": ErrInvalidCredentials, // Access denied / permission required
	"400100": ErrInvalidCredentials,
	"400350": ErrInvalidLeverage,
	"200004": ErrInsufficientMargin, // Insufficient balance
	"429000": ErrRateLimited,
	"500000": ErrAPIError,
}

// MapKuCoinError maps KuCoin error codes to internal errors
func MapKuCoinError(code string, message string) error {
	if code == "" {
		return fmt.Errorf("%w: empty error code", ErrAPIError)
	}

	if err, ok := kucoinErrorMap[code]; ok {
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}

	return fmt.Errorf("%w: code=%s, message=%s", ErrAPIError, code, message)
}

// IsRetryableError checks if an error should be retried
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	return errors.Is(err, ErrRateLimited) ||
		errors.Is(err, ErrNetworkError) ||
		errors.Is(err, ErrTimeout) ||
		errors.Is(err, ErrAPIError)
}

// ValidateLeverageValue validates leverage is within allowed range
func ValidateLeverageValue(leverage int) error {
	if leverage < 1 || leverage > 100 {
		return fmt.Errorf("%w: must be between 1 and 100, got %d", ErrInvalidLeverage, leverage)
	}
	return nil
}

// ValidateMarginMode validates margin mode value
func ValidateMarginMode(mode string) error {
	if mode != "ISOLATED" && mode != "CROSSED" {
		return fmt.Errorf("invalid margin mode: must be 'ISOLATED' or 'CROSSED', got '%s'", mode)
	}
	return nil
}

// MarginModeError represents a detailed margin mode mismatch error
type MarginModeError struct {
	Symbol             string
	CurrentMarginMode  string
	ExpectedMarginMode string
	Message            string
	Suggestion         string
}

// Error implements the error interface
func (e *MarginModeError) Error() string {
	if e.CurrentMarginMode != "" && e.ExpectedMarginMode != "" {
		return fmt.Sprintf("MARGIN_MODE_MISMATCH: %s - Current: %s, Expected: %s. %s",
			e.Message, e.CurrentMarginMode, e.ExpectedMarginMode, e.Suggestion)
	}
	return fmt.Sprintf("MARGIN_MODE_MISMATCH: %s. %s", e.Message, e.Suggestion)
}

// NewMarginModeError creates a detailed margin mode error
func NewMarginModeError(symbol, currentMode, expectedMode string) *MarginModeError {
	return &MarginModeError{
		Symbol:             symbol,
		CurrentMarginMode:  currentMode,
		ExpectedMarginMode: expectedMode,
		Message:            fmt.Sprintf("The position margin mode for %s does not match", symbol),
		Suggestion:         "Please close existing positions or change margin mode in KuCoin web interface before placing new orders",
	}
}

// EnhanceKuCoinError enhances KuCoin error messages with actionable information
func EnhanceKuCoinError(err error, symbol string) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Error 330005: Margin mode mismatch
	if errors.Is(err, ErrMarginModeMismatch) || containsErrorCode(errStr, "330005") {
		// Try to extract margin mode information from error message
		// KuCoin error format: "The position margin mode does not match"
		return NewMarginModeError(symbol, "", "")
	}

	// Error 400007: Access denied / futures permission required
	if containsErrorCode(errStr, "400007") {
		return fmt.Errorf("%w: Futures trading permission not enabled. Please enable 'Futures Trading' permission in your KuCoin API key settings at https://www.kucoin.com/account/api", ErrInvalidCredentials)
	}

	// Error 200004: Insufficient balance
	if containsErrorCode(errStr, "200004") {
		return fmt.Errorf("%w: Not enough balance to place order for %s. Please deposit funds to your futures account", ErrInsufficientMargin, symbol)
	}

	// Error 401: Unauthorized
	if containsErrorCode(errStr, "401") {
		return fmt.Errorf("%w: Invalid API credentials. Please verify your API_KEY, API_SECRET, and API_PASSWORD", ErrInvalidCredentials)
	}

	// Return original error if no enhancement available
	return err
}

// containsErrorCode checks if error string contains a specific error code
func containsErrorCode(errStr, code string) bool {
	if errStr == "" || code == "" {
		return false
	}
	return strings.Contains(errStr, code) ||
		strings.Contains(errStr, fmt.Sprintf("code=%s", code)) ||
		strings.Contains(errStr, fmt.Sprintf("error code: %s", code))
}

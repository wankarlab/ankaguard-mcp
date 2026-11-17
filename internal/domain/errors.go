package domain

import (
	"fmt"
	"time"
)

// ErrorCode represents error codes used throughout the application
type ErrorCode string

const (
	// General errors
	ErrCodeInternalError    ErrorCode = "INTERNAL_ERROR"
	ErrCodeInvalidRequest   ErrorCode = "INVALID_REQUEST"
	ErrCodeValidationFailed ErrorCode = "VALIDATION_FAILED"
	ErrCodeNotFound         ErrorCode = "NOT_FOUND"
	ErrCodeAlreadyExists    ErrorCode = "ALREADY_EXISTS"
	ErrCodeConflict         ErrorCode = "CONFLICT"

	// Authentication and authorization errors
	ErrCodeUnauthorized      ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden         ErrorCode = "FORBIDDEN"
	ErrCodeTokenExpired      ErrorCode = "TOKEN_EXPIRED"
	ErrCodeInvalidToken      ErrorCode = "INVALID_TOKEN"
	ErrCodeInsufficientLevel ErrorCode = "INSUFFICIENT_LEVEL"

	// Trading errors
	ErrCodeInsufficientFunds    ErrorCode = "INSUFFICIENT_FUNDS"
	ErrCodeInvalidSymbol        ErrorCode = "INVALID_SYMBOL"
	ErrCodeInvalidOrderType     ErrorCode = "INVALID_ORDER_TYPE"
	ErrCodeOrderNotFound        ErrorCode = "ORDER_NOT_FOUND"
	ErrCodePositionNotFound     ErrorCode = "POSITION_NOT_FOUND"
	ErrCodeOrderAlreadyFilled   ErrorCode = "ORDER_ALREADY_FILLED"
	ErrCodeOrderAlreadyCanceled ErrorCode = "ORDER_ALREADY_CANCELED"
	ErrCodeMinOrderSize         ErrorCode = "MIN_ORDER_SIZE"
	ErrCodeMaxOrderSize         ErrorCode = "MAX_ORDER_SIZE"
	ErrCodeInvalidPrice         ErrorCode = "INVALID_PRICE"
	ErrCodeMarketClosed         ErrorCode = "MARKET_CLOSED"

	// Exchange errors
	ErrCodeExchangeUnavailable     ErrorCode = "EXCHANGE_UNAVAILABLE"
	ErrCodeExchangeError           ErrorCode = "EXCHANGE_ERROR"
	ErrCodeAPIKeyInvalid           ErrorCode = "API_KEY_INVALID"
	ErrCodeAPIKeyExpired           ErrorCode = "API_KEY_EXPIRED"
	ErrCodeRateLimitExceeded       ErrorCode = "RATE_LIMIT_EXCEEDED"
	ErrCodeExchangeTimeout         ErrorCode = "EXCHANGE_TIMEOUT"
	ErrCodeInsufficientPermissions ErrorCode = "INSUFFICIENT_PERMISSIONS"

	// Risk management errors
	ErrCodeRiskLimitExceeded   ErrorCode = "RISK_LIMIT_EXCEEDED"
	ErrCodeMaxDrawdownExceeded ErrorCode = "MAX_DRAWDOWN_EXCEEDED"
	ErrCodeMaxExposureExceeded ErrorCode = "MAX_EXPOSURE_EXCEEDED"
	ErrCodeMaxLeverageExceeded ErrorCode = "MAX_LEVERAGE_EXCEEDED"
	ErrCodeTradingPaused       ErrorCode = "TRADING_PAUSED"

	// Strategy errors
	ErrCodeStrategyNotFound   ErrorCode = "STRATEGY_NOT_FOUND"
	ErrCodeStrategyRunning    ErrorCode = "STRATEGY_RUNNING"
	ErrCodeStrategyStopped    ErrorCode = "STRATEGY_STOPPED"
	ErrCodeInvalidParameters  ErrorCode = "INVALID_PARAMETERS"
	ErrCodeBacktestFailed     ErrorCode = "BACKTEST_FAILED"
	ErrCodeOptimizationFailed ErrorCode = "OPTIMIZATION_FAILED"

	// Data errors
	ErrCodeDataNotAvailable ErrorCode = "DATA_NOT_AVAILABLE"
	ErrCodeInvalidTimeRange ErrorCode = "INVALID_TIME_RANGE"
	ErrCodeDataCorrupted    ErrorCode = "DATA_CORRUPTED"

	// WebSocket errors
	ErrCodeConnectionClosed   ErrorCode = "CONNECTION_CLOSED"
	ErrCodeInvalidMessage     ErrorCode = "INVALID_MESSAGE"
	ErrCodeSubscriptionFailed ErrorCode = "SUBSCRIPTION_FAILED"

	// MCP errors
	ErrCodeToolNotFound        ErrorCode = "TOOL_NOT_FOUND"
	ErrCodeToolExecutionFailed ErrorCode = "TOOL_EXECUTION_FAILED"
	ErrCodeInvalidToolParams   ErrorCode = "INVALID_TOOL_PARAMS"
	ErrCodeMCPProtocolError    ErrorCode = "MCP_PROTOCOL_ERROR"
)

// APIError represents a structured API error
type APIError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
	Field     string    `json:"field,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	RequestID string    `json:"request_id,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
}

// Error implements the error interface
func (e *APIError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewAPIError creates a new API error
func NewAPIError(code ErrorCode, message string) *APIError {
	return &APIError{
		Code:      code,
		Message:   message,
		Timestamp: time.Now().UTC(),
	}
}

// NewAPIErrorWithDetails creates a new API error with details
func NewAPIErrorWithDetails(code ErrorCode, message, details string) *APIError {
	return &APIError{
		Code:      code,
		Message:   message,
		Details:   details,
		Timestamp: time.Now().UTC(),
	}
}

// NewValidationError creates a new validation error
func NewValidationError(field, message string) *APIError {
	return &APIError{
		Code:      ErrCodeValidationFailed,
		Message:   message,
		Field:     field,
		Timestamp: time.Now().UTC(),
	}
}

// WithRequestID adds a request ID to the error
func (e *APIError) WithRequestID(requestID string) *APIError {
	e.RequestID = requestID
	return e
}

// WithUserID adds a user ID to the error
func (e *APIError) WithUserID(userID string) *APIError {
	e.UserID = userID
	return e
}

// WithField adds a field to the error
func (e *APIError) WithField(field string) *APIError {
	e.Field = field
	return e
}

// IsTemporary returns true if the error is temporary and the operation can be retried
func (e *APIError) IsTemporary() bool {
	switch e.Code {
	case ErrCodeExchangeTimeout,
		ErrCodeExchangeUnavailable,
		ErrCodeRateLimitExceeded,
		ErrCodeConnectionClosed:
		return true
	default:
		return false
	}
}

// IsRetryable returns true if the operation should be retried
func (e *APIError) IsRetryable() bool {
	return e.IsTemporary()
}

// HTTPStatusCode returns the appropriate HTTP status code for the error
func (e *APIError) HTTPStatusCode() int {
	switch e.Code {
	case ErrCodeInvalidRequest,
		ErrCodeValidationFailed,
		ErrCodeInvalidSymbol,
		ErrCodeInvalidOrderType,
		ErrCodeInvalidPrice,
		ErrCodeMinOrderSize,
		ErrCodeMaxOrderSize,
		ErrCodeInvalidTimeRange,
		ErrCodeInvalidMessage,
		ErrCodeInvalidToolParams:
		return 400 // Bad Request

	case ErrCodeUnauthorized,
		ErrCodeTokenExpired,
		ErrCodeInvalidToken,
		ErrCodeAPIKeyInvalid,
		ErrCodeAPIKeyExpired:
		return 401 // Unauthorized

	case ErrCodeForbidden,
		ErrCodeInsufficientLevel,
		ErrCodeInsufficientPermissions:
		return 403 // Forbidden

	case ErrCodeNotFound,
		ErrCodeOrderNotFound,
		ErrCodePositionNotFound,
		ErrCodeStrategyNotFound,
		ErrCodeToolNotFound:
		return 404 // Not Found

	case ErrCodeAlreadyExists,
		ErrCodeConflict,
		ErrCodeOrderAlreadyFilled,
		ErrCodeOrderAlreadyCanceled:
		return 409 // Conflict

	case ErrCodeInsufficientFunds,
		ErrCodeRiskLimitExceeded,
		ErrCodeMaxDrawdownExceeded,
		ErrCodeMaxExposureExceeded,
		ErrCodeMaxLeverageExceeded,
		ErrCodeTradingPaused:
		return 422 // Unprocessable Entity

	case ErrCodeRateLimitExceeded:
		return 429 // Too Many Requests

	case ErrCodeExchangeTimeout,
		ErrCodeExchangeUnavailable:
		return 503 // Service Unavailable

	default:
		return 500 // Internal Server Error
	}
}

// Common error instances
var (
	ErrInternalError       = NewAPIError(ErrCodeInternalError, "Internal server error")
	ErrInvalidRequest      = NewAPIError(ErrCodeInvalidRequest, "Invalid request")
	ErrUnauthorized        = NewAPIError(ErrCodeUnauthorized, "Unauthorized")
	ErrForbidden           = NewAPIError(ErrCodeForbidden, "Forbidden")
	ErrNotFound            = NewAPIError(ErrCodeNotFound, "Resource not found")
	ErrAlreadyExists       = NewAPIError(ErrCodeAlreadyExists, "Resource already exists")
	ErrValidationFailed    = NewAPIError(ErrCodeValidationFailed, "Validation failed")
	ErrInsufficientFunds   = NewAPIError(ErrCodeInsufficientFunds, "Insufficient funds")
	ErrRateLimitExceeded   = NewAPIError(ErrCodeRateLimitExceeded, "Rate limit exceeded")
	ErrExchangeUnavailable = NewAPIError(ErrCodeExchangeUnavailable, "Exchange unavailable")
	ErrTradingPaused       = NewAPIError(ErrCodeTradingPaused, "Trading is paused due to risk limits")
)

// Cache error instances
var (
	ErrCacheNotAvailable = NewAPIError(ErrCodeDataNotAvailable, "Cache service not available")
	ErrCacheKeyNotFound  = NewAPIError(ErrCodeNotFound, "Cache key not found")
)

package resilience

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// ResilientClient envuelve un cliente con reintentos y circuit breaker
type ResilientClient struct {
	circuitBreaker *CircuitBreaker
	retryConfig    *RetryConfig
	logger         *logrus.Logger
}

// NewResilientClient crea un nuevo cliente resiliente
func NewResilientClient(logger *logrus.Logger) *ResilientClient {
	cbConfig := &CircuitBreakerConfig{
		MaxFailures:      5,
		Timeout:          30 * time.Second,
		MaxRequests:      3,
		SuccessThreshold: 2,
	}

	retryConfig := &RetryConfig{
		MaxAttempts:       3,
		InitialBackoff:    200 * time.Millisecond,
		MaxBackoff:        5 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter:            true,
	}

	return &ResilientClient{
		circuitBreaker: NewCircuitBreaker(cbConfig, logger),
		retryConfig:    retryConfig,
		logger:         logger,
	}
}

// ExecuteWithResilience ejecuta una función con reintentos y circuit breaker
func (rc *ResilientClient) ExecuteWithResilience(ctx context.Context, operation string, fn func(context.Context) error) error {
	// Primero verificar el circuit breaker
	if err := rc.circuitBreaker.beforeRequest(); err != nil {
		rc.logger.WithFields(logrus.Fields{
			"operation": operation,
			"state":     rc.circuitBreaker.GetState().String(),
		}).Warn("Circuit breaker prevented operation")
		return fmt.Errorf("circuit breaker open for %s: %w", operation, err)
	}

	// Ejecutar con reintentos
	err := RetryWithBackoff(ctx, rc.retryConfig, fn, rc.logger)

	// Registrar resultado en circuit breaker
	rc.circuitBreaker.afterRequest(err)

	return err
}

// GetStats retorna estadísticas del cliente resiliente
func (rc *ResilientClient) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"circuit_breaker": rc.circuitBreaker.GetStats(),
		"retry_config": map[string]interface{}{
			"max_attempts":       rc.retryConfig.MaxAttempts,
			"initial_backoff":    rc.retryConfig.InitialBackoff.String(),
			"max_backoff":        rc.retryConfig.MaxBackoff.String(),
			"backoff_multiplier": rc.retryConfig.BackoffMultiplier,
			"jitter":             rc.retryConfig.Jitter,
		},
	}
}

// Reset resetea el circuit breaker
func (rc *ResilientClient) Reset() {
	rc.circuitBreaker.Reset()
}

// GetCircuitBreakerState retorna el estado del circuit breaker
func (rc *ResilientClient) GetCircuitBreakerState() State {
	return rc.circuitBreaker.GetState()
}

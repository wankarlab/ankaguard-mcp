package resilience

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"
)

// RetryConfig configura el comportamiento de reintentos
type RetryConfig struct {
	MaxAttempts       int           // Número máximo de intentos
	InitialBackoff    time.Duration // Backoff inicial
	MaxBackoff        time.Duration // Backoff máximo
	BackoffMultiplier float64       // Multiplicador para backoff exponencial
	Jitter            bool          // Agregar jitter aleatorio
}

// DefaultRetryConfig retorna configuración por defecto
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter:            true,
	}
}

// AggressiveRetryConfig para operaciones críticas
func AggressiveRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:       5,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        5 * time.Second,
		BackoffMultiplier: 1.5,
		Jitter:            true,
	}
}

// RetryableFunc es una función que puede ser reintentada
type RetryableFunc func(ctx context.Context) error

// RetryWithBackoff ejecuta una función con reintentos exponenciales
func RetryWithBackoff(ctx context.Context, config *RetryConfig, fn RetryableFunc, logger *logrus.Logger) error {
	if config == nil {
		config = DefaultRetryConfig()
	}

	var lastErr error

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Verificar si el contexto fue cancelado
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled: %w", ctx.Err())
		default:
		}

		// Ejecutar la función
		err := fn(ctx)
		if err == nil {
			// Éxito
			if attempt > 0 && logger != nil {
				logger.WithField("attempts", attempt+1).Info("Operation succeeded after retries")
			}
			return nil
		}

		lastErr = err

		// Verificar si el error es retryable
		if !IsRetryable(err) {
			if logger != nil {
				logger.WithError(err).Warn("Non-retryable error encountered")
			}
			return err
		}

		// Si es el último intento, no esperar
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calcular backoff
		backoff := calculateBackoff(attempt, config)

		if logger != nil {
			logger.WithFields(logrus.Fields{
				"attempt":      attempt + 1,
				"max_attempts": config.MaxAttempts,
				"backoff":      backoff,
				"error":        err.Error(),
			}).Warn("Operation failed, retrying...")
		}

		// Esperar antes del siguiente intento
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled during backoff: %w", ctx.Err())
		}
	}

	return fmt.Errorf("operation failed after %d attempts: %w", config.MaxAttempts, lastErr)
}

// calculateBackoff calcula el tiempo de espera con backoff exponencial
func calculateBackoff(attempt int, config *RetryConfig) time.Duration {
	// Backoff exponencial: initialBackoff * (multiplier ^ attempt)
	backoff := float64(config.InitialBackoff) * math.Pow(config.BackoffMultiplier, float64(attempt))

	// Limitar al máximo
	if backoff > float64(config.MaxBackoff) {
		backoff = float64(config.MaxBackoff)
	}

	duration := time.Duration(backoff)

	// Agregar jitter si está habilitado (±25%)
	if config.Jitter {
		jitterFactor := float64(time.Now().UnixNano()%100) / 100.0
		jitter := float64(duration) * 0.25 * (2*jitterFactor - 1)
		duration = time.Duration(float64(duration) + jitter)
	}

	return duration
}

// IsRetryable determina si un error es retryable
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Errores de contexto no son retryables
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	errStr := err.Error()

	// Errores de red son retryables
	if contains(errStr, "timeout") ||
		contains(errStr, "connection refused") ||
		contains(errStr, "connection reset") ||
		contains(errStr, "temporary failure") ||
		contains(errStr, "NETWORK_ERROR") ||
		contains(errStr, "EOF") {
		return true
	}

	// Rate limiting es retryable
	if contains(errStr, "rate limit") ||
		contains(errStr, "429") ||
		contains(errStr, "RATE_LIMIT_EXCEEDED") {
		return true
	}

	// Errores de servidor temporales son retryables
	if contains(errStr, "500") ||
		contains(errStr, "502") ||
		contains(errStr, "503") ||
		contains(errStr, "504") {
		return true
	}

	// Errores de autenticación NO son retryables
	if contains(errStr, "401") ||
		contains(errStr, "403") ||
		contains(errStr, "INVALID_CREDENTIALS") ||
		contains(errStr, "FUTURES_PERMISSION_REQUIRED") {
		return false
	}

	// Errores de validación NO son retryables
	if contains(errStr, "INVALID_") ||
		contains(errStr, "INSUFFICIENT_BALANCE") ||
		contains(errStr, "MARGIN_MODE_MISMATCH") {
		return false
	}

	// Por defecto, no reintentar
	return false
}

// contains verifica si una cadena contiene otra (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr) &&
			(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
				findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

package resilience

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrTooManyRequests = errors.New("too many requests")
)

// State representa el estado del circuit breaker
type State int

const (
	StateClosed   State = iota // Funcionando normalmente
	StateOpen                  // Circuito abierto (rechazando requests)
	StateHalfOpen              // Probando si el servicio se recuperó
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig configura el circuit breaker
type CircuitBreakerConfig struct {
	MaxFailures      uint32        // Número de fallos antes de abrir
	Timeout          time.Duration // Tiempo antes de intentar half-open
	MaxRequests      uint32        // Requests permitidos en half-open
	SuccessThreshold uint32        // Éxitos necesarios para cerrar desde half-open
}

// DefaultCircuitBreakerConfig retorna configuración por defecto
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		MaxFailures:      5,
		Timeout:          30 * time.Second,
		MaxRequests:      3,
		SuccessThreshold: 2,
	}
}

// CircuitBreaker implementa el patrón Circuit Breaker
type CircuitBreaker struct {
	config       *CircuitBreakerConfig
	state        State
	failures     uint32
	successes    uint32
	requests     uint32
	lastFailTime time.Time
	mu           sync.RWMutex
	logger       *logrus.Logger
}

// NewCircuitBreaker crea un nuevo circuit breaker
func NewCircuitBreaker(config *CircuitBreakerConfig, logger *logrus.Logger) *CircuitBreaker {
	if config == nil {
		config = DefaultCircuitBreakerConfig()
	}

	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
		logger: logger,
	}
}

// Execute ejecuta una función con protección de circuit breaker
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	// Verificar si podemos ejecutar
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	// Ejecutar la función
	err := fn()

	// Registrar el resultado
	cb.afterRequest(err)

	return err
}

// beforeRequest verifica si se puede ejecutar el request
func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		// Funcionando normalmente
		return nil

	case StateOpen:
		// Verificar si es tiempo de intentar half-open
		if time.Since(cb.lastFailTime) > cb.config.Timeout {
			cb.setState(StateHalfOpen)
			cb.requests = 0
			cb.successes = 0
			if cb.logger != nil {
				cb.logger.Info("Circuit breaker transitioning to half-open state")
			}
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// Limitar requests en half-open
		if cb.requests >= cb.config.MaxRequests {
			return ErrTooManyRequests
		}
		cb.requests++
		return nil

	default:
		return fmt.Errorf("unknown circuit breaker state: %v", cb.state)
	}
}

// afterRequest registra el resultado del request
func (cb *CircuitBreaker) afterRequest(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}
}

// onSuccess maneja un request exitoso
func (cb *CircuitBreaker) onSuccess() {
	switch cb.state {
	case StateClosed:
		// Reset failures counter
		cb.failures = 0

	case StateHalfOpen:
		cb.successes++
		if cb.successes >= cb.config.SuccessThreshold {
			cb.setState(StateClosed)
			cb.failures = 0
			cb.successes = 0
			cb.requests = 0
			if cb.logger != nil {
				cb.logger.Info("Circuit breaker closed after successful recovery")
			}
		}
	}
}

// onFailure maneja un request fallido
func (cb *CircuitBreaker) onFailure() {
	cb.failures++
	cb.lastFailTime = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failures >= cb.config.MaxFailures {
			cb.setState(StateOpen)
			if cb.logger != nil {
				cb.logger.WithFields(logrus.Fields{
					"failures": cb.failures,
					"timeout":  cb.config.Timeout,
				}).Warn("Circuit breaker opened due to failures")
			}
		}

	case StateHalfOpen:
		// Un fallo en half-open vuelve a abrir el circuito
		cb.setState(StateOpen)
		cb.successes = 0
		cb.requests = 0
		if cb.logger != nil {
			cb.logger.Warn("Circuit breaker reopened after failure in half-open state")
		}
	}
}

// setState cambia el estado del circuit breaker
func (cb *CircuitBreaker) setState(state State) {
	if cb.state != state {
		oldState := cb.state
		cb.state = state
		if cb.logger != nil {
			cb.logger.WithFields(logrus.Fields{
				"old_state": oldState.String(),
				"new_state": state.String(),
			}).Info("Circuit breaker state changed")
		}
	}
}

// GetState retorna el estado actual
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetStats retorna estadísticas del circuit breaker
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return map[string]interface{}{
		"state":             cb.state.String(),
		"failures":          cb.failures,
		"successes":         cb.successes,
		"requests":          cb.requests,
		"last_fail_time":    cb.lastFailTime,
		"max_failures":      cb.config.MaxFailures,
		"timeout":           cb.config.Timeout.String(),
		"max_requests":      cb.config.MaxRequests,
		"success_threshold": cb.config.SuccessThreshold,
	}
}

// Reset resetea el circuit breaker al estado cerrado
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.requests = 0

	if cb.logger != nil {
		cb.logger.Info("Circuit breaker manually reset")
	}
}

package llm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCircuitBreaker_InitialState(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Minute)

	assert.Equal(t, StateClosed, cb.GetState())
	assert.True(t, cb.IsAvailable())
	assert.Equal(t, 0, cb.GetFailures())
}

func TestCircuitBreaker_RecordSuccess(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Minute)

	cb.RecordSuccess()

	assert.Equal(t, StateClosed, cb.GetState())
	assert.Equal(t, 0, cb.GetFailures())
	assert.False(t, cb.GetLastSuccess().IsZero())
}

func TestCircuitBreaker_RecordFailure(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Minute)

	// Record failures
	cb.RecordFailure()
	assert.Equal(t, 1, cb.GetFailures())
	assert.Equal(t, StateClosed, cb.GetState())

	cb.RecordFailure()
	assert.Equal(t, 2, cb.GetFailures())
	assert.Equal(t, StateClosed, cb.GetState())

	// Third failure should open the circuit
	cb.RecordFailure()
	assert.Equal(t, 3, cb.GetFailures())
	assert.Equal(t, StateOpen, cb.GetState())
	assert.False(t, cb.IsAvailable())
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, StateOpen, cb.GetState())
	assert.False(t, cb.IsAvailable())

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Should transition to half-open
	assert.True(t, cb.IsAvailable())
	state := cb.GetState()
	assert.Equal(t, StateHalfOpen, state)
}

func TestCircuitBreaker_HalfOpenToClose(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset
	time.Sleep(150 * time.Millisecond)
	cb.GetState() // Trigger transition to half-open

	// Success in half-open should close the circuit
	cb.RecordSuccess()
	assert.Equal(t, StateClosed, cb.GetState())
	assert.Equal(t, 0, cb.GetFailures())
}

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset
	time.Sleep(150 * time.Millisecond)
	cb.GetState() // Trigger transition to half-open

	// Failure in half-open should reopen the circuit
	cb.RecordFailure()
	assert.Equal(t, StateOpen, cb.GetState())
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(2, 1*time.Minute)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, StateOpen, cb.GetState())

	// Reset
	cb.Reset()
	assert.Equal(t, StateClosed, cb.GetState())
	assert.Equal(t, 0, cb.GetFailures())
	assert.True(t, cb.IsAvailable())
}

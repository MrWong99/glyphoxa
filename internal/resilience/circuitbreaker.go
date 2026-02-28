// Package resilience provides circuit breaker and provider failover primitives.
//
// The central type is [CircuitBreaker], a classic three-state breaker
// (closed → open → half-open) that protects callers from cascading failures.
// [FallbackGroup] composes multiple instances of any provider type with per-entry
// circuit breakers so that a failing primary is automatically bypassed in favour
// of healthy fallbacks.
//
// All types are safe for concurrent use.
package resilience

import (
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrCircuitOpen is returned by [CircuitBreaker.Execute] when the breaker is in
// the open state and the reset timeout has not yet elapsed.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// State represents the current operating mode of a [CircuitBreaker].
type State int

const (
	// StateClosed is the normal operating state — all calls are forwarded.
	StateClosed State = iota

	// StateOpen indicates the breaker has tripped due to consecutive failures.
	// Calls are rejected immediately with [ErrCircuitOpen] until the reset
	// timeout elapses.
	StateOpen

	// StateHalfOpen is the probe state entered after the reset timeout. A limited
	// number of calls are allowed through; if they succeed the breaker closes,
	// otherwise it re-opens.
	StateHalfOpen
)

// String returns the human-readable name of the state.
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

// CircuitBreakerConfig holds tuning knobs for a [CircuitBreaker].
type CircuitBreakerConfig struct {
	// Name is a human-readable label used in log messages.
	Name string

	// MaxFailures is the number of consecutive failures in the closed state
	// before the breaker opens. Default: 5.
	MaxFailures int

	// ResetTimeout is how long the breaker stays open before transitioning to
	// half-open. Default: 30s.
	ResetTimeout time.Duration

	// HalfOpenMax is the maximum number of probe calls allowed in the half-open
	// state before the breaker decides whether to close or re-open. Default: 3.
	HalfOpenMax int
}

// CircuitBreaker implements the three-state circuit breaker pattern.
// It is safe for concurrent use from multiple goroutines.
type CircuitBreaker struct {
	name         string
	maxFailures  int
	resetTimeout time.Duration
	halfOpenMax  int

	mu              sync.Mutex
	state           State
	consecutiveFail int
	lastFailure     time.Time
	halfOpenCalls   int
	halfOpenFails   int
}

// NewCircuitBreaker creates a [CircuitBreaker] with the supplied configuration.
// Zero-value config fields are replaced with sensible defaults.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMax <= 0 {
		cfg.HalfOpenMax = 3
	}
	return &CircuitBreaker{
		name:         cfg.Name,
		maxFailures:  cfg.MaxFailures,
		resetTimeout: cfg.ResetTimeout,
		halfOpenMax:  cfg.HalfOpenMax,
		state:        StateClosed,
	}
}

// Execute runs fn if the breaker allows it. In the open state it returns
// [ErrCircuitOpen] without calling fn. In the half-open state a limited number
// of probe calls are permitted.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()
	switch cb.state {
	case StateOpen:
		if time.Since(cb.lastFailure) >= cb.resetTimeout {
			// Transition to half-open.
			cb.state = StateHalfOpen
			cb.halfOpenCalls = 0
			cb.halfOpenFails = 0
			slog.Info("circuit breaker transitioning to half-open",
				"name", cb.name)
		} else {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}

	case StateHalfOpen:
		if cb.halfOpenCalls >= cb.halfOpenMax {
			// Already exhausted the probe budget — stay open.
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
	}

	// Record that we're about to make a call (relevant for half-open accounting).
	inHalfOpen := cb.state == StateHalfOpen
	if inHalfOpen {
		cb.halfOpenCalls++
	}
	cb.mu.Unlock()

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.recordFailure(inHalfOpen)
	} else {
		cb.recordSuccess(inHalfOpen)
	}
	return err
}

// recordFailure handles failure accounting. Must be called with cb.mu held.
func (cb *CircuitBreaker) recordFailure(inHalfOpen bool) {
	cb.lastFailure = time.Now()

	if inHalfOpen {
		cb.halfOpenFails++
		// Any failure in half-open immediately re-opens.
		cb.state = StateOpen
		cb.consecutiveFail = cb.maxFailures
		slog.Warn("circuit breaker re-opened from half-open",
			"name", cb.name)
		return
	}

	// Closed state.
	cb.consecutiveFail++
	if cb.consecutiveFail >= cb.maxFailures {
		cb.state = StateOpen
		slog.Warn("circuit breaker opened",
			"name", cb.name,
			"consecutive_failures", cb.consecutiveFail)
	}
}

// recordSuccess handles success accounting. Must be called with cb.mu held.
func (cb *CircuitBreaker) recordSuccess(inHalfOpen bool) {
	if inHalfOpen {
		// Check if we have enough successful probes to close.
		successes := cb.halfOpenCalls - cb.halfOpenFails
		if successes >= cb.halfOpenMax {
			cb.state = StateClosed
			cb.consecutiveFail = 0
			cb.halfOpenCalls = 0
			cb.halfOpenFails = 0
			slog.Info("circuit breaker closed after successful probes",
				"name", cb.name)
		}
		return
	}

	// Closed state — reset the consecutive failure counter on success.
	cb.consecutiveFail = 0
}

// State returns the current [State] of the breaker. If the breaker is open and
// the reset timeout has elapsed, the returned state is [StateHalfOpen] (the
// actual transition happens on the next [Execute] call).
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateOpen && time.Since(cb.lastFailure) >= cb.resetTimeout {
		return StateHalfOpen
	}
	return cb.state
}

// Reset manually forces the breaker back to [StateClosed], clearing all failure
// counters.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.consecutiveFail = 0
	cb.halfOpenCalls = 0
	cb.halfOpenFails = 0
	slog.Info("circuit breaker manually reset", "name", cb.name)
}

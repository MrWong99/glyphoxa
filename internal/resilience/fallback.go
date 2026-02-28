package resilience

import (
	"errors"
	"fmt"
	"log/slog"
)

// ErrAllFailed is returned when every entry in a [FallbackGroup] fails or has an
// open circuit breaker.
var ErrAllFailed = errors.New("all providers failed")

// FallbackConfig configures the per-entry circuit breaker created for each
// provider in a [FallbackGroup].
type FallbackConfig struct {
	CircuitBreaker CircuitBreakerConfig
}

// fallbackEntry pairs a provider value with its dedicated circuit breaker.
type fallbackEntry[T any] struct {
	name    string
	value   T
	breaker *CircuitBreaker
}

// FallbackGroup wraps a primary and zero or more fallback instances of the same
// provider type. When the primary fails (or its circuit breaker is open), the
// next healthy fallback is tried in registration order.
//
// FallbackGroup is safe for concurrent use.
type FallbackGroup[T any] struct {
	entries []fallbackEntry[T]
	cfg     FallbackConfig
}

// NewFallbackGroup creates a [FallbackGroup] with primary as the first entry.
// Additional fallbacks are registered via [FallbackGroup.AddFallback].
func NewFallbackGroup[T any](primary T, primaryName string, cfg FallbackConfig) *FallbackGroup[T] {
	cbCfg := cfg.CircuitBreaker
	cbCfg.Name = primaryName
	return &FallbackGroup[T]{
		entries: []fallbackEntry[T]{
			{
				name:    primaryName,
				value:   primary,
				breaker: NewCircuitBreaker(cbCfg),
			},
		},
		cfg: cfg,
	}
}

// AddFallback appends a fallback provider. Fallbacks are tried in the order they
// are added, after the primary.
func (fg *FallbackGroup[T]) AddFallback(name string, fallback T) {
	cbCfg := fg.cfg.CircuitBreaker
	cbCfg.Name = name
	fg.entries = append(fg.entries, fallbackEntry[T]{
		name:    name,
		value:   fallback,
		breaker: NewCircuitBreaker(cbCfg),
	})
}

// Execute tries fn against each entry in order until one succeeds.
// Circuit-breaker-open entries are skipped. Returns [ErrAllFailed] wrapped with
// the last error if every entry fails.
func (fg *FallbackGroup[T]) Execute(fn func(T) error) error {
	var lastErr error
	for i := range fg.entries {
		entry := &fg.entries[i]
		err := entry.breaker.Execute(func() error {
			return fn(entry.value)
		})
		if err == nil {
			return nil
		}
		lastErr = err
		if errors.Is(err, ErrCircuitOpen) {
			slog.Debug("skipping provider (circuit open)", "provider", entry.name)
		} else {
			slog.Warn("provider failed, trying next",
				"provider", entry.name, "error", err)
		}
	}
	return fmt.Errorf("%w: %v", ErrAllFailed, lastErr)
}

// ExecuteWithResult tries fn against each entry in the group until one succeeds,
// returning both the result value and error. This is a package-level function
// because Go does not support method-level type parameters.
func ExecuteWithResult[T any, R any](fg *FallbackGroup[T], fn func(T) (R, error)) (R, error) {
	var (
		lastErr error
		zero    R
	)
	for i := range fg.entries {
		entry := &fg.entries[i]
		var result R
		err := entry.breaker.Execute(func() error {
			var innerErr error
			result, innerErr = fn(entry.value)
			return innerErr
		})
		if err == nil {
			return result, nil
		}
		lastErr = err
		if errors.Is(err, ErrCircuitOpen) {
			slog.Debug("skipping provider (circuit open)", "provider", entry.name)
		} else {
			slog.Warn("provider failed, trying next",
				"provider", entry.name, "error", err)
		}
	}
	return zero, fmt.Errorf("%w: %v", ErrAllFailed, lastErr)
}

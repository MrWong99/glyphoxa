package resilience

import (
	"errors"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func TestNewCircuitBreaker_Defaults(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test"})
	if cb.maxFailures != 5 {
		t.Errorf("maxFailures = %d, want 5", cb.maxFailures)
	}
	if cb.resetTimeout != 30*time.Second {
		t.Errorf("resetTimeout = %v, want 30s", cb.resetTimeout)
	}
	if cb.halfOpenMax != 3 {
		t.Errorf("halfOpenMax = %d, want 3", cb.halfOpenMax)
	}
	if cb.State() != StateClosed {
		t.Errorf("initial state = %v, want closed", cb.State())
	}
}

func TestCircuitBreaker_ClosedAllowsCalls(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "test", MaxFailures: 3})
	called := false
	err := cb.Execute(func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("fn was not called")
	}
}

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  3,
		ResetTimeout: time.Hour, // long timeout so it stays open
	})

	// 3 consecutive failures should open the breaker.
	for i := 0; i < 3; i++ {
		_ = cb.Execute(func() error { return errTest })
	}

	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want open after %d failures", cb.State(), 3)
	}

	// Next call should be rejected.
	err := cb.Execute(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:        "test",
		MaxFailures: 3,
	})

	// 2 failures, then a success — should not open.
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return nil })

	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want closed (success should reset counter)", cb.State())
	}

	// Need 3 more consecutive failures to open now.
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })
	if cb.State() != StateClosed {
		t.Fatal("should still be closed after 2 failures post-reset")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  2,
		ResetTimeout: 10 * time.Millisecond,
		HalfOpenMax:  2,
	})

	// Open the breaker.
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })
	if cb.State() != StateOpen {
		t.Fatal("expected open")
	}

	// Wait for reset timeout.
	time.Sleep(15 * time.Millisecond)

	// State() should now report half-open.
	if cb.State() != StateHalfOpen {
		t.Fatalf("state = %v, want half-open after timeout", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  2,
		ResetTimeout: 10 * time.Millisecond,
		HalfOpenMax:  2,
	})

	// Open the breaker.
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	// Wait for reset timeout.
	time.Sleep(15 * time.Millisecond)

	// Successful probe calls should close the breaker.
	for i := 0; i < 2; i++ {
		err := cb.Execute(func() error { return nil })
		if err != nil {
			t.Fatalf("probe %d: unexpected error: %v", i, err)
		}
	}

	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want closed after successful probes", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  2,
		ResetTimeout: 10 * time.Millisecond,
		HalfOpenMax:  3,
	})

	// Open the breaker.
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })

	// Wait for reset timeout.
	time.Sleep(15 * time.Millisecond)

	// A failure in half-open should re-open.
	err := cb.Execute(func() error { return errTest })
	if err == nil {
		t.Fatal("expected error from failing probe")
	}

	// Should be open again (not half-open since lastFailure was just set).
	cb.mu.Lock()
	s := cb.state
	cb.mu.Unlock()
	if s != StateOpen {
		t.Fatalf("state = %v, want open after half-open failure", s)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  2,
		ResetTimeout: time.Hour,
	})

	// Open the breaker.
	_ = cb.Execute(func() error { return errTest })
	_ = cb.Execute(func() error { return errTest })
	if cb.State() != StateOpen {
		t.Fatal("expected open")
	}

	// Manual reset.
	cb.Reset()
	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want closed after reset", cb.State())
	}

	// Should work normally again.
	err := cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
}

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

package resilience

import (
	"errors"
	"testing"
	"time"
)

func TestFallbackGroup_PrimarySuccess(t *testing.T) {
	fg := NewFallbackGroup("primary", "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fg.AddFallback("secondary", "secondary")

	var called string
	err := fg.Execute(func(v string) error {
		called = v
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != "primary" {
		t.Fatalf("called = %q, want primary", called)
	}
}

func TestFallbackGroup_PrimaryFailFallbackSuccess(t *testing.T) {
	fg := NewFallbackGroup("primary", "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fg.AddFallback("secondary", "secondary")

	var called string
	err := fg.Execute(func(v string) error {
		if v == "primary" {
			return errTest
		}
		called = v
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != "secondary" {
		t.Fatalf("called = %q, want secondary", called)
	}
}

func TestFallbackGroup_AllFail(t *testing.T) {
	fg := NewFallbackGroup("primary", "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fg.AddFallback("secondary", "secondary")

	err := fg.Execute(func(v string) error {
		return errTest
	})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
}

func TestFallbackGroup_CircuitBreakerSkipsOpenProvider(t *testing.T) {
	fg := NewFallbackGroup("primary", "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{
			MaxFailures:  2,
			ResetTimeout: time.Hour,
		},
	})
	fg.AddFallback("secondary", "secondary")

	// Fail the primary enough to open its breaker.
	for i := 0; i < 2; i++ {
		_ = fg.Execute(func(v string) error {
			if v == "primary" {
				return errTest
			}
			return nil
		})
	}

	// Now the primary's breaker should be open — calls should go to secondary.
	var called string
	err := fg.Execute(func(v string) error {
		called = v
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != "secondary" {
		t.Fatalf("called = %q, want secondary (primary circuit should be open)", called)
	}
}

func TestExecuteWithResult_Success(t *testing.T) {
	fg := NewFallbackGroup(10, "ten", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fg.AddFallback("twenty", 20)

	result, err := ExecuteWithResult(fg, func(v int) (string, error) {
		if v == 10 {
			return "from-ten", nil
		}
		return "from-twenty", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "from-ten" {
		t.Fatalf("result = %q, want from-ten", result)
	}
}

func TestExecuteWithResult_Failover(t *testing.T) {
	fg := NewFallbackGroup(10, "ten", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fg.AddFallback("twenty", 20)

	result, err := ExecuteWithResult(fg, func(v int) (string, error) {
		if v == 10 {
			return "", errTest
		}
		return "from-twenty", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "from-twenty" {
		t.Fatalf("result = %q, want from-twenty", result)
	}
}

func TestExecuteWithResult_AllFail(t *testing.T) {
	fg := NewFallbackGroup(10, "ten", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})

	_, err := ExecuteWithResult(fg, func(v int) (string, error) {
		return "", errTest
	})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
}

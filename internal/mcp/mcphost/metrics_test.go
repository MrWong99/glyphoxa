package mcphost

import (
	"testing"
)

// TestRollingWindowBasic verifies that a new window starts empty.
func TestRollingWindowBasic(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(10)
	if got := w.Count(); got != 0 {
		t.Errorf("Count() = %d, want 0", got)
	}
	if got := w.P50(); got != 0 {
		t.Errorf("P50() = %d, want 0", got)
	}
	if got := w.P99(); got != 0 {
		t.Errorf("P99() = %d, want 0", got)
	}
	if got := w.ErrorRate(); got != 0 {
		t.Errorf("ErrorRate() = %f, want 0", got)
	}
}

// TestRollingWindowDefaultSize verifies that size ≤ 0 defaults to 100.
func TestRollingWindowDefaultSize(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(0)
	if w.size != 100 {
		t.Errorf("size = %d, want 100", w.size)
	}
}

// TestRollingWindowP50 verifies the median calculation.
func TestRollingWindowP50(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(10)
	// Record odd-length sequence so median is well-defined.
	for _, ms := range []int64{10, 20, 30, 40, 50} {
		w.Record(ms, false)
	}
	// Sorted: [10,20,30,40,50] → index 2 → 30.
	if got := w.P50(); got != 30 {
		t.Errorf("P50() = %d, want 30", got)
	}
}

// TestRollingWindowP99 verifies the 99th-percentile calculation.
func TestRollingWindowP99(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(100)
	for i := int64(1); i <= 100; i++ {
		w.Record(i, false)
	}
	// Sorted: [1..100], idx = int(99 * 0.99) = int(98.01) = 98, value = 99.
	got := w.P99()
	if got < 98 || got > 100 {
		t.Errorf("P99() = %d, want in [98,100]", got)
	}
}

// TestRollingWindowErrorRate verifies error-rate tracking.
func TestRollingWindowErrorRate(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(10)
	w.Record(100, false)
	w.Record(100, false)
	w.Record(100, true) // 1 error out of 3 total → ~0.33
	got := w.ErrorRate()
	if got < 0.3 || got > 0.4 {
		t.Errorf("ErrorRate() = %f, want ~0.333", got)
	}
}

// TestRollingWindowCount verifies the total invocation count.
func TestRollingWindowCount(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(5)
	for i := 0; i < 7; i++ {
		w.Record(int64(i*10), false)
	}
	if got := w.Count(); got != 7 {
		t.Errorf("Count() = %d, want 7", got)
	}
}

// TestRollingWindowRing verifies that the ring buffer wraps correctly.
func TestRollingWindowRing(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(3)
	w.Record(100, false)
	w.Record(200, false)
	w.Record(300, false)
	// Window full: [100,200,300] → P50 = 200.
	if got := w.P50(); got != 200 {
		t.Errorf("P50() after fill = %d, want 200", got)
	}
	// Overwrite oldest with 400 → [200,300,400] → P50 = 300.
	w.Record(400, false)
	if got := w.P50(); got != 300 {
		t.Errorf("P50() after overwrite = %d, want 300", got)
	}
}

// TestRollingWindowSingleSample verifies P50 and P99 with one value.
func TestRollingWindowSingleSample(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(10)
	w.Record(42, false)
	if got := w.P50(); got != 42 {
		t.Errorf("P50() = %d, want 42", got)
	}
	if got := w.P99(); got != 42 {
		t.Errorf("P99() = %d, want 42", got)
	}
}

// TestRollingWindowConcurrent ensures no data races under concurrent access.
func TestRollingWindowConcurrent(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(50)
	done := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func(v int64) {
			for j := 0; j < 20; j++ {
				w.Record(v, j%3 == 0)
			}
			done <- struct{}{}
		}(int64(i * 10))
	}
	for i := 0; i < 5; i++ {
		<-done
	}
	// Just ensure no panic and count is sane.
	if c := w.Count(); c != 100 {
		t.Errorf("Count() = %d, want 100", c)
	}
}

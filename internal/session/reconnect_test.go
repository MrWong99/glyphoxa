package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
	audiomock "github.com/MrWong99/glyphoxa/pkg/audio/mock"
)

func TestReconnector_Connect(t *testing.T) {
	t.Run("successful initial connection", func(t *testing.T) {
		conn := &audiomock.Connection{}
		platform := &audiomock.Platform{
			ConnectResult: conn,
		}

		r := NewReconnector(ReconnectorConfig{
			Platform:  platform,
			ChannelID: "channel-1",
		})

		got, err := r.Connect(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != conn {
			t.Error("expected returned connection to match mock")
		}
		if r.Connection() != conn {
			t.Error("expected stored connection to match mock")
		}

		if len(platform.ConnectCalls) != 1 {
			t.Errorf("expected 1 connect call, got %d", len(platform.ConnectCalls))
		}
		if platform.ConnectCalls[0].ChannelID != "channel-1" {
			t.Errorf("expected channel-1, got %s", platform.ConnectCalls[0].ChannelID)
		}
	})

	t.Run("connection failure", func(t *testing.T) {
		platform := &audiomock.Platform{
			ConnectError: errors.New("auth failed"),
		}

		r := NewReconnector(ReconnectorConfig{
			Platform:  platform,
			ChannelID: "channel-1",
		})

		_, err := r.Connect(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if r.Connection() != nil {
			t.Error("expected nil connection after failure")
		}
	})
}

func TestReconnector_Defaults(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{
		Platform:  &audiomock.Platform{},
		ChannelID: "ch",
	})

	if r.maxRetries != 10 {
		t.Errorf("expected default maxRetries=10, got %d", r.maxRetries)
	}
	if r.backoff != 1*time.Second {
		t.Errorf("expected default backoff=1s, got %v", r.backoff)
	}
	if r.maxBackoff != 30*time.Second {
		t.Errorf("expected default maxBackoff=30s, got %v", r.maxBackoff)
	}
}

func TestReconnector_ReconnectOnDisconnect(t *testing.T) {
	conn1 := &audiomock.Connection{}
	conn2 := &audiomock.Connection{}

	var reconnected atomic.Pointer[audio.Connection]

	// Custom connect logic: first call = conn1, second = conn2.
	customPlatform := &connectCountPlatform{
		connections: []audio.Connection{conn1, conn2},
	}

	r := NewReconnector(ReconnectorConfig{
		Platform:   customPlatform,
		ChannelID:  "channel-1",
		MaxRetries: 3,
		Backoff:    1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		OnReconnect: func(c audio.Connection) {
			reconnected.Store(&c)
		},
	})

	// Initial connect.
	_, err := r.Connect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := t.Context()

	r.Monitor(ctx)

	// Simulate disconnect.
	r.NotifyDisconnect()

	// Wait for reconnection.
	time.Sleep(50 * time.Millisecond)

	gotPtr := reconnected.Load()
	if gotPtr == nil {
		t.Fatal("expected OnReconnect to be called")
	}
	if *gotPtr != conn2 {
		t.Error("expected OnReconnect to be called with conn2")
	}

	_ = r.Stop()
}

func TestReconnector_ExponentialBackoff(t *testing.T) {
	var failCount atomic.Int32

	platform := &failNTimesPlatform{
		failTimes: 3,
		conn:      &audiomock.Connection{},
		count:     &failCount,
	}

	var reconnected atomic.Bool

	r := NewReconnector(ReconnectorConfig{
		Platform:   platform,
		ChannelID:  "channel-1",
		MaxRetries: 5,
		Backoff:    1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		OnReconnect: func(c audio.Connection) {
			reconnected.Store(true)
		},
	})

	// Set initial connection directly.
	r.mu.Lock()
	r.conn = &audiomock.Connection{}
	r.mu.Unlock()

	ctx := t.Context()

	r.Monitor(ctx)
	r.NotifyDisconnect()

	// Wait for retries to complete.
	time.Sleep(200 * time.Millisecond)

	if !reconnected.Load() {
		t.Error("expected successful reconnection after failures")
	}

	attempts := failCount.Load()
	// Should have had 3 failures + 1 success = 4 total attempts.
	if attempts < 4 {
		t.Errorf("expected at least 4 connection attempts, got %d", attempts)
	}

	_ = r.Stop()
}

func TestReconnector_MaxRetriesExhausted(t *testing.T) {
	var connectAttempts atomic.Int32
	platform := &countingFailPlatform{
		err:   errors.New("permanently down"),
		count: &connectAttempts,
	}

	var reconnected atomic.Bool
	r := NewReconnector(ReconnectorConfig{
		Platform:   platform,
		ChannelID:  "channel-1",
		MaxRetries: 2,
		Backoff:    1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		OnReconnect: func(c audio.Connection) {
			reconnected.Store(true)
		},
	})

	r.mu.Lock()
	r.conn = &audiomock.Connection{}
	r.mu.Unlock()

	ctx := t.Context()

	r.Monitor(ctx)
	r.NotifyDisconnect()

	// Wait for retries to exhaust.
	time.Sleep(100 * time.Millisecond)

	if reconnected.Load() {
		t.Error("expected OnReconnect NOT to be called when all retries fail")
	}

	// Platform should have been called maxRetries times.
	if got := connectAttempts.Load(); got != 2 {
		t.Errorf("expected 2 connect attempts, got %d", got)
	}

	_ = r.Stop()
}

func TestReconnector_Stop(t *testing.T) {
	conn := &audiomock.Connection{}
	platform := &audiomock.Platform{ConnectResult: conn}

	r := NewReconnector(ReconnectorConfig{
		Platform:  platform,
		ChannelID: "channel-1",
	})

	_, _ = r.Connect(context.Background())

	err := r.Stop()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.Connection() != nil {
		t.Error("expected nil connection after Stop")
	}

	if conn.CallCountDisconnect != 1 {
		t.Errorf("expected 1 Disconnect call, got %d", conn.CallCountDisconnect)
	}

	// Double stop should not panic.
	err = r.Stop()
	if err != nil {
		t.Fatalf("unexpected error on double Stop: %v", err)
	}
}

func TestReconnector_NotifyDisconnectNonBlocking(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{
		Platform:  &audiomock.Platform{},
		ChannelID: "ch",
	})

	// Multiple calls should not block.
	r.NotifyDisconnect()
	r.NotifyDisconnect()
	r.NotifyDisconnect()
}

// connectCountPlatform returns connections from a list, cycling through them.
type connectCountPlatform struct {
	mu          sync.Mutex
	connections []audio.Connection
	callCount   int
}

func (p *connectCountPlatform) Connect(_ context.Context, _ string) (audio.Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.callCount
	p.callCount++
	if idx < len(p.connections) {
		return p.connections[idx], nil
	}
	return p.connections[len(p.connections)-1], nil
}

// failNTimesPlatform fails the first N Connect calls, then succeeds.
type failNTimesPlatform struct {
	failTimes int
	conn      audio.Connection
	count     *atomic.Int32
}

func (p *failNTimesPlatform) Connect(_ context.Context, _ string) (audio.Connection, error) {
	n := p.count.Add(1)
	if int(n) <= p.failTimes {
		return nil, errors.New("connection failed")
	}
	return p.conn, nil
}

// countingFailPlatform always fails but counts attempts atomically.
type countingFailPlatform struct {
	err   error
	count *atomic.Int32
}

func (p *countingFailPlatform) Connect(_ context.Context, _ string) (audio.Connection, error) {
	p.count.Add(1)
	return nil, p.err
}

package session

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// Default reconnection parameters.
const (
	defaultMaxRetries = 10
	defaultBackoff    = 1 * time.Second
	defaultMaxBackoff = 30 * time.Second
)

// Reconnector monitors an audio connection and automatically reconnects
// on disconnection, preserving NPC state.
//
// Callers obtain the initial connection via [Reconnector.Connect], then call
// [Reconnector.Monitor] to start a background goroutine that watches for
// disconnections. When a drop is detected (via [Reconnector.NotifyDisconnect]),
// the monitor attempts reconnection with exponential backoff and invokes the
// configured OnReconnect callback on success.
//
// All methods are safe for concurrent use.
type Reconnector struct {
	platform    audio.Platform
	channelID   string
	maxRetries  int
	backoff     time.Duration
	maxBackoff  time.Duration
	onReconnect func(audio.Connection)

	mu           sync.Mutex
	conn         audio.Connection
	done         chan struct{}
	stopOnce     sync.Once
	disconnected chan struct{} // signalled when a disconnect is detected
}

// ReconnectorConfig configures a [Reconnector].
type ReconnectorConfig struct {
	// Platform is the audio platform used to establish connections.
	Platform audio.Platform

	// ChannelID is the voice channel to connect to.
	ChannelID string

	// MaxRetries is the maximum number of reconnection attempts before giving up.
	// Defaults to 10 if zero.
	MaxRetries int

	// Backoff is the initial backoff duration between retries. Doubles each
	// attempt up to MaxBackoff. Defaults to 1s if zero.
	Backoff time.Duration

	// MaxBackoff is the upper limit on backoff duration. Defaults to 30s if zero.
	MaxBackoff time.Duration

	// OnReconnect is called after a successful reconnection with the new
	// connection. May be nil.
	OnReconnect func(audio.Connection)
}

// NewReconnector creates a new [Reconnector] with the given configuration.
func NewReconnector(cfg ReconnectorConfig) *Reconnector {
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	backoff := cfg.Backoff
	if backoff <= 0 {
		backoff = defaultBackoff
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	return &Reconnector{
		platform:     cfg.Platform,
		channelID:    cfg.ChannelID,
		maxRetries:   maxRetries,
		backoff:      backoff,
		maxBackoff:   maxBackoff,
		onReconnect:  cfg.OnReconnect,
		done:         make(chan struct{}),
		disconnected: make(chan struct{}, 1),
	}
}

// Connect performs the initial connection to the voice channel.
func (r *Reconnector) Connect(ctx context.Context) (audio.Connection, error) {
	conn, err := r.platform.Connect(ctx, r.channelID)
	if err != nil {
		return nil, fmt.Errorf("reconnector initial connect: %w", err)
	}

	r.mu.Lock()
	r.conn = conn
	r.mu.Unlock()

	return conn, nil
}

// Monitor starts monitoring the connection in a background goroutine.
// If a disconnection is signalled via [Reconnector.NotifyDisconnect], it
// attempts reconnection with exponential backoff.
func (r *Reconnector) Monitor(ctx context.Context) {
	go r.monitorLoop(ctx)
}

// NotifyDisconnect signals the monitor that the connection has been lost
// and reconnection should be attempted. Safe to call multiple times; only
// the first call per reconnection cycle has effect.
func (r *Reconnector) NotifyDisconnect() {
	select {
	case r.disconnected <- struct{}{}:
	default:
		// Already signalled; avoid blocking.
	}
}

// Stop halts monitoring and disconnects the current connection.
// Safe to call multiple times.
func (r *Reconnector) Stop() error {
	r.stopOnce.Do(func() {
		close(r.done)
	})

	r.mu.Lock()
	conn := r.conn
	r.conn = nil
	r.mu.Unlock()

	if conn != nil {
		return conn.Disconnect()
	}
	return nil
}

// Connection returns the current active connection. May return nil during
// reconnection.
func (r *Reconnector) Connection() audio.Connection {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conn
}

// monitorLoop waits for disconnect notifications and attempts reconnection.
func (r *Reconnector) monitorLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case <-r.disconnected:
			r.attemptReconnect(ctx)
		}
	}
}

// attemptReconnect tries to reconnect with exponential backoff.
func (r *Reconnector) attemptReconnect(ctx context.Context) {
	currentBackoff := r.backoff

	for attempt := 1; attempt <= r.maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		default:
		}

		slog.Info("attempting reconnection",
			"channel_id", r.channelID,
			"attempt", attempt,
			"max_retries", r.maxRetries,
			"backoff", currentBackoff,
		)

		conn, err := r.platform.Connect(ctx, r.channelID)
		if err == nil {
			r.mu.Lock()
			oldConn := r.conn
			r.conn = conn
			r.mu.Unlock()

			// Disconnect the old (failed) connection to release its resources.
			if oldConn != nil {
				_ = oldConn.Disconnect()
			}

			slog.Info("reconnection successful",
				"channel_id", r.channelID,
				"attempt", attempt,
			)

			if r.onReconnect != nil {
				r.onReconnect(conn)
			}
			return
		}

		slog.Warn("reconnection attempt failed",
			"channel_id", r.channelID,
			"attempt", attempt,
			"error", err,
		)

		// Wait before retrying.
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case <-time.After(currentBackoff):
		}

		// Exponential backoff.
		currentBackoff *= 2
		if currentBackoff > r.maxBackoff {
			currentBackoff = r.maxBackoff
		}
	}

	slog.Error("reconnection failed after max retries",
		"channel_id", r.channelID,
		"max_retries", r.maxRetries,
	)
}

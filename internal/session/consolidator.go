package session

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
)

// defaultConsolidationInterval is the default period between consolidation
// ticks.
const defaultConsolidationInterval = 30 * time.Minute

// Consolidator periodically flushes hot conversation context to the memory
// store. This ensures that long-running sessions (4+ hours) persist their
// conversation history even if the process crashes or the context window
// is pruned.
//
// All methods are safe for concurrent use.
type Consolidator struct {
	store      memory.SessionStore
	contextMgr *ContextManager
	interval   time.Duration
	sessionID  string

	mu sync.Mutex
	// lastIndex tracks how many messages have already been consolidated
	// to avoid writing duplicates.
	lastIndex int
	done      chan struct{}
	stopOnce  sync.Once
}

// ConsolidatorConfig configures a [Consolidator].
type ConsolidatorConfig struct {
	// Store is the L1 session store to write entries to.
	Store memory.SessionStore

	// ContextMgr is the context manager whose messages are consolidated.
	ContextMgr *ContextManager

	// SessionID identifies the game session.
	SessionID string

	// Interval is how often to consolidate. Defaults to 30 minutes if zero.
	Interval time.Duration
}

// NewConsolidator creates a new [Consolidator] with the given configuration.
func NewConsolidator(cfg ConsolidatorConfig) *Consolidator {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultConsolidationInterval
	}
	return &Consolidator{
		store:      cfg.Store,
		contextMgr: cfg.ContextMgr,
		interval:   interval,
		sessionID:  cfg.SessionID,
		done:       make(chan struct{}),
	}
}

// Start begins periodic consolidation in a background goroutine.
// The goroutine runs until [Consolidator.Stop] is called or ctx is cancelled.
func (c *Consolidator) Start(ctx context.Context) {
	go c.loop(ctx)
}

// Stop halts the consolidation loop. Safe to call multiple times.
func (c *Consolidator) Stop() {
	c.stopOnce.Do(func() {
		close(c.done)
	})
}

// ConsolidateNow performs an immediate consolidation, writing any new
// messages from the context manager to the session store.
func (c *Consolidator) ConsolidateNow(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.consolidate(ctx)
}

// loop runs the periodic consolidation ticker.
func (c *Consolidator) loop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
			c.mu.Lock()
			if err := c.consolidate(ctx); err != nil {
				slog.Warn("periodic consolidation failed",
					"session_id", c.sessionID,
					"error", err,
				)
			}
			c.mu.Unlock()
		}
	}
}

// consolidate writes new messages to the session store. Must be called with
// c.mu held.
func (c *Consolidator) consolidate(ctx context.Context) error {
	msgs := c.contextMgr.Messages()

	// Skip summary messages (prefixed with "[Previous conversation summary]")
	// and only write actual conversation messages. We track by index into the
	// full message list to avoid duplicates.
	if c.lastIndex >= len(msgs) {
		return nil // nothing new
	}

	var writeErr error
	for i := c.lastIndex; i < len(msgs); i++ {
		m := msgs[i]
		// Skip synthetic summary messages.
		if len(m.Content) > 0 && m.Content[0] == '[' {
			continue
		}

		entry := memory.TranscriptEntry{
			SpeakerID:   m.Name,
			SpeakerName: m.Name,
			Text:        m.Content,
			Timestamp:   time.Now(),
		}

		// Assign NPCID for assistant messages.
		if m.Role == "assistant" {
			entry.NPCID = m.Name
		}

		if err := c.store.WriteEntry(ctx, c.sessionID, entry); err != nil {
			writeErr = fmt.Errorf("consolidate entry %d: %w", i, err)
			slog.Warn("failed to write consolidation entry",
				"session_id", c.sessionID,
				"index", i,
				"error", err,
			)
			// Continue writing remaining entries — partial consolidation is
			// better than none.
		}
	}

	c.lastIndex = len(msgs)
	return writeErr
}

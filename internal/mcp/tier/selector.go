// Package tier provides a lightweight heuristic-based budget tier selector for
// MCP tool budgets in Glyphoxa voice sessions.
//
// The [Selector] analyses STT transcript text using keyword detection and
// conversation state to choose the appropriate [mcp.BudgetTier] for each
// player utterance. It deliberately avoids LLM calls to keep selection latency
// well below 1 ms — fast enough to run inline in the audio receive loop.
//
// Tier priority (highest first):
//
//  1. Explicit DM override (non-zero override value)
//  2. DEEP keyword match — demoted to STANDARD if within anti-spam window
//  3. STANDARD keyword match
//  4. First conversation turn → STANDARD
//  5. High player queue depth (≥ 3) → FAST
//  6. Default → FAST
package tier

import (
	"strings"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/internal/mcp"
)

// defaultMinDeepInterval is the minimum time between consecutive DEEP tier
// selections. A second DEEP selection within this window is demoted to
// STANDARD to prevent runaway expensive tool usage.
const defaultMinDeepInterval = 30 * time.Second

// defaultDeepKeywords are the keywords that trigger [mcp.BudgetDeep] tier.
// They indicate high-complexity or time-tolerant requests.
var defaultDeepKeywords = []string{
	"think carefully", "take your time", "explain everything",
	"tell me everything", "in detail", "deep search",
	"generate image", "search the web",
}

// defaultStandardKeywords are the keywords that trigger [mcp.BudgetStandard]
// tier. They indicate memory or rule lookups that need more than the fastest
// tools but don't warrant full deep access.
var defaultStandardKeywords = []string{
	"remember", "last time", "do you recall", "previously",
	"what happened", "search", "look up", "rules",
	"how does", "what are the rules", "who is", "who was",
	"tell me about", "history of", "quest",
}

// Option is a functional option for configuring a [Selector].
type Option func(*Selector)

// WithDeepKeywords replaces the default deep-tier trigger keywords with the
// provided list. Each keyword is matched case-insensitively as a substring of
// the transcript text.
func WithDeepKeywords(keywords ...string) Option {
	return func(s *Selector) {
		s.deepKeywords = append([]string(nil), keywords...)
	}
}

// WithStandardKeywords replaces the default standard-tier trigger keywords
// with the provided list. Each keyword is matched case-insensitively as a
// substring of the transcript text.
func WithStandardKeywords(keywords ...string) Option {
	return func(s *Selector) {
		s.standardKeywords = append([]string(nil), keywords...)
	}
}

// WithMinDeepInterval sets the minimum elapsed time required between two
// consecutive [mcp.BudgetDeep] selections. If a DEEP selection was made
// within this interval, the next matching request is demoted to
// [mcp.BudgetStandard].
//
// The default is 30 seconds.
func WithMinDeepInterval(d time.Duration) Option {
	return func(s *Selector) {
		s.minDeepInterval = d
	}
}

// Selector determines the appropriate [mcp.BudgetTier] for a given
// conversational context. It uses lightweight heuristics (keyword detection,
// conversation state) rather than LLM calls to keep selection fast (< 1ms).
//
// All methods are safe for concurrent use.
type Selector struct {
	// Configuration — immutable after construction.
	deepKeywords     []string      // keywords that trigger DEEP tier
	standardKeywords []string      // keywords that trigger STANDARD tier
	minDeepInterval  time.Duration // minimum time between DEEP selections

	// State — protected by mu.
	mu           sync.Mutex
	turnCount    int       // turns in current conversation
	lastDeepTime time.Time // time of last DEEP selection
	queueDepth   int       // number of players waiting (set externally)
}

// NewSelector creates a new Selector with the given options applied over the
// defaults. The selector is ready to use immediately.
func NewSelector(opts ...Option) *Selector {
	s := &Selector{
		deepKeywords:     append([]string(nil), defaultDeepKeywords...),
		standardKeywords: append([]string(nil), defaultStandardKeywords...),
		minDeepInterval:  defaultMinDeepInterval,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Select returns the appropriate [mcp.BudgetTier] for the given transcript
// text. It applies the following priority (highest first):
//
//  1. Explicit DM command override — if dmOverride is non-zero, return it directly
//     without consulting any other heuristic.
//  2. DEEP keyword match — if any deep keyword is found in text. Subject to the
//     anti-spam rule: if a DEEP selection was made less than minDeepInterval ago,
//     the result is demoted to STANDARD instead.
//  3. High queue depth — if three or more players are waiting, FAST is returned
//     to reduce response latency (overrides STANDARD keyword heuristics, but
//     not DEEP keyword matches).
//  4. STANDARD keyword match — if any standard keyword is found in text.
//  5. First turn of conversation — STANDARD is returned to allow memory lookups
//     for the initial greeting.
//  6. Default — FAST.
//
// Select is goroutine-safe and executes in sub-millisecond time (pure string
// operations, no I/O).
func (s *Selector) Select(text string, dmOverride mcp.BudgetTier) mcp.BudgetTier {
	// Priority 1: DM override wins unconditionally (non-zero means an explicit tier).
	// BudgetFast == 0 is the zero value, so dmOverride == 0 means "no override set".
	if dmOverride != 0 {
		return dmOverride
	}

	lower := strings.ToLower(text)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Priority 2: DEEP keyword match (with anti-spam guard).
	if containsAny(lower, s.deepKeywords) {
		now := time.Now()
		if !s.lastDeepTime.IsZero() && now.Sub(s.lastDeepTime) < s.minDeepInterval {
			// Anti-spam: too soon since the last DEEP — demote to STANDARD.
			return mcp.BudgetStandard
		}
		s.lastDeepTime = now
		return mcp.BudgetDeep
	}

	// Priority 3: High queue depth — reduce latency for waiting players.
	// This intentionally overrides STANDARD keyword heuristics so that crowded
	// sessions don't get stuck waiting for slower tool sets.
	if s.queueDepth >= 3 {
		return mcp.BudgetFast
	}

	// Priority 4: STANDARD keyword match.
	if containsAny(lower, s.standardKeywords) {
		return mcp.BudgetStandard
	}

	// Priority 5: First turn — allow memory lookups for the opening greeting.
	if s.turnCount == 0 {
		return mcp.BudgetStandard
	}

	// Priority 6: Default.
	return mcp.BudgetFast
}

// RecordTurn increments the conversation turn counter. Call this after each
// completed player-NPC interaction so that the "first turn" heuristic advances
// correctly.
func (s *Selector) RecordTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnCount++
}

// SetQueueDepth updates the number of players currently waiting for an NPC
// response. A depth of three or more causes [Select] to prefer [mcp.BudgetFast]
// over keyword-based STANDARD selections (but DEEP keyword matches still
// take priority).
func (s *Selector) SetQueueDepth(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueDepth = n
}

// Reset clears all per-session state (turn count, last deep time, queue depth).
// Call this when starting a new conversation session.
func (s *Selector) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnCount = 0
	s.lastDeepTime = time.Time{}
	s.queueDepth = 0
}

// containsAny reports whether lower contains any of the given keywords as a
// substring. lower must already be lowercased; keywords are compared as-is
// (callers must ensure they are lowercase if case-insensitive matching is
// required).
func containsAny(lower string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

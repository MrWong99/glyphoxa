package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// charsPerToken is the heuristic ratio used for token estimation.
// English text averages roughly 4 characters per token across common
// LLM tokenizers. This avoids pulling in a tokenizer dependency.
const charsPerToken = 4

// ContextManager tracks token usage in a conversation and triggers
// summarisation when approaching the context window limit.
//
// It maintains an ordered list of [llm.Message] values and accumulated
// summaries. When the estimated token count exceeds thresholdRatio × maxTokens,
// the oldest half of the messages is summarised and replaced by a compact
// summary message. This keeps the working set within the provider's context
// window while preserving narratively important information.
//
// All methods are safe for concurrent use.
type ContextManager struct {
	maxTokens      int
	thresholdRatio float64
	summariser     Summariser

	mu            sync.Mutex
	currentTokens int
	messages      []llm.Message
	summaries     []string
}

// ContextManagerConfig configures a [ContextManager].
type ContextManagerConfig struct {
	// MaxTokens is the provider's context window size (e.g., 128000).
	MaxTokens int

	// ThresholdRatio is the fraction of MaxTokens at which summarisation is
	// triggered. Defaults to 0.75 if zero or negative.
	ThresholdRatio float64

	// Summariser is used to compress older messages when the threshold is
	// exceeded. Must not be nil.
	Summariser Summariser
}

// NewContextManager creates a new [ContextManager] with the given configuration.
// If ThresholdRatio is zero or negative, 0.75 is used.
func NewContextManager(cfg ContextManagerConfig) *ContextManager {
	ratio := cfg.ThresholdRatio
	if ratio <= 0 {
		ratio = 0.75
	}
	return &ContextManager{
		maxTokens:      cfg.MaxTokens,
		thresholdRatio: ratio,
		summariser:     cfg.Summariser,
		messages:       make([]llm.Message, 0),
		summaries:      make([]string, 0),
	}
}

// AddMessages appends messages and estimates token count.
// If the accumulated tokens exceed threshold × maxTokens, the oldest half
// of the messages is automatically summarised and replaced.
func (cm *ContextManager) AddMessages(ctx context.Context, msgs ...llm.Message) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for _, m := range msgs {
		tokens := estimateTokens(m)
		cm.messages = append(cm.messages, m)
		cm.currentTokens += tokens
	}

	threshold := int(float64(cm.maxTokens) * cm.thresholdRatio)
	if cm.currentTokens > threshold && len(cm.messages) > 1 {
		if err := cm.summariseOldest(ctx); err != nil {
			return fmt.Errorf("context manager auto-summarise: %w", err)
		}
	}

	return nil
}

// Messages returns the current conversation history, including any
// summary prefixes. The returned slice is ready to pass to
// [engine.PromptContext.Messages].
func (cm *ContextManager) Messages() []llm.Message {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	result := make([]llm.Message, 0, len(cm.summaries)+len(cm.messages))

	// Prepend accumulated summaries as system context.
	for _, s := range cm.summaries {
		result = append(result, llm.Message{
			Role:    "system",
			Content: fmt.Sprintf("[Previous conversation summary]: %s", s),
		})
	}

	result = append(result, cm.messages...)
	return result
}

// TokenEstimate returns the current estimated token count, including
// summary tokens.
func (cm *ContextManager) TokenEstimate() int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.currentTokens
}

// Reset clears all messages and summaries.
func (cm *ContextManager) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.messages = cm.messages[:0]
	cm.summaries = cm.summaries[:0]
	cm.currentTokens = 0
}

// summariseOldest compresses the oldest half of messages into a summary.
// Must be called with cm.mu held.
func (cm *ContextManager) summariseOldest(ctx context.Context) error {
	half := len(cm.messages) / 2
	if half == 0 {
		half = 1
	}

	toSummarise := make([]llm.Message, half)
	copy(toSummarise, cm.messages[:half])

	// Temporarily release the lock for the (potentially slow) LLM call.
	cm.mu.Unlock()
	summary, err := cm.summariser.Summarise(ctx, toSummarise)
	cm.mu.Lock()
	if err != nil {
		return err
	}

	// Recalculate tokens for the removed messages.
	removedTokens := 0
	for _, m := range cm.messages[:half] {
		removedTokens += estimateTokens(m)
	}

	// Remove summarised messages from the front.
	cm.messages = cm.messages[half:]
	cm.currentTokens -= removedTokens

	// Add summary tokens.
	summaryTokens := len(summary) / charsPerToken
	cm.summaries = append(cm.summaries, summary)
	cm.currentTokens += summaryTokens

	return nil
}

// estimateTokens returns a rough token count for a single message using
// the 1-token-per-4-characters heuristic.
func estimateTokens(m llm.Message) int {
	chars := len(m.Content) + len(m.Role) + len(m.Name)
	for _, tc := range m.ToolCalls {
		chars += len(tc.Name) + len(tc.Arguments) + len(tc.ID)
	}
	tokens := chars / charsPerToken
	if tokens == 0 && chars > 0 {
		tokens = 1
	}
	return tokens
}

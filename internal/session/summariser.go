// Package session provides session lifecycle management for Glyphoxa NPC agents.
//
// It includes context window management ([ContextManager]), conversation
// summarisation ([Summariser], [LLMSummariser]), periodic memory consolidation
// ([Consolidator]), audio reconnection ([Reconnector]), and graceful memory
// degradation ([MemoryGuard]).
//
// All exported types are safe for concurrent use.
package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// summarisationPrompt is the system prompt sent to the LLM when summarising
// conversation segments.
const summarisationPrompt = `Summarise the following conversation between NPC(s) and players in a tabletop RPG session. 
Preserve: key decisions, revealed information, emotional states, promises made, and any 
game-mechanical outcomes (dice rolls, damage, item exchanges). 
Be concise but preserve all narratively important details.`

// Summariser produces a concise summary of a conversation segment.
type Summariser interface {
	// Summarise takes a slice of messages and returns a condensed summary string.
	Summarise(ctx context.Context, messages []llm.Message) (string, error)
}

// LLMSummariser uses an LLM provider to summarise conversations.
type LLMSummariser struct {
	llm llm.Provider
}

// NewLLMSummariser creates a new [LLMSummariser] backed by the given provider.
func NewLLMSummariser(provider llm.Provider) *LLMSummariser {
	return &LLMSummariser{llm: provider}
}

// Summarise sends messages to the LLM with a summarisation prompt and returns
// the summary text. It formats the conversation history into a single user
// message and asks the model to produce a concise summary.
func (s *LLMSummariser) Summarise(ctx context.Context, messages []llm.Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	// Format messages into a readable transcript for the summariser.
	var sb strings.Builder
	for _, m := range messages {
		speaker := m.Role
		if m.Name != "" {
			speaker = m.Name
		}
		fmt.Fprintf(&sb, "[%s]: %s\n", speaker, m.Content)
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: summarisationPrompt,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: sb.String(),
			},
		},
		Temperature: 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("summarise: %w", err)
	}

	return resp.Content, nil
}

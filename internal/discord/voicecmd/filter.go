// Package voicecmd implements keyword detection on STT finals for DM-only
// voice shortcuts. It checks final transcripts against a set of regex patterns
// and executes the corresponding orchestrator actions when a match is found.
//
// Voice commands are only processed for the DM's audio stream (identified by
// their platform user ID) and are intercepted before the utterance reaches
// the NPC routing pipeline.
package voicecmd

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/MrWong99/glyphoxa/internal/agent/orchestrator"
)

// Pattern pairs a compiled regex with the action to execute when it matches.
type Pattern struct {
	// Regex is the compiled pattern. Named groups or positional groups are
	// passed to Action as matches[1], matches[2], etc.
	Regex *regexp.Regexp

	// Name is a human-readable label for logging.
	Name string

	// Action executes the voice command using the matched groups.
	// matches is the full submatch slice from Regex.FindStringSubmatch.
	Action func(ctx context.Context, orch *orchestrator.Orchestrator, matches []string) (string, error)
}

// Filter checks STT finals against a set of patterns and executes matching
// voice commands via the orchestrator.
//
// All methods are safe for concurrent use — Filter is stateless (the
// orchestrator handles its own locking).
type Filter struct {
	patterns []Pattern
	dmUserID string
}

// New creates a Filter that only processes transcripts from dmUserID.
// If dmUserID is empty, the filter matches no one.
func New(dmUserID string) *Filter {
	return &Filter{
		patterns: defaultPatterns(),
		dmUserID: dmUserID,
	}
}

// Check tests whether text from userID matches a voice command pattern.
// If a match is found, the corresponding action is executed on orch and
// Check returns (true, nil). If no pattern matches, it returns (false, nil).
// Errors from action execution are returned as (true, err).
//
// Only transcripts from the configured DM user are checked; all others
// return (false, nil) immediately.
func (f *Filter) Check(ctx context.Context, userID string, text string, orch *orchestrator.Orchestrator) (bool, error) {
	if f.dmUserID == "" || userID != f.dmUserID {
		return false, nil
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false, nil
	}

	for _, p := range f.patterns {
		matches := p.Regex.FindStringSubmatch(trimmed)
		if matches == nil {
			continue
		}

		result, err := p.Action(ctx, orch, matches)
		if err != nil {
			slog.Warn("voicecmd: command failed",
				"pattern", p.Name,
				"text", trimmed,
				"error", err,
			)
			return true, fmt.Errorf("voicecmd: %s: %w", p.Name, err)
		}

		slog.Info("voicecmd: command executed",
			"pattern", p.Name,
			"text", trimmed,
			"result", result,
		)
		return true, nil
	}

	return false, nil
}

// SetDMUserID updates the DM user ID. Used when a new session starts
// with a different DM.
func (f *Filter) SetDMUserID(userID string) {
	f.dmUserID = userID
}

// defaultPatterns returns the built-in set of DM voice command patterns.
func defaultPatterns() []Pattern {
	return []Pattern{
		{
			Name:  "mute-by-name",
			Regex: regexp.MustCompile(`(?i)^mute\s+(.+)$`),
			Action: func(ctx context.Context, orch *orchestrator.Orchestrator, matches []string) (string, error) {
				return muteByName(orch, matches[1])
			},
		},
		{
			Name:  "be-quiet",
			Regex: regexp.MustCompile(`(?i)^(.+?),?\s+be\s+quiet$`),
			Action: func(ctx context.Context, orch *orchestrator.Orchestrator, matches []string) (string, error) {
				return muteByName(orch, matches[1])
			},
		},
		{
			Name:  "unmute-by-name",
			Regex: regexp.MustCompile(`(?i)^unmute\s+(.+)$`),
			Action: func(ctx context.Context, orch *orchestrator.Orchestrator, matches []string) (string, error) {
				return unmuteByName(orch, matches[1])
			},
		},
		{
			Name:  "everyone-stop",
			Regex: regexp.MustCompile(`(?i)^everyone,?\s+stop$`),
			Action: func(_ context.Context, orch *orchestrator.Orchestrator, _ []string) (string, error) {
				n := orch.MuteAll()
				return fmt.Sprintf("muted %d agents", n), nil
			},
		},
		{
			Name:  "everyone-continue",
			Regex: regexp.MustCompile(`(?i)^everyone,?\s+continue$`),
			Action: func(_ context.Context, orch *orchestrator.Orchestrator, _ []string) (string, error) {
				n := orch.UnmuteAll()
				return fmt.Sprintf("unmuted %d agents", n), nil
			},
		},
		{
			Name:  "speak-as",
			Regex: regexp.MustCompile(`(?i)^(.+?),?\s+say\s+(.+)$`),
			Action: func(ctx context.Context, orch *orchestrator.Orchestrator, matches []string) (string, error) {
				return speakAs(ctx, orch, matches[1], matches[2])
			},
		},
	}
}

// muteByName mutes the NPC whose name matches (case-insensitive).
func muteByName(orch *orchestrator.Orchestrator, name string) (string, error) {
	a := orch.AgentByName(strings.TrimSpace(name))
	if a == nil {
		return "", fmt.Errorf("no NPC named %q", name)
	}
	if err := orch.MuteAgent(a.ID()); err != nil {
		return "", err
	}
	return fmt.Sprintf("muted %s", a.Name()), nil
}

// unmuteByName unmutes the NPC whose name matches (case-insensitive).
func unmuteByName(orch *orchestrator.Orchestrator, name string) (string, error) {
	a := orch.AgentByName(strings.TrimSpace(name))
	if a == nil {
		return "", fmt.Errorf("no NPC named %q", name)
	}
	if err := orch.UnmuteAgent(a.ID()); err != nil {
		return "", err
	}
	return fmt.Sprintf("unmuted %s", a.Name()), nil
}

// speakAs tells the named NPC to speak the given text via SpeakText.
func speakAs(ctx context.Context, orch *orchestrator.Orchestrator, name string, text string) (string, error) {
	a := orch.AgentByName(strings.TrimSpace(name))
	if a == nil {
		return "", fmt.Errorf("no NPC named %q", name)
	}
	if err := a.SpeakText(ctx, text); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s speaks: %q", a.Name(), text), nil
}


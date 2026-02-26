package hotctx

import (
	"fmt"
	"strings"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// FormatSystemPrompt converts a [HotContext] into a system prompt string
// suitable for direct injection into an NPC LLM call.
//
// npcPersonality is a free-text personality description that is appended to the
// opening line. If hctx is nil, a minimal fallback prompt is returned.
//
// The formatter is pure: it performs no I/O, has no side effects, and is safe
// for concurrent use.
//
// Empty sections (nil identity, no relationships, no scene, no transcript) are
// omitted entirely rather than rendering as empty headers.
func FormatSystemPrompt(hctx *HotContext, npcPersonality string) string {
	if hctx == nil {
		name := "an NPC"
		p := strings.TrimSpace(npcPersonality)
		if p != "" {
			return fmt.Sprintf("You are %s. %s", name, p)
		}
		return fmt.Sprintf("You are %s.", name)
	}

	var sb strings.Builder

	// ── Opening line ──────────────────────────────────────────────────────────
	npcName := npcNameFromContext(hctx)
	personality := strings.TrimSpace(npcPersonality)
	if personality != "" {
		fmt.Fprintf(&sb, "You are %s. %s", npcName, personality)
	} else {
		fmt.Fprintf(&sb, "You are %s.", npcName)
	}

	// ── Identity section ──────────────────────────────────────────────────────
	if hctx.Identity != nil {
		identSection := formatIdentitySection(&hctx.Identity.Entity)
		if identSection != "" {
			sb.WriteString("\n\n## Your Identity\n")
			sb.WriteString(identSection)
		}

		// Relationships sub-section
		if len(hctx.Identity.Relationships) > 0 {
			relSection := formatRelationshipsSection(hctx.Identity.Relationships, hctx.Identity.RelatedEntities)
			if relSection != "" {
				sb.WriteString("\n\n## Your Relationships\n")
				sb.WriteString(relSection)
			}
		}
	}

	// ── Scene section ─────────────────────────────────────────────────────────
	if hctx.SceneContext != nil {
		sceneSection := formatSceneSection(hctx.SceneContext)
		if sceneSection != "" {
			sb.WriteString("\n\n## Current Scene\n")
			sb.WriteString(sceneSection)
		}
	}

	// ── Recent conversation section ───────────────────────────────────────────
	if len(hctx.RecentTranscript) > 0 {
		convoSection := formatTranscriptSection(hctx.RecentTranscript)
		if convoSection != "" {
			sb.WriteString("\n\n## Recent Conversation\n")
			sb.WriteString(convoSection)
		}
	}

	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// npcNameFromContext extracts the NPC's display name from the context, falling
// back to "an NPC" when the identity is absent.
func npcNameFromContext(hctx *HotContext) string {
	if hctx.Identity != nil && hctx.Identity.Entity.Name != "" {
		return hctx.Identity.Entity.Name
	}
	return "an NPC"
}

// formatIdentitySection renders the NPC entity's core attributes as lines.
// Returns an empty string when there is nothing meaningful to render.
func formatIdentitySection(e *memory.Entity) string {
	if e == nil {
		return ""
	}
	var lines []string

	if e.Name != "" {
		lines = append(lines, fmt.Sprintf("Name: %s", e.Name))
	}
	if e.Type != "" {
		lines = append(lines, fmt.Sprintf("Type: %s", e.Type))
	}

	// Render well-known attributes first, then any extras.
	wellKnown := []string{"occupation", "appearance", "speaking_style", "personality", "alignment"}
	rendered := make(map[string]bool)
	for _, k := range wellKnown {
		if v, ok := e.Attributes[k]; ok {
			lines = append(lines, fmt.Sprintf("%s: %v", k, v))
			rendered[k] = true
		}
	}
	for k, v := range e.Attributes {
		if !rendered[k] {
			lines = append(lines, fmt.Sprintf("%s: %v", k, v))
		}
	}

	return strings.Join(lines, "\n")
}

// formatRelationshipsSection renders a human-readable list of NPC relationships
// using the provided related-entity lookup slice.
func formatRelationshipsSection(rels []memory.Relationship, relatedEntities []memory.Entity) string {
	if len(rels) == 0 {
		return ""
	}

	// Build ID → entity lookup for O(1) access.
	lookup := make(map[string]memory.Entity, len(relatedEntities))
	for _, e := range relatedEntities {
		lookup[e.ID] = e
	}

	var lines []string
	for _, r := range rels {
		// Use TargetID for outgoing relationships, SourceID for incoming.
		peerID := r.TargetID
		peer, ok := lookup[peerID]
		if !ok {
			// Peer entity not in lookup; render with just the ID.
			lines = append(lines, fmt.Sprintf("You know entity %s. Your relationship: %s", peerID, r.RelType))
			continue
		}
		peerType := peer.Type
		if peerType == "" {
			peerType = "entity"
		}

		desc := fmt.Sprintf("You know %s (a %s). Your relationship: %s", peer.Name, peerType, r.RelType)

		// Optionally include a relationship description attribute.
		if d, ok := r.Attributes["description"]; ok {
			desc += fmt.Sprintf(" — %v", d)
		}
		lines = append(lines, desc)
	}

	return strings.Join(lines, "\n")
}

// formatSceneSection renders location, present NPCs/players, and active quests.
// Returns an empty string when none of those sub-sections have content.
func formatSceneSection(sc *SceneContext) string {
	if sc == nil {
		return ""
	}

	var lines []string

	if sc.Location != nil {
		locLine := fmt.Sprintf("Location: %s", sc.Location.Name)
		if desc, ok := sc.Location.Attributes["description"]; ok {
			locLine += fmt.Sprintf(" - %v", desc)
		}
		lines = append(lines, locLine)
	}

	if len(sc.PresentNPCs) > 0 {
		var names []string
		for _, e := range sc.PresentNPCs {
			t := e.Type
			if t == "" {
				t = "entity"
			}
			names = append(names, fmt.Sprintf("%s (%s)", e.Name, t))
		}
		lines = append(lines, fmt.Sprintf("Also present: %s", strings.Join(names, ", ")))
	}

	if len(sc.ActiveQuests) > 0 {
		var questParts []string
		for _, q := range sc.ActiveQuests {
			entry := q.Name
			if status, ok := q.Attributes["status"]; ok {
				entry += fmt.Sprintf(" [%v]", status)
			}
			questParts = append(questParts, entry)
		}
		lines = append(lines, fmt.Sprintf("Active quests: %s", strings.Join(questParts, ", ")))
	}

	return strings.Join(lines, "\n")
}

// formatTranscriptSection renders the recent conversation with relative
// timestamps (e.g., "2m ago") and speaker labels.
func formatTranscriptSection(entries []types.TranscriptEntry) string {
	if len(entries) == 0 {
		return ""
	}

	now := time.Now()
	var lines []string
	for _, e := range entries {
		speaker := e.SpeakerName
		if speaker == "" {
			speaker = e.SpeakerID
		}
		if speaker == "" {
			speaker = "Unknown"
		}

		relTime := formatRelativeTime(now.Sub(e.Timestamp))
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", relTime, speaker, e.Text))
	}

	return strings.Join(lines, "\n")
}

// formatRelativeTime converts a duration to a compact human-readable label
// such as "just now", "30s ago", "2m ago", "1h ago".
func formatRelativeTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

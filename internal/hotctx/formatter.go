package hotctx

import (
	"fmt"
	"strings"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
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
		writeIdentitySection(&sb, &hctx.Identity.Entity)
		writeRelationshipsSection(&sb, hctx.Identity.Relationships, hctx.Identity.RelatedEntities)
	}

	// ── Scene section ─────────────────────────────────────────────────────────
	writeSceneSection(&sb, hctx.SceneContext)

	// ── Recent conversation section ───────────────────────────────────────────
	writeTranscriptSection(&sb, hctx.RecentTranscript)

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

// writeIdentitySection writes the NPC entity's core attributes directly to sb.
// Writes nothing when there is nothing meaningful to render.
func writeIdentitySection(sb *strings.Builder, e *memory.Entity) {
	if e == nil {
		return
	}

	hasContent := e.Name != "" || e.Type != "" || len(e.Attributes) > 0
	if !hasContent {
		return
	}

	sb.WriteString("\n\n## Your Identity\n")
	first := true
	writeLine := func(format string, args ...any) {
		if !first {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(sb, format, args...)
		first = false
	}

	if e.Name != "" {
		writeLine("Name: %s", e.Name)
	}
	if e.Type != "" {
		writeLine("Type: %s", e.Type)
	}

	// Render well-known attributes first, then any extras.
	wellKnown := []string{memory.AttrOccupation, memory.AttrAppearance, memory.AttrSpeakingStyle, memory.AttrPersonality, memory.AttrAlignment}
	rendered := make(map[string]bool)
	for _, k := range wellKnown {
		if v, ok := e.Attributes[k]; ok {
			writeLine("%s: %v", k, v)
			rendered[k] = true
		}
	}
	for k, v := range e.Attributes {
		if !rendered[k] {
			writeLine("%s: %v", k, v)
		}
	}
}

// writeRelationshipsSection writes a human-readable list of NPC relationships
// directly to sb using the provided related-entity lookup slice.
func writeRelationshipsSection(sb *strings.Builder, rels []memory.Relationship, relatedEntities []memory.Entity) {
	if len(rels) == 0 {
		return
	}

	// Build ID → entity lookup for O(1) access.
	lookup := make(map[string]memory.Entity, len(relatedEntities))
	for _, e := range relatedEntities {
		lookup[e.ID] = e
	}

	sb.WriteString("\n\n## Your Relationships\n")
	for i, r := range rels {
		if i > 0 {
			sb.WriteByte('\n')
		}
		// Use TargetID for outgoing relationships, SourceID for incoming.
		peerID := r.TargetID
		peer, ok := lookup[peerID]
		if !ok {
			// Peer entity not in lookup; render with just the ID.
			fmt.Fprintf(sb, "You know entity %s. Your relationship: %s", peerID, r.RelType)
			continue
		}
		peerType := peer.Type
		if peerType == "" {
			peerType = "entity"
		}

		fmt.Fprintf(sb, "You know %s (a %s). Your relationship: %s", peer.Name, peerType, r.RelType)

		// Optionally include a relationship description attribute.
		if d, ok := r.Attributes["description"]; ok {
			fmt.Fprintf(sb, " — %v", d)
		}
	}
}

// writeSceneSection writes location, present entities, and active quests
// directly to sb. Writes nothing when none of those sub-sections have content.
func writeSceneSection(sb *strings.Builder, sc *SceneContext) {
	if sc == nil {
		return
	}

	hasContent := sc.Location != nil || len(sc.PresentEntities) > 0 || len(sc.ActiveQuests) > 0
	if !hasContent {
		return
	}

	sb.WriteString("\n\n## Current Scene\n")
	first := true
	writeLine := func(format string, args ...any) {
		if !first {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(sb, format, args...)
		first = false
	}

	if sc.Location != nil {
		if desc, ok := sc.Location.Attributes["description"]; ok {
			writeLine("Location: %s - %v", sc.Location.Name, desc)
		} else {
			writeLine("Location: %s", sc.Location.Name)
		}
	}

	if len(sc.PresentEntities) > 0 {
		var names []string
		for _, e := range sc.PresentEntities {
			t := e.Type
			if t == "" {
				t = "entity"
			}
			names = append(names, fmt.Sprintf("%s (%s)", e.Name, t))
		}
		writeLine("Also present: %s", strings.Join(names, ", "))
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
		writeLine("Active quests: %s", strings.Join(questParts, ", "))
	}
}

// writeTranscriptSection writes the recent conversation with relative
// timestamps (e.g., "2m ago") and speaker labels directly to sb.
func writeTranscriptSection(sb *strings.Builder, entries []memory.TranscriptEntry) {
	if len(entries) == 0 {
		return
	}

	sb.WriteString("\n\n## Recent Conversation\n")
	now := time.Now()
	for i, e := range entries {
		if i > 0 {
			sb.WriteByte('\n')
		}
		speaker := e.SpeakerName
		if speaker == "" {
			speaker = e.SpeakerID
		}
		if speaker == "" {
			speaker = "Unknown"
		}

		relTime := formatRelativeTime(now.Sub(e.Timestamp))
		fmt.Fprintf(sb, "[%s] %s: %s", relTime, speaker, e.Text)
	}
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

// Package orchestrator provides an [agent.Router] implementation that manages
// NPC agents within a Glyphoxa session. It adds address detection, cross-NPC
// awareness, and DM override capabilities on top of the base [agent.Router]
// contract.
package orchestrator

import (
	"errors"
	"slices"
	"strings"

	"github.com/MrWong99/glyphoxa/internal/agent"
)

// ErrNoTarget is returned when address detection cannot determine which NPC
// was addressed by a player's utterance.
var ErrNoTarget = errors.New("orchestrator: no target NPC identified")

// candidate is a pre-sorted name-to-ID mapping entry.
type candidate struct {
	key string
	id  string
}

// AddressDetector determines which NPC was spoken to by scanning the
// transcript text for NPC names, falling back through a priority chain of
// heuristics (DM override → last-speaker continuation → single-NPC fallback).
type AddressDetector struct {
	// nameIndex maps lowercase NPC names (and name fragments) to agent IDs.
	nameIndex map[string]string

	// sorted is the nameIndex entries pre-sorted by descending key length
	// so that more specific (longer) names match before shorter fragments.
	// Built once in buildIndex and reused on every matchName call.
	sorted []candidate
}

// NewAddressDetector builds a name index from the given agents.
//
// The index includes the full lowercase name of each NPC and every individual
// word of length ≥ 3 from that name. For example, "Grimjaw the Blacksmith"
// produces entries for "grimjaw the blacksmith", "grimjaw", and "blacksmith"
// (the word "the" is too short).
func NewAddressDetector(agents []agent.NPCAgent) *AddressDetector {
	d := &AddressDetector{
		nameIndex: make(map[string]string),
	}
	d.buildIndex(agents)
	return d
}

// Detect returns the agent ID of the NPC addressed in the transcript.
//
// The detection strategy is applied in order:
//  1. Explicit name match — scan text for indexed NPC names/fragments.
//  2. DM override — if speaker has an active puppet override in dmOverrides.
//  3. Last-speaker continuation — route to lastSpeaker if set and active.
//  4. Single-NPC fallback — if exactly one unmuted NPC exists, route there.
//  5. No match — return ("", [ErrNoTarget]).
func (d *AddressDetector) Detect(
	text string,
	lastSpeaker string,
	activeAgents map[string]*agentEntry,
	dmOverrides map[string]string,
	speaker string,
) (string, error) {
	// 1. Explicit name match — scan for the longest (most specific) key first.
	if id := d.matchName(text, activeAgents); id != "" {
		return id, nil
	}

	// 2. DM override / puppet mode.
	if npcID, ok := dmOverrides[speaker]; ok {
		if entry, exists := activeAgents[npcID]; exists && !entry.muted {
			return npcID, nil
		}
	}

	// 3. Last-speaker continuation.
	if lastSpeaker != "" {
		if entry, ok := activeAgents[lastSpeaker]; ok && !entry.muted {
			return lastSpeaker, nil
		}
	}

	// 4. Single-NPC fallback.
	var unmutedID string
	unmutedCount := 0
	for id, entry := range activeAgents {
		if !entry.muted {
			unmutedID = id
			unmutedCount++
			if unmutedCount > 1 {
				break
			}
		}
	}
	if unmutedCount == 1 {
		return unmutedID, nil
	}

	return "", ErrNoTarget
}

// Rebuild rebuilds the name index from a fresh set of agents.
// Call this after adding or removing agents.
func (d *AddressDetector) Rebuild(agents []agent.NPCAgent) {
	d.nameIndex = make(map[string]string)
	d.buildIndex(agents)
}

// buildIndex populates nameIndex from the given agents and pre-sorts
// candidates by descending key length for efficient matching.
func (d *AddressDetector) buildIndex(agents []agent.NPCAgent) {
	for _, a := range agents {
		name := a.Name()
		id := a.ID()
		lower := strings.ToLower(name)

		// Index the full name.
		d.nameIndex[lower] = id

		// Index individual words ≥ 3 characters.
		for word := range strings.FieldsSeq(lower) {
			if len(word) >= 3 {
				d.nameIndex[word] = id
			}
		}
	}

	// Pre-sort candidates by descending key length so matchName can iterate
	// without allocating or sorting per call.
	d.sorted = make([]candidate, 0, len(d.nameIndex))
	for key, id := range d.nameIndex {
		d.sorted = append(d.sorted, candidate{key: key, id: id})
	}
	slices.SortFunc(d.sorted, func(a, b candidate) int {
		return len(b.key) - len(a.key) // descending
	})
}

// matchName scans the lowercase transcript text for indexed NPC names.
// It checks longer keys first so that "grimjaw the blacksmith" is preferred
// over "grimjaw" when both appear in the text. Only unmuted agents are matched.
//
// The pre-sorted candidate list is built once in buildIndex, so this method
// performs zero allocations and no sorting per call.
func (d *AddressDetector) matchName(text string, activeAgents map[string]*agentEntry) string {
	lower := strings.ToLower(text)

	for _, c := range d.sorted {
		if strings.Contains(lower, c.key) {
			if entry, ok := activeAgents[c.id]; ok && !entry.muted {
				return c.id
			}
		}
	}
	return ""
}

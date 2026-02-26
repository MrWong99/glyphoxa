// Package hotctx assembles the always-injected "hot" context for every NPC LLM
// call in the Glyphoxa voice AI pipeline.
//
// The hot layer consists of three components that are fetched concurrently:
//
//  1. NPC identity snapshot from the knowledge graph (L3).
//  2. Recent session transcript from the session store (L1).
//  3. Scene context: current location, present NPCs, and active quests.
//
// Target assembly latency is < 50 ms. Use [FormatSystemPrompt] to convert a
// [HotContext] into a system prompt string ready for LLM injection.
package hotctx

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/MrWong99/glyphoxa/pkg/memory"
)

// ─────────────────────────────────────────────────────────────────────────────
// Public types
// ─────────────────────────────────────────────────────────────────────────────

// HotContext is the assembled context injected into every NPC LLM prompt.
// All fields are optional — callers should check for nil/empty before using.
type HotContext struct {
	// Identity is the NPC's knowledge-graph identity snapshot.
	Identity *memory.NPCIdentity

	// RecentTranscript is the last N minutes of session conversation, capped at
	// the assembler's maxEntries setting.
	RecentTranscript []memory.TranscriptEntry

	// SceneContext contains the current location and other entities present.
	SceneContext *SceneContext

	// PreFetchResults contains speculatively pre-fetched cold-layer results that
	// were injected before assembly (e.g., from [PreFetcher]).
	PreFetchResults []memory.ContextResult

	// AssemblyDuration records how long [Assembler.Assemble] took.
	AssemblyDuration time.Duration
}

// SceneContext describes the current scene from the NPC's perspective.
type SceneContext struct {
	// Location is the entity node for the NPC's current location, or nil if
	// no LOCATED_AT relationship exists.
	Location *memory.Entity

	// PresentNPCs lists other entities (NPCs, players) at the same location.
	PresentNPCs []memory.Entity

	// ActiveQuests lists quest entities the NPC is involved in via QUEST_GIVER
	// or PARTICIPATED_IN relationships.
	ActiveQuests []memory.Entity
}

// ─────────────────────────────────────────────────────────────────────────────
// Assembler
// ─────────────────────────────────────────────────────────────────────────────

// Assembler concurrently fetches all three hot-layer components and combines
// them into a [HotContext].
type Assembler struct {
	sessionStore   memory.SessionStore
	graph          memory.KnowledgeGraph
	recentDuration time.Duration
	maxEntries     int
}

// Option is a functional option for [NewAssembler].
type Option func(*Assembler)

// WithRecentDuration sets how far back in time [Assembler.Assemble] looks when
// fetching the recent session transcript. Defaults to 5 minutes.
func WithRecentDuration(d time.Duration) Option {
	return func(a *Assembler) { a.recentDuration = d }
}

// WithMaxTranscriptEntries caps the number of transcript entries included in
// [HotContext.RecentTranscript]. When the session store returns more than n
// entries the most-recent n are kept. Defaults to 50.
func WithMaxTranscriptEntries(n int) Option {
	return func(a *Assembler) { a.maxEntries = n }
}

// NewAssembler creates an [Assembler] with sensible defaults.
// Apply [Option] values to override the defaults.
func NewAssembler(sessionStore memory.SessionStore, graph memory.KnowledgeGraph, opts ...Option) *Assembler {
	a := &Assembler{
		sessionStore:   sessionStore,
		graph:          graph,
		recentDuration: 5 * time.Minute,
		maxEntries:     50,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Assemble concurrently fetches all three hot-layer components and returns a
// fully populated [HotContext].
//
// The three fetches (identity snapshot, recent transcript, scene context) run
// in parallel via errgroup. If any fetch returns an error, assembly is aborted
// and that error is returned — wrapped with a "hot context: " prefix.
//
// Assemble respects context cancellation on all underlying I/O calls.
func (a *Assembler) Assemble(ctx context.Context, npcID string, sessionID string) (*HotContext, error) {
	start := time.Now()

	var (
		identity   *memory.NPCIdentity
		transcript []memory.TranscriptEntry
		scene      *SceneContext
	)

	eg, egCtx := errgroup.WithContext(ctx)

	// ── goroutine 1: NPC identity snapshot ───────────────────────────────────
	eg.Go(func() error {
		snap, err := a.graph.IdentitySnapshot(egCtx, npcID)
		if err != nil {
			return fmt.Errorf("hot context: identity snapshot for %q: %w", npcID, err)
		}
		identity = snap
		return nil
	})

	// ── goroutine 2: recent session transcript ────────────────────────────────
	eg.Go(func() error {
		entries, err := a.sessionStore.GetRecent(egCtx, sessionID, a.recentDuration)
		if err != nil {
			return fmt.Errorf("hot context: get recent transcript for session %q: %w", sessionID, err)
		}
		// Truncate to the most-recent maxEntries entries.
		if len(entries) > a.maxEntries {
			entries = entries[len(entries)-a.maxEntries:]
		}
		transcript = entries
		return nil
	})

	// ── goroutine 3: scene context ────────────────────────────────────────────
	eg.Go(func() error {
		sc, err := a.buildSceneContext(egCtx, npcID)
		if err != nil {
			return fmt.Errorf("hot context: build scene context for %q: %w", npcID, err)
		}
		scene = sc
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return &HotContext{
		Identity:         identity,
		RecentTranscript: transcript,
		SceneContext:     scene,
		AssemblyDuration: time.Since(start),
	}, nil
}

// buildSceneContext builds scene context for npcID by:
//  1. Looking up LOCATED_AT outgoing relationships to find the current location.
//  2. If a location is found, fetching its entity and its 1-hop neighbours (other
//     entities present at the same location).
//  3. Looking up QUEST_GIVER and PARTICIPATED_IN relationships to collect quests.
func (a *Assembler) buildSceneContext(ctx context.Context, npcID string) (*SceneContext, error) {
	// Fetch all outgoing relationships from the NPC in one call.
	rels, err := a.graph.GetRelationships(ctx, npcID, memory.WithOutgoing())
	if err != nil {
		return nil, fmt.Errorf("get relationships: %w", err)
	}

	sc := &SceneContext{
		PresentNPCs:  []memory.Entity{},
		ActiveQuests: []memory.Entity{},
	}

	var locationID string
	for _, r := range rels {
		switch r.RelType {
		case "LOCATED_AT":
			locationID = r.TargetID

		case "QUEST_GIVER", "PARTICIPATED_IN":
			// Only include if the target entity is of type "quest".
			entity, err := a.graph.GetEntity(ctx, r.TargetID)
			if err != nil {
				return nil, fmt.Errorf("get quest entity %q: %w", r.TargetID, err)
			}
			if entity != nil && entity.Type == "quest" {
				sc.ActiveQuests = append(sc.ActiveQuests, *entity)
			}
		}
	}

	if locationID != "" {
		loc, err := a.graph.GetEntity(ctx, locationID)
		if err != nil {
			return nil, fmt.Errorf("get location entity %q: %w", locationID, err)
		}
		sc.Location = loc

		// Find other entities present at the same location (1-hop neighbours of
		// the location node that have a LOCATED_AT edge pointing to it).
		neighbours, err := a.graph.Neighbors(ctx, locationID, 1,
			memory.TraverseRelTypes("LOCATED_AT"),
		)
		if err != nil {
			return nil, fmt.Errorf("get neighbours of location %q: %w", locationID, err)
		}
		for _, n := range neighbours {
			// Exclude the NPC itself.
			if n.ID != npcID {
				sc.PresentNPCs = append(sc.PresentNPCs, n)
			}
		}
	}

	return sc, nil
}

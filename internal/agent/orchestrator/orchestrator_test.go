package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/agent"
	agentmock "github.com/MrWong99/glyphoxa/internal/agent/mock"
	enginemock "github.com/MrWong99/glyphoxa/internal/engine/mock"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newMockAgent(id, name string) (*agentmock.NPCAgent, *enginemock.VoiceEngine) {
	eng := &enginemock.VoiceEngine{}
	return &agentmock.NPCAgent{
		IDResult:     id,
		NameResult:   name,
		EngineResult: eng,
	}, eng
}

func transcript(text string) stt.Transcript {
	return stt.Transcript{Text: text, IsFinal: true, Confidence: 1.0}
}

// ── Address Detection ────────────────────────────────────────────────────────

func TestAddressDetector(t *testing.T) {
	t.Parallel()

	grimjaw, _ := newMockAgent("grimjaw-1", "Grimjaw the Blacksmith")
	elara, _ := newMockAgent("elara-1", "Elara")
	sage, _ := newMockAgent("sage-1", "Greymantle the Sage")

	active := map[string]*agentEntry{
		"grimjaw-1": {agent: grimjaw},
		"elara-1":   {agent: elara},
		"sage-1":    {agent: sage},
	}
	noOverrides := map[string]string{}

	detector := NewAddressDetector([]agent.NPCAgent{grimjaw, elara, sage})

	t.Run("explicit full name match", func(t *testing.T) {
		t.Parallel()
		id, err := detector.Detect("Hey Grimjaw the Blacksmith, how are you?", "", active, noOverrides, "player-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "grimjaw-1" {
			t.Fatalf("want grimjaw-1, got %s", id)
		}
	})

	t.Run("name fragment match", func(t *testing.T) {
		t.Parallel()
		id, err := detector.Detect("Grimjaw, can you repair this?", "", active, noOverrides, "player-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "grimjaw-1" {
			t.Fatalf("want grimjaw-1, got %s", id)
		}
	})

	t.Run("case-insensitive matching", func(t *testing.T) {
		t.Parallel()
		id, err := detector.Detect("ELARA what do you think?", "", active, noOverrides, "player-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "elara-1" {
			t.Fatalf("want elara-1, got %s", id)
		}
	})

	t.Run("multiple NPC names — first match wins", func(t *testing.T) {
		t.Parallel()
		// "blacksmith" appears before "elara" in the text.
		id, err := detector.Detect("blacksmith and elara, let's go", "", active, noOverrides, "player-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "grimjaw-1" {
			t.Fatalf("want grimjaw-1 (first match), got %s", id)
		}
	})

	t.Run("no name — last speaker continuation", func(t *testing.T) {
		t.Parallel()
		id, err := detector.Detect("tell me more about that", "elara-1", active, noOverrides, "player-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "elara-1" {
			t.Fatalf("want elara-1, got %s", id)
		}
	})

	t.Run("no name no last speaker — single NPC fallback", func(t *testing.T) {
		t.Parallel()
		singleActive := map[string]*agentEntry{
			"elara-1": {agent: elara},
		}
		id, err := detector.Detect("hello there", "", singleActive, noOverrides, "player-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "elara-1" {
			t.Fatalf("want elara-1, got %s", id)
		}
	})

	t.Run("no match at all — ErrNoTarget", func(t *testing.T) {
		t.Parallel()
		_, err := detector.Detect("hello anyone?", "", active, noOverrides, "player-1")
		if !errors.Is(err, ErrNoTarget) {
			t.Fatalf("want ErrNoTarget, got %v", err)
		}
	})

	t.Run("DM override takes precedence over last speaker", func(t *testing.T) {
		t.Parallel()
		overrides := map[string]string{"player-1": "sage-1"}
		id, err := detector.Detect("whatever", "elara-1", active, overrides, "player-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "sage-1" {
			t.Fatalf("want sage-1 (puppet override), got %s", id)
		}
	})
}

// ── Orchestrator Routing ─────────────────────────────────────────────────────

func TestOrchestratorRoute(t *testing.T) {
	t.Parallel()

	t.Run("route to correct NPC by name", func(t *testing.T) {
		t.Parallel()
		grimjaw, _ := newMockAgent("g1", "Grimjaw")
		elara, _ := newMockAgent("e1", "Elara")
		o := New([]agent.NPCAgent{grimjaw, elara})

		got, err := o.Route(context.Background(), "player-1", transcript("Hey Grimjaw!"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID() != "g1" {
			t.Fatalf("want g1, got %s", got.ID())
		}
	})

	t.Run("route to muted NPC returns ErrNoTarget", func(t *testing.T) {
		t.Parallel()
		grimjaw, _ := newMockAgent("g1", "Grimjaw")
		o := New([]agent.NPCAgent{grimjaw})

		if err := o.MuteAgent("g1"); err != nil {
			t.Fatalf("mute: %v", err)
		}
		_, err := o.Route(context.Background(), "player-1", transcript("Hey Grimjaw"))
		if !errors.Is(err, ErrNoTarget) {
			t.Fatalf("want ErrNoTarget, got %v", err)
		}
	})

	t.Run("mute and unmute toggle", func(t *testing.T) {
		t.Parallel()
		grimjaw, _ := newMockAgent("g1", "Grimjaw")
		o := New([]agent.NPCAgent{grimjaw})

		if err := o.MuteAgent("g1"); err != nil {
			t.Fatalf("mute: %v", err)
		}
		_, err := o.Route(context.Background(), "player-1", transcript("Grimjaw"))
		if !errors.Is(err, ErrNoTarget) {
			t.Fatalf("expected ErrNoTarget while muted")
		}

		if err := o.UnmuteAgent("g1"); err != nil {
			t.Fatalf("unmute: %v", err)
		}
		got, err := o.Route(context.Background(), "player-1", transcript("Grimjaw"))
		if err != nil {
			t.Fatalf("route after unmute: %v", err)
		}
		if got.ID() != "g1" {
			t.Fatalf("want g1, got %s", got.ID())
		}
	})

	t.Run("MuteAgent unknown ID returns error", func(t *testing.T) {
		t.Parallel()
		o := New(nil)
		err := o.MuteAgent("nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
	})

	t.Run("UnmuteAgent unknown ID returns error", func(t *testing.T) {
		t.Parallel()
		o := New(nil)
		err := o.UnmuteAgent("nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
	})

	t.Run("cross-NPC context injection during Route", func(t *testing.T) {
		t.Parallel()
		grimjaw, gEng := newMockAgent("g1", "Grimjaw")
		elara, _ := newMockAgent("e1", "Elara")
		o := New([]agent.NPCAgent{grimjaw, elara})

		// Add some utterances to the buffer so that context injection fires.
		o.buffer.Add(BufferEntry{
			SpeakerID:   "e1",
			SpeakerName: "Elara",
			Text:        "I heard a noise in the forest.",
			NPCID:       "e1",
			Timestamp:   time.Now(),
		})

		got, err := o.Route(context.Background(), "player-1", transcript("Hey Grimjaw"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID() != "g1" {
			t.Fatalf("want g1, got %s", got.ID())
		}

		// The engine should have received an InjectContext call with the buffer entry.
		if len(gEng.InjectContextCalls) != 1 {
			t.Fatalf("expected 1 InjectContext call, got %d", len(gEng.InjectContextCalls))
		}
		entries := gEng.InjectContextCalls[0].Update.RecentUtterances
		if len(entries) != 1 {
			t.Fatalf("expected 1 recent utterance, got %d", len(entries))
		}
		if entries[0].Text != "I heard a noise in the forest." {
			t.Fatalf("unexpected text: %s", entries[0].Text)
		}
	})

	t.Run("Route with DM puppet override", func(t *testing.T) {
		t.Parallel()
		grimjaw, _ := newMockAgent("g1", "Grimjaw")
		elara, _ := newMockAgent("e1", "Elara")
		o := New([]agent.NPCAgent{grimjaw, elara})

		if err := o.SetPuppet("player-1", "e1"); err != nil {
			t.Fatalf("set puppet: %v", err)
		}

		// Even though the text says "Grimjaw", puppet override should not apply
		// because explicit name match has higher priority. Let's use ambiguous text.
		got, err := o.Route(context.Background(), "player-1", transcript("tell me a story"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID() != "e1" {
			t.Fatalf("want e1 (puppet), got %s", got.ID())
		}
	})

	t.Run("SetPuppet unknown NPC returns error", func(t *testing.T) {
		t.Parallel()
		o := New(nil)
		err := o.SetPuppet("player-1", "nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown NPC")
		}
	})

	t.Run("SetPuppet clear override", func(t *testing.T) {
		t.Parallel()
		grimjaw, _ := newMockAgent("g1", "Grimjaw")
		elara, _ := newMockAgent("e1", "Elara")
		o := New([]agent.NPCAgent{grimjaw, elara})

		if err := o.SetPuppet("player-1", "e1"); err != nil {
			t.Fatalf("set puppet: %v", err)
		}
		// Clear the override.
		if err := o.SetPuppet("player-1", ""); err != nil {
			t.Fatalf("clear puppet: %v", err)
		}

		// With no name, no last speaker, and two unmuted NPCs → ErrNoTarget.
		_, err := o.Route(context.Background(), "player-1", transcript("hello"))
		if !errors.Is(err, ErrNoTarget) {
			t.Fatalf("want ErrNoTarget after clearing puppet, got %v", err)
		}
	})

	t.Run("Route respects context cancellation", func(t *testing.T) {
		t.Parallel()
		grimjaw, _ := newMockAgent("g1", "Grimjaw")
		o := New([]agent.NPCAgent{grimjaw})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := o.Route(ctx, "player-1", transcript("Grimjaw"))
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})
}

// ── Agent Management ─────────────────────────────────────────────────────────

func TestAgentManagement(t *testing.T) {
	t.Parallel()

	t.Run("AddAgent adds to active agents", func(t *testing.T) {
		t.Parallel()
		o := New(nil)
		npc, _ := newMockAgent("a1", "Alice")

		if err := o.AddAgent(npc); err != nil {
			t.Fatalf("add: %v", err)
		}

		agents := o.ActiveAgents()
		if len(agents) != 1 {
			t.Fatalf("want 1 agent, got %d", len(agents))
		}
		if agents[0].ID() != "a1" {
			t.Fatalf("want a1, got %s", agents[0].ID())
		}
	})

	t.Run("AddAgent duplicate ID returns error", func(t *testing.T) {
		t.Parallel()
		npc, _ := newMockAgent("a1", "Alice")
		o := New([]agent.NPCAgent{npc})

		dup, _ := newMockAgent("a1", "Alice Clone")
		err := o.AddAgent(dup)
		if err == nil {
			t.Fatal("expected error for duplicate ID")
		}
	})

	t.Run("RemoveAgent removes from active agents", func(t *testing.T) {
		t.Parallel()
		npc, _ := newMockAgent("a1", "Alice")
		o := New([]agent.NPCAgent{npc})

		if err := o.RemoveAgent("a1"); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if len(o.ActiveAgents()) != 0 {
			t.Fatal("expected no agents after removal")
		}
	})

	t.Run("RemoveAgent unknown ID returns error", func(t *testing.T) {
		t.Parallel()
		o := New(nil)
		err := o.RemoveAgent("nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown agent")
		}
	})

	t.Run("ActiveAgents returns all agents", func(t *testing.T) {
		t.Parallel()
		a, _ := newMockAgent("a1", "Alice")
		b, _ := newMockAgent("b1", "Bob")
		o := New([]agent.NPCAgent{a, b})

		agents := o.ActiveAgents()
		if len(agents) != 2 {
			t.Fatalf("want 2 agents, got %d", len(agents))
		}
	})

	t.Run("BroadcastScene calls UpdateScene on unmuted agents", func(t *testing.T) {
		t.Parallel()
		alice, _ := newMockAgent("a1", "Alice")
		bob, _ := newMockAgent("b1", "Bob")
		muted, _ := newMockAgent("m1", "Muted")
		o := New([]agent.NPCAgent{alice, bob, muted})

		if err := o.MuteAgent("m1"); err != nil {
			t.Fatalf("mute: %v", err)
		}

		scene := agent.SceneContext{
			Location:  "Tavern",
			TimeOfDay: "evening",
		}
		if err := o.BroadcastScene(context.Background(), scene); err != nil {
			t.Fatalf("broadcast: %v", err)
		}

		// alice and bob should have received the scene; muted should not.
		if len(alice.UpdateSceneCalls) != 1 {
			t.Fatalf("alice: want 1 UpdateScene call, got %d", len(alice.UpdateSceneCalls))
		}
		if len(bob.UpdateSceneCalls) != 1 {
			t.Fatalf("bob: want 1 UpdateScene call, got %d", len(bob.UpdateSceneCalls))
		}
		if len(muted.UpdateSceneCalls) != 0 {
			t.Fatalf("muted: want 0 UpdateScene calls, got %d", len(muted.UpdateSceneCalls))
		}
		if alice.UpdateSceneCalls[0].Scene.Location != "Tavern" {
			t.Fatalf("unexpected scene location: %s", alice.UpdateSceneCalls[0].Scene.Location)
		}
	})

	t.Run("AddAgent makes new agent routable", func(t *testing.T) {
		t.Parallel()
		o := New(nil)
		npc, _ := newMockAgent("a1", "Alice")

		if err := o.AddAgent(npc); err != nil {
			t.Fatalf("add: %v", err)
		}

		got, err := o.Route(context.Background(), "player-1", transcript("Hey Alice"))
		if err != nil {
			t.Fatalf("route: %v", err)
		}
		if got.ID() != "a1" {
			t.Fatalf("want a1, got %s", got.ID())
		}
	})

	t.Run("RemoveAgent clears last speaker", func(t *testing.T) {
		t.Parallel()
		alice, _ := newMockAgent("a1", "Alice")
		bob, _ := newMockAgent("b1", "Bob")
		o := New([]agent.NPCAgent{alice, bob})

		// Route to alice so she becomes lastSpeaker.
		_, err := o.Route(context.Background(), "player-1", transcript("Alice"))
		if err != nil {
			t.Fatalf("route: %v", err)
		}

		// Remove alice.
		if err := o.RemoveAgent("a1"); err != nil {
			t.Fatalf("remove: %v", err)
		}

		// Ambiguous text with bob as only agent → should fallback to bob (single NPC).
		got, err := o.Route(context.Background(), "player-1", transcript("go on"))
		if err != nil {
			t.Fatalf("route after remove: %v", err)
		}
		if got.ID() != "b1" {
			t.Fatalf("want b1, got %s", got.ID())
		}
	})
}

// ── Utterance Buffer ─────────────────────────────────────────────────────────

func TestUtteranceBuffer(t *testing.T) {
	t.Parallel()

	t.Run("Add and Recent retrieval", func(t *testing.T) {
		t.Parallel()
		buf := NewUtteranceBuffer(10, 5*time.Minute)

		buf.Add(BufferEntry{SpeakerID: "p1", SpeakerName: "Player", Text: "hello", Timestamp: time.Now()})
		buf.Add(BufferEntry{SpeakerID: "n1", SpeakerName: "NPC", Text: "greetings", NPCID: "n1", Timestamp: time.Now()})

		recent := buf.Recent("", 10)
		if len(recent) != 2 {
			t.Fatalf("want 2 entries, got %d", len(recent))
		}
		if recent[0].Text != "hello" || recent[1].Text != "greetings" {
			t.Fatalf("unexpected entries: %+v", recent)
		}
	})

	t.Run("entries evicted by age", func(t *testing.T) {
		t.Parallel()
		buf := NewUtteranceBuffer(10, 100*time.Millisecond)

		buf.Add(BufferEntry{SpeakerID: "p1", Text: "old", Timestamp: time.Now().Add(-200 * time.Millisecond)})
		buf.Add(BufferEntry{SpeakerID: "p1", Text: "new", Timestamp: time.Now()})

		// The old entry should be evicted.
		entries := buf.Entries()
		if len(entries) != 1 {
			t.Fatalf("want 1 entry after age eviction, got %d", len(entries))
		}
		if entries[0].Text != "new" {
			t.Fatalf("want 'new', got %q", entries[0].Text)
		}
	})

	t.Run("entries evicted by size", func(t *testing.T) {
		t.Parallel()
		buf := NewUtteranceBuffer(2, 5*time.Minute)

		buf.Add(BufferEntry{SpeakerID: "p1", Text: "a", Timestamp: time.Now()})
		buf.Add(BufferEntry{SpeakerID: "p1", Text: "b", Timestamp: time.Now()})
		buf.Add(BufferEntry{SpeakerID: "p1", Text: "c", Timestamp: time.Now()})

		entries := buf.Entries()
		if len(entries) != 2 {
			t.Fatalf("want 2 entries after size eviction, got %d", len(entries))
		}
		if entries[0].Text != "b" || entries[1].Text != "c" {
			t.Fatalf("want [b, c], got [%s, %s]", entries[0].Text, entries[1].Text)
		}
	})

	t.Run("Recent excludes specified NPC", func(t *testing.T) {
		t.Parallel()
		buf := NewUtteranceBuffer(10, 5*time.Minute)

		buf.Add(BufferEntry{SpeakerID: "n1", SpeakerName: "NPC1", Text: "mine", NPCID: "n1", Timestamp: time.Now()})
		buf.Add(BufferEntry{SpeakerID: "n2", SpeakerName: "NPC2", Text: "other", NPCID: "n2", Timestamp: time.Now()})
		buf.Add(BufferEntry{SpeakerID: "p1", SpeakerName: "Player", Text: "player", Timestamp: time.Now()})

		recent := buf.Recent("n1", 10)
		if len(recent) != 2 {
			t.Fatalf("want 2 entries (excluding n1), got %d", len(recent))
		}
		for _, e := range recent {
			if e.NPCID == "n1" {
				t.Fatal("n1 entries should be excluded")
			}
		}
	})

	t.Run("concurrent Add and Recent safety", func(t *testing.T) {
		t.Parallel()
		buf := NewUtteranceBuffer(100, 5*time.Minute)

		var wg sync.WaitGroup
		for i := range 50 {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				buf.Add(BufferEntry{
					SpeakerID: "p1",
					Text:      "msg",
					Timestamp: time.Now(),
				})
			}(i)
			go func(i int) {
				defer wg.Done()
				_ = buf.Recent("", 10)
			}(i)
		}
		wg.Wait()

		// No race detector panic = success. Just verify some entries exist.
		entries := buf.Entries()
		if len(entries) == 0 {
			t.Fatal("expected non-zero entries after concurrent adds")
		}
	})

	t.Run("Recent respects maxEntries limit", func(t *testing.T) {
		t.Parallel()
		buf := NewUtteranceBuffer(20, 5*time.Minute)

		for i := range 10 {
			buf.Add(BufferEntry{SpeakerID: "p1", Text: "msg", Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond)})
		}

		recent := buf.Recent("", 3)
		if len(recent) != 3 {
			t.Fatalf("want 3 entries, got %d", len(recent))
		}
	})

	t.Run("Recent returns most recent entries", func(t *testing.T) {
		t.Parallel()
		buf := NewUtteranceBuffer(20, 5*time.Minute)

		now := time.Now()
		buf.Add(BufferEntry{SpeakerID: "p1", Text: "first", Timestamp: now})
		buf.Add(BufferEntry{SpeakerID: "p1", Text: "second", Timestamp: now.Add(time.Millisecond)})
		buf.Add(BufferEntry{SpeakerID: "p1", Text: "third", Timestamp: now.Add(2 * time.Millisecond)})

		recent := buf.Recent("", 2)
		if len(recent) != 2 {
			t.Fatalf("want 2 entries, got %d", len(recent))
		}
		// Should be the most recent 2 in chronological order.
		if recent[0].Text != "second" || recent[1].Text != "third" {
			t.Fatalf("want [second, third], got [%s, %s]", recent[0].Text, recent[1].Text)
		}
	})
}

// ── Options ──────────────────────────────────────────────────────────────────

func TestOptions(t *testing.T) {
	t.Parallel()

	t.Run("WithBufferSize", func(t *testing.T) {
		t.Parallel()
		npc, _ := newMockAgent("a1", "Alice")
		o := New([]agent.NPCAgent{npc}, WithBufferSize(2))

		o.buffer.Add(BufferEntry{SpeakerID: "p1", Text: "a", Timestamp: time.Now()})
		o.buffer.Add(BufferEntry{SpeakerID: "p1", Text: "b", Timestamp: time.Now()})
		o.buffer.Add(BufferEntry{SpeakerID: "p1", Text: "c", Timestamp: time.Now()})

		entries := o.buffer.Entries()
		if len(entries) != 2 {
			t.Fatalf("want 2 entries with buffer size 2, got %d", len(entries))
		}
	})

	t.Run("WithBufferDuration", func(t *testing.T) {
		t.Parallel()
		npc, _ := newMockAgent("a1", "Alice")
		o := New([]agent.NPCAgent{npc}, WithBufferDuration(100*time.Millisecond))

		o.buffer.Add(BufferEntry{SpeakerID: "p1", Text: "old", Timestamp: time.Now().Add(-200 * time.Millisecond)})
		o.buffer.Add(BufferEntry{SpeakerID: "p1", Text: "new", Timestamp: time.Now()})

		entries := o.buffer.Entries()
		if len(entries) != 1 {
			t.Fatalf("want 1 entry after duration eviction, got %d", len(entries))
		}
	})
}

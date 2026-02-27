package entity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/entity"
)

// ─────────────────────────────────────────────────────────────────────────────
// Foundry VTT test fixtures
// ─────────────────────────────────────────────────────────────────────────────

const foundryWorldJSON = `{
  "actors": [
    {
      "_id": "actor-001",
      "name": "Balthazar the Wizard",
      "type": "npc",
      "img": "icons/wizard.png",
      "flags": {},
      "system": {}
    },
    {
      "_id": "actor-002",
      "name": "Town Guard",
      "type": "npc",
      "img": "",
      "flags": {}
    }
  ],
  "items": [
    {
      "_id": "item-001",
      "name": "Sword of Dawn",
      "type": "weapon",
      "img": "icons/sword.png",
      "flags": {}
    }
  ],
  "journal": [
    {
      "_id": "journal-001",
      "name": "History of the Realm",
      "content": "<p>Long ago, in a land far away...</p>",
      "flags": {}
    },
    {
      "_id": "journal-002",
      "name": "The Prophecy",
      "content": "",
      "pages": [
        {
          "name": "Part 1",
          "text": { "content": "<p>Stars will align when...</p>" }
        }
      ],
      "flags": {}
    }
  ]
}`

const foundryEmptyJSON = `{
  "actors": [],
  "items": [],
  "journal": []
}`

// ─────────────────────────────────────────────────────────────────────────────
// Roll20 test fixtures
// ─────────────────────────────────────────────────────────────────────────────

const roll20JSON = `{
  "schema": 2,
  "characters": [
    {
      "id": "char-001",
      "name": "Seraphina",
      "bio": "<p>A skilled rogue from the eastern provinces.</p>",
      "attribs": [
        {"name": "strength", "current": 10, "max": 10},
        {"name": "dexterity", "current": 18, "max": 18}
      ]
    },
    {
      "id": "char-002",
      "name": "Bron the Smith",
      "bio": "",
      "attribs": []
    }
  ],
  "handouts": [
    {
      "id": "handout-001",
      "name": "The Ancient Map",
      "notes": "<p>A tattered map showing the path to the dungeon.</p>",
      "gmnotes": ""
    },
    {
      "id": "handout-002",
      "name": "Secret Notes",
      "notes": "",
      "gmnotes": "<p>Only for the DM: the treasure is cursed.</p>"
    }
  ]
}`

const roll20EmptyJSON = `{
  "schema": 2,
  "characters": [],
  "handouts": []
}`

// ─────────────────────────────────────────────────────────────────────────────
// Foundry VTT tests
// ─────────────────────────────────────────────────────────────────────────────

func TestImportFoundryVTT(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	n, err := entity.ImportFoundryVTT(ctx, s, strings.NewReader(foundryWorldJSON))
	if err != nil {
		t.Fatalf("ImportFoundryVTT: unexpected error: %v", err)
	}
	// 2 actors + 1 item + 2 journal = 5 entities
	if n != 5 {
		t.Fatalf("ImportFoundryVTT: expected 5 imported, got %d", n)
	}

	npcs, err := s.List(ctx, entity.ListOptions{Type: entity.EntityNPC})
	if err != nil {
		t.Fatalf("List(npc): %v", err)
	}
	if len(npcs) != 2 {
		t.Fatalf("List(npc): expected 2, got %d", len(npcs))
	}

	items, err := s.List(ctx, entity.ListOptions{Type: entity.EntityItem})
	if err != nil {
		t.Fatalf("List(item): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List(item): expected 1, got %d", len(items))
	}
	if items[0].Name != "Sword of Dawn" {
		t.Fatalf("List(item): expected 'Sword of Dawn', got %q", items[0].Name)
	}

	lore, err := s.List(ctx, entity.ListOptions{Type: entity.EntityLore})
	if err != nil {
		t.Fatalf("List(lore): %v", err)
	}
	if len(lore) != 2 {
		t.Fatalf("List(lore): expected 2, got %d", len(lore))
	}

	// Verify HTML stripping worked for the journal content.
	var historyLore entity.EntityDefinition
	for _, l := range lore {
		if l.Name == "History of the Realm" {
			historyLore = l
			break
		}
	}
	if historyLore.Name == "" {
		t.Fatal("List(lore): 'History of the Realm' not found")
	}
	if strings.Contains(historyLore.Description, "<p>") {
		t.Errorf("ImportFoundryVTT: HTML not stripped from journal content: %q", historyLore.Description)
	}

	// Verify pages fallback for empty content journal.
	var prophecyLore entity.EntityDefinition
	for _, l := range lore {
		if l.Name == "The Prophecy" {
			prophecyLore = l
			break
		}
	}
	if prophecyLore.Name == "" {
		t.Fatal("List(lore): 'The Prophecy' not found")
	}
	if prophecyLore.Description == "" {
		t.Error("ImportFoundryVTT: expected page content fallback for empty journal content, got empty description")
	}
}

func TestImportFoundryVTT_EmptyData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	n, err := entity.ImportFoundryVTT(ctx, s, strings.NewReader(foundryEmptyJSON))
	if err != nil {
		t.Fatalf("ImportFoundryVTT empty: unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("ImportFoundryVTT empty: expected 0 imported, got %d", n)
	}
}

func TestImportFoundryVTT_InvalidJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	_, err := entity.ImportFoundryVTT(ctx, s, strings.NewReader("{not json}"))
	if err == nil {
		t.Fatal("ImportFoundryVTT invalid: expected error, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Roll20 tests
// ─────────────────────────────────────────────────────────────────────────────

func TestImportRoll20(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	n, err := entity.ImportRoll20(ctx, s, strings.NewReader(roll20JSON))
	if err != nil {
		t.Fatalf("ImportRoll20: unexpected error: %v", err)
	}
	// 2 characters + 2 handouts = 4 entities
	if n != 4 {
		t.Fatalf("ImportRoll20: expected 4 imported, got %d", n)
	}

	npcs, err := s.List(ctx, entity.ListOptions{Type: entity.EntityNPC})
	if err != nil {
		t.Fatalf("List(npc): %v", err)
	}
	if len(npcs) != 2 {
		t.Fatalf("List(npc): expected 2, got %d", len(npcs))
	}

	// Verify attributes are mapped to properties.
	var seraphina entity.EntityDefinition
	for _, npc := range npcs {
		if npc.Name == "Seraphina" {
			seraphina = npc
			break
		}
	}
	if seraphina.Name == "" {
		t.Fatal("List(npc): Seraphina not found")
	}
	if seraphina.Properties["strength"] != "10" {
		t.Errorf("Seraphina properties: expected strength=10, got %q", seraphina.Properties["strength"])
	}

	// Bio HTML should be stripped.
	if strings.Contains(seraphina.Description, "<p>") {
		t.Errorf("ImportRoll20: HTML not stripped from bio: %q", seraphina.Description)
	}

	lore, err := s.List(ctx, entity.ListOptions{Type: entity.EntityLore})
	if err != nil {
		t.Fatalf("List(lore): %v", err)
	}
	if len(lore) != 2 {
		t.Fatalf("List(lore): expected 2, got %d", len(lore))
	}

	// Verify GMNotes fallback when notes is empty.
	var secretNotes entity.EntityDefinition
	for _, l := range lore {
		if l.Name == "Secret Notes" {
			secretNotes = l
			break
		}
	}
	if secretNotes.Name == "" {
		t.Fatal("List(lore): 'Secret Notes' not found")
	}
	if secretNotes.Description == "" {
		t.Error("ImportRoll20: expected gmnotes fallback for empty notes, got empty description")
	}
}

func TestImportRoll20_EmptyData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	n, err := entity.ImportRoll20(ctx, s, strings.NewReader(roll20EmptyJSON))
	if err != nil {
		t.Fatalf("ImportRoll20 empty: unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("ImportRoll20 empty: expected 0 imported, got %d", n)
	}
}

func TestImportRoll20_InvalidJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	_, err := entity.ImportRoll20(ctx, s, strings.NewReader("not json at all"))
	if err == nil {
		t.Fatal("ImportRoll20 invalid: expected error, got nil")
	}
}

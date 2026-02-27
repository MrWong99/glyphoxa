package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Foundry VTT
// ─────────────────────────────────────────────────────────────────────────────

// foundryWorld is the top-level structure of a Foundry VTT world export JSON.
// Unknown fields are silently ignored.
type foundryWorld struct {
	Actors  []foundryActor   `json:"actors"`
	Items   []foundryItem    `json:"items"`
	Journal []foundryJournal `json:"journal"`
}

type foundryActor struct {
	ID   string         `json:"_id"`
	Name string         `json:"name"`
	Type string         `json:"type"`
	Img  string         `json:"img"`
	Flags map[string]any `json:"flags"`
	// system holds game-system-specific stats; we capture it as raw JSON.
	System json.RawMessage `json:"system"`
}

type foundryItem struct {
	ID   string         `json:"_id"`
	Name string         `json:"name"`
	Type string         `json:"type"`
	Img  string         `json:"img"`
	Flags map[string]any `json:"flags"`
}

type foundryJournal struct {
	ID      string         `json:"_id"`
	Name    string         `json:"name"`
	Content string         `json:"content"`
	Flags   map[string]any `json:"flags"`
	// Pages is the newer Foundry v10+ format; we use the first page's text.
	Pages []foundryPage `json:"pages"`
}

type foundryPage struct {
	Name string              `json:"name"`
	Text foundryPageText     `json:"text"`
}

type foundryPageText struct {
	Content string `json:"content"`
}

// ImportFoundryVTT imports entities from a Foundry VTT world export (JSON).
// It extracts actors (NPCs), items, and journal entries.
//
// Unknown JSON fields are silently ignored. The import is best-effort:
// if an entity cannot be stored the error is returned together with the count
// of entities imported so far.
func ImportFoundryVTT(ctx context.Context, store Store, r io.Reader) (int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("entity: foundry vtt: read input: %w", err)
	}

	var world foundryWorld
	if err := json.Unmarshal(data, &world); err != nil {
		return 0, fmt.Errorf("entity: foundry vtt: parse json: %w", err)
	}

	var entities []EntityDefinition

	// Map actors → EntityNPC.
	for _, a := range world.Actors {
		if a.Name == "" {
			continue
		}
		props := make(map[string]string)
		if a.Type != "" {
			props["actor_type"] = a.Type
		}
		if a.Img != "" {
			props["img"] = a.Img
		}
		entities = append(entities, EntityDefinition{
			ID:          a.ID,
			Name:        a.Name,
			Type:        EntityNPC,
			Description: fmt.Sprintf("Imported from Foundry VTT actor: %s", a.Name),
			Properties:  props,
			Tags:        []string{"foundry", "actor"},
		})
	}

	// Map items → EntityItem.
	for _, item := range world.Items {
		if item.Name == "" {
			continue
		}
		props := make(map[string]string)
		if item.Type != "" {
			props["item_type"] = item.Type
		}
		if item.Img != "" {
			props["img"] = item.Img
		}
		entities = append(entities, EntityDefinition{
			ID:          item.ID,
			Name:        item.Name,
			Type:        EntityItem,
			Description: fmt.Sprintf("Imported from Foundry VTT item: %s", item.Name),
			Properties:  props,
			Tags:        []string{"foundry", "item"},
		})
	}

	// Map journal entries → EntityLore.
	for _, j := range world.Journal {
		if j.Name == "" {
			continue
		}
		// Prefer inline content; fall back to first page.
		content := j.Content
		if content == "" && len(j.Pages) > 0 {
			content = j.Pages[0].Text.Content
		}
		// Strip HTML tags from Foundry's rich text.
		content = stripHTMLTags(content)

		entities = append(entities, EntityDefinition{
			ID:          j.ID,
			Name:        j.Name,
			Type:        EntityLore,
			Description: content,
			Tags:        []string{"foundry", "journal"},
		})
	}

	n, err := store.BulkImport(ctx, entities)
	if err != nil {
		return n, fmt.Errorf("entity: foundry vtt: import: %w", err)
	}
	return n, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Roll20
// ─────────────────────────────────────────────────────────────────────────────

// roll20Export is the top-level structure of a Roll20 campaign export JSON.
// Unknown fields are silently ignored.
type roll20Export struct {
	Schema     int               `json:"schema"`
	Characters []roll20Character `json:"characters"`
	Handouts   []roll20Handout   `json:"handouts"`
}

type roll20Character struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Bio     string           `json:"bio"`
	Attribs []roll20Attrib   `json:"attribs"`
}

type roll20Attrib struct {
	Name    string `json:"name"`
	Current any    `json:"current"`
	Max     any    `json:"max"`
}

type roll20Handout struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Notes   string `json:"notes"`
	GMNotes string `json:"gmnotes"`
}

// ImportRoll20 imports entities from a Roll20 campaign export (JSON).
// It extracts characters (as NPCs) and handouts (as lore).
//
// Unknown JSON fields are silently ignored. The import is best-effort:
// if an entity cannot be stored the error is returned together with the count
// of entities imported so far.
func ImportRoll20(ctx context.Context, store Store, r io.Reader) (int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("entity: roll20: read input: %w", err)
	}

	var export roll20Export
	if err := json.Unmarshal(data, &export); err != nil {
		return 0, fmt.Errorf("entity: roll20: parse json: %w", err)
	}

	var entities []EntityDefinition

	// Map characters → EntityNPC.
	for _, c := range export.Characters {
		if c.Name == "" {
			continue
		}
		props := make(map[string]string)
		for _, attr := range c.Attribs {
			if attr.Name != "" {
				props[attr.Name] = fmt.Sprintf("%v", attr.Current)
			}
		}
		desc := stripHTMLTags(c.Bio)

		entities = append(entities, EntityDefinition{
			ID:          c.ID,
			Name:        c.Name,
			Type:        EntityNPC,
			Description: desc,
			Properties:  props,
			Tags:        []string{"roll20", "character"},
		})
	}

	// Map handouts → EntityLore.
	for _, h := range export.Handouts {
		if h.Name == "" {
			continue
		}
		notes := stripHTMLTags(h.Notes)
		if notes == "" {
			notes = stripHTMLTags(h.GMNotes)
		}

		entities = append(entities, EntityDefinition{
			ID:          h.ID,
			Name:        h.Name,
			Type:        EntityLore,
			Description: notes,
			Tags:        []string{"roll20", "handout"},
		})
	}

	n, err := store.BulkImport(ctx, entities)
	if err != nil {
		return n, fmt.Errorf("entity: roll20: import: %w", err)
	}
	return n, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared helpers
// ─────────────────────────────────────────────────────────────────────────────

// stripHTMLTags removes HTML tags from s using a simple state machine.
// It is intentionally minimal — not a full HTML parser — but sufficient for
// the rich-text fields exported by Foundry VTT and Roll20.
func stripHTMLTags(s string) string {
	if !strings.ContainsRune(s, '<') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

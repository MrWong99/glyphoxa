package entity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/entity"
)

const validCampaignYAML = `
campaign:
  name: "Test Campaign"
  description: "A test campaign for unit tests"
  system: "dnd5e"
entities:
  - name: "Thorin Oakenshield"
    type: npc
    description: "Dwarf king in exile"
    tags:
      - dwarf
      - noble
    properties:
      race: dwarf
  - name: "Erebor"
    type: location
    description: "The Lonely Mountain"
    tags:
      - mountain
      - dwarven
`

const minimalCampaignYAML = `
campaign:
  name: "Minimal"
entities: []
`

func TestLoadCampaignFromReader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantName   string
		wantSystem string
		wantCount  int
	}{
		{
			name:       "valid campaign",
			input:      validCampaignYAML,
			wantErr:    false,
			wantName:   "Test Campaign",
			wantSystem: "dnd5e",
			wantCount:  2,
		},
		{
			name:      "minimal campaign no entities",
			input:     minimalCampaignYAML,
			wantErr:   false,
			wantName:  "Minimal",
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cf, err := entity.LoadCampaignFromReader(strings.NewReader(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatal("LoadCampaignFromReader: expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadCampaignFromReader: unexpected error: %v", err)
			}
			if cf.Campaign.Name != tc.wantName {
				t.Errorf("campaign name: expected %q, got %q", tc.wantName, cf.Campaign.Name)
			}
			if tc.wantSystem != "" && cf.Campaign.System != tc.wantSystem {
				t.Errorf("campaign system: expected %q, got %q", tc.wantSystem, cf.Campaign.System)
			}
			if len(cf.Entities) != tc.wantCount {
				t.Errorf("entity count: expected %d, got %d", tc.wantCount, len(cf.Entities))
			}
		})
	}
}

func TestLoadCampaignFromReader_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "completely invalid YAML",
			input: ":::not valid yaml:::",
		},
		{
			name:  "unknown top-level key",
			input: "campaign:\n  name: x\nunknown_key: true\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := entity.LoadCampaignFromReader(strings.NewReader(tc.input))
			if err == nil {
				t.Fatal("LoadCampaignFromReader: expected error for invalid input, got nil")
			}
		})
	}
}

func TestImportCampaign(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	cf, err := entity.LoadCampaignFromReader(strings.NewReader(validCampaignYAML))
	if err != nil {
		t.Fatalf("LoadCampaignFromReader: %v", err)
	}

	n, err := entity.ImportCampaign(ctx, s, cf)
	if err != nil {
		t.Fatalf("ImportCampaign: unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("ImportCampaign: expected 2 imported, got %d", n)
	}

	// Verify entities are actually in the store.
	all, err := s.List(ctx, entity.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List: expected 2 entities, got %d", len(all))
	}

	// Verify NPC is findable by type.
	npcs, err := s.List(ctx, entity.ListOptions{Type: entity.EntityNPC})
	if err != nil {
		t.Fatalf("List(npc): %v", err)
	}
	if len(npcs) != 1 || npcs[0].Name != "Thorin Oakenshield" {
		t.Fatalf("List(npc): expected Thorin Oakenshield, got %+v", npcs)
	}
}

func TestImportCampaign_NilCampaign(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()
	_, err := entity.ImportCampaign(ctx, s, nil)
	if err == nil {
		t.Fatal("ImportCampaign: expected error for nil campaign, got nil")
	}
}

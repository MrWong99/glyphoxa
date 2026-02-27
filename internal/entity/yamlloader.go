package entity

import (
	"context"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// CampaignFile is the top-level structure of a Glyphoxa campaign YAML file.
//
// Example:
//
//	campaign:
//	  name: "The Lost Mine of Phandelver"
//	  system: "dnd5e"
//	entities:
//	  - name: "Gundren Rockseeker"
//	    type: npc
//	    description: "A dwarf merchant hiring adventurers."
type CampaignFile struct {
	Campaign CampaignMeta       `yaml:"campaign"`
	Entities []EntityDefinition `yaml:"entities"`
}

// CampaignMeta holds top-level metadata for a campaign.
type CampaignMeta struct {
	// Name is the campaign's display name.
	Name string `yaml:"name"`

	// Description is a free-text summary of the campaign.
	Description string `yaml:"description"`

	// System is the game system identifier (e.g., "dnd5e", "pf2e", "custom").
	System string `yaml:"system"`
}

// LoadCampaignFile reads and parses a campaign YAML file from disk.
// Returns a descriptive error if the file cannot be opened or parsed.
func LoadCampaignFile(path string) (*CampaignFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("entity: open campaign file %q: %w", path, err)
	}
	defer f.Close()

	cf, err := LoadCampaignFromReader(f)
	if err != nil {
		return nil, fmt.Errorf("entity: parse campaign file %q: %w", path, err)
	}
	return cf, nil
}

// LoadCampaignFromReader parses campaign YAML from an [io.Reader].
// The reader is consumed entirely; the caller is responsible for closing it.
func LoadCampaignFromReader(r io.Reader) (*CampaignFile, error) {
	var cf CampaignFile
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true) // reject unknown top-level keys to catch typos
	if err := dec.Decode(&cf); err != nil {
		return nil, fmt.Errorf("entity: decode campaign yaml: %w", err)
	}
	return &cf, nil
}

// ImportCampaign imports all entities from a parsed [CampaignFile] into store.
// Returns the number of entities successfully imported.
// An error from the store aborts the import and returns the count so far.
func ImportCampaign(ctx context.Context, store Store, campaign *CampaignFile) (int, error) {
	if campaign == nil {
		return 0, fmt.Errorf("entity: campaign must not be nil")
	}
	n, err := store.BulkImport(ctx, campaign.Entities)
	if err != nil {
		return n, fmt.Errorf("entity: import campaign %q: %w", campaign.Campaign.Name, err)
	}
	return n, nil
}

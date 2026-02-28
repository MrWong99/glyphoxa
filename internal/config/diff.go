package config

// ConfigDiff describes what changed between two configs.
// Only fields that can be safely hot-reloaded are tracked.
type ConfigDiff struct {
	NPCsChanged     bool      // true if any NPC personality, voice, or budget_tier changed
	NPCChanges      []NPCDiff // per-NPC diffs
	LogLevelChanged bool
	NewLogLevel     LogLevel
}

// NPCDiff describes what changed for a single NPC between two configs.
type NPCDiff struct {
	Name               string
	PersonalityChanged bool
	VoiceChanged       bool
	BudgetTierChanged  bool
	Added              bool
	Removed            bool
}

// Diff compares old and new configs and returns what changed.
// Only tracks changes that are safe to apply without restart.
func Diff(old, new *Config) ConfigDiff {
	d := ConfigDiff{}

	// Log level
	if old.Server.LogLevel != new.Server.LogLevel {
		d.LogLevelChanged = true
		d.NewLogLevel = new.Server.LogLevel
	}

	// Build NPC lookup maps keyed by name.
	oldNPCs := make(map[string]*NPCConfig, len(old.NPCs))
	for i := range old.NPCs {
		oldNPCs[old.NPCs[i].Name] = &old.NPCs[i]
	}
	newNPCs := make(map[string]*NPCConfig, len(new.NPCs))
	for i := range new.NPCs {
		newNPCs[new.NPCs[i].Name] = &new.NPCs[i]
	}

	// Detect modified and removed NPCs.
	for name, oldNPC := range oldNPCs {
		newNPC, exists := newNPCs[name]
		if !exists {
			d.NPCChanges = append(d.NPCChanges, NPCDiff{
				Name:    name,
				Removed: true,
			})
			d.NPCsChanged = true
			continue
		}
		nd := diffNPC(name, oldNPC, newNPC)
		if nd.PersonalityChanged || nd.VoiceChanged || nd.BudgetTierChanged {
			d.NPCChanges = append(d.NPCChanges, nd)
			d.NPCsChanged = true
		}
	}

	// Detect added NPCs.
	for name := range newNPCs {
		if _, exists := oldNPCs[name]; !exists {
			d.NPCChanges = append(d.NPCChanges, NPCDiff{
				Name:  name,
				Added: true,
			})
			d.NPCsChanged = true
		}
	}

	return d
}

// diffNPC compares two NPC configs with the same name.
func diffNPC(name string, old, new *NPCConfig) NPCDiff {
	nd := NPCDiff{Name: name}

	if old.Personality != new.Personality {
		nd.PersonalityChanged = true
	}

	if old.Voice != new.Voice {
		nd.VoiceChanged = true
	}

	if old.BudgetTier != new.BudgetTier {
		nd.BudgetTierChanged = true
	}

	return nd
}

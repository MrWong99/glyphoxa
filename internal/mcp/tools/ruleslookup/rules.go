package ruleslookup

// Rule holds a single game rule entry from the embedded SRD data.
type Rule struct {
	// ID is the unique machine-readable identifier for the rule.
	ID string `json:"id"`

	// Name is the human-readable display name.
	Name string `json:"name"`

	// Category classifies the rule (e.g. "condition", "combat", "spell", "general").
	Category string `json:"category"`

	// System is the game system this rule belongs to (e.g. "dnd5e").
	System string `json:"system"`

	// Text is the full rule description.
	Text string `json:"text"`
}

// srdRules is the embedded set of D&D 5e SRD rules used by the rules-lookup tools.
// Includes conditions, combat rules, spells, and general rules.
var srdRules = []Rule{
	// ─────────────────────────────────────────────────────────────────────────
	// Conditions
	// ─────────────────────────────────────────────────────────────────────────
	{
		ID:       "condition-blinded",
		Name:     "Blinded",
		Category: "condition",
		System:   "dnd5e",
		Text:     `A blinded creature can't see and automatically fails any ability check that requires sight. Attack rolls against the creature have advantage, and the creature's attack rolls have disadvantage.`,
	},
	{
		ID:       "condition-charmed",
		Name:     "Charmed",
		Category: "condition",
		System:   "dnd5e",
		Text:     `A charmed creature can't attack the charmer or target the charmer with harmful abilities or magical effects. The charmer has advantage on any ability check to interact socially with the creature.`,
	},
	{
		ID:       "condition-frightened",
		Name:     "Frightened",
		Category: "condition",
		System:   "dnd5e",
		Text:     `A frightened creature has disadvantage on ability checks and attack rolls while the source of its fear is within line of sight. The creature can't willingly move closer to the source of its fear.`,
	},
	{
		ID:       "condition-poisoned",
		Name:     "Poisoned",
		Category: "condition",
		System:   "dnd5e",
		Text:     `A poisoned creature has disadvantage on attack rolls and ability checks.`,
	},
	{
		ID:       "condition-stunned",
		Name:     "Stunned",
		Category: "condition",
		System:   "dnd5e",
		Text:     `A stunned creature is incapacitated (see the condition), can't move, and can speak only falteringly. The creature automatically fails Strength and Dexterity saving throws. Attack rolls against the creature have advantage.`,
	},

	// ─────────────────────────────────────────────────────────────────────────
	// Combat rules
	// ─────────────────────────────────────────────────────────────────────────
	{
		ID:       "combat-opportunity-attack",
		Name:     "Opportunity Attack",
		Category: "combat",
		System:   "dnd5e",
		Text:     `You can make an opportunity attack when a hostile creature that you can see moves out of your reach. To make the opportunity attack, you use your reaction to make one melee attack against the provoking creature. The attack occurs right before the creature leaves your reach.`,
	},
	{
		ID:       "combat-cover",
		Name:     "Cover",
		Category: "combat",
		System:   "dnd5e",
		Text:     `Walls, trees, creatures, and other obstacles can provide cover during combat. There are three degrees of cover, each of which provides a different benefit to a creature: half cover (+2 AC and Dex saves), three-quarters cover (+5 AC and Dex saves), and total cover (can't be targeted directly by attacks or spells).`,
	},
	{
		ID:       "combat-flanking",
		Name:     "Flanking",
		Category: "combat",
		System:   "dnd5e",
		Text:     `Optional rule. When a creature and at least one of its allies are adjacent to an enemy and on opposite sides of the enemy's space, they are flanking that enemy, and each of them has advantage on melee attack rolls against that enemy.`,
	},
	{
		ID:       "combat-grapple",
		Name:     "Grapple",
		Category: "combat",
		System:   "dnd5e",
		Text:     `When you want to grab a creature or wrestle with it, you can use the Attack action to make a special melee attack — a grapple. The target of your grapple must be no more than one size larger than you. Using at least one free hand, you try to seize the target by making an Athletics check contested by the target's Athletics or Acrobatics (their choice). If you succeed, the target is grappled.`,
	},
	{
		ID:       "combat-shove",
		Name:     "Shove",
		Category: "combat",
		System:   "dnd5e",
		Text:     `Using the Attack action, you can make a special melee attack to shove a creature, either to knock it prone or push it away from you. The target must be no more than one size larger than you and within your reach. You make a Strength (Athletics) check contested by the target's Strength (Athletics) or Dexterity (Acrobatics) check. If you win, you either knock the target prone or push it 5 feet away.`,
	},

	// ─────────────────────────────────────────────────────────────────────────
	// Spells
	// ─────────────────────────────────────────────────────────────────────────
	{
		ID:       "spell-fireball",
		Name:     "Fireball",
		Category: "spell",
		System:   "dnd5e",
		Text:     `3rd-level evocation. Casting time: 1 action. Range: 150 feet. Components: V, S, M (a tiny ball of bat guano and sulfur). Duration: Instantaneous. A bright streak flashes from your pointing finger to a point you choose within range and then blossoms with a low roar into an explosion of flame. Each creature in a 20-foot-radius sphere centred on that point must make a Dexterity saving throw. A target takes 8d6 fire damage on a failed save, or half as much on a successful one. Higher levels: +1d6 per slot level above 3rd.`,
	},
	{
		ID:       "spell-shield",
		Name:     "Shield",
		Category: "spell",
		System:   "dnd5e",
		Text:     `1st-level abjuration. Casting time: 1 reaction, which you take when you are hit by an attack or targeted by the magic missile spell. Range: Self. Components: V, S. Duration: 1 round. An invisible barrier of magical force appears and protects you. Until the start of your next turn, you have a +5 bonus to AC, including against the triggering attack, and you take no damage from magic missile.`,
	},
	{
		ID:       "spell-healing-word",
		Name:     "Healing Word",
		Category: "spell",
		System:   "dnd5e",
		Text:     `1st-level evocation. Casting time: 1 bonus action. Range: 60 feet. Components: V. Duration: Instantaneous. A creature of your choice that you can see within range regains hit points equal to 1d4 + your spellcasting ability modifier. This spell has no effect on undead or constructs. Higher levels: +1d4 per slot level above 1st.`,
	},
	{
		ID:       "spell-counterspell",
		Name:     "Counterspell",
		Category: "spell",
		System:   "dnd5e",
		Text:     `3rd-level abjuration. Casting time: 1 reaction, which you take when you see a creature within 60 feet casting a spell. Range: 60 feet. Components: S. Duration: Instantaneous. You attempt to interrupt a creature in the process of casting a spell. If the creature is casting a spell of 3rd level or lower, its spell fails and has no effect. If it is casting a spell of 4th level or higher, make an ability check using your spellcasting ability. The DC equals 10 + the spell's level. On a success, the creature's spell fails.`,
	},
	{
		ID:       "spell-misty-step",
		Name:     "Misty Step",
		Category: "spell",
		System:   "dnd5e",
		Text:     `2nd-level conjuration. Casting time: 1 bonus action. Range: Self. Components: V. Duration: Instantaneous. Briefly surrounded by silvery mist, you teleport up to 30 feet to an unoccupied space that you can see.`,
	},

	// ─────────────────────────────────────────────────────────────────────────
	// General rules
	// ─────────────────────────────────────────────────────────────────────────
	{
		ID:       "general-short-rest",
		Name:     "Short Rest",
		Category: "general",
		System:   "dnd5e",
		Text:     `A short rest is a period of downtime, at least 1 hour long, during which a character does nothing more strenuous than eating, drinking, reading, and tending to wounds. A character can spend one or more Hit Dice at the end of a short rest, up to the character's maximum number of Hit Dice, which is equal to the character's level.`,
	},
	{
		ID:       "general-long-rest",
		Name:     "Long Rest",
		Category: "general",
		System:   "dnd5e",
		Text:     `A long rest is a period of extended downtime, at least 8 hours long, during which a character sleeps or performs light activity: reading, talking, eating, or standing watch for no more than 2 hours. At the end of a long rest, a character regains all lost hit points and all spent hit dice up to half the character's total. A character must have at least 1 hit point to take a long rest.`,
	},
	{
		ID:       "general-death-saves",
		Name:     "Death Saving Throws",
		Category: "general",
		System:   "dnd5e",
		Text:     `Whenever you start your turn with 0 hit points, you must make a special saving throw called a death saving throw to determine whether you creep closer to death or hang on to life. Roll a d20: on 10 or higher you succeed. On a 1 you suffer two failures. On a 20 you regain 1 hit point. Three successes = stable. Three failures = dead.`,
	},
	{
		ID:       "general-concentration",
		Name:     "Concentration",
		Category: "general",
		System:   "dnd5e",
		Text:     `Some spells require you to maintain concentration. If you lose concentration, the spell ends. You lose concentration if you cast another concentration spell, take damage (DC 10 or half the damage taken, whichever is higher, Constitution saving throw), are incapacitated or killed, or are otherwise distracted (DM's discretion).`,
	},
	{
		ID:       "general-advantage",
		Name:     "Advantage and Disadvantage",
		Category: "general",
		System:   "dnd5e",
		Text:     `Advantage: roll two d20s and take the higher result. Disadvantage: roll two d20s and take the lower result. If you have both advantage and disadvantage, they cancel out and you roll a single d20. Advantage and disadvantage don't stack; having multiple sources of advantage still means rolling only two dice.`,
	},
}

// rulesByID is a precomputed map of rule ID → Rule for O(1) lookup.
var rulesByID map[string]Rule

func init() {
	rulesByID = make(map[string]Rule, len(srdRules))
	for _, r := range srdRules {
		rulesByID[r.ID] = r
	}
}

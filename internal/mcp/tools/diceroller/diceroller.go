// Package diceroller provides built-in MCP tools for resolving dice rolls and
// random table lookups in a TTRPG session.
//
// Two tools are exported via [Tools]:
//   - "roll"       — evaluates a standard dice expression (e.g. "2d6+3").
//   - "roll_table" — rolls on a named in-memory random table.
//
// All handlers are safe for concurrent use. Randomness uses [math/rand/v2]
// with a per-process automatically-seeded source.
package diceroller

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/MrWong99/glyphoxa/internal/mcp/tools"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// rollArgs is the JSON-decoded input for the "roll" tool.
type rollArgs struct {
	// Expression is the dice expression to evaluate (e.g. "2d6+3").
	Expression string `json:"expression"`
}

// rollResult is the JSON-encoded output of the "roll" tool.
type rollResult struct {
	// Expression is the original dice expression, echoed back to the caller.
	Expression string `json:"expression"`

	// Rolls holds the individual die results (before the modifier is applied).
	Rolls []int `json:"rolls"`

	// Total is the sum of all rolls plus the modifier.
	Total int `json:"total"`
}

// rollTableArgs is the JSON-decoded input for the "roll_table" tool.
type rollTableArgs struct {
	// TableName identifies which random table to roll on.
	TableName string `json:"table_name"`
}

// rollTableResult is the JSON-encoded output of the "roll_table" tool.
type rollTableResult struct {
	// Table is the name of the table that was rolled on.
	Table string `json:"table"`

	// Roll is the raw die result (1-based index into the table).
	Roll int `json:"roll"`

	// Result is the textual entry from the table corresponding to Roll.
	Result string `json:"result"`
}

// parseExpression parses a dice expression of the form NdS, NdS+M, or NdS-M.
// N is the number of dice (defaults to 1 when omitted), S is the number of
// sides (must be ≥ 1), and M is an optional integer modifier (may be negative).
//
// Returns (count, sides, modifier, nil) on success, or a descriptive error.
func parseExpression(expr string) (count, sides, modifier int, err error) {
	expr = strings.ToLower(strings.TrimSpace(expr))

	dIdx := strings.Index(expr, "d")
	if dIdx == -1 {
		return 0, 0, 0, fmt.Errorf("diceroller: invalid expression %q: missing 'd' separator", expr)
	}

	// Parse count (the part before 'd').
	countStr := expr[:dIdx]
	if countStr == "" {
		count = 1
	} else {
		count, err = strconv.Atoi(countStr)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("diceroller: invalid dice count %q in expression %q", countStr, expr)
		}
	}
	if count < 1 {
		return 0, 0, 0, fmt.Errorf("diceroller: dice count must be ≥ 1, got %d in expression %q", count, expr)
	}

	// Parse sides and optional modifier (the part after 'd').
	rest := expr[dIdx+1:]

	plusIdx := strings.Index(rest, "+")
	// Find the minus sign, but not if rest is empty.
	minusIdx := strings.Index(rest, "-")

	switch {
	case plusIdx != -1:
		sides, err = strconv.Atoi(rest[:plusIdx])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("diceroller: invalid sides %q in expression %q", rest[:plusIdx], expr)
		}
		modifier, err = strconv.Atoi(rest[plusIdx+1:])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("diceroller: invalid modifier %q in expression %q", rest[plusIdx+1:], expr)
		}

	case minusIdx != -1:
		sides, err = strconv.Atoi(rest[:minusIdx])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("diceroller: invalid sides %q in expression %q", rest[:minusIdx], expr)
		}
		mod, err2 := strconv.Atoi(rest[minusIdx+1:])
		if err2 != nil {
			return 0, 0, 0, fmt.Errorf("diceroller: invalid modifier %q in expression %q", rest[minusIdx+1:], expr)
		}
		modifier = -mod

	default:
		sides, err = strconv.Atoi(rest)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("diceroller: invalid sides %q in expression %q", rest, expr)
		}
	}

	if sides < 1 {
		return 0, 0, 0, fmt.Errorf("diceroller: sides must be ≥ 1, got %d in expression %q", sides, expr)
	}

	return count, sides, modifier, nil
}

// rollHandler implements the "roll" tool. It parses the dice expression from
// the JSON args, performs the rolls, and returns a JSON-encoded [rollResult].
func rollHandler(_ context.Context, args string) (string, error) {
	var a rollArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("diceroller: failed to parse arguments: %w", err)
	}
	if a.Expression == "" {
		return "", fmt.Errorf("diceroller: expression must not be empty")
	}

	count, sides, modifier, err := parseExpression(a.Expression)
	if err != nil {
		return "", err
	}

	rolls := make([]int, count)
	total := modifier
	for i := range count {
		r := rand.IntN(sides) + 1
		rolls[i] = r
		total += r
	}

	res, err := json.Marshal(rollResult{
		Expression: a.Expression,
		Rolls:      rolls,
		Total:      total,
	})
	if err != nil {
		return "", fmt.Errorf("diceroller: failed to encode result: %w", err)
	}
	return string(res), nil
}

// rollTableHandler implements the "roll_table" tool. It looks up the named
// table, rolls an appropriate die, and returns the matching entry as a
// JSON-encoded [rollTableResult].
func rollTableHandler(_ context.Context, args string) (string, error) {
	var a rollTableArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("diceroller: failed to parse arguments: %w", err)
	}

	entries, ok := builtinTables[a.TableName]
	if !ok {
		known := make([]string, 0, len(builtinTables))
		for k := range builtinTables {
			known = append(known, k)
		}
		return "", fmt.Errorf("diceroller: unknown table %q; available tables: %v", a.TableName, known)
	}

	roll := rand.IntN(len(entries)) + 1 // 1-based die result
	result := entries[roll-1]

	res, err := json.Marshal(rollTableResult{
		Table:  a.TableName,
		Roll:   roll,
		Result: result,
	})
	if err != nil {
		return "", fmt.Errorf("diceroller: failed to encode result: %w", err)
	}
	return string(res), nil
}

// builtinTables holds the in-memory random tables available to roll_table.
// Entries are 1-indexed by roll value; the slice index is roll-1.
var builtinTables = map[string][]string{
	"wild_magic": {
		"Flumph allies: 1d6 Flumphs appear and are friendly.",
		"You turn blue for 1d10 days. Any magic cast on you has a 10% chance of failing.",
		"You cast Fireball centred on yourself.",
		"You cast Levitate on yourself.",
		"You grow a long beard made of feathers that remains until you sneeze.",
		"You must shout when you speak for the next minute.",
		"A spectral shield hovers near you for the next minute (+2 AC).",
		"You are immune to being intoxicated by alcohol for the next 5d6 days.",
		"Your hair falls out and regrows in a bright colour (roll 1d6: 1=red, 2=blue, 3=green, 4=purple, 5=orange, 6=white).",
		"For the next minute, any flammable object you touch bursts into flame.",
		"You regain 2d10 hit points.",
		"You teleport up to 60 feet to an unoccupied space of your choice.",
		"You cast Grease centred on yourself.",
		"Creatures have disadvantage on saving throws against your spells for the next minute.",
		"You are frightened by the nearest creature until the end of your next turn.",
		"Each creature within 30 feet of you becomes invisible for the next minute.",
		"You gain resistance to all damage for the next minute.",
		"A random creature within 60 feet of you becomes poisoned for 1d4 hours.",
		"You glow with bright light in a 30-foot radius for the next minute.",
		"You cast Polymorph on yourself, transforming into a sheep.",
	},
	"treasure_hoard": {
		"A pouch of 2d6 × 100 gold pieces.",
		"A gemstone worth 1d6 × 50 gp (roll: ruby).",
		"A magic weapon (+1 shortsword).",
		"A scroll of a 2nd-level spell.",
		"1d4 potions of healing.",
		"A small silver statuette worth 250 gp.",
		"An ornate golden goblet worth 150 gp.",
		"A set of masterwork thieves' tools worth 120 gp.",
		"A sealed letter of credit worth 500 gp (redeemable at any major bank).",
		"A rare spell component worth 200 gp (powdered moonstone).",
		"A piece of jewellery worth 1d4 × 100 gp.",
		"A cursed item: belt of dwarvenkind (attune reveals it makes the wearer incredibly hungry).",
		"A bag of holding (already used, contains mundane camping gear).",
		"2d10 × 10 platinum pieces.",
		"A map to a hidden dungeon.",
		"An ivory statuette of an elephant worth 250 gp.",
		"A set of artisan's tools (jeweller's) worth 25 gp.",
		"A silvered dagger (+1 against lycanthropes).",
		"A potion of animal friendship.",
		"Three doses of antitoxin.",
	},
	"random_encounter": {
		"A patrol of 1d6 town guards, suspicious but not hostile.",
		"A merchant caravan under attack by 1d4 bandits.",
		"An injured traveller in need of healing (DC 12 Medicine).",
		"A pack of 2d4 wolves stalking the party from the tree line.",
		"A friendly herbalist who trades potions for rare plants.",
		"A collapsed bridge — the party must find another route or fix it (DC 15 Athletics).",
		"A toll booth manned by corrupt guards demanding 5 gp per person.",
		"A travelling bard who knows a rumour about the party's current quest.",
		"A swarm of 1d4 giant bees that have broken from their hive.",
		"A lone skeleton rises from a roadside grave.",
		"A goblin ambush — 2d6 goblins with a goblin boss.",
		"A holy pilgrim heading to the nearest temple who asks for escort.",
		"1d4 stirges nest in an overhang above the road.",
		"A displaced giant badger is blocking the road and is very angry.",
		"A lost child who was separated from a travelling circus.",
		"Two rival adventurers who ask the party to arbitrate a dispute.",
		"A sudden violent thunderstorm (disadvantage on ranged attacks).",
		"A friendly owlbear that has been domesticated and escaped its owner.",
		"A message carried by a trained raven meant for someone in the next town.",
		"A hidden pit trap (DC 14 Perception to spot, 2d6 fall damage on failure).",
	},
}

// Tools returns the slice of built-in dice-roller tools ready for
// registration with the MCP Host.
//
// The returned tools are:
//   - "roll": evaluates a dice expression such as "2d6+3".
//   - "roll_table": rolls on a named built-in random table.
func Tools() []tools.Tool {
	return []tools.Tool{
		{
			Definition: llm.ToolDefinition{
				Name:        "roll",
				Description: "Evaluate a dice expression and return each individual die result and the total. Supports standard notation such as 2d6+3, 1d20, or 4d8-1.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"expression": map[string]any{
							"type":        "string",
							"description": "Dice expression to evaluate, e.g. 2d6+3, 1d20, 4d8-1",
						},
					},
					"required": []string{"expression"},
				},
				EstimatedDurationMs: 5,
				MaxDurationMs:       20,
				Idempotent:          false,
				CacheableSeconds:    0,
			},
			Handler:     rollHandler,
			DeclaredP50: 5,
			DeclaredMax: 20,
		},
		{
			Definition: llm.ToolDefinition{
				Name:        "roll_table",
				Description: "Roll on a named random table and return the result. Useful for generating spontaneous encounters, treasure, or wild magic effects.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"table_name": map[string]any{
							"type":        "string",
							"description": "Name of the random table to roll on.",
							"enum":        []string{"wild_magic", "treasure_hoard", "random_encounter"},
						},
					},
					"required": []string{"table_name"},
				},
				EstimatedDurationMs: 5,
				MaxDurationMs:       20,
				Idempotent:          false,
				CacheableSeconds:    0,
			},
			Handler:     rollTableHandler,
			DeclaredP50: 5,
			DeclaredMax: 20,
		},
	}
}

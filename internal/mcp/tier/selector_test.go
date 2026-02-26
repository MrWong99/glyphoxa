package tier_test

import (
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/internal/mcp/tier"
)

// TestSelect_DeepKeyword verifies that DEEP-tier keywords trigger BudgetDeep.
func TestSelect_DeepKeyword(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()

	// First ensure a clean deep turn (no anti-spam).
	got := s.Select("tell me everything about the Shadowfell conspiracy", 0)
	if got != mcp.BudgetDeep {
		t.Errorf("Select with DEEP keyword = %s, want DEEP", got)
	}
}

// TestSelect_DeepKeyword_InDetail verifies the "in detail" keyword.
func TestSelect_DeepKeyword_InDetail(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()
	got := s.Select("explain the ancient ruins in detail", 0)
	if got != mcp.BudgetDeep {
		t.Errorf("Select = %s, want DEEP", got)
	}
}

// TestSelect_StandardKeyword verifies that STANDARD-tier keywords trigger BudgetStandard.
func TestSelect_StandardKeyword(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()
	s.RecordTurn() // advance past turn 0 so first-turn heuristic doesn't interfere

	got := s.Select("do you remember the tavern in Thornfield?", 0)
	if got != mcp.BudgetStandard {
		t.Errorf("Select with STANDARD keyword = %s, want STANDARD", got)
	}
}

// TestSelect_StandardKeyword_Rules verifies rule-lookup keywords.
func TestSelect_StandardKeyword_Rules(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()
	s.RecordTurn()

	got := s.Select("what are the rules for grappling?", 0)
	if got != mcp.BudgetStandard {
		t.Errorf("Select = %s, want STANDARD", got)
	}
}

// TestSelect_Default_Fast verifies that a normal utterance returns BudgetFast.
func TestSelect_Default_Fast(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()
	s.RecordTurn() // skip first-turn heuristic

	got := s.Select("Good morning, traveller!", 0)
	if got != mcp.BudgetFast {
		t.Errorf("Select normal text = %s, want FAST", got)
	}
}

// TestSelect_FirstTurn_Standard verifies that the very first turn returns STANDARD.
func TestSelect_FirstTurn_Standard(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()

	// turnCount starts at 0 — no keywords, no override.
	got := s.Select("Hello there.", 0)
	if got != mcp.BudgetStandard {
		t.Errorf("Select on first turn = %s, want STANDARD", got)
	}
}

// TestSelect_HighQueueDepth_Fast verifies that a queue depth >= 3 forces FAST
// even when STANDARD keywords are present.
func TestSelect_HighQueueDepth_Fast(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()
	s.RecordTurn() // past turn 0
	s.SetQueueDepth(3)

	got := s.Select("do you remember the old bridge?", 0)
	if got != mcp.BudgetFast {
		t.Errorf("Select with queue depth 3 = %s, want FAST", got)
	}
}

// TestSelect_HighQueueDepth_DoesNotAffectDeep verifies that DEEP keywords still
// win over high queue depth (DEEP > queue heuristic in priority).
func TestSelect_HighQueueDepth_DoesNotAffectDeep(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()
	s.RecordTurn()
	s.SetQueueDepth(5)

	got := s.Select("think carefully about what happened next", 0)
	if got != mcp.BudgetDeep {
		t.Errorf("Select with DEEP keyword + high queue = %s, want DEEP", got)
	}
}

// TestSelect_AntiSpam_DemotesSecondDeepToStandard verifies that two DEEP
// selections within 30 s results in the second being demoted to STANDARD.
func TestSelect_AntiSpam_DemotesSecondDeepToStandard(t *testing.T) {
	t.Parallel()
	// Use a very long interval so the second call is definitely within window.
	s := tier.NewSelector(tier.WithMinDeepInterval(10 * time.Minute))

	first := s.Select("tell me everything about the prophecy", 0)
	if first != mcp.BudgetDeep {
		t.Fatalf("first DEEP selection = %s, want DEEP", first)
	}

	second := s.Select("explain everything that happened yesterday", 0)
	if second != mcp.BudgetStandard {
		t.Errorf("second DEEP selection within interval = %s, want STANDARD (anti-spam)", second)
	}
}

// TestSelect_AntiSpam_AllowsDeepAfterInterval verifies that DEEP is allowed
// again once the minimum interval has elapsed.
func TestSelect_AntiSpam_AllowsDeepAfterInterval(t *testing.T) {
	t.Parallel()
	// Use a tiny interval so we can expire it trivially in tests.
	s := tier.NewSelector(tier.WithMinDeepInterval(time.Millisecond))

	first := s.Select("think carefully about everything", 0)
	if first != mcp.BudgetDeep {
		t.Fatalf("first DEEP selection = %s, want DEEP", first)
	}

	time.Sleep(5 * time.Millisecond) // exceed the 1 ms interval

	second := s.Select("take your time to explain", 0)
	if second != mcp.BudgetDeep {
		t.Errorf("second DEEP selection after interval = %s, want DEEP", second)
	}
}

// TestSelect_DMOverride_AlwaysWins verifies that a non-zero dmOverride
// bypasses all heuristics.
func TestSelect_DMOverride_AlwaysWins(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()

	// Even DEEP keyword text should be overridden by BudgetFast override.
	got := s.Select("think carefully about everything in detail", mcp.BudgetFast)
	// Wait — BudgetFast is the zero value! The spec says "non-zero returns it".
	// So passing BudgetFast (0) as dmOverride means "no override" per the interface.
	// Use BudgetStandard (1) or BudgetDeep (2) to test the override path.

	// Test STANDARD override on a plain-text utterance.
	got = s.Select("hello there", mcp.BudgetStandard)
	if got != mcp.BudgetStandard {
		t.Errorf("DM override STANDARD = %s, want STANDARD", got)
	}

	// Test DEEP override on a plain-text utterance.
	got = s.Select("hello there", mcp.BudgetDeep)
	if got != mcp.BudgetDeep {
		t.Errorf("DM override DEEP = %s, want DEEP", got)
	}
}

// TestSelect_DMOverride_BeatsAntiSpam verifies that a DM override bypasses
// even the anti-spam guard.
func TestSelect_DMOverride_BeatsAntiSpam(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector(tier.WithMinDeepInterval(10 * time.Minute))

	// Trigger anti-spam window.
	s.Select("tell me everything", 0)

	// DM override to DEEP should still work.
	got := s.Select("any text at all", mcp.BudgetDeep)
	if got != mcp.BudgetDeep {
		t.Errorf("DM override DEEP with active anti-spam = %s, want DEEP", got)
	}
}

// TestReset_ClearsState verifies that Reset restores the selector to its
// initial state.
func TestReset_ClearsState(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()

	// Advance state: record a turn and trigger the deep anti-spam.
	s.RecordTurn()
	s.SetQueueDepth(5)
	s.Select("tell me everything", 0) // sets lastDeepTime

	s.Reset()

	// After reset, turnCount == 0 → first turn heuristic applies.
	got := s.Select("hello", 0)
	if got != mcp.BudgetStandard {
		t.Errorf("after Reset, first turn = %s, want STANDARD", got)
	}

	// Anti-spam should be cleared: DEEP should be available immediately.
	got = s.Select("think carefully about everything", 0)
	if got != mcp.BudgetDeep {
		t.Errorf("after Reset, DEEP keyword = %s, want DEEP", got)
	}
}

// TestRecordTurn_AdvancesTurnCount verifies that RecordTurn causes the
// first-turn heuristic to no longer apply.
func TestRecordTurn_AdvancesTurnCount(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()

	// Turn 0 → STANDARD by first-turn heuristic.
	if got := s.Select("just chatting", 0); got != mcp.BudgetStandard {
		t.Errorf("turn 0 = %s, want STANDARD", got)
	}

	s.RecordTurn()

	// Turn 1 → FAST (no keywords).
	if got := s.Select("just chatting", 0); got != mcp.BudgetFast {
		t.Errorf("turn 1 = %s, want FAST", got)
	}
}

// TestSetQueueDepth_BelowThreshold verifies that a queue depth below 3 does
// not force FAST.
func TestSetQueueDepth_BelowThreshold(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()
	s.RecordTurn()
	s.SetQueueDepth(2)

	// STANDARD keyword should still win over queue depth of 2.
	got := s.Select("do you remember the map?", 0)
	if got != mcp.BudgetStandard {
		t.Errorf("queue depth 2 with STANDARD keyword = %s, want STANDARD", got)
	}
}

// TestWithCustomKeywords verifies that custom keywords override the defaults.
func TestWithCustomKeywords(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector(
		tier.WithDeepKeywords("summon the elder gods"),
		tier.WithStandardKeywords("check the ledger"),
	)
	s.RecordTurn()

	if got := s.Select("summon the elder gods from beyond the veil", 0); got != mcp.BudgetDeep {
		t.Errorf("custom DEEP keyword = %s, want DEEP", got)
	}
	if got := s.Select("please check the ledger for me", 0); got != mcp.BudgetStandard {
		t.Errorf("custom STANDARD keyword = %s, want STANDARD", got)
	}
	// Default deep keyword should no longer trigger DEEP.
	if got := s.Select("think carefully about everything", 0); got == mcp.BudgetDeep {
		t.Errorf("overridden default DEEP keyword still triggered DEEP (shouldn't)")
	}
}

// TestSelect_CaseInsensitive verifies keyword matching is case-insensitive.
func TestSelect_CaseInsensitive(t *testing.T) {
	t.Parallel()
	s := tier.NewSelector()

	got := s.Select("TELL ME EVERYTHING about the dragon!", 0)
	if got != mcp.BudgetDeep {
		t.Errorf("uppercase DEEP keyword = %s, want DEEP", got)
	}
}

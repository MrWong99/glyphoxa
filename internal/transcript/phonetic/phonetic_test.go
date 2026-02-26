package phonetic_test

import (
	"testing"

	"github.com/MrWong99/glyphoxa/internal/transcript/phonetic"
)

func TestMatcher_SingleWordMatch(t *testing.T) {
	t.Parallel()

	m := phonetic.New()

	// "elder nacks" is a two-word n-gram that should phonetically match "Eldrinax".
	// Double Metaphone("elder") → primary code overlaps with Double Metaphone("eldrinax")
	// because both share a common leading phoneme cluster.
	entities := []string{"Eldrinax", "Grimjaw", "Tower of Whispers"}

	corrected, conf, matched := m.Match("elder nacks", entities)
	if !matched {
		t.Fatalf("Match(%q, entities): matched=false, want true", "elder nacks")
	}
	if corrected != "Eldrinax" {
		t.Errorf("Match(%q): corrected=%q, want %q", "elder nacks", corrected, "Eldrinax")
	}
	if conf < 0.7 {
		t.Errorf("Match(%q): confidence=%f, want >= 0.7", "elder nacks", conf)
	}
}

func TestMatcher_MultiWordEntityMatch(t *testing.T) {
	t.Parallel()

	m := phonetic.New()

	entities := []string{"Tower of Whispers", "Eldrinax", "Grimjaw"}

	// "tower of wispers" should match the multi-word entity "Tower of Whispers".
	corrected, conf, matched := m.Match("tower of wispers", entities)
	if !matched {
		t.Fatalf("Match(%q, entities): matched=false, want true", "tower of wispers")
	}
	if corrected != "Tower of Whispers" {
		t.Errorf("Match(%q): corrected=%q, want %q", "tower of wispers", corrected, "Tower of Whispers")
	}
	if conf < 0.7 {
		t.Errorf("Match(%q): confidence=%f, want >= 0.7", "tower of wispers", conf)
	}
}

func TestMatcher_NoMatch(t *testing.T) {
	t.Parallel()

	m := phonetic.New()
	entities := []string{"Eldrinax", "Grimjaw"}

	corrected, conf, matched := m.Match("hello", entities)
	if matched {
		t.Fatalf("Match(%q, entities): matched=true, want false", "hello")
	}
	if corrected != "hello" {
		t.Errorf("Match(%q): corrected=%q, want original word %q", "hello", corrected, "hello")
	}
	if conf != 0 {
		t.Errorf("Match(%q): confidence=%f, want 0", "hello", conf)
	}
}

func TestMatcher_CaseInsensitivity(t *testing.T) {
	t.Parallel()

	m := phonetic.New()
	entities := []string{"Eldrinax"}

	// Uppercased input should still match.
	corrected, _, matched := m.Match("ELDRINAX", entities)
	if !matched {
		t.Fatalf("Match(%q, entities): matched=false, want true", "ELDRINAX")
	}
	// Should return the original entity casing.
	if corrected != "Eldrinax" {
		t.Errorf("Match(%q): corrected=%q, want %q", "ELDRINAX", corrected, "Eldrinax")
	}
}

func TestMatcher_ExactMatch(t *testing.T) {
	t.Parallel()

	m := phonetic.New()
	entities := []string{"Grimjaw", "Eldrinax"}

	// Exact case-insensitive match should return high confidence.
	corrected, conf, matched := m.Match("grimjaw", entities)
	if !matched {
		t.Fatalf("Match(%q, entities): matched=false, want true", "grimjaw")
	}
	if corrected != "Grimjaw" {
		t.Errorf("Match(%q): corrected=%q, want %q", "grimjaw", corrected, "Grimjaw")
	}
	if conf < 0.9 {
		t.Errorf("Match(%q): confidence=%f, want >= 0.9 for near-exact match", "grimjaw", conf)
	}
}

func TestMatcher_PhoneticThresholdFiltering(t *testing.T) {
	t.Parallel()

	// Set a very high phonetic threshold so near-matches are rejected.
	m := phonetic.New(
		phonetic.WithPhoneticThreshold(0.99),
		phonetic.WithFuzzyThreshold(0.99),
	)
	entities := []string{"Eldrinax"}

	_, _, matched := m.Match("elder nacks", entities)
	if matched {
		t.Fatal("Match with threshold=0.99 should reject near-matches, got matched=true")
	}
}

func TestMatcher_EmptyEntities(t *testing.T) {
	t.Parallel()

	m := phonetic.New()
	corrected, conf, matched := m.Match("eldrinax", nil)
	if matched {
		t.Fatal("Match with nil entities should return matched=false")
	}
	if corrected != "eldrinax" {
		t.Errorf("corrected=%q, want original", corrected)
	}
	if conf != 0 {
		t.Errorf("conf=%f, want 0", conf)
	}
}

func TestMatcher_EmptyWord(t *testing.T) {
	t.Parallel()

	m := phonetic.New()
	corrected, conf, matched := m.Match("", []string{"Eldrinax"})
	if matched {
		t.Fatal("Match with empty word should return matched=false")
	}
	if corrected != "" {
		t.Errorf("corrected=%q, want empty string", corrected)
	}
	if conf != 0 {
		t.Errorf("conf=%f, want 0", conf)
	}
}

func TestWithOptions(t *testing.T) {
	t.Parallel()

	// Verify that options are applied without panicking.
	m := phonetic.New(
		phonetic.WithPhoneticThreshold(0.75),
		phonetic.WithFuzzyThreshold(0.90),
	)
	if m == nil {
		t.Fatal("New returned nil")
	}
}

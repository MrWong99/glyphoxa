package agent_test

import (
	"testing"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/internal/engine"
	enginemock "github.com/MrWong99/glyphoxa/internal/engine/mock"
	"github.com/MrWong99/glyphoxa/internal/mcp"
	mcpmock "github.com/MrWong99/glyphoxa/internal/mcp/mock"
	audiomock "github.com/MrWong99/glyphoxa/pkg/audio/mock"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

func TestNewLoader(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	loader := agent.NewLoader(assembler, "session-001")
	if loader == nil {
		t.Fatal("NewLoader returned nil")
	}
}

func TestLoader_Load_Valid(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	eng := &enginemock.VoiceEngine{
		ProcessResult: &engine.Response{
			Text:  "Hello.",
			Audio: closedAudioCh(),
		},
	}

	loader := agent.NewLoader(assembler, "session-001")
	a, err := loader.Load("npc-1", testIdentity(), eng, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if a == nil {
		t.Fatal("Load returned nil agent")
	}
	if a.ID() != "npc-1" {
		t.Errorf("ID() = %q, want %q", a.ID(), "npc-1")
	}
	if a.Name() != "Greymantle the Sage" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Greymantle the Sage")
	}
	if a.Engine() != eng {
		t.Error("Engine() does not match the provided engine")
	}
}

func TestLoader_Load_NilAssembler(t *testing.T) {
	t.Parallel()

	loader := agent.NewLoader(nil, "session-001")
	eng := &enginemock.VoiceEngine{}

	_, err := loader.Load("npc-1", testIdentity(), eng, mcp.BudgetFast)
	if err == nil {
		t.Fatal("expected error for nil assembler, got nil")
	}
}

func TestLoader_Load_EmptySessionID(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	loader := agent.NewLoader(assembler, "")
	eng := &enginemock.VoiceEngine{}

	_, err := loader.Load("npc-1", testIdentity(), eng, mcp.BudgetFast)
	if err == nil {
		t.Fatal("expected error for empty session ID, got nil")
	}
}

func TestLoader_Load_EmptyNPCID(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	eng := &enginemock.VoiceEngine{}

	loader := agent.NewLoader(assembler, "session-001")
	_, err := loader.Load("", testIdentity(), eng, mcp.BudgetFast)
	if err == nil {
		t.Fatal("expected error for empty NPC ID, got nil")
	}
}

func TestLoader_Load_NilEngine(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	loader := agent.NewLoader(assembler, "session-001")
	_, err := loader.Load("npc-1", testIdentity(), nil, mcp.BudgetFast)
	if err == nil {
		t.Fatal("expected error for nil engine, got nil")
	}
}

func TestLoader_WithMCPHost(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	eng := &enginemock.VoiceEngine{
		ProcessResult: &engine.Response{
			Text:  "Tool-enabled response.",
			Audio: closedAudioCh(),
		},
	}
	mcpHost := &mcpmock.Host{
		AvailableToolsResult: []llm.ToolDefinition{
			{Name: "dice_roll", Description: "Roll dice"},
		},
	}

	loader := agent.NewLoader(assembler, "session-001", agent.WithMCPHost(mcpHost))
	a, err := loader.Load("npc-1", testIdentity(), eng, mcp.BudgetStandard)
	if err != nil {
		t.Fatalf("Load with MCPHost returned error: %v", err)
	}
	if a == nil {
		t.Fatal("Load returned nil agent")
	}

	// Verify MCPHost was consulted during agent creation.
	if mcpHost.CallCount("AvailableTools") != 1 {
		t.Errorf("expected 1 AvailableTools call, got %d", mcpHost.CallCount("AvailableTools"))
	}

	// Verify tools were set on the engine.
	if len(eng.SetToolsCalls) != 1 {
		t.Errorf("expected 1 SetTools call, got %d", len(eng.SetToolsCalls))
	}

	// Verify tool call handler was registered.
	if eng.CallCountOnToolCall != 1 {
		t.Errorf("expected 1 OnToolCall registration, got %d", eng.CallCountOnToolCall)
	}
}

func TestLoader_WithMixer(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	mixer := &audiomock.Mixer{}
	eng := &enginemock.VoiceEngine{
		ProcessResult: &engine.Response{
			Text:  "Audio response.",
			Audio: closedAudioCh(),
		},
	}

	loader := agent.NewLoader(assembler, "session-001", agent.WithMixer(mixer))
	a, err := loader.Load("npc-1", testIdentity(), eng, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("Load with Mixer returned error: %v", err)
	}
	if a == nil {
		t.Fatal("Load returned nil agent")
	}

	// We can verify the mixer is wired by performing a HandleUtterance and
	// checking that Enqueue was called.
	// (Tested more thoroughly in npc_test.go, just a smoke check here.)
}

func TestLoader_MultipleAgents(t *testing.T) {
	t.Parallel()

	assembler := testAssembler()
	loader := agent.NewLoader(assembler, "session-001")

	eng1 := &enginemock.VoiceEngine{
		ProcessResult: &engine.Response{Text: "A", Audio: closedAudioCh()},
	}
	eng2 := &enginemock.VoiceEngine{
		ProcessResult: &engine.Response{Text: "B", Audio: closedAudioCh()},
	}

	id1 := testIdentity()
	id1.Name = "NPC Alpha"

	id2 := testIdentity()
	id2.Name = "NPC Beta"

	a1, err := loader.Load("npc-alpha", id1, eng1, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("Load npc-alpha: %v", err)
	}
	a2, err := loader.Load("npc-beta", id2, eng2, mcp.BudgetDeep)
	if err != nil {
		t.Fatalf("Load npc-beta: %v", err)
	}

	if a1.ID() == a2.ID() {
		t.Error("expected different IDs for different agents")
	}
	if a1.Name() != "NPC Alpha" {
		t.Errorf("a1.Name() = %q, want %q", a1.Name(), "NPC Alpha")
	}
	if a2.Name() != "NPC Beta" {
		t.Errorf("a2.Name() = %q, want %q", a2.Name(), "NPC Beta")
	}
}

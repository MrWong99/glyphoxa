package bridge_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	mckmock "github.com/MrWong99/glyphoxa/internal/mcp/mock"
	"github.com/MrWong99/glyphoxa/internal/mcp/bridge"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	s2smock "github.com/MrWong99/glyphoxa/pkg/provider/s2s/mock"
)

// newSession returns a mock Session ready for use in tests.
func newSession() *s2smock.Session {
	return &s2smock.Session{
		AudioCh:       make(chan []byte, 8),
		TranscriptsCh: make(chan memory.TranscriptEntry, 4),
	}
}

// TestNewBridge_CallsSetTools verifies that NewBridge immediately declares
// the tier-appropriate tool set on the session.
func TestNewBridge_CallsSetTools(t *testing.T) {
	t.Parallel()
	tools := []llm.ToolDefinition{
		{Name: "dice_roller", Description: "Roll dice"},
	}
	host := &mckmock.Host{AvailableToolsResult: tools}
	sess := newSession()

	_, err := bridge.NewBridge(host, sess, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("NewBridge returned unexpected error: %v", err)
	}

	if got := len(sess.SetToolsCalls); got != 1 {
		t.Fatalf("expected 1 SetTools call, got %d", got)
	}
	got := sess.SetToolsCalls[0].Tools
	if len(got) != 1 || got[0].Name != "dice_roller" {
		t.Errorf("unexpected tools declared on session: %v", got)
	}
}

// TestNewBridge_RegistersToolCallHandler verifies that NewBridge registers a
// non-nil ToolCallHandler on the session.
func TestNewBridge_RegistersToolCallHandler(t *testing.T) {
	t.Parallel()
	host := &mckmock.Host{}
	sess := newSession()

	_, err := bridge.NewBridge(host, sess, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("NewBridge returned unexpected error: %v", err)
	}

	if sess.Handler() == nil {
		t.Error("expected a ToolCallHandler to be registered after NewBridge, got nil")
	}
	if got := sess.OnToolCallSetCount; got != 1 {
		t.Errorf("expected OnToolCall to be called once, got %d", got)
	}
}

// TestNewBridge_ToolCallRoutedThroughHost verifies that when the S2S model
// triggers a tool call, the bridge executes it via host.ExecuteTool and
// returns the content string.
func TestNewBridge_ToolCallRoutedThroughHost(t *testing.T) {
	t.Parallel()
	host := &mckmock.Host{
		ExecuteToolResult: &mcp.ToolResult{Content: `{"name":"Eldrinax","race":"elf"}`},
	}
	sess := newSession()

	_, err := bridge.NewBridge(host, sess, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("NewBridge returned unexpected error: %v", err)
	}

	handler := sess.Handler()
	if handler == nil {
		t.Fatal("ToolCallHandler is nil — cannot invoke tool")
	}

	result, err := handler("lookup_npc", `{"id":"42"}`)
	if err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}
	if want := `{"name":"Eldrinax","race":"elf"}`; result != want {
		t.Errorf("handler result = %q, want %q", result, want)
	}

	if got := host.CallCount("ExecuteTool"); got != 1 {
		t.Errorf("expected 1 ExecuteTool call, got %d", got)
	}
	calls := host.Calls()
	var execCall *mckmock.Call
	for i := range calls {
		if calls[i].Method == "ExecuteTool" {
			execCall = &calls[i]
			break
		}
	}
	if execCall == nil {
		t.Fatal("ExecuteTool call not recorded")
	}
	if execCall.Args[0] != "lookup_npc" {
		t.Errorf("ExecuteTool name = %q, want %q", execCall.Args[0], "lookup_npc")
	}
	if execCall.Args[1] != `{"id":"42"}` {
		t.Errorf("ExecuteTool args = %q, want %q", execCall.Args[1], `{"id":"42"}`)
	}
}

// TestNewBridge_ToolCallError verifies that ExecuteTool errors are surfaced as
// handler errors.
func TestNewBridge_ToolCallError(t *testing.T) {
	t.Parallel()
	host := &mckmock.Host{
		ExecuteToolErr: errors.New("tool server unavailable"),
	}
	sess := newSession()

	_, err := bridge.NewBridge(host, sess, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("NewBridge returned unexpected error: %v", err)
	}

	handler := sess.Handler()
	_, err = handler("broken_tool", `{}`)
	if err == nil {
		t.Fatal("expected handler to return an error when ExecuteTool fails")
	}
}

// TestBridge_UpdateTier verifies that UpdateTier fetches the new tier's tools
// and updates the session.
func TestBridge_UpdateTier(t *testing.T) {
	t.Parallel()
	fastTools := []llm.ToolDefinition{{Name: "dice_roller"}}
	deepTools := []llm.ToolDefinition{{Name: "dice_roller"}, {Name: "web_search"}}

	host := &mckmock.Host{AvailableToolsResult: fastTools}
	sess := newSession()

	b, err := bridge.NewBridge(host, sess, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("NewBridge returned unexpected error: %v", err)
	}

	// Simulate host returning richer tool set for DEEP tier.
	host.AvailableToolsResult = deepTools

	if err := b.UpdateTier(context.Background(), mcp.BudgetDeep); err != nil {
		t.Fatalf("UpdateTier returned unexpected error: %v", err)
	}

	// Expect two SetTools calls: initial + update.
	if got := len(sess.SetToolsCalls); got != 2 {
		t.Fatalf("expected 2 SetTools calls, got %d", got)
	}
	updated := sess.SetToolsCalls[1].Tools
	if len(updated) != 2 {
		t.Errorf("expected 2 tools after UpdateTier to DEEP, got %d: %v", len(updated), updated)
	}
}

// TestBridge_UpdateTier_CancelledContext verifies that UpdateTier respects a
// cancelled context and does not mutate the session.
func TestBridge_UpdateTier_CancelledContext(t *testing.T) {
	t.Parallel()
	host := &mckmock.Host{}
	sess := newSession()

	b, err := bridge.NewBridge(host, sess, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("NewBridge returned unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if err := b.UpdateTier(ctx, mcp.BudgetDeep); err == nil {
		t.Error("expected UpdateTier to return an error for a cancelled context")
	}

	// Only the initial SetTools call should have happened.
	if got := len(sess.SetToolsCalls); got != 1 {
		t.Errorf("expected 1 SetTools call (no update), got %d", got)
	}
}

// TestBridge_Close verifies that Close deregisters the ToolCallHandler.
func TestBridge_Close(t *testing.T) {
	t.Parallel()
	host := &mckmock.Host{}
	sess := newSession()

	b, err := bridge.NewBridge(host, sess, mcp.BudgetFast)
	if err != nil {
		t.Fatalf("NewBridge returned unexpected error: %v", err)
	}

	b.Close()

	if got := sess.Handler(); got != nil {
		t.Error("expected ToolCallHandler to be nil after Close")
	}
}

// TestBridge_WithToolTimeout verifies that the timeout option is accepted without error.
func TestBridge_WithToolTimeout(t *testing.T) {
	t.Parallel()
	host := &mckmock.Host{}
	sess := newSession()

	_, err := bridge.NewBridge(host, sess, mcp.BudgetFast, bridge.WithToolTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("NewBridge with custom timeout returned unexpected error: %v", err)
	}
}

// TestNewBridge_NilHost verifies that NewBridge rejects a nil host.
func TestNewBridge_NilHost(t *testing.T) {
	t.Parallel()
	sess := newSession()
	_, err := bridge.NewBridge(nil, sess, mcp.BudgetFast)
	if err == nil {
		t.Error("expected error for nil host, got nil")
	}
}

// TestNewBridge_NilSession verifies that NewBridge rejects a nil session.
func TestNewBridge_NilSession(t *testing.T) {
	t.Parallel()
	host := &mckmock.Host{}
	_, err := bridge.NewBridge(host, nil, mcp.BudgetFast)
	if err == nil {
		t.Error("expected error for nil session, got nil")
	}
}

package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/app"
	"github.com/MrWong99/glyphoxa/internal/config"
	"github.com/MrWong99/glyphoxa/internal/entity"
	audiomock "github.com/MrWong99/glyphoxa/pkg/audio/mock"
	memorymock "github.com/MrWong99/glyphoxa/pkg/memory/mock"
)

func newTestSessionManager() (*app.SessionManager, *audiomock.Platform, *audiomock.Connection) {
	conn := &audiomock.Connection{}
	platform := &audiomock.Platform{ConnectResult: conn}
	cfg := &config.Config{
		Campaign: config.CampaignConfig{
			Name: "Ironhold",
		},
	}
	providers := &app.Providers{}
	store := &memorymock.SessionStore{}

	sm := app.NewSessionManager(app.SessionManagerConfig{
		Platform:     platform,
		Config:       cfg,
		Providers:    providers,
		SessionStore: store,
	})
	return sm, platform, conn
}

func TestSessionManager_StartStop(t *testing.T) {
	t.Parallel()

	sm, platform, conn := newTestSessionManager()

	ctx := context.Background()
	if err := sm.Start(ctx, "voice-channel-1", "dm-user-1"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if !sm.IsActive() {
		t.Fatal("expected session to be active after Start")
	}

	info := sm.Info()
	if info.ChannelID != "voice-channel-1" {
		t.Errorf("ChannelID = %q, want %q", info.ChannelID, "voice-channel-1")
	}
	if info.StartedBy != "dm-user-1" {
		t.Errorf("StartedBy = %q, want %q", info.StartedBy, "dm-user-1")
	}
	if info.CampaignName != "Ironhold" {
		t.Errorf("CampaignName = %q, want %q", info.CampaignName, "Ironhold")
	}
	if info.SessionID == "" {
		t.Error("SessionID should not be empty")
	}

	// Platform should have been called with the channel ID.
	if len(platform.ConnectCalls) != 1 {
		t.Fatalf("Connect calls = %d, want 1", len(platform.ConnectCalls))
	}
	if platform.ConnectCalls[0].ChannelID != "voice-channel-1" {
		t.Errorf("Connect channelID = %q, want %q", platform.ConnectCalls[0].ChannelID, "voice-channel-1")
	}

	if err := sm.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if sm.IsActive() {
		t.Fatal("expected session to be inactive after Stop")
	}

	// Connection should have been disconnected.
	if conn.CallCountDisconnect != 1 {
		t.Errorf("Disconnect calls = %d, want 1", conn.CallCountDisconnect)
	}
}

func TestSessionManager_DoubleStart(t *testing.T) {
	t.Parallel()

	sm, _, _ := newTestSessionManager()

	ctx := context.Background()
	if err := sm.Start(ctx, "ch-1", "user-1"); err != nil {
		t.Fatalf("first Start() error: %v", err)
	}

	err := sm.Start(ctx, "ch-2", "user-2")
	if err == nil {
		t.Fatal("second Start() should return error")
	}
}

func TestSessionManager_StopWithoutStart(t *testing.T) {
	t.Parallel()

	sm, _, _ := newTestSessionManager()

	err := sm.Stop(context.Background())
	if err == nil {
		t.Fatal("Stop() without Start should return error")
	}
}

func TestSessionManager_IsActive(t *testing.T) {
	t.Parallel()

	sm, _, _ := newTestSessionManager()

	if sm.IsActive() {
		t.Fatal("expected inactive before Start")
	}

	if err := sm.Start(context.Background(), "ch-1", "user-1"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if !sm.IsActive() {
		t.Fatal("expected active after Start")
	}

	if err := sm.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if sm.IsActive() {
		t.Fatal("expected inactive after Stop")
	}
}

func TestSessionManager_Info(t *testing.T) {
	t.Parallel()

	sm, _, _ := newTestSessionManager()

	// Info before start should be zero value.
	info := sm.Info()
	if info.SessionID != "" {
		t.Errorf("SessionID before start = %q, want empty", info.SessionID)
	}

	before := time.Now()
	if err := sm.Start(context.Background(), "ch-1", "user-1"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	after := time.Now()

	info = sm.Info()
	if info.SessionID == "" {
		t.Error("SessionID should not be empty after start")
	}
	if info.StartedAt.Before(before) || info.StartedAt.After(after) {
		t.Errorf("StartedAt = %v, expected between %v and %v", info.StartedAt, before, after)
	}
	if info.CampaignName != "Ironhold" {
		t.Errorf("CampaignName = %q, want %q", info.CampaignName, "Ironhold")
	}

	// Orchestrator should be non-nil while active.
	if sm.Orchestrator() == nil {
		t.Error("Orchestrator() should not be nil while session is active")
	}

	if err := sm.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	// Info after stop should be zero value.
	info = sm.Info()
	if info.SessionID != "" {
		t.Errorf("SessionID after stop = %q, want empty", info.SessionID)
	}

	// Orchestrator should be nil after stop.
	if sm.Orchestrator() != nil {
		t.Error("Orchestrator() should be nil after Stop")
	}
}

func TestSessionManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	sm, _, _ := newTestSessionManager()

	// Start a session first.
	if err := sm.Start(context.Background(), "ch-1", "user-1"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Concurrent reads should not panic.
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = sm.IsActive()
		}()
		go func() {
			defer wg.Done()
			_ = sm.Info()
		}()
		go func() {
			defer wg.Done()
			_ = sm.Orchestrator()
		}()
	}
	wg.Wait()

	if err := sm.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestSessionManager_StopCallsConsolidation(t *testing.T) {
	t.Parallel()

	conn := &audiomock.Connection{}
	platform := &audiomock.Platform{ConnectResult: conn}
	store := &memorymock.SessionStore{}
	cfg := &config.Config{
		Campaign: config.CampaignConfig{Name: "TestCampaign"},
	}

	sm := app.NewSessionManager(app.SessionManagerConfig{
		Platform:     platform,
		Config:       cfg,
		Providers:    &app.Providers{},
		SessionStore: store,
	})

	ctx := context.Background()
	if err := sm.Start(ctx, "ch-1", "user-1"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Stop should call consolidation (ConsolidateNow). Since the context
	// manager has no messages, the consolidator won't write anything, but
	// the stop should complete without error.
	if err := sm.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if sm.IsActive() {
		t.Fatal("expected inactive after Stop")
	}
}

func TestSessionManager_SessionIDFormat(t *testing.T) {
	t.Parallel()

	conn := &audiomock.Connection{}
	platform := &audiomock.Platform{ConnectResult: conn}
	cfg := &config.Config{
		Campaign: config.CampaignConfig{Name: "Curse of Strahd"},
	}

	sm := app.NewSessionManager(app.SessionManagerConfig{
		Platform:     platform,
		Config:       cfg,
		Providers:    &app.Providers{},
		SessionStore: &memorymock.SessionStore{},
	})

	if err := sm.Start(context.Background(), "ch-1", "user-1"); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	info := sm.Info()
	// Session ID should contain sanitized campaign name.
	if info.SessionID == "" {
		t.Fatal("SessionID should not be empty")
	}
	// Should start with "session-curse-of-strahd-"
	want := "session-curse-of-strahd-"
	if len(info.SessionID) < len(want) || info.SessionID[:len(want)] != want {
		t.Errorf("SessionID = %q, want prefix %q", info.SessionID, want)
	}

	if err := sm.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestSessionManager_PropagateEntity(t *testing.T) {
	t.Parallel()

	conn := &audiomock.Connection{}
	platform := &audiomock.Platform{ConnectResult: conn}
	store := &memorymock.SessionStore{}
	graph := &memorymock.KnowledgeGraph{}
	entities := entity.NewMemStore()
	cfg := &config.Config{
		Campaign: config.CampaignConfig{Name: "TestCampaign"},
	}

	sm := app.NewSessionManager(app.SessionManagerConfig{
		Platform:     platform,
		Config:       cfg,
		Providers:    &app.Providers{},
		SessionStore: store,
		Graph:        graph,
		Entities:     entities,
	})

	def := entity.EntityDefinition{
		Name:        "Gundren Rockseeker",
		Type:        entity.EntityNPC,
		Description: "A dwarf merchant.",
		Tags:        []string{"ally", "phandalin"},
	}

	ctx := context.Background()
	stored, err := sm.PropagateEntity(ctx, def)
	if err != nil {
		t.Fatalf("PropagateEntity() error: %v", err)
	}

	if stored.ID == "" {
		t.Error("expected generated ID, got empty")
	}
	if stored.Name != "Gundren Rockseeker" {
		t.Errorf("Name = %q, want %q", stored.Name, "Gundren Rockseeker")
	}

	// Verify entity was persisted in the store.
	got, err := entities.Get(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Name != "Gundren Rockseeker" {
		t.Errorf("stored Name = %q, want %q", got.Name, "Gundren Rockseeker")
	}

	// Verify entity was added to the knowledge graph.
	calls := graph.Calls()
	found := false
	for _, c := range calls {
		if c.Method == "AddEntity" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected AddEntity call on KnowledgeGraph")
	}
}

func TestSessionManager_PropagateEntity_NoStore(t *testing.T) {
	t.Parallel()

	conn := &audiomock.Connection{}
	platform := &audiomock.Platform{ConnectResult: conn}
	cfg := &config.Config{
		Campaign: config.CampaignConfig{Name: "TestCampaign"},
	}

	// No entity store provided.
	sm := app.NewSessionManager(app.SessionManagerConfig{
		Platform:     platform,
		Config:       cfg,
		Providers:    &app.Providers{},
		SessionStore: &memorymock.SessionStore{},
	})

	def := entity.EntityDefinition{
		Name: "Test",
		Type: entity.EntityNPC,
	}

	_, err := sm.PropagateEntity(context.Background(), def)
	if err == nil {
		t.Fatal("expected error when entity store is nil")
	}
}

func TestSessionManager_PropagateEntity_NoGraph(t *testing.T) {
	t.Parallel()

	conn := &audiomock.Connection{}
	platform := &audiomock.Platform{ConnectResult: conn}
	entities := entity.NewMemStore()
	cfg := &config.Config{
		Campaign: config.CampaignConfig{Name: "TestCampaign"},
	}

	// Entity store present but no knowledge graph.
	sm := app.NewSessionManager(app.SessionManagerConfig{
		Platform:     platform,
		Config:       cfg,
		Providers:    &app.Providers{},
		SessionStore: &memorymock.SessionStore{},
		Entities:     entities,
	})

	def := entity.EntityDefinition{
		Name:        "Test Entity",
		Type:        entity.EntityLocation,
		Description: "A test location.",
	}

	stored, err := sm.PropagateEntity(context.Background(), def)
	if err != nil {
		t.Fatalf("PropagateEntity() error: %v", err)
	}
	if stored.ID == "" {
		t.Error("expected generated ID")
	}

	// Verify entity was still persisted even without graph.
	got, err := entities.Get(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Name != "Test Entity" {
		t.Errorf("stored Name = %q, want %q", got.Name, "Test Entity")
	}
}

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/app"
	"github.com/MrWong99/glyphoxa/internal/config"
	mcpmock "github.com/MrWong99/glyphoxa/internal/mcp/mock"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	audiomock "github.com/MrWong99/glyphoxa/pkg/audio/mock"
	memorymock "github.com/MrWong99/glyphoxa/pkg/memory/mock"
	llmmock "github.com/MrWong99/glyphoxa/pkg/provider/llm/mock"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	sttmock "github.com/MrWong99/glyphoxa/pkg/provider/stt/mock"
	ttsmock "github.com/MrWong99/glyphoxa/pkg/provider/tts/mock"
	vadmock "github.com/MrWong99/glyphoxa/pkg/provider/vad/mock"
)

// testConfig returns a minimal config with one cascaded NPC for tests.
func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			ListenAddr: "test-channel",
			LogLevel:   config.LogInfo,
		},
		NPCs: []config.NPCConfig{
			{
				Name:        "Grimjaw",
				Personality: "A gruff dwarven bartender.",
				Engine:      config.EngineCascaded,
				BudgetTier:  config.BudgetTierFast,
				Voice: config.VoiceConfig{
					Provider: "test",
					VoiceID:  "dwarf-1",
				},
			},
		},
		Campaign: config.CampaignConfig{
			Name: "test-campaign",
		},
	}
}

// testProviders returns providers with mock LLM/TTS for a cascaded engine.
func testProviders() *app.Providers {
	return &app.Providers{
		LLM: &llmmock.Provider{},
		TTS: &ttsmock.Provider{},
	}
}

func TestNew_WithMocks(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	providers := testProviders()
	sessions := &memorymock.SessionStore{}
	graph := &memorymock.KnowledgeGraph{}
	mcpHost := &mcpmock.Host{}
	mixer := &audiomock.Mixer{}

	application, err := app.New(
		context.Background(),
		cfg,
		providers,
		app.WithSessionStore(sessions),
		app.WithKnowledgeGraph(graph),
		app.WithMCPHost(mcpHost),
		app.WithMixer(mixer),
	)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if application == nil {
		t.Fatal("New() returned nil app")
	}

	// MCP host should have been calibrated during New().
	if got := mcpHost.CallCount("Calibrate"); got != 1 {
		t.Errorf("Calibrate call count = %d, want 1", got)
	}
}

func TestNew_NoNPCs(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.NPCs = nil

	providers := testProviders()
	sessions := &memorymock.SessionStore{}
	graph := &memorymock.KnowledgeGraph{}
	mcpHost := &mcpmock.Host{}
	mixer := &audiomock.Mixer{}

	application, err := app.New(
		context.Background(),
		cfg,
		providers,
		app.WithSessionStore(sessions),
		app.WithKnowledgeGraph(graph),
		app.WithMCPHost(mcpHost),
		app.WithMixer(mixer),
	)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if application == nil {
		t.Fatal("New() returned nil app")
	}
}

func TestApp_Shutdown(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	providers := testProviders()
	sessions := &memorymock.SessionStore{}
	graph := &memorymock.KnowledgeGraph{}
	mcpHost := &mcpmock.Host{}
	mixer := &audiomock.Mixer{}

	application, err := app.New(
		context.Background(),
		cfg,
		providers,
		app.WithSessionStore(sessions),
		app.WithKnowledgeGraph(graph),
		app.WithMCPHost(mcpHost),
		app.WithMixer(mixer),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := application.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error: %v", err)
	}

	// MCP host Close should have been called during shutdown.
	if got := mcpHost.CallCount("Close"); got != 1 {
		t.Errorf("MCP Host Close call count = %d, want 1", got)
	}
}

func TestApp_RunAndShutdown(t *testing.T) {
	t.Parallel()

	cfg := testConfig()

	// STT mock: returns a session that we control.
	sttSession := &sttmock.Session{
		PartialsCh: make(chan stt.Transcript, 16),
		FinalsCh:   make(chan stt.Transcript, 16),
	}

	// VAD mock: always returns speech so audio flows through.
	vadSession := &vadmock.Session{}

	providers := &app.Providers{
		LLM: &llmmock.Provider{},
		TTS: &ttsmock.Provider{},
		STT: &sttmock.Provider{Session: sttSession},
		VAD: &vadmock.Engine{Session: vadSession},
	}

	// Audio mock: a connection with one participant.
	inputCh := make(chan audio.AudioFrame, 16)
	conn := &audiomock.Connection{
		InputStreamsResult: map[string]<-chan audio.AudioFrame{
			"player-1": inputCh,
		},
	}
	providers.Audio = &audiomock.Platform{ConnectResult: conn}

	sessions := &memorymock.SessionStore{}
	graph := &memorymock.KnowledgeGraph{}
	mcpHost := &mcpmock.Host{}
	mixer := &audiomock.Mixer{}

	application, err := app.New(
		context.Background(),
		cfg,
		providers,
		app.WithSessionStore(sessions),
		app.WithKnowledgeGraph(graph),
		app.WithMCPHost(mcpHost),
		app.WithMixer(mixer),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Run in background.
	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	// Give Run a moment to set up goroutines.
	time.Sleep(50 * time.Millisecond)

	// Push an audio frame into the pipeline.
	inputCh <- audio.AudioFrame{
		Data:       []byte{0x01, 0x02, 0x03, 0x04},
		SampleRate: 48000,
		Channels:   1,
	}

	// Give the pipeline time to process.
	time.Sleep(100 * time.Millisecond)

	// VAD should have processed at least one frame.
	if got := len(vadSession.ProcessFrameCalls); got < 1 {
		t.Errorf("VAD ProcessFrame calls = %d, want >= 1", got)
	}

	// STT should have received audio.
	if got := len(sttSession.SendAudioCalls); got < 1 {
		t.Errorf("STT SendAudio calls = %d, want >= 1", got)
	}

	// Cancel context to trigger shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run() returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s after context cancellation")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error: %v", err)
	}

	// Audio connection should have been disconnected.
	if got := conn.CallCountDisconnect; got != 1 {
		t.Errorf("Connection Disconnect call count = %d, want 1", got)
	}
}

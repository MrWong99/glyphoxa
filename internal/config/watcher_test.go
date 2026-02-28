package config_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/config"
)

const watcherValidYAML = `
server:
  log_level: info
providers:
  llm:
    name: openai
  tts:
    name: elevenlabs
memory:
  postgres_dsn: "postgres://localhost/test"
npcs:
  - name: Watcher NPC
    engine: cascaded
`

const watcherUpdatedYAML = `
server:
  log_level: debug
providers:
  llm:
    name: openai
  tts:
    name: elevenlabs
memory:
  postgres_dsn: "postgres://localhost/test"
npcs:
  - name: Updated NPC
    engine: cascaded
`

const watcherInvalidYAML = `
server:
  log_level: bananas
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file %q: %v", path, err)
	}
}

func TestWatcher_InitialLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, watcherValidYAML)

	w, err := config.NewWatcher(cfgPath, nil, config.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Stop()

	cfg := w.Current()
	if cfg == nil {
		t.Fatal("Current() returned nil after initial load")
	}
	if cfg.Server.LogLevel != config.LogInfo {
		t.Errorf("log_level: got %q, want %q", cfg.Server.LogLevel, config.LogInfo)
	}
}

func TestWatcher_DetectsChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, watcherValidYAML)

	var mu sync.Mutex
	var callbackOld, callbackNew *config.Config
	called := make(chan struct{}, 1)

	w, err := config.NewWatcher(cfgPath, func(old, new *config.Config) {
		mu.Lock()
		callbackOld = old
		callbackNew = new
		mu.Unlock()
		select {
		case called <- struct{}{}:
		default:
		}
	}, config.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Stop()

	// Give the initial poll a moment, then update the file.
	time.Sleep(100 * time.Millisecond)
	writeFile(t, cfgPath, watcherUpdatedYAML)

	// Wait for callback.
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("callback was not invoked within timeout")
	}

	mu.Lock()
	defer mu.Unlock()

	if callbackOld == nil || callbackNew == nil {
		t.Fatal("callback received nil configs")
	}
	if callbackOld.Server.LogLevel != config.LogInfo {
		t.Errorf("old log_level: got %q, want %q", callbackOld.Server.LogLevel, config.LogInfo)
	}
	if callbackNew.Server.LogLevel != config.LogDebug {
		t.Errorf("new log_level: got %q, want %q", callbackNew.Server.LogLevel, config.LogDebug)
	}

	// Current should return the new config.
	cur := w.Current()
	if cur.Server.LogLevel != config.LogDebug {
		t.Errorf("Current() log_level: got %q, want %q", cur.Server.LogLevel, config.LogDebug)
	}
}

func TestWatcher_InvalidFileKeepsOldConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, watcherValidYAML)

	callCount := 0
	var mu sync.Mutex

	w, err := config.NewWatcher(cfgPath, func(old, new *config.Config) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}, config.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Stop()

	// Write invalid config.
	time.Sleep(100 * time.Millisecond)
	writeFile(t, cfgPath, watcherInvalidYAML)

	// Wait enough polls for it to notice the change.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	calls := callCount
	mu.Unlock()

	if calls != 0 {
		t.Errorf("callback should not be called for invalid config, got %d calls", calls)
	}

	// Current should still be the old valid config.
	cur := w.Current()
	if cur.Server.LogLevel != config.LogInfo {
		t.Errorf("Current() should still have old config, got log_level=%q", cur.Server.LogLevel)
	}
}

func TestWatcher_InitialLoadFails(t *testing.T) {
	t.Parallel()
	_, err := config.NewWatcher("/nonexistent/path.yaml", nil)
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestWatcher_StopIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, watcherValidYAML)

	w, err := config.NewWatcher(cfgPath, nil, config.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Multiple stops should not panic.
	w.Stop()
	w.Stop()
	w.Stop()
}

func TestWatcher_TouchWithoutContentChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, watcherValidYAML)

	callCount := 0
	var mu sync.Mutex

	w, err := config.NewWatcher(cfgPath, func(old, new *config.Config) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}, config.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Stop()

	// Touch the file (update mtime) without changing content.
	time.Sleep(100 * time.Millisecond)
	now := time.Now().Add(time.Second)
	if err := os.Chtimes(cfgPath, now, now); err != nil {
		t.Fatalf("failed to touch file: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	calls := callCount
	mu.Unlock()

	if calls != 0 {
		t.Errorf("callback should not fire for touch-only, got %d calls", calls)
	}
}

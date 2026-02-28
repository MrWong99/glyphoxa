package config

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Watcher monitors a config file for changes and calls a callback when the
// file is modified. It uses polling (not fsnotify) to keep dependencies minimal.
type Watcher struct {
	path     string
	interval time.Duration
	onChange func(old, new *Config)

	mu       sync.Mutex
	current  *Config
	done     chan struct{}
	stopOnce sync.Once

	// last known file state for change detection
	lastMtime time.Time
	lastHash  [sha256.Size]byte
}

// WatcherOption configures a [Watcher].
type WatcherOption func(*Watcher)

// WithInterval sets the polling interval. The default is 5 seconds.
func WithInterval(d time.Duration) WatcherOption {
	return func(w *Watcher) {
		if d > 0 {
			w.interval = d
		}
	}
}

// NewWatcher creates a config file watcher. It loads the initial config
// immediately and starts polling in a background goroutine.
func NewWatcher(path string, onChange func(old, new *Config), opts ...WatcherOption) (*Watcher, error) {
	w := &Watcher{
		path:     path,
		interval: 5 * time.Second,
		onChange: onChange,
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}

	// Load initial config.
	cfg, hash, mtime, err := w.loadAndHash()
	if err != nil {
		return nil, fmt.Errorf("config: watcher initial load: %w", err)
	}
	w.current = cfg
	w.lastHash = hash
	w.lastMtime = mtime

	go w.poll()
	return w, nil
}

// Current returns the most recently loaded valid config.
func (w *Watcher) Current() *Config {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current
}

// Stop stops the file watcher.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
	})
}

// poll runs in a background goroutine, checking the config file periodically.
func (w *Watcher) poll() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.check()
		}
	}
}

// check reads the config file and, if it has changed and is valid, calls
// onChange and updates the current config.
func (w *Watcher) check() {
	// Quick mtime check first to avoid hashing unchanged files.
	info, err := os.Stat(w.path)
	if err != nil {
		slog.Warn("config watcher: cannot stat file", "path", w.path, "err", err)
		return
	}

	w.mu.Lock()
	mtime := w.lastMtime
	w.mu.Unlock()

	if info.ModTime().Equal(mtime) {
		return
	}

	// Mtime changed — read and hash.
	cfg, hash, newMtime, err := w.loadAndHash()
	if err != nil {
		slog.Warn("config watcher: failed to load config", "path", w.path, "err", err)
		return
	}

	w.mu.Lock()

	if hash == w.lastHash {
		// File was touched but content is identical.
		w.lastMtime = newMtime
		w.mu.Unlock()
		return
	}

	old := w.current
	w.current = cfg
	w.lastHash = hash
	w.lastMtime = newMtime
	w.mu.Unlock()

	slog.Info("config watcher: configuration reloaded", "path", w.path)

	// Invoke the callback outside the lock so it can safely call Current().
	if w.onChange != nil {
		w.onChange(old, cfg)
	}
}

// loadAndHash reads the config file, parses + validates it, and returns the
// config alongside the file's SHA-256 hash and modification time. If the
// config is invalid, it returns an error (the caller should keep the old one).
func (w *Watcher) loadAndHash() (*Config, [sha256.Size]byte, time.Time, error) {
	var zeroHash [sha256.Size]byte

	f, err := os.Open(w.path)
	if err != nil {
		return nil, zeroHash, time.Time{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, zeroHash, time.Time{}, err
	}

	// Read full file into memory for hashing + parsing.
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, zeroHash, time.Time{}, err
	}

	hash := sha256.Sum256(data)

	// Parse using a bytes reader so we don't re-read the file.
	cfg, err := LoadFromReader(bytesReader(data))
	if err != nil {
		return nil, zeroHash, time.Time{}, err
	}

	return cfg, hash, info.ModTime(), nil
}

// bytesReader wraps a byte slice in a minimal io.Reader.
type bytesReaderImpl struct {
	data []byte
	pos  int
}

func bytesReader(b []byte) io.Reader {
	return &bytesReaderImpl{data: b}
}

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

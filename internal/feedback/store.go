// Package feedback provides a simple feedback storage layer for the
// Glyphoxa closed alpha. Feedback is stored as append-only JSON lines
// in a local file, suitable for a small number of alpha testers.
//
// For production use, this should be replaced with a PostgreSQL-backed
// implementation.
package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/internal/discord/commands"
)

// Compile-time interface check.
var _ commands.FeedbackStore = (*FileStore)(nil)

// Record is a single feedback entry written to the file store.
type Record struct {
	Timestamp      time.Time         `json:"timestamp"`
	SessionID      string            `json:"session_id"`
	UserID         string            `json:"user_id"`
	VoiceLatency   int               `json:"voice_latency"`
	NPCPersonality int               `json:"npc_personality"`
	MemoryAccuracy int               `json:"memory_accuracy"`
	DMWorkflow     int               `json:"dm_workflow"`
	Comments       string            `json:"comments,omitempty"`
}

// FileStore persists feedback as JSON lines in a local file.
// Thread-safe for concurrent use.
type FileStore struct {
	mu   sync.Mutex
	path string
}

// NewFileStore creates a FileStore that writes to the given path.
// The file is created if it does not exist.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// SaveFeedback appends a feedback record to the file.
func (fs *FileStore) SaveFeedback(sessionID string, fb commands.Feedback) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	record := Record{
		Timestamp:      time.Now().UTC(),
		SessionID:      sessionID,
		UserID:         fb.UserID,
		VoiceLatency:   fb.VoiceLatency,
		NPCPersonality: fb.NPCPersonality,
		MemoryAccuracy: fb.MemoryAccuracy,
		DMWorkflow:     fb.DMWorkflow,
		Comments:       fb.Comments,
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("feedback: marshal: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(fs.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("feedback: open file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("feedback: write: %w", err)
	}
	return nil
}

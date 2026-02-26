// Package fileio provides built-in MCP tools for sandboxed file reading and
// writing. All file paths are resolved relative to a configured base directory;
// path traversal attempts (e.g. "../") are rejected with an error.
//
// Two tools are exported via [NewTools]:
//   - "write_file" — write text content to a file (creates directories as needed).
//   - "read_file"  — read a file and return its text content.
//
// All handlers are safe for concurrent use.
package fileio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MrWong99/glyphoxa/internal/mcp/tools"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

const (
	// maxReadBytes is the maximum file size that read_file will return.
	// Files larger than this limit are rejected with an error.
	maxReadBytes = 1 << 20 // 1 MiB
)

// writeFileArgs is the JSON-decoded input for the "write_file" tool.
type writeFileArgs struct {
	// Path is the file path relative to the sandbox base directory.
	Path string `json:"path"`

	// Content is the text content to write.
	Content string `json:"content"`
}

// writeFileResult is the JSON-encoded output of the "write_file" tool.
type writeFileResult struct {
	// Path is the relative path of the written file, echoed back to the caller.
	Path string `json:"path"`

	// BytesWritten is the number of bytes written.
	BytesWritten int `json:"bytes_written"`
}

// readFileArgs is the JSON-decoded input for the "read_file" tool.
type readFileArgs struct {
	// Path is the file path relative to the sandbox base directory.
	Path string `json:"path"`
}

// readFileResult is the JSON-encoded output of the "read_file" tool.
type readFileResult struct {
	// Path is the relative path of the file that was read.
	Path string `json:"path"`

	// Content is the full text content of the file.
	Content string `json:"content"`
}

// safePath resolves relPath against baseDir and verifies that the resolved
// absolute path remains inside baseDir (preventing path traversal attacks).
//
// Returns the resolved absolute path on success, or an error if the path
// escapes the sandbox or is otherwise invalid.
func safePath(baseDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("fileio: path must not be empty")
	}

	// filepath.Join cleans the path, resolving ".." components.
	joined := filepath.Join(baseDir, relPath)
	// Ensure the cleaned path is still within baseDir.
	cleanBase := filepath.Clean(baseDir)
	if !strings.HasPrefix(joined, cleanBase+string(filepath.Separator)) && joined != cleanBase {
		return "", fmt.Errorf("fileio: path %q escapes the sandbox directory", relPath)
	}
	return joined, nil
}

// makeWriteFileHandler returns a handler for the "write_file" tool bound to
// the given base directory.
func makeWriteFileHandler(baseDir string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var a writeFileArgs
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("fileio: write_file: failed to parse arguments: %w", err)
		}

		absPath, err := safePath(baseDir, a.Path)
		if err != nil {
			return "", err
		}

		// Check for context cancellation before doing I/O.
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("fileio: write_file: %w", ctx.Err())
		default:
		}

		// Create parent directories as needed.
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return "", fmt.Errorf("fileio: write_file: failed to create directories: %w", err)
		}

		if err := os.WriteFile(absPath, []byte(a.Content), 0o644); err != nil {
			return "", fmt.Errorf("fileio: write_file: failed to write file: %w", err)
		}

		res, err := json.Marshal(writeFileResult{
			Path:         a.Path,
			BytesWritten: len(a.Content),
		})
		if err != nil {
			return "", fmt.Errorf("fileio: write_file: failed to encode result: %w", err)
		}
		return string(res), nil
	}
}

// makeReadFileHandler returns a handler for the "read_file" tool bound to the
// given base directory.
func makeReadFileHandler(baseDir string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var a readFileArgs
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("fileio: read_file: failed to parse arguments: %w", err)
		}

		absPath, err := safePath(baseDir, a.Path)
		if err != nil {
			return "", err
		}

		// Check for context cancellation before doing I/O.
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("fileio: read_file: %w", ctx.Err())
		default:
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return "", fmt.Errorf("fileio: read_file: %w", err)
		}
		if info.Size() > maxReadBytes {
			return "", fmt.Errorf("fileio: read_file: file %q is too large (%d bytes, max %d)",
				a.Path, info.Size(), maxReadBytes)
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			return "", fmt.Errorf("fileio: read_file: failed to read file: %w", err)
		}

		res, err := json.Marshal(readFileResult{
			Path:    a.Path,
			Content: string(data),
		})
		if err != nil {
			return "", fmt.Errorf("fileio: read_file: failed to encode result: %w", err)
		}
		return string(res), nil
	}
}

// NewTools constructs the file I/O tool set sandboxed to baseDir.
//
// baseDir must be an absolute path to an existing directory. All file
// operations are restricted to this directory tree. Path traversal attempts
// are rejected with a descriptive error.
func NewTools(baseDir string) []tools.Tool {
	return []tools.Tool{
		{
			Definition: llm.ToolDefinition{
				Name:        "write_file",
				Description: "Write text content to a file within the session's sandboxed file store. Creates any missing parent directories automatically. Use this to save notes, session summaries, or generated text.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative file path within the sandbox (e.g. notes/session1.md). Must not contain '..' path components.",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Text content to write to the file.",
						},
					},
					"required": []string{"path", "content"},
				},
				EstimatedDurationMs: 20,
				MaxDurationMs:       100,
				Idempotent:          true,
				CacheableSeconds:    0,
			},
			Handler:     makeWriteFileHandler(baseDir),
			DeclaredP50: 20,
			DeclaredMax: 100,
		},
		{
			Definition: llm.ToolDefinition{
				Name:        "read_file",
				Description: "Read the text content of a file from the session's sandboxed file store. Returns the full file content. Files larger than 1 MiB are rejected.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative file path within the sandbox (e.g. notes/session1.md). Must not contain '..' path components.",
						},
					},
					"required": []string{"path"},
				},
				EstimatedDurationMs: 20,
				MaxDurationMs:       100,
				Idempotent:          true,
				CacheableSeconds:    5,
			},
			Handler:     makeReadFileHandler(baseDir),
			DeclaredP50: 20,
			DeclaredMax: 100,
		},
	}
}

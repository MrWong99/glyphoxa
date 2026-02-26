package fileio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// safePath tests
// ─────────────────────────────────────────────────────────────────────────────

func TestSafePath_Valid(t *testing.T) {
	t.Parallel()
	base := t.TempDir()

	cases := []struct {
		rel  string
		want string
	}{
		{"file.txt", filepath.Join(base, "file.txt")},
		{"notes/session1.md", filepath.Join(base, "notes", "session1.md")},
		{"a/b/c/d.json", filepath.Join(base, "a", "b", "c", "d.json")},
	}

	for _, tt := range cases {
		t.Run(tt.rel, func(t *testing.T) {
			got, err := safePath(base, tt.rel)
			if err != nil {
				t.Fatalf("safePath(%q, %q) unexpected error: %v", base, tt.rel, err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSafePath_Traversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()

	badPaths := []string{
		"../escape",
		"../../etc/passwd",
		"foo/../../escape",
		"../",
	}

	for _, rel := range badPaths {
		t.Run(rel, func(t *testing.T) {
			_, err := safePath(base, rel)
			if err == nil {
				t.Errorf("safePath(%q, %q) expected error, got nil", base, rel)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "fileio:") {
				t.Errorf("error %q should be prefixed with 'fileio:'", err.Error())
			}
		})
	}
}

func TestSafePath_EmptyPath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	_, err := safePath(base, "")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// write_file / read_file round-trip
// ─────────────────────────────────────────────────────────────────────────────

func TestWriteReadRoundTrip(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	writeHandler := makeWriteFileHandler(base)
	readHandler := makeReadFileHandler(base)
	ctx := context.Background()

	content := "# Session Notes\n\nThe party entered the dungeon at midnight."
	writeArgs, _ := json.Marshal(writeFileArgs{Path: "notes/session1.md", Content: content})

	writeOut, err := writeHandler(ctx, string(writeArgs))
	if err != nil {
		t.Fatalf("write_file unexpected error: %v", err)
	}

	var wr writeFileResult
	if err := json.Unmarshal([]byte(writeOut), &wr); err != nil {
		t.Fatalf("failed to unmarshal write result: %v\noutput: %s", err, writeOut)
	}
	if wr.Path != "notes/session1.md" {
		t.Errorf("Path = %q, want %q", wr.Path, "notes/session1.md")
	}
	if wr.BytesWritten != len(content) {
		t.Errorf("BytesWritten = %d, want %d", wr.BytesWritten, len(content))
	}

	// Now read it back.
	readArgs, _ := json.Marshal(readFileArgs{Path: "notes/session1.md"})
	readOut, err := readHandler(ctx, string(readArgs))
	if err != nil {
		t.Fatalf("read_file unexpected error: %v", err)
	}

	var rr readFileResult
	if err := json.Unmarshal([]byte(readOut), &rr); err != nil {
		t.Fatalf("failed to unmarshal read result: %v\noutput: %s", err, readOut)
	}
	if rr.Content != content {
		t.Errorf("Content = %q, want %q", rr.Content, content)
	}
}

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeWriteFileHandler(base)
	ctx := context.Background()

	args, _ := json.Marshal(writeFileArgs{Path: "deep/nested/dir/file.txt", Content: "hello"})
	_, err := handler(ctx, string(args))
	if err != nil {
		t.Fatalf("write_file unexpected error: %v", err)
	}

	// Verify the file actually exists on disk.
	abs := filepath.Join(base, "deep", "nested", "dir", "file.txt")
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		t.Errorf("expected file %q to exist", abs)
	}
}

func TestWriteFile_TraversalPrevented(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeWriteFileHandler(base)
	ctx := context.Background()

	args, _ := json.Marshal(writeFileArgs{Path: "../../etc/passwd", Content: "pwned"})
	_, err := handler(ctx, string(args))
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestReadFile_TraversalPrevented(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeReadFileHandler(base)
	ctx := context.Background()

	args, _ := json.Marshal(readFileArgs{Path: "../secret"})
	_, err := handler(ctx, string(args))
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestReadFile_NotFound(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeReadFileHandler(base)
	ctx := context.Background()

	args, _ := json.Marshal(readFileArgs{Path: "nonexistent.txt"})
	_, err := handler(ctx, string(args))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadFile_MaxFileSize(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	readHandler := makeReadFileHandler(base)
	ctx := context.Background()

	// Write a file slightly larger than maxReadBytes.
	bigFile := filepath.Join(base, "big.bin")
	if err := os.WriteFile(bigFile, make([]byte, maxReadBytes+1), 0o644); err != nil {
		t.Fatalf("failed to create large test file: %v", err)
	}

	args, _ := json.Marshal(readFileArgs{Path: "big.bin"})
	_, err := readHandler(ctx, string(args))
	if err == nil {
		t.Error("expected error for file exceeding maxReadBytes")
	}
	if err != nil && !strings.Contains(err.Error(), "too large") {
		t.Errorf("error %q should mention 'too large'", err.Error())
	}
}

func TestReadFile_ExactlyMaxSize(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	readHandler := makeReadFileHandler(base)
	writeHandler := makeWriteFileHandler(base)
	ctx := context.Background()

	// Write exactly maxReadBytes using the write handler.
	content := strings.Repeat("a", maxReadBytes)
	wArgs, _ := json.Marshal(writeFileArgs{Path: "exact.txt", Content: content})
	if _, err := writeHandler(ctx, string(wArgs)); err != nil {
		t.Fatalf("write_file unexpected error: %v", err)
	}

	// Read should succeed.
	rArgs, _ := json.Marshal(readFileArgs{Path: "exact.txt"})
	out, err := readHandler(ctx, string(rArgs))
	if err != nil {
		t.Fatalf("read_file unexpected error for exact max size: %v", err)
	}
	var rr readFileResult
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(rr.Content) != maxReadBytes {
		t.Errorf("Content length = %d, want %d", len(rr.Content), maxReadBytes)
	}
}

func TestWriteFile_BadJSON(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeWriteFileHandler(base)

	_, err := handler(context.Background(), `{bad`)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestReadFile_BadJSON(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeReadFileHandler(base)

	_, err := handler(context.Background(), `{bad`)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestWriteFile_EmptyPath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeWriteFileHandler(base)
	ctx := context.Background()

	args, _ := json.Marshal(writeFileArgs{Path: "", Content: "hello"})
	_, err := handler(ctx, string(args))
	if err == nil {
		t.Error("expected error for empty path")
	}
}

// TestNewTools verifies that [NewTools] returns the expected tool set.
func TestNewTools(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	ts := NewTools(base)

	if len(ts) != 2 {
		t.Fatalf("NewTools returned %d tools, want 2", len(ts))
	}

	names := map[string]bool{}
	for _, tool := range ts {
		names[tool.Definition.Name] = true
		if tool.Handler == nil {
			t.Errorf("tool %q has nil Handler", tool.Definition.Name)
		}
	}

	for _, want := range []string{"write_file", "read_file"} {
		if !names[want] {
			t.Errorf("NewTools missing tool %q", want)
		}
	}
}

// TestContextCancellation verifies that handlers respect context cancellation.
func TestContextCancellation_Write(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	handler := makeWriteFileHandler(base)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	args, _ := json.Marshal(writeFileArgs{Path: "test.txt", Content: "hello"})
	_, err := handler(ctx, string(args))
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestContextCancellation_Read(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// Create a file first using direct OS call.
	if err := os.WriteFile(filepath.Join(base, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := makeReadFileHandler(base)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	args, _ := json.Marshal(readFileArgs{Path: "test.txt"})
	_, err := handler(ctx, string(args))
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

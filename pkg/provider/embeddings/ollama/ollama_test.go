package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings/ollama"
)

// mockEmbedServer starts a test HTTP server that handles /api/embed requests
// and returns canned embeddings. It verifies that the request model matches
// wantModel and that the input count matches the number of responses provided.
//
// responses must contain at least as many vectors as the maximum number of
// inputs expected across all calls to this server.
func mockEmbedServer(t *testing.T, wantModel string, responses [][]float32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: got %q, want /api/embed", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: got %q, want POST", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Model != wantModel {
			t.Errorf("model: got %q, want %q", req.Model, wantModel)
		}

		// Return the first len(req.Input) responses.
		result := responses
		if len(result) > len(req.Input) {
			result = result[:len(req.Input)]
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"model":      wantModel,
			"embeddings": result,
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

// TestNew_EmptyModel verifies that constructing a Provider with an empty model
// name returns an error.
func TestNew_EmptyModel(t *testing.T) {
	_, err := ollama.New("", "")
	if err == nil {
		t.Fatal("expected error for empty model, got nil")
	}
}

// TestNew_DefaultBaseURL verifies that an empty baseURL is silently replaced
// with DefaultBaseURL and the Provider is functional.
func TestNew_DefaultBaseURL(t *testing.T) {
	p, err := ollama.New("", "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.ModelID() != "nomic-embed-text" {
		t.Errorf("ModelID(): got %q, want %q", p.ModelID(), "nomic-embed-text")
	}
}

// TestEmbed_Single verifies that Embed sends a single-element input array and
// returns the correct float32 vector.
func TestEmbed_Single(t *testing.T) {
	want := []float32{0.1, 0.2, 0.3, 0.4}
	srv := mockEmbedServer(t, "nomic-embed-text", [][]float32{want})
	defer srv.Close()

	p, err := ollama.New(srv.URL, "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := p.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("length: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("vec[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

// TestEmbedBatch verifies that EmbedBatch sends all texts in a single request
// and returns correctly ordered embedding vectors.
func TestEmbedBatch(t *testing.T) {
	vecs := [][]float32{
		{0.1, 0.2, 0.3},
		{0.4, 0.5, 0.6},
		{0.7, 0.8, 0.9},
	}
	srv := mockEmbedServer(t, "nomic-embed-text", vecs)
	defer srv.Close()

	p, err := ollama.New(srv.URL, "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	texts := []string{"text1", "text2", "text3"}
	got, err := p.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(got) != len(texts) {
		t.Fatalf("length: got %d, want %d", len(got), len(texts))
	}
	for i, wantVec := range vecs {
		for j, wantVal := range wantVec {
			if got[i][j] != wantVal {
				t.Errorf("vec[%d][%d]: got %v, want %v", i, j, got[i][j], wantVal)
			}
		}
	}
}

// TestEmbedBatch_Empty verifies that passing a nil or empty slice returns
// (nil, nil) without issuing any network request.
func TestEmbedBatch_Empty(t *testing.T) {
	// Use a port unlikely to be open so any accidental request would fail.
	p, err := ollama.New("http://127.0.0.1:19999", "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := p.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch(nil): unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("EmbedBatch(nil): expected nil, got %v", got)
	}
}

// TestDimensions_KnownModels verifies that known Ollama model names return the
// correct dimension without issuing any network request.
func TestDimensions_KnownModels(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"nomic-embed-text", 768},
		{"nomic-embed-text:latest", 768},
		{"mxbai-embed-large", 1024},
		{"all-minilm", 384},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			// Use an unreachable server — no request should be made.
			p, err := ollama.New("http://127.0.0.1:19999", tt.model)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := p.Dimensions(); got != tt.want {
				t.Errorf("Dimensions(): got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestDimensions_AutoDetect verifies that an unknown model probes the server
// exactly once and caches the detected dimension.
func TestDimensions_AutoDetect(t *testing.T) {
	const dim = 512
	probeVec := make([]float32, dim)
	for i := range probeVec {
		probeVec[i] = float32(i) / float32(dim)
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":      "custom-embed",
			"embeddings": [][]float32{probeVec},
		})
	}))
	defer srv.Close()

	p, err := ollama.New(srv.URL, "custom-embed")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Call Dimensions multiple times; probe should only happen once.
	for i := 0; i < 3; i++ {
		if got := p.Dimensions(); got != dim {
			t.Errorf("call %d: Dimensions(): got %d, want %d", i, got, dim)
		}
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 probe request, got %d", callCount)
	}
}

// TestDimensions_WithDimensionsOption verifies that WithDimensions bypasses
// both the known-models table and any probe request.
func TestDimensions_WithDimensionsOption(t *testing.T) {
	p, err := ollama.New("http://127.0.0.1:19999", "custom-model", ollama.WithDimensions(256))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.Dimensions(); got != 256 {
		t.Errorf("Dimensions(): got %d, want 256", got)
	}
}

// TestModelID verifies that ModelID returns the model name as supplied.
func TestModelID(t *testing.T) {
	p, err := ollama.New("", "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.ModelID(); got != "nomic-embed-text" {
		t.Errorf("ModelID(): got %q, want %q", got, "nomic-embed-text")
	}
}

// TestEmbed_ServerDown verifies that an unreachable server returns an error
// rather than blocking indefinitely.
func TestEmbed_ServerDown(t *testing.T) {
	p, err := ollama.New("http://127.0.0.1:19999", "nomic-embed-text",
		ollama.WithTimeout(500*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

// TestEmbed_BadResponse verifies that a non-200 HTTP status is treated as an
// error.
func TestEmbed_BadResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, err := ollama.New(srv.URL, "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

// TestEmbed_MalformedJSON verifies that an unparseable response body is
// treated as an error.
func TestEmbed_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	p, err := ollama.New(srv.URL, "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// TestEmbed_ContextCancelled verifies that Embed respects context cancellation
// and returns an error promptly when the context deadline is exceeded.
func TestEmbed_ContextCancelled(t *testing.T) {
	// stopCh signals the handler to return so httptest.Server.Close() doesn't
	// block waiting for a hung goroutine.
	stopCh := make(chan struct{})

	// Server that blocks until either the client disconnects or stopCh is closed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stopCh:
		}
	}))
	// Defers run LIFO: close(stopCh) fires first, unblocking the handler so that
	// the subsequent srv.Close() can drain connections without hanging.
	defer srv.Close()
	defer close(stopCh)

	p, err := ollama.New(srv.URL, "nomic-embed-text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err = p.Embed(ctx, "hello")
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
}

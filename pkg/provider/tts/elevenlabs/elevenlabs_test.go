package elevenlabs

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- WebSocket message construction ----

func TestBuildWSMessage_WithVoiceSettings(t *testing.T) {
	vs := &voiceSettings{Stability: 0.5, SimilarityBoost: 0.75}
	data, err := buildWSMessage("Hello there", vs)
	if err != nil {
		t.Fatalf("buildWSMessage: %v", err)
	}

	var msg textMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Text != "Hello there" {
		t.Errorf("expected text 'Hello there', got %q", msg.Text)
	}
	if msg.VoiceSettings == nil {
		t.Fatal("expected non-nil voice settings")
	}
	if msg.VoiceSettings.Stability != 0.5 {
		t.Errorf("expected stability 0.5, got %f", msg.VoiceSettings.Stability)
	}
	if msg.VoiceSettings.SimilarityBoost != 0.75 {
		t.Errorf("expected similarity_boost 0.75, got %f", msg.VoiceSettings.SimilarityBoost)
	}
}

func TestBuildWSMessage_WithoutVoiceSettings(t *testing.T) {
	data, err := buildWSMessage("Flush", nil)
	if err != nil {
		t.Fatalf("buildWSMessage: %v", err)
	}

	var msg textMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Text != "Flush" {
		t.Errorf("expected text 'Flush', got %q", msg.Text)
	}
	if msg.VoiceSettings != nil {
		t.Error("expected nil voice_settings when omitempty")
	}
}

func TestBuildWSMessage_FlushCommand(t *testing.T) {
	// ElevenLabs flush = {"text":""} with no other fields.
	data, err := buildWSMessage("", nil)
	if err != nil {
		t.Fatalf("buildWSMessage: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal flush: %v", err)
	}
	textVal, ok := raw["text"]
	if !ok {
		t.Fatal("expected 'text' field in flush message")
	}
	if string(textVal) != `""` {
		t.Errorf("expected empty string for text, got %s", textVal)
	}
	if _, exists := raw["voice_settings"]; exists {
		t.Error("flush message should not contain voice_settings")
	}
}

// ---- URL construction ----

func TestBuildURLForVoice(t *testing.T) {
	url := buildURLForVoice("voice-abc123", "eleven_flash_v2_5")
	if !strings.Contains(url, "voice-abc123") {
		t.Errorf("URL should contain voice ID, got: %s", url)
	}
	if !strings.Contains(url, "eleven_flash_v2_5") {
		t.Errorf("URL should contain model ID, got: %s", url)
	}
	if !strings.HasPrefix(url, "wss://") {
		t.Errorf("URL should be a WebSocket URL, got: %s", url)
	}
}

// ---- Voice list response parsing ----

func TestParseVoicesResponse_Success(t *testing.T) {
	raw := []byte(`{
		"voices": [
			{
				"voice_id": "abc123",
				"name": "Rachel",
				"category": "premade",
				"labels": {"gender": "female", "accent": "american"}
			},
			{
				"voice_id": "def456",
				"name": "Adam",
				"category": "premade",
				"labels": {"gender": "male"}
			}
		]
	}`)

	profiles, err := parseVoicesResponse(raw)
	if err != nil {
		t.Fatalf("parseVoicesResponse: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}

	rachel := profiles[0]
	if rachel.ID != "abc123" {
		t.Errorf("expected ID 'abc123', got %q", rachel.ID)
	}
	if rachel.Name != "Rachel" {
		t.Errorf("expected Name 'Rachel', got %q", rachel.Name)
	}
	if rachel.Provider != "elevenlabs" {
		t.Errorf("expected Provider 'elevenlabs', got %q", rachel.Provider)
	}
	if rachel.Metadata["gender"] != "female" {
		t.Errorf("expected gender 'female', got %q", rachel.Metadata["gender"])
	}
	if rachel.Metadata["category"] != "premade" {
		t.Errorf("expected category 'premade', got %q", rachel.Metadata["category"])
	}

	adam := profiles[1]
	if adam.ID != "def456" {
		t.Errorf("expected ID 'def456', got %q", adam.ID)
	}
}

func TestParseVoicesResponse_Empty(t *testing.T) {
	raw := []byte(`{"voices":[]}`)
	profiles, err := parseVoicesResponse(raw)
	if err != nil {
		t.Fatalf("parseVoicesResponse: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestParseVoicesResponse_InvalidJSON(t *testing.T) {
	_, err := parseVoicesResponse([]byte(`{invalid`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseVoicesResponse_NoLabels(t *testing.T) {
	raw := []byte(`{
		"voices": [
			{"voice_id": "x1", "name": "Ghost", "category": "", "labels": null}
		]
	}`)
	profiles, err := parseVoicesResponse(raw)
	if err != nil {
		t.Fatalf("parseVoicesResponse: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	// category is empty, so it should not appear in metadata.
	if _, ok := profiles[0].Metadata["category"]; ok {
		t.Error("expected no 'category' key in metadata when category is empty")
	}
}

// ---- Constructor tests ----

func TestNew_EmptyAPIKey(t *testing.T) {
	_, err := New("")
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestNew_Defaults(t *testing.T) {
	p, err := New("key")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.model != defaultModel {
		t.Errorf("expected model %q, got %q", defaultModel, p.model)
	}
	if p.outputFormat != defaultOutputFmt {
		t.Errorf("expected outputFormat %q, got %q", defaultOutputFmt, p.outputFormat)
	}
}

func TestNew_WithOptions(t *testing.T) {
	p, err := New("key", WithModel("eleven_multilingual_v2"), WithOutputFormat("pcm_24000"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.model != "eleven_multilingual_v2" {
		t.Errorf("expected model 'eleven_multilingual_v2', got %q", p.model)
	}
	if p.outputFormat != "pcm_24000" {
		t.Errorf("expected outputFormat 'pcm_24000', got %q", p.outputFormat)
	}
}

// internal/providers/openai_compatible/multimodal_test.go
// Purpose: Phase 5 (P5.1) — the OpenAI wire translation: text-only messages
// stay flat/backward-compatible, image parts become the content-array form
// with a base64 data URL.
package openaicompatible

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"helix/internal/providers"
)

func TestToWireMessagesTextOnlyUnchanged(t *testing.T) {
	msgs := []providers.ChatMessage{{Role: "user", Content: "hi"}}
	wire := toWireMessages(msgs)

	if len(wire) != 1 || wire[0]["content"] != "hi" {
		t.Fatalf("text-only message must stay flat: %+v", wire)
	}
	if _, ok := wire[0]["content"].(string); !ok {
		t.Fatalf("text-only content must remain a string, got %T", wire[0]["content"])
	}
}

func TestToWireMessagesMultimodalContentArray(t *testing.T) {
	img := []byte{0xFF, 0xD8, 0xFF, 0x00}
	msgs := []providers.ChatMessage{{
		Role:    "user",
		Content: "what's wrong with this?",
		Parts:   []providers.MessagePart{{Type: providers.PartImage, ImageData: img}},
	}}
	wire := toWireMessages(msgs)

	content, ok := wire[0]["content"].([]map[string]any)
	if !ok {
		t.Fatalf("multimodal content must be an array, got %T", wire[0]["content"])
	}
	if len(content) != 2 {
		t.Fatalf("expected text + image blocks, got %d", len(content))
	}
	if content[0]["type"] != "text" || content[0]["text"] != "what's wrong with this?" {
		t.Fatalf("text block wrong: %+v", content[0])
	}
	imageURL, ok := content[1]["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image block must carry image_url, got %+v", content[1])
	}
	wantURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img)
	if imageURL["url"] != wantURL {
		t.Fatalf("image url = %q, want %q", imageURL["url"], wantURL)
	}

	if _, err := json.Marshal(wire); err != nil {
		t.Fatalf("wire messages must marshal: %v", err)
	}
}

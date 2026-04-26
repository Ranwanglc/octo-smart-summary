package pipeline

import (
	"encoding/base64"
	"testing"
)

func TestExtractText_RawJSONType1(t *testing.T) {
	payload := []byte(`{"type":1,"content":"hello world"}`)
	text, ok := ExtractText(payload)
	if !ok {
		t.Fatal("expected ok=true for type=1 raw JSON")
	}
	if text != "hello world" {
		t.Errorf("got %q, want %q", text, "hello world")
	}
}

func TestExtractText_RawJSONType2(t *testing.T) {
	payload := []byte(`{"type":2,"content":"image.png"}`)
	text, ok := ExtractText(payload)
	if ok {
		t.Fatal("expected ok=false for type=2")
	}
	if text != "" {
		t.Errorf("got %q, want empty", text)
	}
}

func TestExtractText_Base64Type1(t *testing.T) {
	raw := `{"type":1,"content":"base64 text message"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	text, ok := ExtractText([]byte(encoded))
	if !ok {
		t.Fatal("expected ok=true for base64 encoded type=1")
	}
	if text != "base64 text message" {
		t.Errorf("got %q, want %q", text, "base64 text message")
	}
}

func TestExtractText_NilPayload(t *testing.T) {
	text, ok := ExtractText(nil)
	if ok {
		t.Fatal("expected ok=false for nil payload")
	}
	if text != "" {
		t.Errorf("got %q, want empty", text)
	}
}

func TestExtractText_EmptyPayload(t *testing.T) {
	text, ok := ExtractText([]byte{})
	if ok {
		t.Fatal("expected ok=false for empty payload")
	}
	if text != "" {
		t.Errorf("got %q, want empty", text)
	}
}

func TestExtractText_InvalidJSON(t *testing.T) {
	payload := []byte(`not json at all`)
	text, ok := ExtractText(payload)
	if ok {
		t.Fatal("expected ok=false for invalid JSON")
	}
	if text != "" {
		t.Errorf("got %q, want empty", text)
	}
}

func TestExtractText_EmptyContent(t *testing.T) {
	payload := []byte(`{"type":1,"content":""}`)
	text, ok := ExtractText(payload)
	if ok {
		t.Fatal("expected ok=false for empty content")
	}
	if text != "" {
		t.Errorf("got %q, want empty", text)
	}
}

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return value
}

func usage(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	u, ok := value["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage is missing: %#v", value)
	}
	return u
}

func TestNormalizeChatCompletion(t *testing.T) {
	raw := []byte(`{"id":"raw-id","object":"chat.completion","model":"gemini-3.7-flash-safety-le","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":79,"completion_tokens_details":{"reasoning_tokens":74}}}`)
	normalized, changed, err := normalizeJSONPayload(raw, "gemini-3.7-flash-high")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected response to change")
	}
	value := decodeObject(t, normalized)
	if value["id"] != "chatcmpl-raw-id" {
		t.Fatalf("unexpected id: %v", value["id"])
	}
	if value["model"] != "gemini-3.7-flash-high" {
		t.Fatalf("unexpected model: %v", value["model"])
	}
	u := usage(t, value)
	if u["completion_tokens"] != float64(75) || u["total_tokens"] != float64(79) {
		t.Fatalf("unexpected usage: %#v", u)
	}
}

func TestNormalizeResponsesUsage(t *testing.T) {
	raw := []byte(`{"id":"resp-id","object":"response","model":"gemini-3.7-flash","output":[],"usage":{"input_tokens":10,"output_tokens":6,"total_tokens":128,"output_tokens_details":{"reasoning_tokens":112}}}`)
	normalized, changed, err := normalizeJSONPayload(raw, "gemini-3.7-flash-high")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected response to change")
	}
	value := decodeObject(t, normalized)
	u := usage(t, value)
	if value["model"] != "gemini-3.7-flash-high" || u["output_tokens"] != float64(118) || u["total_tokens"] != float64(128) {
		t.Fatalf("unexpected response: %#v", value)
	}
}

func TestNormalizeRawAndSSEStreamChunks(t *testing.T) {
	raw := []byte(`{"id":"stream-id","object":"chat.completion.chunk","model":"gemini-3.7-flash","choices":[]}`)
	normalized, changed, err := normalizeStreamChunk(raw, "gemini-3.7-flash-high")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(string(normalized), `"id":"chatcmpl-stream-id"`) || !strings.Contains(string(normalized), `"model":"gemini-3.7-flash-high"`) {
		t.Fatalf("unexpected raw chunk: %s", normalized)
	}

	sse := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"model\":\"gemini-3.7-flash\",\"usage\":{\"input_tokens\":10,\"output_tokens\":6,\"total_tokens\":128,\"output_tokens_details\":{\"reasoning_tokens\":112}}}}\n\n")
	normalized, changed, err = normalizeStreamChunk(sse, "gemini-3.7-flash-high")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(string(normalized), `"model":"gemini-3.7-flash-high"`) || !strings.Contains(string(normalized), `"output_tokens":118`) {
		t.Fatalf("unexpected SSE chunk: %s", normalized)
	}
}

func TestNonGeminiPassthrough(t *testing.T) {
	raw := []byte(`{"id":"other","object":"chat.completion","model":"deepseek-v4-flash"}`)
	normalized, changed, err := normalizeJSONPayload(raw, "deepseek-v4-flash")
	if err != nil {
		t.Fatal(err)
	}
	if changed || string(normalized) != string(raw) {
		t.Fatalf("non-Gemini response changed: %s", normalized)
	}
}

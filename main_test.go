package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
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

func decodeEnvelopeResult[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("plugin returned an error: %#v", envelope.Error)
	}
	var result T
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatalf("decode envelope result: %v", err)
	}
	return result
}

func callResponseHandler(t *testing.T, request pluginapi.ResponseInterceptRequest) pluginapi.ResponseInterceptResponse {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := handleResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return decodeEnvelopeResult[pluginapi.ResponseInterceptResponse](t, response)
}

func callStreamHandler(t *testing.T, request pluginapi.StreamChunkInterceptRequest) pluginapi.StreamChunkInterceptResponse {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := handleStreamChunk(raw)
	if err != nil {
		t.Fatal(err)
	}
	return decodeEnvelopeResult[pluginapi.StreamChunkInterceptResponse](t, response)
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

func TestAlreadyCompliantUsageIsNotDoubleCounted(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			name: "chat completions",
			raw:  []byte(`{"id":"chatcmpl-ready","object":"chat.completion","model":"gemini-ready","usage":{"prompt_tokens":4,"completion_tokens":75,"total_tokens":79,"completion_tokens_details":{"reasoning_tokens":74}}}`),
		},
		{
			name: "responses",
			raw:  []byte(`{"id":"resp-ready","object":"response","model":"gemini-ready","usage":{"input_tokens":10,"output_tokens":118,"total_tokens":128,"output_tokens_details":{"reasoning_tokens":112}}}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, changed, err := normalizeJSONPayload(test.raw, "gemini-ready")
			if err != nil {
				t.Fatal(err)
			}
			if changed || string(normalized) != string(test.raw) {
				t.Fatalf("already compliant usage changed: %s", normalized)
			}
		})
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

func TestNormalizeMultipleCRLFSSEEvents(t *testing.T) {
	raw := []byte("event: response.created\r\ndata: {\"type\":\"response.created\",\"response\":{\"object\":\"response\",\"model\":\"executed-model\"}}\r\n\r\nevent: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"model\":\"executed-model\"}}\r\n\r\n")
	normalized, changed, err := normalizeStreamChunk(raw, "gemini-public-alias")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected SSE events to change")
	}
	if strings.Count(string(normalized), `"model":"gemini-public-alias"`) != 2 {
		t.Fatalf("unexpected normalized SSE: %s", normalized)
	}
	if strings.Count(string(normalized), "\r\n\r\n") != 2 {
		t.Fatalf("CRLF framing changed: %q", normalized)
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

func TestResponseHandlerScopesByRequestedModel(t *testing.T) {
	raw := []byte(`{"id":"raw-id","object":"chat.completion","model":"gemini-3.7-flash"}`)
	response := callResponseHandler(t, pluginapi.ResponseInterceptRequest{
		Model:          "gemini-3.7-flash",
		RequestedModel: "deepseek-v4-flash",
		Body:           raw,
	})
	if len(response.Body) != 0 {
		t.Fatalf("non-Gemini client request was modified: %s", response.Body)
	}
}

func TestResponseHandlerRestoresRequestedGeminiAlias(t *testing.T) {
	raw := []byte(`{"id":"raw-id","object":"chat.completion","model":"executed-model"}`)
	response := callResponseHandler(t, pluginapi.ResponseInterceptRequest{
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		Body:           raw,
	})
	value := decodeObject(t, response.Body)
	if value["model"] != "gemini-public-alias" {
		t.Fatalf("unexpected model: %v", value["model"])
	}
}

func TestResponseHandlerFallsBackToExecutedModel(t *testing.T) {
	raw := []byte(`{"id":"raw-id","object":"chat.completion","model":"executed-model"}`)
	response := callResponseHandler(t, pluginapi.ResponseInterceptRequest{
		Model: "gemini-legacy-client",
		Body:  raw,
	})
	value := decodeObject(t, response.Body)
	if value["model"] != "gemini-legacy-client" {
		t.Fatalf("unexpected model: %v", value["model"])
	}
}

func TestStreamHandlerRestoresRequestedGeminiAlias(t *testing.T) {
	response := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "requested-model-stream",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     0,
		Body:           []byte(`{"id":"raw-id","object":"chat.completion.chunk","model":"executed-model"}`),
	})
	value := decodeObject(t, response.Body)
	if value["model"] != "gemini-public-alias" {
		t.Fatalf("unexpected model: %v", value["model"])
	}
}

func TestStreamHandlerBuffersFragmentedRawJSON(t *testing.T) {
	first := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "fragmented-raw",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     0,
		Body:           []byte(`{"id":"split-id","object":"chat.completion.ch`),
	})
	if !first.DropChunk || len(first.Body) != 0 {
		t.Fatalf("first fragment was not withheld: %#v", first)
	}

	second := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "fragmented-raw",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     1,
		Body:           []byte(`unk","model":"executed-model"}`),
	})
	if second.DropChunk {
		t.Fatal("complete JSON was dropped")
	}
	value := decodeObject(t, second.Body)
	if value["id"] != "chatcmpl-split-id" || value["model"] != "gemini-public-alias" {
		t.Fatalf("unexpected normalized JSON: %#v", value)
	}
}

func TestStreamHandlerBuffersFragmentedSSE(t *testing.T) {
	first := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "fragmented-sse",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     0,
		Body:           []byte("event: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"model\":\"executed"),
	})
	if !first.DropChunk || len(first.Body) != 0 {
		t.Fatalf("first fragment was not withheld: %#v", first)
	}

	second := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "fragmented-sse",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     1,
		Body:           []byte("-model\"}}\r\n\r\n"),
	})
	if second.DropChunk || !strings.Contains(string(second.Body), `"model":"gemini-public-alias"`) {
		t.Fatalf("unexpected completed SSE event: %#v", second)
	}
	if !strings.HasSuffix(string(second.Body), "\r\n\r\n") {
		t.Fatalf("CRLF framing changed: %q", second.Body)
	}
}

func TestStreamHandlerEmitsCompleteSSEDataLineWithoutNewline(t *testing.T) {
	response := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "sse-without-newline",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     0,
		Body:           []byte(`data: {"type":"response.completed","response":{"object":"response","model":"executed-model"}}`),
	})
	if response.DropChunk || !strings.Contains(string(response.Body), `"model":"gemini-public-alias"`) {
		t.Fatalf("complete SSE data line was not emitted: %#v", response)
	}
}

func TestStreamHandlerEmitsFragmentedSSECompletedWithoutNewline(t *testing.T) {
	first := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "fragmented-sse-without-newline",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     0,
		Body:           []byte(`data: {"type":"response.completed","response":{"object":"response","model":"executed`),
	})
	if !first.DropChunk {
		t.Fatalf("incomplete SSE data line was not withheld: %#v", first)
	}

	second := callStreamHandler(t, pluginapi.StreamChunkInterceptRequest{
		RequestID:      "fragmented-sse-without-newline",
		Model:          "executed-model",
		RequestedModel: "gemini-public-alias",
		ChunkIndex:     1,
		Body:           []byte(`-model"}}`),
	})
	if second.DropChunk || !strings.Contains(string(second.Body), `"model":"gemini-public-alias"`) {
		t.Fatalf("completed SSE data line was not emitted: %#v", second)
	}
}

func TestStreamBufferCapacityDoesNotDropNewChunks(t *testing.T) {
	resetStreamBuffers()
	defer resetStreamBuffers()
	for index := 0; index < maxBufferedStreams; index++ {
		if !storeStreamBuffer(fmt.Sprintf("occupied-%d", index), streamFormatJSON, []byte(`{"partial":`)) {
			t.Fatalf("failed to seed buffer %d", index)
		}
	}

	response, err := normalizeStreamRequest(pluginapi.StreamChunkInterceptRequest{
		RequestID:  "capacity-fallback",
		ChunkIndex: 0,
		Body:       []byte(`{"id":"still-incomplete"`),
	}, "gemini-public-alias")
	if err != nil {
		t.Fatal(err)
	}
	if response.DropChunk || len(response.Body) != 0 {
		t.Fatalf("chunk was not passed through at capacity: %#v", response)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var pluginVersion = "0.0.0-dev"

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ResponseInterceptor    bool `json:"response_interceptor"`
	StreamChunkInterceptor bool `json:"response_stream_interceptor"`
}

func main() {}

func isGeminiModel(model string) bool {
	return strings.HasPrefix(model, "gemini-")
}

func clientModel(requestedModel, executedModel string) string {
	if requestedModel != "" {
		return requestedModel
	}
	return executedModel
}

type streamFormat uint8

const (
	streamFormatUnknown streamFormat = iota
	streamFormatJSON
	streamFormatSSE

	maxBufferedStreamBytes = 1 << 20
	maxBufferedStreams     = 1024
	streamBufferTTL        = 5 * time.Minute
)

type bufferedStream struct {
	format  streamFormat
	body    []byte
	updated time.Time
}

var streamBuffers = struct {
	sync.Mutex
	entries map[string]bufferedStream
}{entries: make(map[string]bufferedStream)}

func numeric(value any) (float64, bool) {
	n, ok := value.(float64)
	return n, ok
}

func normalizeChatUsage(usage map[string]any) bool {
	prompt, promptOK := numeric(usage["prompt_tokens"])
	completion, completionOK := numeric(usage["completion_tokens"])
	total, totalOK := numeric(usage["total_tokens"])
	details, detailsOK := usage["completion_tokens_details"].(map[string]any)
	reasoning, reasoningOK := numeric(details["reasoning_tokens"])
	if !promptOK || !completionOK || !totalOK || !detailsOK || !reasoningOK || reasoning <= 0 {
		return false
	}
	if total != prompt+completion+reasoning {
		return false
	}
	usage["completion_tokens"] = completion + reasoning
	usage["total_tokens"] = prompt + completion + reasoning
	return true
}

func normalizeResponsesUsage(usage map[string]any) bool {
	input, inputOK := numeric(usage["input_tokens"])
	output, outputOK := numeric(usage["output_tokens"])
	total, totalOK := numeric(usage["total_tokens"])
	details, detailsOK := usage["output_tokens_details"].(map[string]any)
	reasoning, reasoningOK := numeric(details["reasoning_tokens"])
	if !inputOK || !outputOK || !totalOK || !detailsOK || !reasoningOK || reasoning <= 0 {
		return false
	}
	if total != input+output+reasoning {
		return false
	}
	usage["output_tokens"] = output + reasoning
	usage["total_tokens"] = input + output + reasoning
	return true
}

func normalizeObject(value map[string]any, model string) bool {
	changed := false
	object, _ := value["object"].(string)
	if object == "chat.completion" || object == "chat.completion.chunk" {
		if id, ok := value["id"].(string); ok && id != "" && !strings.HasPrefix(id, "chatcmpl-") {
			value["id"] = "chatcmpl-" + id
			changed = true
		}
		if value["model"] != model {
			value["model"] = model
			changed = true
		}
		if usage, ok := value["usage"].(map[string]any); ok && normalizeChatUsage(usage) {
			changed = true
		}
	}
	if object == "response" {
		if value["model"] != model {
			value["model"] = model
			changed = true
		}
		if usage, ok := value["usage"].(map[string]any); ok && normalizeResponsesUsage(usage) {
			changed = true
		}
	}
	if response, ok := value["response"].(map[string]any); ok {
		if response["object"] == "response" {
			if response["model"] != model {
				response["model"] = model
				changed = true
			}
			if usage, ok := response["usage"].(map[string]any); ok && normalizeResponsesUsage(usage) {
				changed = true
			}
		}
	}
	return changed
}

func normalizeJSONPayload(raw []byte, model string) ([]byte, bool, error) {
	if !isGeminiModel(model) {
		return raw, false, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw, false, nil
	}
	if !normalizeObject(value, model) {
		return raw, false, nil
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

func normalizeStreamChunk(raw []byte, model string) ([]byte, bool, error) {
	if !isGeminiModel(model) {
		return raw, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if json.Valid(trimmed) {
		return normalizeJSONPayload(trimmed, model)
	}
	lines := bytes.Split(raw, []byte("\n"))
	changed := false
	for index, line := range lines {
		lineWithoutCR := bytes.TrimSuffix(line, []byte("\r"))
		trimmedLine := bytes.TrimLeft(lineWithoutCR, " \t")
		if !bytes.HasPrefix(trimmedLine, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(trimmedLine[len("data:"):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		normalized, lineChanged, err := normalizeJSONPayload(payload, model)
		if err != nil {
			return nil, false, err
		}
		if !lineChanged {
			continue
		}
		prefixLength := len(lineWithoutCR) - len(trimmedLine)
		newLine := append([]byte(nil), lineWithoutCR[:prefixLength]...)
		newLine = append(newLine, []byte("data: ")...)
		newLine = append(newLine, normalized...)
		if len(line) > len(lineWithoutCR) {
			newLine = append(newLine, '\r')
		}
		lines[index] = newLine
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	return bytes.Join(lines, []byte("\n")), true, nil
}

func detectStreamFormat(raw []byte) streamFormat {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return streamFormatUnknown
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return streamFormatJSON
	}
	firstLine := trimmed
	if index := bytes.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = bytes.TrimSuffix(firstLine[:index], []byte("\r"))
	}
	for _, field := range [][]byte{[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:")} {
		if bytes.HasPrefix(field, firstLine) || bytes.HasPrefix(firstLine, field) {
			return streamFormatSSE
		}
	}
	if bytes.HasPrefix(firstLine, []byte(":")) {
		return streamFormatSSE
	}
	return streamFormatUnknown
}

func completeSSEPrefix(raw []byte) int {
	lineStart := 0
	lastBoundary := 0
	for index, value := range raw {
		if value != '\n' {
			continue
		}
		contentEnd := index
		if contentEnd > lineStart && raw[contentEnd-1] == '\r' {
			contentEnd--
		}
		if contentEnd == lineStart {
			lastBoundary = index + 1
		}
		lineStart = index + 1
	}
	return lastBoundary
}

func completeSSEWithoutBlankLine(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("data: [DONE]")) {
		return true
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return false
	}
	foundData := false
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(bytes.TrimSuffix(line, []byte("\r")))
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		foundData = true
		payload := bytes.TrimSpace(line[len("data:"):])
		if !bytes.Equal(payload, []byte("[DONE]")) && !json.Valid(payload) {
			return false
		}
	}
	return foundData
}

func takeStreamBuffer(requestID string) (bufferedStream, bool) {
	streamBuffers.Lock()
	defer streamBuffers.Unlock()
	buffer, ok := streamBuffers.entries[requestID]
	if ok {
		delete(streamBuffers.entries, requestID)
	}
	return buffer, ok
}

func storeStreamBuffer(requestID string, format streamFormat, body []byte) bool {
	now := time.Now()
	streamBuffers.Lock()
	defer streamBuffers.Unlock()
	for key, entry := range streamBuffers.entries {
		if now.Sub(entry.updated) > streamBufferTTL {
			delete(streamBuffers.entries, key)
		}
	}
	if _, exists := streamBuffers.entries[requestID]; !exists && len(streamBuffers.entries) >= maxBufferedStreams {
		return false
	}
	streamBuffers.entries[requestID] = bufferedStream{
		format:  format,
		body:    bytes.Clone(body),
		updated: now,
	}
	return true
}

func discardStreamBuffer(requestID string) {
	if requestID == "" {
		return
	}
	streamBuffers.Lock()
	delete(streamBuffers.entries, requestID)
	streamBuffers.Unlock()
}

func resetStreamBuffers() {
	streamBuffers.Lock()
	streamBuffers.entries = make(map[string]bufferedStream)
	streamBuffers.Unlock()
}

func normalizeStreamRequest(req pluginapi.StreamChunkInterceptRequest, model string) (pluginapi.StreamChunkInterceptResponse, error) {
	response := pluginapi.StreamChunkInterceptResponse{}
	if req.ChunkIndex == pluginapi.StreamChunkHeaderInitIndex {
		discardStreamBuffer(req.RequestID)
		return response, nil
	}
	if !isGeminiModel(model) {
		if req.ChunkIndex == 0 {
			discardStreamBuffer(req.RequestID)
		}
		return response, nil
	}
	if req.RequestID == "" {
		normalized, changed, err := normalizeStreamChunk(req.Body, model)
		if changed {
			response.Body = normalized
		}
		return response, err
	}
	if req.ChunkIndex == 0 {
		discardStreamBuffer(req.RequestID)
	}

	buffer, hadBuffer := takeStreamBuffer(req.RequestID)
	format := buffer.format
	combined := append(bytes.Clone(buffer.body), req.Body...)
	if !hadBuffer {
		format = detectStreamFormat(combined)
	}

	switch format {
	case streamFormatJSON:
		if !json.Valid(bytes.TrimSpace(combined)) {
			if len(combined) > maxBufferedStreamBytes {
				response.Body = combined
				return response, nil
			}
			if !storeStreamBuffer(req.RequestID, format, combined) {
				if hadBuffer {
					response.Body = combined
				}
				return response, nil
			}
			response.DropChunk = true
			return response, nil
		}
		normalized, changed, err := normalizeStreamChunk(combined, model)
		if changed || hadBuffer {
			response.Body = normalized
		}
		return response, err

	case streamFormatSSE:
		completeLength := completeSSEPrefix(combined)
		if completeLength == 0 && completeSSEWithoutBlankLine(combined) {
			completeLength = len(combined)
		}
		if completeLength == 0 {
			if len(combined) > maxBufferedStreamBytes {
				response.Body = combined
				return response, nil
			}
			if !storeStreamBuffer(req.RequestID, format, combined) {
				if hadBuffer {
					response.Body = combined
				}
				return response, nil
			}
			response.DropChunk = true
			return response, nil
		}
		complete := combined[:completeLength]
		remainder := combined[completeLength:]
		if len(bytes.TrimSpace(remainder)) == 0 {
			complete = combined
			remainder = nil
		}
		normalized, changed, err := normalizeStreamChunk(complete, model)
		if len(remainder) > 0 && !storeStreamBuffer(req.RequestID, format, remainder) {
			response.Body = append(normalized, remainder...)
			return response, err
		}
		if changed || hadBuffer || len(remainder) > 0 {
			response.Body = normalized
		}
		return response, err

	default:
		normalized, changed, err := normalizeStreamChunk(req.Body, model)
		if changed {
			response.Body = normalized
		}
		return response, err
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "gemini-openai-normalizer",
			Version:          pluginVersion,
			Author:           "licoba",
			GitHubRepository: "https://github.com/licoba/cpa-plugin-gemini-openai-normalizer",
		},
		Capabilities: registrationCapabilities{
			ResponseInterceptor:    true,
			StreamChunkInterceptor: true,
		},
	}
}

func okEnvelope(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{
		OK:    false,
		Error: &pluginabi.Error{Code: code, Message: message},
	})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"encode error"}}`)
	}
	return raw
}

func handleResponse(request []byte) ([]byte, error) {
	var req pluginapi.ResponseInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	normalized, changed, err := normalizeJSONPayload(req.Body, clientModel(req.RequestedModel, req.Model))
	if err != nil {
		return nil, err
	}
	response := pluginapi.ResponseInterceptResponse{}
	if changed {
		response.Body = normalized
	}
	return okEnvelope(response)
}

func handleStreamChunk(request []byte) ([]byte, error) {
	var req pluginapi.StreamChunkInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	response, err := normalizeStreamRequest(req, clientModel(req.RequestedModel, req.Model))
	if err != nil {
		return nil, err
	}
	return okEnvelope(response)
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodResponseInterceptAfter:
		return handleResponse(request)
	case pluginabi.MethodResponseInterceptStreamChunk:
		return handleStreamChunk(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func pluginError(err error) []byte {
	return errorEnvelope("plugin_error", fmt.Sprint(err))
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

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
	normalized, changed, err := normalizeJSONPayload(req.Body, req.Model)
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
	normalized, changed, err := normalizeStreamChunk(req.Body, req.Model)
	if err != nil {
		return nil, err
	}
	response := pluginapi.StreamChunkInterceptResponse{}
	if changed {
		response.Body = normalized
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

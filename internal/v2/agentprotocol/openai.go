package agentprotocol

import (
	"encoding/json"
	"strings"
)

type OpenAICompatibleAdapter struct{}

func NewOpenAICompatibleAdapter() OpenAICompatibleAdapter { return OpenAICompatibleAdapter{} }

func (OpenAICompatibleAdapter) Name() string { return "openai-compatible" }

func (OpenAICompatibleAdapter) Capabilities() Capabilities {
	return Capabilities{Streaming: true, Tools: true, ParallelTools: true, Images: true, Files: false}
}

func (adapter OpenAICompatibleAdapter) DecodeRequest(payload []byte) (Conversation, error) {
	decoded, err := adapter.DecodeRequestWithMedia(payload)
	if err != nil {
		return Conversation{}, err
	}
	if len(decoded.Attachments.Items) > 0 {
		return Conversation{}, &CanonicalError{Code: "AGENT_MEDIA_REQUIRES_MULTIMODAL_DECODE", Kind: ErrorCapability}
	}
	return decoded.Conversation, nil
}

func decodeTools(raw any) ([]ToolDefinition, error) {
	if raw == nil {
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, &CanonicalError{Code: "AGENT_TOOLS_INVALID", Kind: ErrorExternalRequest}
	}
	seen := make(map[string]struct{}, len(values))
	tools := make([]ToolDefinition, 0, len(values))
	for _, rawTool := range values {
		tool, ok := rawTool.(map[string]any)
		if !ok || strings.TrimSpace(stringValue(tool["type"])) != "function" {
			return nil, &CanonicalError{Code: "AGENT_TOOL_INVALID", Kind: ErrorExternalRequest}
		}
		function, ok := tool["function"].(map[string]any)
		name := strings.TrimSpace(stringValue(function["name"]))
		if !ok || name == "" {
			return nil, &CanonicalError{Code: "AGENT_TOOL_FUNCTION_INVALID", Kind: ErrorExternalRequest}
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, &CanonicalError{Code: "AGENT_TOOL_NAME_DUPLICATE", Kind: ErrorToolMapping, Detail: name}
		}
		seen[name] = struct{}{}
		parameters := map[string]any{"type": "object"}
		if rawParameters, present := function["parameters"]; present && rawParameters != nil {
			var valid bool
			parameters, valid = rawParameters.(map[string]any)
			if !valid {
				return nil, &CanonicalError{Code: "AGENT_TOOL_PARAMETERS_INVALID", Kind: ErrorExternalRequest, Detail: name}
			}
		}
		tools = append(tools, ToolDefinition{Name: name, Description: stringValueUntrimmed(function["description"]), Parameters: parameters})
	}
	return tools, nil
}

func decodeToolCalls(raw any, kind ErrorKind) ([]ToolCall, error) {
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return nil, &CanonicalError{Code: "AGENT_TOOL_CALLS_INVALID", Kind: kind}
	}
	seen := make(map[string]struct{}, len(values))
	calls := make([]ToolCall, 0, len(values))
	for _, rawCall := range values {
		call, ok := rawCall.(map[string]any)
		if !ok || strings.TrimSpace(stringValue(call["type"])) != "function" {
			return nil, &CanonicalError{Code: "AGENT_TOOL_CALL_INVALID", Kind: kind}
		}
		id := strings.TrimSpace(stringValue(call["id"]))
		function, functionOK := call["function"].(map[string]any)
		name := strings.TrimSpace(stringValue(function["name"]))
		if id == "" || !functionOK || name == "" {
			return nil, &CanonicalError{Code: "AGENT_TOOL_CALL_INVALID", Kind: kind}
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, &CanonicalError{Code: "AGENT_TOOL_CALL_ID_DUPLICATE", Kind: ErrorToolMapping}
		}
		seen[id] = struct{}{}
		arguments, err := canonicalJSONObject(function["arguments"], "AGENT_TOOL_ARGUMENTS_INVALID", ErrorToolMapping)
		if err != nil {
			return nil, err
		}
		calls = append(calls, ToolCall{ID: id, Name: name, Arguments: arguments})
	}
	return calls, nil
}

func decodeToolChoice(raw any) (ToolChoice, error) {
	if raw == nil {
		return ToolChoice{}, nil
	}
	if mode, ok := raw.(string); ok {
		mode = strings.TrimSpace(mode)
		if mode == "none" || mode == "auto" || mode == "required" {
			return ToolChoice{Mode: mode}, nil
		}
		return ToolChoice{}, &CanonicalError{Code: "AGENT_TOOL_CHOICE_INVALID", Kind: ErrorExternalRequest}
	}
	object, ok := raw.(map[string]any)
	if !ok || strings.TrimSpace(stringValue(object["type"])) != "function" {
		return ToolChoice{}, &CanonicalError{Code: "AGENT_TOOL_CHOICE_INVALID", Kind: ErrorExternalRequest}
	}
	function, ok := object["function"].(map[string]any)
	name := strings.TrimSpace(stringValue(function["name"]))
	if !ok || name == "" {
		return ToolChoice{}, &CanonicalError{Code: "AGENT_TOOL_CHOICE_INVALID", Kind: ErrorExternalRequest}
	}
	return ToolChoice{Name: name}, nil
}

func decodeResponseFormat(raw any) (ResponseFormat, error) {
	if raw == nil {
		return ResponseFormat{}, nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return ResponseFormat{}, &CanonicalError{Code: "AGENT_RESPONSE_FORMAT_INVALID", Kind: ErrorExternalRequest}
	}
	typeName := strings.TrimSpace(stringValue(value["type"]))
	switch typeName {
	case "text", "json_object":
		return ResponseFormat{Type: typeName}, nil
	case "json_schema":
		schema, ok := value["json_schema"].(map[string]any)
		if !ok || len(schema) == 0 {
			return ResponseFormat{}, &CanonicalError{Code: "AGENT_RESPONSE_FORMAT_INVALID", Kind: ErrorExternalRequest}
		}
		return ResponseFormat{Type: typeName, JSONSchema: schema}, nil
	default:
		return ResponseFormat{}, &CanonicalError{Code: "AGENT_RESPONSE_FORMAT_INVALID", Kind: ErrorExternalRequest}
	}
}

func decodeMetadata(raw any) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	metadata, ok := raw.(map[string]any)
	if !ok || len(metadata) > 32 {
		return nil, &CanonicalError{Code: "AGENT_METADATA_INVALID", Kind: ErrorExternalRequest}
	}
	result := make(map[string]any, len(metadata))
	for key, value := range metadata {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" || len(trimmed) > 64 {
			return nil, &CanonicalError{Code: "AGENT_METADATA_INVALID", Kind: ErrorExternalRequest}
		}
		switch item := value.(type) {
		case string:
			if len(item) > 512 {
				return nil, &CanonicalError{Code: "AGENT_METADATA_INVALID", Kind: ErrorExternalRequest}
			}
		case json.Number, float64, bool, nil:
		default:
			return nil, &CanonicalError{Code: "AGENT_METADATA_INVALID", Kind: ErrorExternalRequest}
		}
		if prior, duplicate := result[trimmed]; duplicate && canonicalJSONText(prior) != canonicalJSONText(value) {
			return nil, &CanonicalError{Code: "AGENT_METADATA_CONFLICT", Kind: ErrorCanonical, Detail: trimmed}
		}
		result[trimmed] = value
	}
	return result, nil
}

func (OpenAICompatibleAdapter) EncodeCompletion(completion Completion, metadata CompletionMetadata) (map[string]any, error) {
	if completion.Status != "" && completion.Status != CompletionCompleted {
		return nil, &CanonicalError{Code: "AGENT_COMPLETION_STATUS_UNSUPPORTED", Kind: ErrorCanonical, Detail: string(completion.Status)}
	}
	message := map[string]any{"role": string(RoleAssistant), "content": completion.Content}
	if len(completion.ToolCalls) > 0 {
		message["tool_calls"] = encodeToolCalls(completion.ToolCalls)
	}
	return map[string]any{
		"id": "chatcmpl-" + strings.TrimPrefix(metadata.ID, "request_"), "object": "chat.completion",
		"created": metadata.Created.Unix(), "model": metadata.Model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": completion.FinishReason}},
	}, nil
}

func (OpenAICompatibleAdapter) DecodeStreamChunk(payload []byte) (StreamChunk, error) {
	if strings.TrimSpace(string(payload)) == "[DONE]" {
		return StreamChunk{Done: true}, nil
	}
	var value map[string]any
	if err := decodeJSON(payload, &value); err != nil {
		return StreamChunk{}, &CanonicalError{Code: "AGENT_STREAM_CHUNK_INVALID", Kind: ErrorStream}
	}
	choices, ok := value["choices"].([]any)
	if !ok || len(choices) == 0 {
		return StreamChunk{}, &CanonicalError{Code: "AGENT_STREAM_CHUNK_INVALID", Kind: ErrorStream}
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return StreamChunk{}, &CanonicalError{Code: "AGENT_STREAM_CHUNK_INVALID", Kind: ErrorStream}
	}
	delta, _ := choice["delta"].(map[string]any)
	chunk := StreamChunk{Role: Role(stringValue(delta["role"])), ContentDelta: stringValueUntrimmed(delta["content"]), FinishReason: stringValue(choice["finish_reason"])}
	if rawCalls, present := delta["tool_calls"]; present {
		values, ok := rawCalls.([]any)
		if !ok {
			return StreamChunk{}, &CanonicalError{Code: "AGENT_STREAM_TOOL_CALL_INVALID", Kind: ErrorStream}
		}
		for _, raw := range values {
			call, ok := raw.(map[string]any)
			if !ok {
				return StreamChunk{}, &CanonicalError{Code: "AGENT_STREAM_TOOL_CALL_INVALID", Kind: ErrorStream}
			}
			function, _ := call["function"].(map[string]any)
			chunk.ToolCallDeltas = append(chunk.ToolCallDeltas, ToolCallDelta{
				Index: intValue(call["index"]), ID: stringValue(call["id"]), Name: stringValue(function["name"]), ArgumentsDelta: stringValueUntrimmed(function["arguments"]),
			})
		}
	}
	return chunk, nil
}

func (OpenAICompatibleAdapter) EncodeStreamChunk(chunk StreamChunk, metadata CompletionMetadata) (map[string]any, error) {
	if chunk.Done {
		return nil, nil
	}
	if chunk.Error != nil {
		return nil, chunk.Error
	}
	delta := map[string]any{}
	if chunk.Role != "" {
		delta["role"] = string(chunk.Role)
	}
	if chunk.ContentDelta != "" {
		delta["content"] = chunk.ContentDelta
	}
	if len(chunk.ToolCallDeltas) > 0 {
		calls := make([]any, 0, len(chunk.ToolCallDeltas))
		for _, call := range chunk.ToolCallDeltas {
			calls = append(calls, map[string]any{
				"index": call.Index, "id": call.ID, "type": "function",
				"function": map[string]any{"name": call.Name, "arguments": call.ArgumentsDelta},
			})
		}
		delta["tool_calls"] = calls
	}
	var finishReason any
	if chunk.FinishReason != "" {
		finishReason = chunk.FinishReason
	}
	return map[string]any{
		"id": "chatcmpl-" + strings.TrimPrefix(metadata.ID, "request_"), "object": "chat.completion.chunk",
		"created": metadata.Created.Unix(), "model": metadata.Model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finishReason}},
	}, nil
}

func encodeToolCalls(calls []ToolCall) []any {
	encoded := make([]any, 0, len(calls))
	for _, call := range calls {
		encoded = append(encoded, map[string]any{
			"id": call.ID, "type": "function",
			"function": map[string]any{"name": call.Name, "arguments": canonicalJSONText(call.Arguments)},
		})
	}
	return encoded
}

func stringValue(value any) string { return strings.TrimSpace(stringValueUntrimmed(value)) }

func stringValueUntrimmed(value any) string {
	text, _ := value.(string)
	return text
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func intValue(value any) int {
	switch typed := value.(type) {
	case json.Number:
		integer, _ := typed.Int64()
		return int(integer)
	case float64:
		return int(typed)
	}
	return 0
}

func hasTool(tools []ToolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

var _ Adapter = OpenAICompatibleAdapter{}

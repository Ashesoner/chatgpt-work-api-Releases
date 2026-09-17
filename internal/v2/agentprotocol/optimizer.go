package agentprotocol

import (
	"encoding/json"
	"strings"
)

type OptimizationReport struct {
	DuplicateMessagesRemoved int
	MetadataKeysNormalized   int
	ToolResultsNormalized    int
}

type ContextOptimizer struct{}

func NewContextOptimizer() *ContextOptimizer { return &ContextOptimizer{} }

func (o *ContextOptimizer) Optimize(input Conversation) (Conversation, OptimizationReport, error) {
	output := Conversation{
		Model: strings.TrimSpace(input.Model), ToolChoice: input.ToolChoice,
		ResponseFormat: input.ResponseFormat, Stream: input.Stream,
	}
	if output.Model == "" {
		output.Model = DefaultModel
	}
	report := OptimizationReport{}
	output.Tools = cloneTools(input.Tools)
	if len(input.Metadata) > 0 {
		output.Metadata = make(map[string]any, len(input.Metadata))
		for key, value := range input.Metadata {
			trimmed := strings.TrimSpace(key)
			if trimmed == "" {
				return Conversation{}, report, &CanonicalError{Code: "AGENT_METADATA_INVALID", Kind: ErrorCanonical}
			}
			if trimmed != key {
				report.MetadataKeysNormalized++
			}
			if prior, exists := output.Metadata[trimmed]; exists && canonicalJSONText(prior) != canonicalJSONText(value) {
				return Conversation{}, report, &CanonicalError{Code: "AGENT_METADATA_CONFLICT", Kind: ErrorCanonical, Detail: trimmed}
			}
			output.Metadata[trimmed] = value
		}
	}

	for _, original := range input.Messages {
		message := cloneMessage(original)
		if message.ToolResult != nil && json.Valid([]byte(strings.TrimSpace(message.ToolResult.Content))) {
			var value any
			if err := decodeJSON([]byte(message.ToolResult.Content), &value); err == nil {
				normalized := canonicalJSONText(value)
				if normalized != message.ToolResult.Content {
					report.ToolResultsNormalized++
					message.ToolResult.Content = normalized
				}
			}
		}
		if len(output.Messages) > 0 && safelyDeduplicable(message) && messagesEqual(output.Messages[len(output.Messages)-1], message) {
			report.DuplicateMessagesRemoved++
			continue
		}
		output.Messages = append(output.Messages, message)
	}
	return output, report, nil
}

func safelyDeduplicable(message Message) bool {
	return message.Role == RoleSystem || message.Role == RoleDeveloper || message.Role == RoleTool
}

func messagesEqual(left, right Message) bool {
	if left.Role != right.Role || left.Content != right.Content || left.Name != right.Name || !contentPartsEqual(left.Parts, right.Parts) {
		return false
	}
	if (left.ToolResult == nil) != (right.ToolResult == nil) {
		return false
	}
	if left.ToolResult != nil {
		if left.ToolResult.CallID != right.ToolResult.CallID || left.ToolResult.Name != right.ToolResult.Name || left.ToolResult.Content != right.ToolResult.Content || !contentPartsEqual(left.ToolResult.Parts, right.ToolResult.Parts) {
			return false
		}
	}
	return len(left.ToolCalls) == 0 && len(right.ToolCalls) == 0
}

func contentPartsEqual(left, right []ContentPart) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneMessage(input Message) Message {
	output := input
	if len(input.Parts) > 0 {
		output.Parts = append([]ContentPart(nil), input.Parts...)
	}
	if input.ToolResult != nil {
		result := *input.ToolResult
		if len(input.ToolResult.Parts) > 0 {
			result.Parts = append([]ContentPart(nil), input.ToolResult.Parts...)
		}
		output.ToolResult = &result
	}
	if len(input.ToolCalls) > 0 {
		output.ToolCalls = make([]ToolCall, 0, len(input.ToolCalls))
		for _, call := range input.ToolCalls {
			call.Arguments = cloneJSONMap(call.Arguments)
			output.ToolCalls = append(output.ToolCalls, call)
		}
	}
	return output
}

func cloneTools(input []ToolDefinition) []ToolDefinition {
	if len(input) == 0 {
		return nil
	}
	output := make([]ToolDefinition, 0, len(input))
	for _, tool := range input {
		tool.Parameters = cloneJSONMap(tool.Parameters)
		output = append(output, tool)
	}
	return output
}

package agentprotocol

import (
	"fmt"
	"strings"

	"github.com/AAAYNMMM/CWapi/internal/v2/attachments"
)

type mediaCollector struct {
	inputs []attachments.InlineInput
	refs   []string
}

func (c *mediaCollector) add(input attachments.InlineInput) string {
	ref := fmt.Sprintf("image_%04d", len(c.inputs)+1)
	c.inputs = append(c.inputs, input)
	c.refs = append(c.refs, ref)
	return ref
}

// DecodeRequestWithMedia parses an OpenAI-compatible request exactly once and
// returns both the canonical conversation and validated inline image bytes.
// Images become canonical image_ref content parts; the raw bytes never pass
// through text placeholders or a second JSON encode/decode cycle.
func (adapter OpenAICompatibleAdapter) DecodeRequestWithMedia(payload []byte) (DecodedRequest, error) {
	var root map[string]any
	if err := decodeJSON(payload, &root); err != nil || root == nil {
		return DecodedRequest{}, &CanonicalError{Code: "AGENT_REQUEST_JSON_INVALID", Kind: ErrorExternalRequest}
	}
	if model, present := root["model"]; present && model != nil {
		if _, ok := model.(string); !ok {
			return DecodedRequest{}, &CanonicalError{Code: "AGENT_MODEL_INVALID", Kind: ErrorExternalRequest}
		}
	}
	if stream, present := root["stream"]; present && stream != nil {
		if _, ok := stream.(bool); !ok {
			return DecodedRequest{}, &CanonicalError{Code: "AGENT_STREAM_INVALID", Kind: ErrorExternalRequest}
		}
	}

	messagesValue, present := root["messages"]
	if !present || messagesValue == nil {
		return DecodedRequest{}, &CanonicalError{Code: "AGENT_MESSAGES_REQUIRED", Kind: ErrorExternalRequest}
	}
	rawMessages, ok := messagesValue.([]any)
	if !ok || len(rawMessages) == 0 {
		return DecodedRequest{}, &CanonicalError{Code: "AGENT_MESSAGES_INVALID", Kind: ErrorExternalRequest}
	}

	collector := &mediaCollector{}
	conversation := Conversation{Model: strings.TrimSpace(stringValue(root["model"])), Stream: boolValue(root["stream"])}
	if conversation.Model == "" {
		conversation.Model = DefaultModel
	}
	knownCalls := make(map[string]struct{})
	for messageIndex, raw := range rawMessages {
		message, err := adapter.decodeMessageWithMedia(raw, knownCalls, collector, messageIndex)
		if err != nil {
			return DecodedRequest{}, err
		}
		conversation.Messages = append(conversation.Messages, message)
		for _, call := range message.ToolCalls {
			if _, duplicate := knownCalls[call.ID]; duplicate {
				return DecodedRequest{}, &CanonicalError{Code: "AGENT_TOOL_CALL_ID_DUPLICATE", Kind: ErrorToolMapping}
			}
			knownCalls[call.ID] = struct{}{}
		}
	}

	if raw, present := root["attachments"]; present {
		supplied, err := decodeInlineAttachments(raw)
		if err != nil {
			return DecodedRequest{}, err
		}
		target := lastUserMessage(conversation.Messages)
		if target < 0 {
			return DecodedRequest{}, &CanonicalError{Code: "AGENT_ATTACHMENTS_REQUIRE_USER_MESSAGE", Kind: ErrorExternalRequest}
		}
		for index, input := range supplied {
			if strings.TrimSpace(input.Name) == "" {
				input.Name = fmt.Sprintf("attachment-%02d", index+1)
			}
			ref := collector.add(input)
			conversation.Messages[target].Parts = append(conversation.Messages[target].Parts, ContentPart{Type: "image_ref", ImageRef: ref})
		}
	}

	tools, err := decodeTools(root["tools"])
	if err != nil {
		return DecodedRequest{}, err
	}
	conversation.Tools = tools
	choice, err := decodeToolChoice(root["tool_choice"])
	if err != nil {
		return DecodedRequest{}, err
	}
	conversation.ToolChoice = choice
	if choice.Name != "" && !hasTool(conversation.Tools, choice.Name) {
		return DecodedRequest{}, &CanonicalError{Code: "AGENT_TOOL_CHOICE_UNDECLARED", Kind: ErrorToolMapping, Detail: choice.Name}
	}
	format, err := decodeResponseFormat(root["response_format"])
	if err != nil {
		return DecodedRequest{}, err
	}
	conversation.ResponseFormat = format
	metadata, err := decodeMetadata(root["metadata"])
	if err != nil {
		return DecodedRequest{}, err
	}
	conversation.Metadata = metadata

	var batch attachments.Batch
	if len(collector.inputs) > 0 {
		batch, err = attachments.DecodeInline(collector.inputs, attachments.AgentPolicy())
		if err != nil {
			return DecodedRequest{}, err
		}
		if len(batch.Items) != len(collector.refs) {
			return DecodedRequest{}, &CanonicalError{Code: "AGENT_ATTACHMENT_MAPPING_INVALID", Kind: ErrorCanonical}
		}
		for index := range batch.Items {
			if batch.Items[index].Metadata.Kind != "image" {
				return DecodedRequest{}, &CanonicalError{Code: "AGENT_IMAGE_ATTACHMENT_REQUIRED", Kind: ErrorCapability}
			}
			batch.Items[index].Metadata.Ref = collector.refs[index]
		}
	}
	return DecodedRequest{Conversation: conversation, Attachments: batch}, nil
}

func decodeInlineAttachments(raw any) ([]attachments.InlineInput, error) {
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return nil, &CanonicalError{Code: "AGENT_ATTACHMENTS_INVALID", Kind: ErrorExternalRequest}
	}
	result := make([]attachments.InlineInput, 0, len(values))
	for _, rawItem := range values {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, &CanonicalError{Code: "AGENT_ATTACHMENTS_INVALID", Kind: ErrorExternalRequest}
		}
		readString := func(key string) (string, bool) {
			value, present := item[key]
			if !present || value == nil {
				return "", true
			}
			text, valid := value.(string)
			return text, valid
		}
		name, okName := readString("name")
		mimeType, okMIME := readString("mime_type")
		dataBase64, okBase64 := readString("data_base64")
		dataURI, okURI := readString("data_uri")
		text, okText := readString("text")
		if !okName || !okMIME || !okBase64 || !okURI || !okText {
			return nil, &CanonicalError{Code: "AGENT_ATTACHMENTS_INVALID", Kind: ErrorExternalRequest}
		}
		result = append(result, attachments.InlineInput{Name: name, MIMEType: mimeType, DataBase64: dataBase64, DataURI: dataURI, Text: text})
	}
	return result, nil
}

func (adapter OpenAICompatibleAdapter) decodeMessageWithMedia(raw any, knownCalls map[string]struct{}, collector *mediaCollector, messageIndex int) (Message, error) {
	value, ok := raw.(map[string]any)
	if !ok {
		return Message{}, &CanonicalError{Code: "AGENT_MESSAGES_INVALID", Kind: ErrorExternalRequest}
	}
	role := Role(strings.TrimSpace(stringValue(value["role"])))
	switch role {
	case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant, RoleTool:
	default:
		return Message{}, &CanonicalError{Code: "AGENT_MESSAGE_ROLE_INVALID", Kind: ErrorExternalRequest}
	}
	message := Message{Role: role, Name: strings.TrimSpace(stringValue(value["name"]))}
	if role != RoleTool {
		content, parts, err := decodeContentWithMedia(value["content"], collector, messageIndex)
		if err != nil {
			return Message{}, err
		}
		message.Content, message.Parts = content, parts
	}

	if rawCalls, present := value["tool_calls"]; present && rawCalls != nil {
		if role != RoleAssistant {
			return Message{}, &CanonicalError{Code: "AGENT_TOOL_CALL_ROLE_INVALID", Kind: ErrorToolMapping}
		}
		calls, err := decodeToolCalls(rawCalls, ErrorExternalRequest)
		if err != nil {
			return Message{}, err
		}
		message.ToolCalls = calls
	}
	if role == RoleAssistant && message.Content == "" && len(message.Parts) == 0 && len(message.ToolCalls) == 0 {
		return Message{}, &CanonicalError{Code: "AGENT_MESSAGE_CONTENT_REQUIRED", Kind: ErrorExternalRequest}
	}
	if role == RoleTool {
		callID := strings.TrimSpace(stringValue(value["tool_call_id"]))
		if callID == "" {
			return Message{}, &CanonicalError{Code: "AGENT_TOOL_RESULT_CALL_ID_REQUIRED", Kind: ErrorToolMapping}
		}
		if _, found := knownCalls[callID]; !found {
			return Message{}, &CanonicalError{Code: "AGENT_TOOL_RESULT_CALL_NOT_FOUND", Kind: ErrorToolMapping, Detail: callID}
		}
		var content string
		var parts []ContentPart
		var err error
		if _, multipart := value["content"].([]any); multipart {
			content, parts, err = decodeContentWithMedia(value["content"], collector, messageIndex)
		} else {
			content, err = canonicalContent(value["content"], "AGENT_TOOL_RESULT_CONTENT_INVALID", ErrorCanonical)
		}
		if err != nil {
			return Message{}, err
		}
		message.ToolResult = &ToolResult{CallID: callID, Name: message.Name, Content: content, Parts: parts}
	}
	return message, nil
}

func decodeContentWithMedia(raw any, collector *mediaCollector, messageIndex int) (string, []ContentPart, error) {
	if raw == nil {
		return "", nil, nil
	}
	if text, ok := raw.(string); ok {
		return text, nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return "", nil, &CanonicalError{Code: "AGENT_MESSAGE_CONTENT_UNSUPPORTED", Kind: ErrorCapability}
	}
	var text strings.Builder
	parts := make([]ContentPart, 0, len(values))
	imageIndex := 0
	for _, rawPart := range values {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return "", nil, &CanonicalError{Code: "AGENT_MESSAGE_CONTENT_UNSUPPORTED", Kind: ErrorCapability}
		}
		switch strings.TrimSpace(stringValue(part["type"])) {
		case "text":
			partText, ok := part["text"].(string)
			if !ok {
				return "", nil, &CanonicalError{Code: "AGENT_MESSAGE_CONTENT_UNSUPPORTED", Kind: ErrorCapability}
			}
			text.WriteString(partText)
			parts = append(parts, ContentPart{Type: "text", Text: partText})
		case "image_url":
			image, ok := part["image_url"].(map[string]any)
			if !ok {
				return "", nil, &CanonicalError{Code: "AGENT_MEDIA_INPUT_UNSUPPORTED", Kind: ErrorCapability}
			}
			dataURI := strings.TrimSpace(stringValue(image["url"]))
			if !strings.HasPrefix(strings.ToLower(dataURI), "data:") {
				return "", nil, &CanonicalError{Code: "AGENT_IMAGE_URL_UNSUPPORTED", Kind: ErrorCapability}
			}
			imageIndex++
			ref := collector.add(attachments.InlineInput{Name: fmt.Sprintf("message-%02d-image-%02d", messageIndex+1, imageIndex), DataURI: dataURI})
			parts = append(parts, ContentPart{Type: "image_ref", ImageRef: ref})
		default:
			return "", nil, &CanonicalError{Code: "AGENT_MEDIA_INPUT_UNSUPPORTED", Kind: ErrorCapability}
		}
	}
	return text.String(), parts, nil
}

func lastUserMessage(messages []Message) int {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == RoleUser {
			return index
		}
	}
	return -1
}

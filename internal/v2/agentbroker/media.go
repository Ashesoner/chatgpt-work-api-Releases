package agentbroker

import (
	"errors"
	"strings"

	"github.com/AAAYNMMM/CWapi/internal/v2/agentprotocol"
	"github.com/AAAYNMMM/CWapi/internal/v2/attachments"
)

func validateImageBindings(conversation agentprotocol.Conversation, batch attachments.Batch) error {
	referenced := make(map[string]struct{})
	addParts := func(parts []agentprotocol.ContentPart) error {
		for _, part := range parts {
			if part.Type != "image_ref" {
				continue
			}
			ref := strings.TrimSpace(part.ImageRef)
			if ref == "" {
				return errors.New("AGENT_IMAGE_REF_MISMATCH")
			}
			referenced[ref] = struct{}{}
		}
		return nil
	}
	for _, message := range conversation.Messages {
		if err := addParts(message.Parts); err != nil {
			return err
		}
		if message.ToolResult != nil {
			if err := addParts(message.ToolResult.Parts); err != nil {
				return err
			}
		}
	}

	supplied := make(map[string]struct{}, len(batch.Items))
	for _, item := range batch.Items {
		if item.Metadata.Kind != "image" {
			return errors.New("AGENT_IMAGE_ATTACHMENT_REQUIRED")
		}
		ref := strings.TrimSpace(item.Metadata.Ref)
		if ref == "" {
			return errors.New("AGENT_IMAGE_REF_MISMATCH")
		}
		if _, duplicate := supplied[ref]; duplicate {
			return errors.New("AGENT_IMAGE_REF_MISMATCH")
		}
		supplied[ref] = struct{}{}
	}
	if len(referenced) != len(supplied) {
		return errors.New("AGENT_IMAGE_REF_MISMATCH")
	}
	for ref := range referenced {
		if _, ok := supplied[ref]; !ok {
			return errors.New("AGENT_IMAGE_REF_MISMATCH")
		}
	}
	return nil
}

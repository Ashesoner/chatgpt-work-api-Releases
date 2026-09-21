package agentbroker

import (
	"strings"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/v2/agentprotocol"
	"github.com/AAAYNMMM/CWapi/internal/v2/mcpserver"
)

func (b *Broker) acceptStreamChunksLocked(bridgeID string, chunks []mcpserver.AgentStreamChunk, now time.Time) []mcpserver.AgentEvent {
	if len(chunks) == 0 {
		return nil
	}
	events := make([]mcpserver.AgentEvent, 0, len(chunks))
	for _, input := range chunks {
		requestID := strings.TrimSpace(input.RequestID)
		req := b.requests[requestID]
		if requestID == "" || req == nil || req.bridgeID != bridgeID || !isActiveRequestState(req.state) || !req.stream || req.streamCh == nil {
			events = append(events, mcpserver.AgentEvent{Type: "error", RequestID: requestID, Code: "AGENT_STREAM_REQUEST_INVALID", Message: "AGENT_STREAM_REQUEST_INVALID", Retryable: false, At: now.UTC().Format(time.RFC3339Nano)})
			continue
		}
		chunk := agentprotocol.StreamChunk{Role: agentprotocol.RoleAssistant, ContentDelta: input.ContentDelta}
		for _, delta := range input.ToolCallDeltas {
			chunk.ToolCallDeltas = append(chunk.ToolCallDeltas, agentprotocol.ToolCallDelta{
				Index: delta.Index, ID: delta.ID, Name: delta.Name, ArgumentsDelta: delta.ArgumentsDelta,
			})
		}
		if chunk.ContentDelta == "" && len(chunk.ToolCallDeltas) == 0 {
			continue
		}
		chunkBytes := len(chunk.ContentDelta)
		for _, delta := range chunk.ToolCallDeltas {
			chunkBytes += len(delta.ID) + len(delta.Name) + len(delta.ArgumentsDelta)
		}
		if req.streamBytes+chunkBytes > b.cfg.MaxBatchBytes {
			events = append(events, mcpserver.AgentEvent{Type: "error", RequestID: requestID, Code: "AGENT_STREAM_TOO_LARGE", Message: "AGENT_STREAM_TOO_LARGE", Retryable: true, At: now.UTC().Format(time.RFC3339Nano)})
			continue
		}
		select {
		case req.streamCh <- chunk:
			req.streamChunks = append(req.streamChunks, chunk)
			req.streamBytes += chunkBytes
			req.streamVersion++
			b.refreshActivityLocked(req, now)
			events = append(events, mcpserver.AgentEvent{Type: "stream", RequestID: requestID, At: now.UTC().Format(time.RFC3339Nano)})
		default:
			events = append(events, mcpserver.AgentEvent{Type: "error", RequestID: requestID, Code: "AGENT_STREAM_BACKPRESSURE", Message: "AGENT_STREAM_BACKPRESSURE", Retryable: true, At: now.UTC().Format(time.RFC3339Nano)})
		}
	}
	return events
}

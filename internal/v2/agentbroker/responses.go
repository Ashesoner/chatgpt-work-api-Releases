package agentbroker

import (
	"errors"
	"strings"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/v2/agentprotocol"
	"github.com/AAAYNMMM/CWapi/internal/v2/mcpserver"
)

type preparedResponse struct {
	source        mcpserver.AgentExchangeResponse
	requestID     string
	completion    agentprotocol.Completion
	fingerprint   string
	expectedEvent string
	err           error
	validationErr error
	streamVersion uint64
}

func prepareResponses(responses []mcpserver.AgentExchangeResponse) []preparedResponse {
	if len(responses) == 0 {
		return nil
	}
	prepared := make([]preparedResponse, 0, len(responses))
	for _, response := range responses {
		item := preparedResponse{source: response, requestID: strings.TrimSpace(response.RequestID)}
		if item.requestID == "" || response.Response == nil {
			item.err = errors.New("AGENT_RESPONSE_INVALID")
			prepared = append(prepared, item)
			continue
		}
		item.completion, item.err = agentprotocol.DecodeBridgeCompletion(response.Response, nil)
		if item.err != nil {
			prepared = append(prepared, item)
			continue
		}
		var ok bool
		item.fingerprint, ok = completionFingerprint(item.completion)
		if !ok {
			item.err = errors.New("AGENT_RESPONSE_INVALID")
			prepared = append(prepared, item)
			continue
		}
		item.expectedEvent = "completion"
		if item.completion.FinishReason == "tool_calls" {
			item.expectedEvent = "tool_call"
		}
		prepared = append(prepared, item)
	}
	return prepared
}

func (b *Broker) validatePreparedResponses(bridgeID string, responses []preparedResponse) {
	type validationTarget struct {
		index         int
		conversation  agentprotocol.Conversation
		streamChunks  []agentprotocol.StreamChunk
		streamVersion uint64
	}
	targets := make([]validationTarget, 0, len(responses))
	b.mu.Lock()
	for index := range responses {
		prepared := &responses[index]
		if prepared.err != nil || prepared.requestID == "" {
			continue
		}
		req := b.requests[prepared.requestID]
		if req == nil || req.bridgeID != bridgeID || !isActiveRequestState(req.state) {
			continue
		}
		// Conversation state is immutable after enqueue. Snapshot the value under
		// the broker lock, then perform schema/response-format validation outside
		// the lock so large JSON responses never block heartbeat or queue traffic.
		responses[index].streamVersion = req.streamVersion
		targets = append(targets, validationTarget{
			index: index, conversation: req.conversation, streamVersion: req.streamVersion,
			streamChunks: append([]agentprotocol.StreamChunk(nil), req.streamChunks...),
		})
	}
	b.mu.Unlock()
	for _, target := range targets {
		prepared := &responses[target.index]
		prepared.validationErr = agentprotocol.ValidateCompletion(prepared.completion, &target.conversation)
		if prepared.validationErr != nil || len(target.streamChunks) == 0 {
			continue
		}
		chunks := append([]agentprotocol.StreamChunk(nil), target.streamChunks...)
		chunks = append(chunks, agentprotocol.StreamChunk{FinishReason: prepared.completion.FinishReason})
		assembled, err := agentprotocol.AssembleStreamCompletion(chunks)
		if err != nil {
			prepared.validationErr = err
			continue
		}
		fingerprint, ok := completionFingerprint(assembled)
		if !ok || fingerprint != prepared.fingerprint {
			prepared.validationErr = errors.New("AGENT_STREAM_FINAL_MISMATCH")
		}
	}
}

func (b *Broker) acceptPreparedResponsesLocked(bridgeID string, responses []preparedResponse, now time.Time) ([]mcpserver.AgentExchangeResult, bool, []mcpserver.AgentEvent) {
	if len(responses) == 0 {
		return nil, false, nil
	}
	results := make([]mcpserver.AgentExchangeResult, 0, len(responses))
	events := make([]mcpserver.AgentEvent, 0, len(responses))
	followupExpected := false
	for _, prepared := range responses {
		requestID := prepared.requestID
		result := mcpserver.AgentExchangeResult{RequestID: requestID}
		if prepared.err != nil {
			code := errorCode(prepared.err)
			if code == "" {
				code = "AGENT_RESPONSE_INVALID"
			}
			result.State, result.Error = "rejected", code
			result.Detail = responseErrorDetail(prepared.err, requestID, prepared.source.Response, true)
			if req := b.requests[requestID]; req != nil && req.bridgeID == bridgeID && isActiveRequestState(req.state) {
				b.markRetryableLocked(req, code, now)
			}
			events = append(events, errorEvent(result.Detail, now))
			results = append(results, result)
			continue
		}
		if prior, ok := b.receipts[requestID]; ok {
			if prior.bridgeID == bridgeID && prior.fingerprint == prepared.fingerprint {
				result.State = "duplicate"
				followupExpected = followupExpected || prepared.completion.FinishReason == "tool_calls"
			} else {
				result.State, result.Error = "rejected", "AGENT_RESPONSE_CONFLICT"
				result.Detail = responseErrorDetail(errors.New(result.Error), requestID, prepared.source.Response, false)
			}
			results = append(results, result)
			continue
		}
		req := b.requests[requestID]
		if req == nil || req.bridgeID != bridgeID || !isActiveRequestState(req.state) || !now.Before(req.deadline) {
			if req != nil && isActiveRequestState(req.state) && !now.Before(req.deadline) {
				b.finishLocked(req, StateTimedOut, "AGENT_REQUEST_TIMEOUT", Completion{})
			}
			result.State, result.Error = "rejected", "REQUEST_NO_LONGER_ACTIVE"
			result.Detail = responseErrorDetail(errors.New(result.Error), requestID, prepared.source.Response, false)
			results = append(results, result)
			continue
		}
		if req.stream && prepared.streamVersion != req.streamVersion {
			err := errors.New("AGENT_STREAM_CHANGED")
			result.State, result.Error = "rejected", err.Error()
			result.Detail = responseErrorDetail(err, requestID, prepared.source.Response, true)
			b.markRetryableLocked(req, result.Error, now)
			events = append(events, errorEvent(result.Detail, now))
			results = append(results, result)
			continue
		}
		if prepared.validationErr != nil {
			code := errorCode(prepared.validationErr)
			result.State, result.Error = "rejected", code
			result.Detail = responseErrorDetail(prepared.validationErr, requestID, prepared.source.Response, true)
			b.markRetryableLocked(req, code, now)
			events = append(events, errorEvent(result.Detail, now))
			results = append(results, result)
			continue
		}
		if supplied := strings.TrimSpace(prepared.source.Event); supplied != "" && supplied != prepared.expectedEvent {
			err := errors.New("AGENT_RESPONSE_EVENT_MISMATCH")
			result.State, result.Error = "rejected", err.Error()
			result.Detail = responseErrorDetail(err, requestID, prepared.source.Response, true)
			b.markRetryableLocked(req, result.Error, now)
			events = append(events, errorEvent(result.Detail, now))
			results = append(results, result)
			continue
		}
		b.refreshActivityLocked(req, now)
		b.receipts[requestID] = receipt{bridgeID: bridgeID, fingerprint: prepared.fingerprint, expires: now.Add(b.cfg.ReceiptTTL)}
		if prepared.completion.FinishReason == "tool_calls" {
			b.finishLocked(req, StateWaitingTool, "", prepared.completion)
			for _, call := range prepared.completion.ToolCalls {
				events = append(events, mcpserver.AgentEvent{Type: "tool_call", RequestID: requestID, ToolCallID: call.ID, ToolName: call.Name, At: now.UTC().Format(time.RFC3339Nano)})
			}
			followupExpected = true
		} else {
			b.finishLocked(req, StateCompleted, "", prepared.completion)
			events = append(events, mcpserver.AgentEvent{Type: "completion", RequestID: requestID, At: now.UTC().Format(time.RFC3339Nano)})
		}
		b.completed++
		result.State = "completed"
		results = append(results, result)
	}
	return results, followupExpected, events
}

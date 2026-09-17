package mcpserver

import (
	"context"
	"errors"
	"net/url"

	"github.com/AAAYNMMM/CWapi/internal/v2/attachments"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ToolAgentOpen     = "agent_open"
	ToolAgentExchange = "agent_exchange"
	ToolAgentClose    = "agent_close"
)

type AgentOpenInput struct{}

type AgentOpenOutput struct {
	State       string `json:"state"`
	Resumed     bool   `json:"resumed"`
	MaxInflight int    `json:"max_inflight"`
	Revision    uint64 `json:"state_revision"`
}

type AgentExchangeResponse struct {
	RequestID string         `json:"request_id"`
	Event     string         `json:"event,omitempty"`
	Response  map[string]any `json:"response"`
}

type AgentProgress struct {
	RequestID string `json:"request_id"`
	Message   string `json:"message"`
}

type AgentStreamToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

type AgentStreamChunk struct {
	RequestID      string                     `json:"request_id"`
	ContentDelta   string                     `json:"content_delta,omitempty"`
	ToolCallDeltas []AgentStreamToolCallDelta `json:"tool_call_deltas,omitempty"`
}

type AgentExchangeInput struct {
	Responses    []AgentExchangeResponse `json:"responses,omitempty"`
	Progress     []AgentProgress         `json:"progress,omitempty"`
	StreamChunks []AgentStreamChunk      `json:"stream_chunks,omitempty"`
	Capacity     int                     `json:"capacity,omitempty"`
}

type AgentStructuredError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Retryable  bool   `json:"retryable"`
}

type AgentExchangeResult struct {
	RequestID string                `json:"request_id,omitempty"`
	State     string                `json:"state"`
	Error     string                `json:"error,omitempty"`
	Detail    *AgentStructuredError `json:"error_detail,omitempty"`
}

type AgentExchangeRequest struct {
	RequestID          string                 `json:"request_id"`
	TaskID             string                 `json:"task_id,omitempty"`
	CorrelationID      string                 `json:"correlation_id,omitempty"`
	State              string                 `json:"state"`
	LifecycleState     string                 `json:"lifecycle_state"`
	Delivery           int                    `json:"delivery"`
	PreviousState      string                 `json:"previous_state,omitempty"`
	ResumeReason       string                 `json:"resume_reason,omitempty"`
	CreatedAt          string                 `json:"created_at"`
	ClaimedAt          string                 `json:"claimed_at"`
	LastDeliveredAt    string                 `json:"last_delivered_at"`
	LastActivity       string                 `json:"last_activity"`
	DeadlineAt         string                 `json:"deadline_at"`
	ActivityDeadlineAt string                 `json:"activity_deadline_at"`
	HardDeadlineAt     string                 `json:"hard_deadline_at"`
	Progress           string                 `json:"progress,omitempty"`
	ProgressAt         string                 `json:"progress_at,omitempty"`
	Event              string                 `json:"event,omitempty"`
	Request            map[string]any         `json:"request"`
	Attachments        []attachments.Metadata `json:"attachments,omitempty"`
	ContentItems       []attachments.Item     `json:"-"`
}

type AgentEvent struct {
	Type       string `json:"type"`
	RequestID  string `json:"request_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
	At         string `json:"at"`
}

type AgentRequestActivity struct {
	RequestID      string `json:"request_id"`
	TaskID         string `json:"task_id,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	State          string `json:"state"`
	Progress       string `json:"progress,omitempty"`
	LastActivity   string `json:"last_activity"`
	HardDeadlineAt string `json:"hard_deadline_at"`
}

type AgentExchangeActivity struct {
	Revision        uint64                 `json:"revision"`
	Changed         bool                   `json:"changed"`
	Pending         int                    `json:"pending"`
	Inflight        int                    `json:"inflight"`
	Active          int                    `json:"active"`
	QueuedRequests  int                    `json:"queued_requests"`
	ActiveRequests  int                    `json:"active_requests"`
	IdleCount       int                    `json:"idle_count"`
	WaitedMillis    int64                  `json:"waited_millis"`
	LastState       string                 `json:"last_state,omitempty"`
	LastError       string                 `json:"last_error,omitempty"`
	LastHeartbeatAt string                 `json:"last_heartbeat_at,omitempty"`
	LastProgress    string                 `json:"last_progress,omitempty"`
	NextAction      string                 `json:"next_action"`
	ImageBytes      int64                  `json:"image_bytes"`
	Requests        []AgentRequestActivity `json:"requests,omitempty"`
}

type AgentExchangeOutput struct {
	State    string                 `json:"state"`
	Activity AgentExchangeActivity  `json:"activity"`
	Results  []AgentExchangeResult  `json:"results,omitempty"`
	Requests []AgentExchangeRequest `json:"requests,omitempty"`
	Events   []AgentEvent           `json:"events,omitempty"`
}

type AgentCloseInput struct{}

type AgentCloseOutput struct {
	State string `json:"state"`
}

type AgentService interface {
	Open(context.Context, AgentOpenInput) (AgentOpenOutput, error)
	Exchange(context.Context, AgentExchangeInput) (AgentExchangeOutput, error)
	Close(context.Context, AgentCloseInput) (AgentCloseOutput, error)
}

func RegisterAgent(server *mcp.Server, service AgentService) error {
	if server == nil || service == nil {
		return errors.New("AGENT_SERVICE_REQUIRED")
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolAgentOpen,
		Description: "Open or resume the logical Web GPT bridge. Active request state survives a temporary bridge detach and is redelivered with the same request_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AgentOpenInput) (*mcp.CallToolResult, AgentOpenOutput, error) {
		output, err := service.Open(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: ToolAgentExchange,
		Description: "Submit final responses, request-scoped progress and optional stream_chunks, then receive queued or resumed requests. " +
			"delivery greater than one is the same request_id redelivered. Retryable parse/tool errors return error_detail and leave the request recoverable. " +
			"Heartbeat renews bridge liveness only; progress/stream/delivery/response renew request activity while a hard lifetime never extends. " +
			"Inline images are paired with ordered image_ref content parts and emitted as native MCP ImageContent with original bytes and MIME type; generic files remain unsupported.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AgentExchangeInput) (*mcp.CallToolResult, AgentExchangeOutput, error) {
		if input.Capacity < 0 {
			return nil, AgentExchangeOutput{}, errors.New("AGENT_EXCHANGE_INPUT_INVALID")
		}
		output, err := service.Exchange(ctx, input)
		if err != nil {
			return nil, output, err
		}
		result := &mcp.CallToolResult{}
		for _, request := range output.Requests {
			for _, item := range request.ContentItems {
				if item.Metadata.Kind != "image" {
					return nil, AgentExchangeOutput{}, errors.New("AGENT_IMAGE_ATTACHMENT_REQUIRED")
				}
				ref := item.Metadata.Ref
				if ref == "" {
					ref = item.Metadata.Name
				}
				uri := "cwapi://agent/" + url.PathEscape(request.RequestID) + "/" + url.PathEscape(ref) + "/" + url.PathEscape(item.Metadata.Name)
				result.Content = append(result.Content, attachments.MCPContent(item, "request_id="+request.RequestID+" ref="+ref, uri)...)
			}
		}
		if len(result.Content) == 0 {
			return nil, output, nil
		}
		return result, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolAgentClose,
		Description: "Detach CWapi's current Agent bridge. Active requests are preserved for resume until they complete, fail finally, disconnect, or expire.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AgentCloseInput) (*mcp.CallToolResult, AgentCloseOutput, error) {
		output, err := service.Close(ctx, input)
		return nil, output, err
	})
	return nil
}

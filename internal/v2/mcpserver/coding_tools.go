package mcpserver

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/AAAYNMMM/CWapi/internal/executiondiag"
	"github.com/AAAYNMMM/CWapi/internal/v2/attachments"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ToolCodingOpen       = "coding_open"
	ToolCodingExec       = "coding_exec"
	ToolCodingStatus     = "coding_status"
	ToolCodingAttachment = "coding_attachment"
	ToolCodingClose      = "coding_close"
)

type CodingOpenInput struct {
	RepositoryURL  string `json:"repository_url" jsonschema:"Git repository URL"`
	TargetRef      string `json:"target_ref" jsonschema:"branch or ref used as the task target"`
	ExpectedCommit string `json:"expected_commit,omitempty" jsonschema:"optional exact commit guard"`
	Resume         bool   `json:"resume,omitempty" jsonschema:"resume the repository's existing active/durable workspace when compatible"`
}

type CodingOpenOutput struct {
	Repository     string `json:"repository"`
	TargetRef      string `json:"target_ref"`
	ResolvedCommit string `json:"resolved_commit"`
	CurrentHead    string `json:"current_head"`
	CurrentBranch  string `json:"current_branch,omitempty"`
	Detached       bool   `json:"detached,omitempty"`
	TrackedDirty   bool   `json:"tracked_dirty"`
	Resumed        bool   `json:"resumed"`
	State          string `json:"state"`
}

type CodingExecInput struct {
	RepositoryURL  string   `json:"repository_url" jsonschema:"Git repository URL used to locate the repository's active Coding session"`
	TargetRef      string   `json:"target_ref,omitempty" jsonschema:"optional branch/ref used to select one branch-aware active Coding session"`
	Action         string   `json:"action,omitempty" jsonschema:"run (default), start, status or stop; start creates a CWapi-managed persistent process"`
	ProcessID      string   `json:"process_id,omitempty" jsonschema:"persistent process identifier required by status and stop"`
	Command        string   `json:"command,omitempty" jsonschema:"executable name or forward-slash path for run/start; do not include shell quoting"`
	Argv           []string `json:"argv,omitempty" jsonschema:"exact argument vector"`
	CWD            string   `json:"cwd,omitempty" jsonschema:"optional forward-slash relative directory inside the prepared repository"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" jsonschema:"optional command timeout from 1 to 600 seconds"`
	StdoutCursor   int64    `json:"stdout_cursor,omitempty" jsonschema:"for status, absolute stdout cursor returned by the previous status call"`
	StderrCursor   int64    `json:"stderr_cursor,omitempty" jsonschema:"for status, absolute stderr cursor returned by the previous status call"`
}

type CodingExecOutput struct {
	Diagnostics     *executiondiag.Snapshot `json:"diagnostics,omitempty"`
	State           string                  `json:"state"`
	ProcessID       string                  `json:"process_id,omitempty"`
	PID             int                     `json:"pid,omitempty"`
	StartedAt       string                  `json:"started_at,omitempty"`
	ExitCode        int                     `json:"exit_code"`
	Stdout          string                  `json:"stdout,omitempty"`
	Stderr          string                  `json:"stderr,omitempty"`
	StdoutCursor    int64                   `json:"stdout_cursor,omitempty"`
	StderrCursor    int64                   `json:"stderr_cursor,omitempty"`
	StdoutTruncated bool                    `json:"stdout_truncated,omitempty"`
	StderrTruncated bool                    `json:"stderr_truncated,omitempty"`
	Truncated       bool                    `json:"truncated,omitempty"`
}

type CodingStatusInput struct {
	RepositoryURL string `json:"repository_url" jsonschema:"Git repository URL used to locate the repository's active Coding session"`
	TargetRef     string `json:"target_ref,omitempty" jsonschema:"optional branch/ref used to select one branch-aware active Coding session"`
}

type CodingProcessSummary struct {
	ProcessID      string `json:"process_id"`
	State          string `json:"state"`
	Command        string `json:"command"`
	PID            int    `json:"pid,omitempty"`
	StartedAt      string `json:"started_at"`
	ElapsedSeconds int64  `json:"elapsed_seconds"`
}

type CodingStatusOutput struct {
	LastExecution        *executiondiag.Outcome `json:"last_execution,omitempty"`
	State                string                 `json:"state"`
	Repository           string                 `json:"repository,omitempty"`
	TargetRef            string                 `json:"target_ref,omitempty"`
	ResolvedCommit       string                 `json:"resolved_commit,omitempty"`
	CurrentHead          string                 `json:"current_head,omitempty"`
	CurrentBranch        string                 `json:"current_branch,omitempty"`
	Detached             bool                   `json:"detached,omitempty"`
	TrackingHead         string                 `json:"tracking_head,omitempty"`
	TrackedDirty         bool                   `json:"tracked_dirty,omitempty"`
	Divergence           string                 `json:"divergence,omitempty"`
	LastError            string                 `json:"last_error,omitempty"`
	ActiveAction         string                 `json:"active_action,omitempty"`
	ActiveCommand        string                 `json:"active_command,omitempty"`
	ActiveStartedAt      string                 `json:"active_started_at,omitempty"`
	ActiveElapsedSeconds *int64                 `json:"active_elapsed_seconds,omitempty"`
	PersistentProcesses  []CodingProcessSummary `json:"persistent_processes,omitempty"`
}

type CodingAttachmentInput struct {
	RepositoryURL string   `json:"repository_url" jsonschema:"Git repository URL used to locate the repository's active Coding session"`
	TargetRef     string   `json:"target_ref,omitempty" jsonschema:"optional branch/ref used to select one branch-aware active Coding session"`
	Paths         []string `json:"paths" jsonschema:"one or more forward-slash relative raster image paths inside the prepared repository"`
}

type CodingAttachmentOutput struct {
	Repository   string                 `json:"repository"`
	TargetRef    string                 `json:"target_ref,omitempty"`
	State        string                 `json:"state"`
	TotalBytes   int64                  `json:"total_bytes"`
	Attachments  []attachments.Metadata `json:"attachments"`
	ContentItems []attachments.Item     `json:"-"`
}

type CodingAttachmentService interface {
	Attachment(context.Context, CodingAttachmentInput) (CodingAttachmentOutput, error)
}
type CodingCloseInput struct {
	RepositoryURL string `json:"repository_url" jsonschema:"Git repository URL whose current active Coding session should be closed"`
	TargetRef     string `json:"target_ref,omitempty" jsonschema:"optional branch/ref used to select one branch-aware active Coding session"`
}

type CodingCloseOutput struct {
	Repository string `json:"repository,omitempty"`
	State      string `json:"state"`
}

type CodingService interface {
	Open(context.Context, CodingOpenInput) (CodingOpenOutput, error)
	Exec(context.Context, CodingExecInput) (CodingExecOutput, error)
	Status(context.Context, CodingStatusInput) (CodingStatusOutput, error)
	Close(context.Context, CodingCloseInput) (CodingCloseOutput, error)
}

func RegisterCoding(server *mcp.Server, service CodingService) error {
	if server == nil || service == nil {
		return errors.New("CODING_SERVICE_REQUIRED")
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolCodingOpen,
		Description: "Open or resume one branch-aware coding workspace selected by repository_url + target_ref. Different target_ref values of the same repository may have independent active sessions. Web GPT does not receive or retain a session ID, and CWapi does not start a Codex agent or model turn.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CodingOpenInput) (*mcp.CallToolResult, CodingOpenOutput, error) {
		input.RepositoryURL = strings.TrimSpace(input.RepositoryURL)
		input.TargetRef = strings.TrimSpace(input.TargetRef)
		input.ExpectedCommit = strings.TrimSpace(input.ExpectedCommit)
		if input.RepositoryURL == "" || input.TargetRef == "" {
			return nil, CodingOpenOutput{}, errors.New("CODING_OPEN_INPUT_INVALID")
		}
		output, err := service.Open(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolCodingExec,
		Description: "Run one exact foreground command or manage a CWapi-owned persistent process in an active workspace. target_ref optionally selects one branch-aware active session; omit it only when the repository has a single active branch. action defaults to run. Use start for commands expected to run longer than a normal MCP call, then status with stdout_cursor/stderr_cursor for incremental output, and stop with process_id. CWapi never starts a Codex thread/turn or uses a Codex account.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CodingExecInput) (*mcp.CallToolResult, CodingExecOutput, error) {
		input.RepositoryURL = strings.TrimSpace(input.RepositoryURL)
		input.TargetRef = strings.TrimSpace(input.TargetRef)
		input.Action = strings.ToLower(strings.TrimSpace(input.Action))
		input.ProcessID = strings.TrimSpace(input.ProcessID)
		input.Command = strings.TrimSpace(input.Command)
		input.CWD = strings.TrimSpace(input.CWD)
		if input.Action == "" {
			input.Action = "run"
		}
		validStart := (input.Action == "run" || input.Action == "start") && input.Command != "" && input.ProcessID == "" && input.StdoutCursor == 0 && input.StderrCursor == 0
		validStatus := input.Action == "status" && input.ProcessID != "" && input.Command == "" && len(input.Argv) == 0 && input.CWD == "" && input.TimeoutSeconds == 0 && input.StdoutCursor >= 0 && input.StderrCursor >= 0
		validStop := input.Action == "stop" && input.ProcessID != "" && input.Command == "" && len(input.Argv) == 0 && input.CWD == "" && input.TimeoutSeconds == 0 && input.StdoutCursor == 0 && input.StderrCursor == 0
		if input.RepositoryURL == "" || (!validStart && !validStatus && !validStop) {
			return nil, CodingExecOutput{}, errors.New("CODING_EXEC_INPUT_INVALID")
		}
		output, err := service.Exec(ctx, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolCodingStatus,
		Description: "Return current local Git truth for an active durable workspace without fetching or invoking a Codex model. target_ref optionally selects one branch-aware active session; omit it only when the repository has a single active branch. While busy, includes the active foreground action, executable, start time and elapsed seconds; argv is intentionally not exposed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CodingStatusInput) (*mcp.CallToolResult, CodingStatusOutput, error) {
		input.RepositoryURL = strings.TrimSpace(input.RepositoryURL)
		input.TargetRef = strings.TrimSpace(input.TargetRef)
		if input.RepositoryURL == "" {
			return nil, CodingStatusOutput{}, errors.New("CODING_REPOSITORY_REQUIRED")
		}
		output, err := service.Status(ctx, input)
		return nil, output, err
	})
	attachmentService, _ := service.(CodingAttachmentService)
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolCodingAttachment,
		Description: "Return one or more raster images from an active workspace as native MCP ImageContent. target_ref optionally selects one branch-aware active session; omit it only when the repository has a single active branch. Original image bytes and MIME type are preserved; CWapi does not recompress, resize, transcode or OCR them. Ordinary files remain unsupported.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CodingAttachmentInput) (*mcp.CallToolResult, CodingAttachmentOutput, error) {
		input.RepositoryURL = strings.TrimSpace(input.RepositoryURL)
		input.TargetRef = strings.TrimSpace(input.TargetRef)
		if input.RepositoryURL == "" || len(input.Paths) == 0 {
			return nil, CodingAttachmentOutput{}, errors.New("CODING_ATTACHMENT_INPUT_INVALID")
		}
		if attachmentService == nil {
			return nil, CodingAttachmentOutput{}, errors.New("CODING_ATTACHMENT_UNAVAILABLE")
		}
		output, err := attachmentService.Attachment(ctx, input)
		if err != nil {
			return nil, output, err
		}
		result := &mcp.CallToolResult{}
		for _, item := range output.ContentItems {
			if item.Metadata.Kind != "image" {
				return nil, CodingAttachmentOutput{}, errors.New("CODING_ATTACHMENT_IMAGE_ONLY")
			}
			ref := strings.TrimSpace(item.Metadata.Ref)
			if ref == "" {
				ref = item.Metadata.Name
			}
			uri := "cwapi://coding/" + url.PathEscape(output.Repository) + "/" + url.PathEscape(ref) + "/" + url.PathEscape(item.Metadata.Name)
			result.Content = append(result.Content, attachments.MCPContent(item, "repository="+output.Repository+" target_ref="+output.TargetRef, uri)...)
		}
		return result, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolCodingClose,
		Description: "Close one active Coding session handle without resetting, cleaning or deleting the durable workspace. target_ref optionally selects the branch-aware active session; omit it only when the repository has a single active branch.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CodingCloseInput) (*mcp.CallToolResult, CodingCloseOutput, error) {
		input.RepositoryURL = strings.TrimSpace(input.RepositoryURL)
		input.TargetRef = strings.TrimSpace(input.TargetRef)
		if input.RepositoryURL == "" {
			return nil, CodingCloseOutput{}, errors.New("CODING_REPOSITORY_REQUIRED")
		}
		output, err := service.Close(ctx, input)
		return nil, output, err
	})
	return nil
}

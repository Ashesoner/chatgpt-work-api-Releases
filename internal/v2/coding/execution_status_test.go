package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/AAAYNMMM/CWapi/internal/executiondiag"
	"github.com/AAAYNMMM/CWapi/internal/v2/codextoolhost"
	"github.com/AAAYNMMM/CWapi/internal/v2/mcpserver"
)

func TestReadyPreservesExecutionFailurePerBranchAndRecovers(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)
	runtime.open(t, "branch-b", false)
	status, err := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a"})
	if err != nil || status.LastExecution == nil || status.LastExecution.State != "unverified" {
		t.Fatal(status, err)
	}
	runtime.service.execute = func(context.Context, string, codextoolhost.ExecInput) (codextoolhost.ExecResult, error) {
		return codextoolhost.ExecResult{Diagnostics: &executiondiag.Snapshot{AccessProfile: "safe", Phases: []executiondiag.Phase{{Name: "command_exec", State: "failed"}}}}, errors.New("CODEX_TOOLHOST_COMMAND_TIMEOUT: deadline")
	}
	_, err = runtime.service.Exec(context.Background(), mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a", Command: "cmd"})
	if err == nil {
		t.Fatal("expected failure")
	}
	status, err = runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a"})
	if err != nil || status.State != "ready" || status.LastExecution.State != "failed" || status.LastExecution.Diagnostics.Boundary() != "command_exec" {
		t.Fatal(status, err)
	}
	other, err := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-b"})
	if err != nil || other.LastExecution.State != "unverified" {
		t.Fatal("failure leaked between branches", other, err)
	}
	if runtime.service.RuntimeSnapshot().LastExecution.Error == "" {
		t.Fatal("GUI runtime snapshot lost failure")
	}
	runtime.service.execute = func(context.Context, string, codextoolhost.ExecInput) (codextoolhost.ExecResult, error) {
		return codextoolhost.ExecResult{State: "completed"}, nil
	}
	_, err = runtime.service.Exec(context.Background(), mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a", Command: "cmd"})
	status, statusErr := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a"})
	if err != nil || statusErr != nil || status.LastExecution.State != "completed" || status.LastExecution.Error != "" {
		t.Fatal(status, err, statusErr)
	}
}

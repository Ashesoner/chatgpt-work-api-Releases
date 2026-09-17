package coding

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/AAAYNMMM/CWapi/internal/v2/codextoolhost"
	"github.com/AAAYNMMM/CWapi/internal/v2/mcpserver"
	"github.com/AAAYNMMM/CWapi/internal/v2/workspace"
)

const (
	branchTestRepositoryURL = "https://github.com/Ashesoner/Repo"
	branchTestRepository    = "ashesoner/repo"
)

type branchTestRuntime struct {
	service *Service

	mu            sync.Mutex
	executedPaths []string
}

func newBranchTestRuntime(t *testing.T) *branchTestRuntime {
	t.Helper()
	runtime := &branchTestRuntime{}
	prepare := func(_ context.Context, in workspace.PrepareInput) (workspace.Result, error) {
		identity, err := workspace.NewWorkspaceIdentity(branchTestRepository, in.TargetRef)
		if err != nil {
			return workspace.Result{}, err
		}
		branch := strings.TrimPrefix(identity.TargetRef, "refs/heads/")
		commit := strings.Repeat("a", 40)
		if branch == "branch-b" {
			commit = strings.Repeat("b", 40)
		}
		return workspace.Result{
			Repository:     branchTestRepository,
			Path:           "C:/tmp/" + branch,
			TargetRef:      identity.TargetRef,
			ResolvedCommit: commit,
			CurrentHead:    commit,
			CurrentBranch:  branch,
			Resumed:        in.Resume,
		}, nil
	}
	execute := func(_ context.Context, path string, _ codextoolhost.ExecInput) (codextoolhost.ExecResult, error) {
		runtime.mu.Lock()
		runtime.executedPaths = append(runtime.executedPaths, path)
		runtime.mu.Unlock()
		return codextoolhost.ExecResult{State: "completed", Stdout: path}, nil
	}
	inspect := func(_ context.Context, _ string, targetRef string) (workspace.Snapshot, error) {
		identity, err := workspace.NewWorkspaceIdentity(branchTestRepository, targetRef)
		if err != nil {
			return workspace.Snapshot{}, err
		}
		branch := strings.TrimPrefix(identity.TargetRef, "refs/heads/")
		commit := strings.Repeat("a", 40)
		if branch == "branch-b" {
			commit = strings.Repeat("b", 40)
		}
		return workspace.Snapshot{
			Repository:     branchTestRepository,
			TargetRef:      identity.TargetRef,
			ResolvedCommit: commit,
			CurrentHead:    commit,
			CurrentBranch:  branch,
			TrackingHead:   commit,
			Divergence:     "aligned",
		}, nil
	}
	service, err := newService(prepare, execute, inspect)
	if err != nil {
		t.Fatal(err)
	}
	runtime.service = service
	return runtime
}

func (r *branchTestRuntime) open(t *testing.T, ref string, resume bool) mcpserver.CodingOpenOutput {
	t.Helper()
	output, err := r.service.Open(context.Background(), mcpserver.CodingOpenInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     ref,
		Resume:        resume,
	})
	if err != nil {
		t.Fatalf("open %s resume=%v: %v", ref, resume, err)
	}
	return output
}

func (r *branchTestRuntime) executionCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.executedPaths)
}

func TestBranchAwareOpenOwnershipAndSameBranchResume(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)
	runtime.open(t, "branch-b", false)

	snapshot := runtime.service.RuntimeSnapshot()
	if snapshot.Active != 2 {
		t.Fatalf("active=%d want 2", snapshot.Active)
	}
	if len(snapshot.Repositories) != 1 || snapshot.Repositories[0] != branchTestRepository {
		t.Fatalf("unexpected repositories: %#v", snapshot.Repositories)
	}

	_, err := runtime.service.Open(context.Background(), mcpserver.CodingOpenInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "refs/heads/branch-a",
	})
	if err == nil || !strings.Contains(err.Error(), "CODING_WORKSPACE_BUSY") {
		t.Fatalf("same branch must stay BUSY, got %v", err)
	}

	resumed := runtime.open(t, "refs/heads/branch-a", true)
	if !resumed.Resumed || resumed.TargetRef != "refs/heads/branch-a" {
		t.Fatalf("unexpected resume output: %#v", resumed)
	}
	if runtime.service.RuntimeSnapshot().Active != 2 {
		t.Fatal("same-branch resume must not create another active workspace")
	}
}

func TestBranchAwareExecRoutesByTargetRef(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)
	runtime.open(t, "branch-b", false)

	for _, tc := range []struct {
		ref      string
		wantPath string
	}{
		{ref: "refs/heads/branch-a", wantPath: "C:/tmp/branch-a"},
		{ref: "branch-b", wantPath: "C:/tmp/branch-b"},
	} {
		output, err := runtime.service.Exec(context.Background(), mcpserver.CodingExecInput{
			RepositoryURL: branchTestRepositoryURL,
			TargetRef:     tc.ref,
			Command:       "test-command",
		})
		if err != nil {
			t.Fatalf("exec %s: %v", tc.ref, err)
		}
		if output.Stdout != tc.wantPath {
			t.Fatalf("exec %s used %q want %q", tc.ref, output.Stdout, tc.wantPath)
		}
	}
}

func TestBranchAwareStatusRoutesByTargetRef(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)
	runtime.open(t, "branch-b", false)

	for _, tc := range []struct {
		ref        string
		wantRef    string
		wantBranch string
	}{
		{ref: "branch-a", wantRef: "refs/heads/branch-a", wantBranch: "branch-a"},
		{ref: "refs/heads/branch-b", wantRef: "refs/heads/branch-b", wantBranch: "branch-b"},
	} {
		output, err := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{
			RepositoryURL: branchTestRepositoryURL,
			TargetRef:     tc.ref,
		})
		if err != nil {
			t.Fatalf("status %s: %v", tc.ref, err)
		}
		if output.TargetRef != tc.wantRef || output.CurrentBranch != tc.wantBranch {
			t.Fatalf("status %s => ref=%q branch=%q", tc.ref, output.TargetRef, output.CurrentBranch)
		}
	}
}

func TestBranchAwareCloseTargetsOnlySelectedBranch(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)
	runtime.open(t, "branch-b", false)

	output, err := runtime.service.Close(context.Background(), mcpserver.CodingCloseInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "refs/heads/branch-a",
	})
	if err != nil {
		t.Fatalf("close branch-a: %v", err)
	}
	if output.State != "closed" {
		t.Fatalf("close state=%q", output.State)
	}
	if runtime.service.RuntimeSnapshot().Active != 1 {
		t.Fatalf("active after close=%d want 1", runtime.service.RuntimeSnapshot().Active)
	}

	if _, err := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "branch-b",
	}); err != nil {
		t.Fatalf("branch-b must remain active: %v", err)
	}
	if _, err := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "branch-a",
	}); err == nil || !strings.Contains(err.Error(), "CODING_SESSION_NOT_ACTIVE") {
		t.Fatalf("closed branch-a must be inactive, got %v", err)
	}
}

func TestRepositoryOnlySingleBranchCompatibility(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)

	execOutput, err := runtime.service.Exec(context.Background(), mcpserver.CodingExecInput{
		RepositoryURL: branchTestRepositoryURL,
		Command:       "test-command",
	})
	if err != nil {
		t.Fatalf("repository-only exec: %v", err)
	}
	if execOutput.Stdout != "C:/tmp/branch-a" {
		t.Fatalf("repository-only exec used %q", execOutput.Stdout)
	}

	statusOutput, err := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{
		RepositoryURL: branchTestRepositoryURL,
	})
	if err != nil {
		t.Fatalf("repository-only status: %v", err)
	}
	if statusOutput.TargetRef != "refs/heads/branch-a" {
		t.Fatalf("repository-only status target=%q", statusOutput.TargetRef)
	}

	closeOutput, err := runtime.service.Close(context.Background(), mcpserver.CodingCloseInput{
		RepositoryURL: branchTestRepositoryURL,
	})
	if err != nil {
		t.Fatalf("repository-only close: %v", err)
	}
	if closeOutput.State != "closed" {
		t.Fatalf("repository-only close state=%q", closeOutput.State)
	}
}

func TestRepositoryOnlyRoutingRejectsMultipleBranches(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)
	runtime.open(t, "branch-b", false)

	_, execErr := runtime.service.Exec(context.Background(), mcpserver.CodingExecInput{
		RepositoryURL: branchTestRepositoryURL,
		Command:       "test-command",
	})
	if execErr == nil || !strings.Contains(execErr.Error(), "CODING_SESSION_AMBIGUOUS") {
		t.Fatalf("repository-only exec must be ambiguous, got %v", execErr)
	}
	if runtime.executionCount() != 0 {
		t.Fatal("ambiguous exec must not execute in any workspace")
	}

	_, statusErr := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{
		RepositoryURL: branchTestRepositoryURL,
	})
	if statusErr == nil || !strings.Contains(statusErr.Error(), "CODING_SESSION_AMBIGUOUS") {
		t.Fatalf("repository-only status must be ambiguous, got %v", statusErr)
	}

	_, closeErr := runtime.service.Close(context.Background(), mcpserver.CodingCloseInput{
		RepositoryURL: branchTestRepositoryURL,
	})
	if closeErr == nil || !strings.Contains(closeErr.Error(), "CODING_SESSION_AMBIGUOUS") {
		t.Fatalf("repository-only close must be ambiguous, got %v", closeErr)
	}
	if runtime.service.RuntimeSnapshot().Active != 2 {
		t.Fatal("ambiguous close must not close either branch")
	}
}

func TestUnknownTargetRefDoesNotFallback(t *testing.T) {
	runtime := newBranchTestRuntime(t)
	runtime.open(t, "branch-a", false)

	_, execErr := runtime.service.Exec(context.Background(), mcpserver.CodingExecInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "missing-branch",
		Command:       "test-command",
	})
	if execErr == nil || !strings.Contains(execErr.Error(), "CODING_SESSION_NOT_ACTIVE") || !strings.Contains(execErr.Error(), "refs/heads/missing-branch") {
		t.Fatalf("missing target exec: %v", execErr)
	}
	if runtime.executionCount() != 0 {
		t.Fatal("missing target must not fall back to the only active branch")
	}

	_, statusErr := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "missing-branch",
	})
	if statusErr == nil || !strings.Contains(statusErr.Error(), "CODING_SESSION_NOT_ACTIVE") {
		t.Fatalf("missing target status: %v", statusErr)
	}

	_, closeErr := runtime.service.Close(context.Background(), mcpserver.CodingCloseInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "missing-branch",
	})
	if closeErr == nil || !strings.Contains(closeErr.Error(), "CODING_SESSION_NOT_ACTIVE") {
		t.Fatalf("missing target close: %v", closeErr)
	}

	status, err := runtime.service.Status(context.Background(), mcpserver.CodingStatusInput{
		RepositoryURL: branchTestRepositoryURL,
		TargetRef:     "branch-a",
	})
	if err != nil || status.CurrentBranch != "branch-a" {
		t.Fatalf("existing branch must remain active: %#v err=%v", status, err)
	}
}

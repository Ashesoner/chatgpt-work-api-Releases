package workspace

import "testing"

func TestWorkspaceIdentityBranchAware(t *testing.T) {
	a, err := NewWorkspaceIdentity("Ashesoner/Repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewWorkspaceIdentity("ashesoner/repo", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewWorkspaceIdentity("ashesoner/repo", "feature/test")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("same branch must canonicalize identically: %#v %#v", a, b)
	}
	if WorkspaceKey(a) != WorkspaceKey(b) {
		t.Fatal("same repository + branch must have stable key")
	}
	if WorkspaceKey(a) == WorkspaceKey(c) {
		t.Fatal("different branches must have different workspace keys")
	}
	if len(WorkspaceKey(a)) != 64 {
		t.Fatalf("runtime workspace key length=%d want 64", len(WorkspaceKey(a)))
	}
	if WorkspaceDirectoryKey(a) != WorkspaceDirectoryKey(b) {
		t.Fatal("same repository + branch must have stable directory key")
	}
	if WorkspaceDirectoryKey(a) == WorkspaceDirectoryKey(c) {
		t.Fatal("different branches must have different directory keys")
	}
	if len(WorkspaceDirectoryKey(a)) != workspaceKeyHexLength || workspaceKeyHexLength != 24 {
		t.Fatalf("short workspace directory key length=%d want 24", len(WorkspaceDirectoryKey(a)))
	}
	if WorkspaceDirectoryKey(a) != WorkspaceKey(a)[:workspaceKeyHexLength] {
		t.Fatal("short directory key must be a stable truncation of the full runtime identity key")
	}
	if legacyBranchAwareWorkspaceKey(a) != WorkspaceKey(a) {
		t.Fatal("legacy branch-aware directory key must remain the original full identity key")
	}
	if WorkspaceDirectoryKey(a) == legacyWorkspaceKey(a.Repository) {
		t.Fatal("short V1 directory key must not reuse repository-only workspace key")
	}
}

func TestCanonicalTargetRef(t *testing.T) {
	canonical, branch, err := CanonicalTargetRef(" refs/heads/feature/a ")
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "refs/heads/feature/a" || branch != "feature/a" {
		t.Fatalf("unexpected canonical target: %q %q", canonical, branch)
	}
}

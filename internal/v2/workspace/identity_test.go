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
	if WorkspaceKey(a) == legacyWorkspaceKey(a.Repository) {
		t.Fatal("V1 key must not reuse repository-only workspace key")
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

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AAAYNMMM/CWapi/internal/security"
)

const testWorkspaceCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestIndexBranchAwareAndLegacyCompatibility(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	createTestWorkspace(t, root, "ashesoner/repo", "branch-a", false)
	createTestWorkspace(t, root, "ashesoner/repo", "branch-b", false)
	createTestWorkspace(t, root, "ashesoner/legacy", "main", true)

	invalid := filepath.Join(root, strings.Repeat("f", 64))
	if err := os.MkdirAll(invalid, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalid, "workspace.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{root: root}
	snapshot := manager.Index()
	if snapshot.RepositoryCount != 2 {
		t.Fatalf("repository_count=%d want 2", snapshot.RepositoryCount)
	}
	if !reflect.DeepEqual(snapshot.Repositories, []string{"ashesoner/legacy", "ashesoner/repo"}) {
		t.Fatalf("repositories=%#v", snapshot.Repositories)
	}
	want := []WorkspaceEntry{
		{Repository: "ashesoner/legacy", TargetRef: "refs/heads/main", Branch: "main"},
		{Repository: "ashesoner/repo", TargetRef: "refs/heads/branch-a", Branch: "branch-a"},
		{Repository: "ashesoner/repo", TargetRef: "refs/heads/branch-b", Branch: "branch-b"},
	}
	if !reflect.DeepEqual(snapshot.Workspaces, want) {
		t.Fatalf("workspaces=%#v want %#v", snapshot.Workspaces, want)
	}
	if snapshot.InvalidEntries != 1 {
		t.Fatalf("invalid_entries=%d want 1", snapshot.InvalidEntries)
	}
}

func TestDeleteAtIsBranchScoped(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	branchA := createTestWorkspace(t, root, "ashesoner/repo", "branch-a", false)
	branchB := createTestWorkspace(t, root, "ashesoner/repo", "branch-b", false)
	branchARepo := filepath.Join(branchA, "repo")
	shortRuntime, err := security.WorkspaceRuntimeRoot(dataRoot, branchARepo)
	if err != nil {
		t.Fatal(err)
	}
	legacyRuntime, err := security.LegacyWorkspaceRuntimeRoot(dataRoot, branchARepo)
	if err != nil {
		t.Fatal(err)
	}
	for _, runtimeRoot := range []string{shortRuntime, legacyRuntime} {
		if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := DeleteAt(dataRoot, "ashesoner/repo", "refs/heads/branch-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(branchA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("branch-a container still exists or unexpected error: %v", err)
	}
	for _, runtimeRoot := range []string{shortRuntime, legacyRuntime} {
		if _, err := os.Stat(runtimeRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("branch-a runtime root still exists or unexpected error: %s: %v", runtimeRoot, err)
		}
	}
	if info, err := os.Stat(branchB); err != nil || !info.IsDir() {
		t.Fatalf("branch-b must remain intact: %v", err)
	}
}

func TestResolveAtDoesNotFallbackToAnotherBranch(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	createTestWorkspace(t, root, "ashesoner/repo", "branch-a", false)
	createTestWorkspace(t, root, "ashesoner/repo", "branch-a", true)

	_, err := ResolveAt(dataRoot, "ashesoner/repo", "branch-b")
	if err == nil || !strings.Contains(err.Error(), "WORKSPACE_NOT_FOUND") {
		t.Fatalf("missing target must not fallback: %v", err)
	}
}

func TestResolveAtLegacyRequiresExactMetadata(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	legacy := createTestWorkspace(t, root, "ashesoner/repo", "legacy-branch", true)

	repoPath, err := ResolveAt(dataRoot, "ashesoner/repo", "refs/heads/legacy-branch")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(legacy, "repo")
	if repoPath != want {
		t.Fatalf("repoPath=%q want %q", repoPath, want)
	}

	if _, err := ResolveAt(dataRoot, "ashesoner/repo", "other-branch"); err == nil || !strings.Contains(err.Error(), "WORKSPACE_NOT_FOUND") {
		t.Fatalf("legacy metadata branch mismatch must be rejected: %v", err)
	}
}

func TestResolveAtPrefersBranchAwareContainer(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	legacy := createTestWorkspace(t, root, "ashesoner/repo", "main", true)
	primary := createTestWorkspace(t, root, "ashesoner/repo", "main", false)

	repoPath, err := ResolveAt(dataRoot, "ashesoner/repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if repoPath != filepath.Join(primary, "repo") {
		t.Fatalf("resolver did not prefer branch-aware container: %q", repoPath)
	}
	if repoPath == filepath.Join(legacy, "repo") {
		t.Fatal("resolver unexpectedly selected legacy container")
	}
}

func TestResolveAtRecognizesLegacyBranchAware64(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	legacy64 := createTestWorkspaceLegacyBranchAware64(t, root, "ashesoner/repo", "branch-a")

	repoPath, err := ResolveAt(dataRoot, "ashesoner/repo", "branch-a")
	if err != nil {
		t.Fatal(err)
	}
	if repoPath != filepath.Join(legacy64, "repo") {
		t.Fatalf("repoPath=%q want legacy64 repo", repoPath)
	}
}

func TestResolveAtRejectsMetadataMismatch(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	container := createTestWorkspace(t, root, "ashesoner/repo", "branch-a", false)
	metaPath := filepath.Join(container, "workspace.json")
	meta, err := loadMetadata(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	meta.TargetRef = "refs/heads/branch-b"
	if err := saveMetadata(metaPath, meta); err != nil {
		t.Fatal(err)
	}

	_, err = ResolveAt(dataRoot, "ashesoner/repo", "branch-a")
	if err == nil || !strings.Contains(err.Error(), "WORKSPACE_METADATA_IDENTITY_MISMATCH") {
		t.Fatalf("metadata mismatch must be rejected: %v", err)
	}

	m := &Manager{dataRoot: dataRoot, root: root}
	identity, err := NewWorkspaceIdentity("ashesoner/repo", "branch-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareContainer(identity); err == nil || !strings.Contains(err.Error(), "WORKSPACE_METADATA_IDENTITY_MISMATCH") {
		t.Fatalf("prepare must reject metadata mismatch: %v", err)
	}
}

func TestPrepareContainerPrefersShortThenLegacyFormats(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, "workspaces")
	m := &Manager{dataRoot: dataRoot, root: root}
	identity, err := NewWorkspaceIdentity("ashesoner/repo", "main")
	if err != nil {
		t.Fatal(err)
	}

	legacyRepo := createTestWorkspace(t, root, identity.Repository, identity.TargetRef, true)
	selected, err := m.prepareContainer(identity)
	if err != nil || selected != legacyRepo {
		t.Fatalf("repository-only legacy selected=%q err=%v", selected, err)
	}

	if err := os.RemoveAll(legacyRepo); err != nil {
		t.Fatal(err)
	}
	legacy64 := createTestWorkspaceLegacyBranchAware64(t, root, identity.Repository, identity.TargetRef)
	selected, err = m.prepareContainer(identity)
	if err != nil || selected != legacy64 {
		t.Fatalf("branch-aware legacy64 selected=%q err=%v", selected, err)
	}

	short := createTestWorkspace(t, root, identity.Repository, identity.TargetRef, false)
	selected, err = m.prepareContainer(identity)
	if err != nil || selected != short {
		t.Fatalf("short selected=%q err=%v", selected, err)
	}
}

func createTestWorkspaceLegacyBranchAware64(t *testing.T, root, repositoryName, targetRef string) string {
	t.Helper()
	identity, err := NewWorkspaceIdentity(repositoryName, targetRef)
	if err != nil {
		t.Fatal(err)
	}
	container := filepath.Join(root, legacyBranchAwareWorkspaceKey(identity))
	if err := os.MkdirAll(filepath.Join(container, "repo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveMetadata(filepath.Join(container, "workspace.json"), metadata{
		Schema: metadataSchema, Repository: identity.Repository, NormalizedURL: "https://github.com/" + identity.Repository,
		TargetRef: identity.TargetRef, ResolvedCommit: testWorkspaceCommit,
	}); err != nil {
		t.Fatal(err)
	}
	return container
}

func createTestWorkspace(t *testing.T, root, repositoryName, targetRef string, legacy bool) string {
	t.Helper()
	identity, err := NewWorkspaceIdentity(repositoryName, targetRef)
	if err != nil {
		t.Fatal(err)
	}
	key := WorkspaceDirectoryKey(identity)
	if legacy {
		key = legacyWorkspaceKey(identity.Repository)
	}
	container := filepath.Join(root, key)
	if err := os.MkdirAll(filepath.Join(container, "repo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveMetadata(filepath.Join(container, "workspace.json"), metadata{
		Schema:         metadataSchema,
		Repository:     identity.Repository,
		NormalizedURL:  "https://github.com/" + identity.Repository,
		TargetRef:      identity.TargetRef,
		ResolvedCommit: testWorkspaceCommit,
	}); err != nil {
		t.Fatal(err)
	}
	return container
}

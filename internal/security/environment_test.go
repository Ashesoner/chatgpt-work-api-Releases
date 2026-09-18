package security

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceRuntimeRootUsesStableShortKey(t *testing.T) {
	dataRoot := t.TempDir()
	workspaceRoot := filepath.Join(dataRoot, "workspaces", "workspace-id", "repo")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	first, err := WorkspaceRuntimeRoot(dataRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WorkspaceRuntimeRoot(dataRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("runtime root must be stable: %q != %q", first, second)
	}
	key := filepath.Base(first)
	if len(key) != workspaceRuntimeKeyHexLength {
		t.Fatalf("runtime key length=%d want %d", len(key), workspaceRuntimeKeyHexLength)
	}
	if _, err := hex.DecodeString(key); err != nil {
		t.Fatalf("runtime key must be hex: %v", err)
	}
}

func TestWorkspaceRuntimeRootDoesNotReuseLegacy64Directory(t *testing.T) {
	dataRoot := t.TempDir()
	workspaceRoot := filepath.Join(dataRoot, "workspaces", "legacy-workspace", "repo")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyRoot, err := LegacyWorkspaceRuntimeRoot(dataRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(filepath.Base(legacyRoot)) != 64 {
		t.Fatalf("legacy runtime key length=%d want 64", len(filepath.Base(legacyRoot)))
	}
	if err := os.MkdirAll(legacyRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := WorkspaceRuntimeRoot(dataRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got == legacyRoot {
		t.Fatal("new commands must not reuse the long legacy runtime root")
	}
	if len(filepath.Base(got)) != workspaceRuntimeKeyHexLength {
		t.Fatalf("new runtime key length=%d want %d", len(filepath.Base(got)), workspaceRuntimeKeyHexLength)
	}
}

func TestWorkspaceRuntimeRootRejectsInvalidShortEntry(t *testing.T) {
	dataRoot := t.TempDir()
	workspaceRoot := filepath.Join(dataRoot, "workspaces", "workspace-id", "repo")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalPath(workspaceRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	shortRoot := filepath.Join(dataRoot, "runtime", "workspaces", workspaceRuntimeKey(canonical))
	if err := os.MkdirAll(filepath.Dir(shortRoot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shortRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := WorkspaceRuntimeRoot(dataRoot, workspaceRoot); err == nil || !strings.Contains(err.Error(), "SECURITY_WORKSPACE_RUNTIME_ROOT_INVALID") {
		t.Fatalf("invalid short runtime entry must be rejected: %v", err)
	}
}

func TestPrepareCommandRuntimeUsesShortWorkspaceCache(t *testing.T) {
	dataRoot := t.TempDir()
	workspaceRoot := filepath.Join(dataRoot, "workspaces", "workspace-id", "repo")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := PrepareCommandRuntime(dataRoot, workspaceRoot, "proc-"+strings.Repeat("a", 24), string(ProfileSafe))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Cleanup()

	if len(filepath.Base(runtime.WorkspaceRuntime)) != workspaceRuntimeKeyHexLength {
		t.Fatalf("workspace runtime is not short: %q", runtime.WorkspaceRuntime)
	}
	env, err := runtime.Environment([]string{"PATH=" + os.Getenv("PATH")}, "safe")
	if err != nil {
		t.Fatal(err)
	}
	wantModCache := filepath.Join(runtime.WorkspaceRuntime, "cache", "go-mod")
	found := false
	for _, entry := range env {
		if strings.EqualFold(entry, "GOMODCACHE="+wantModCache) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("GOMODCACHE does not point at short runtime root: %q", wantModCache)
	}
}

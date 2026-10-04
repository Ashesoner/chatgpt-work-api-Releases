//go:build windows

package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCommandIdentityStableAcrossHomesCWDAndRestart(t *testing.T) {
	root := t.TempDir()
	repo, data := filepath.Join(root, "repo"), filepath.Join(root, "data")
	sub := filepath.Join(repo, "sub")
	extra := filepath.Join(root, "cache")
	for _, dir := range []string{sub, extra} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	seedHome := func(name, cwd string, extras []string) commandCapabilities {
		t.Helper()
		home := filepath.Join(root, name)
		if err := os.Mkdir(home, 0700); err != nil {
			t.Fatal(err)
		}
		if err := prepareCommandIdentity(data, home, CommandSpec{CWD: cwd, WritableRoot: repo, WritableRoots: extras, Sandbox: CommandSandboxWorkspaceWrite}); err != nil {
			t.Fatal(err)
		}
		payload, err := os.ReadFile(filepath.Join(home, "cap_sid"))
		if err != nil {
			t.Fatal(err)
		}
		var caps commandCapabilities
		if err := json.Unmarshal(payload, &caps); err != nil {
			t.Fatal(err)
		}
		return caps
	}
	first := seedHome("first", repo, []string{extra})
	second := seedHome("second", sub, nil)
	repoKey, _ := commandPathKey(repo)
	extraKey, _ := commandPathKey(extra)
	if first.WritableRootByPath[repoKey] == first.WritableRootByPath[extraKey] {
		t.Fatal("different writable roots share a capability")
	}
	canonicalAlias, err := commandPathKey(strings.ToUpper(filepath.ToSlash(filepath.Join(repo, "."))))
	if err != nil || canonicalAlias != repoKey {
		t.Fatal("canonical-equivalent root changed identity", canonicalAlias, err)
	}
	if first.WorkspaceByCWD[repoKey] != second.WritableRootByPath[repoKey] {
		t.Fatal("CWD changed root capability")
	}
	if second.WritableRootByPath[extraKey] != "" {
		t.Fatal("stale extra-root capability leaked into new command")
	}
	third := seedHome("third", repo, []string{extra})
	if third.WritableRootByPath[extraKey] != first.WritableRootByPath[extraKey] {
		t.Fatal("recreated private home lost durable root identity")
	}
	if _, err := windows.StringToSid(first.WorkspaceByCWD[repoKey]); err != nil {
		t.Fatalf("invalid Windows SID: %v", err)
	}
	otherRepo := filepath.Join(root, "other-repo")
	if err := os.Mkdir(otherRepo, 0700); err != nil {
		t.Fatal(err)
	}
	otherHome := filepath.Join(root, "other-home")
	if err := os.Mkdir(otherHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := prepareCommandIdentity(data, otherHome, CommandSpec{CWD: otherRepo, WritableRoot: otherRepo, WritableRoots: []string{extra}, Sandbox: CommandSandboxWorkspaceWrite}); err != nil {
		t.Fatal(err)
	}
	payload, _ := os.ReadFile(filepath.Join(otherHome, "cap_sid"))
	var other commandCapabilities
	_ = json.Unmarshal(payload, &other)
	if other.WritableRootByPath[extraKey] == first.WritableRootByPath[extraKey] {
		t.Fatal("workspaces share capability identity")
	}
}

func TestCommandIdentityConcurrentCreationAndCorruptState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	seeds := make([][]byte, 16)
	var wg sync.WaitGroup
	for i := range seeds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			seeds[i], err = loadCommandIdentity(path, "repo")
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for _, seed := range seeds {
		if !bytes.Equal(seed, seeds[0]) {
			t.Fatal("concurrent creation changed identity")
		}
	}
	if _, err := loadCommandIdentity(path, "other-repo"); err == nil {
		t.Fatal("workspace identity mismatch accepted")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCommandIdentity(path, "repo"); err == nil {
		t.Fatal("corrupt identity silently regenerated")
	}
}

func TestFullCommandDoesNotPersistSandboxIdentity(t *testing.T) {
	root := t.TempDir()
	if err := prepareCommandIdentity(filepath.Join(root, "absent"), root, CommandSpec{Sandbox: CommandSandboxFullAccess}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cap_sid")); !os.IsNotExist(err) {
		t.Fatal("FULL wrote sandbox state")
	}
}

// Grant an external root, then omit it on the next request from the SAME
// workspace. Its old ACL remains, but the new restricted token must not carry
// that capability. This exercises the backend rather than a host path precheck.
func TestWindowsRemovedWritableRootCapabilityDenied(t *testing.T) {
	executable, parent := os.Getenv("CWAPI_CODEX_TEST_EXECUTABLE"), os.Getenv("CWAPI_TEST_ROOT")
	if executable == "" || parent == "" {
		t.Skip("native runtime opt-in not configured")
	}
	root, err := os.MkdirTemp(parent, "removed-root-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	repo, extra := filepath.Join(root, "repo"), filepath.Join(root, "external")
	for _, dir := range []string{repo, extra} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	service, err := NewCommandService(filepath.Join(root, "data"), executable)
	if err != nil {
		t.Fatal(err)
	}
	for i, granted := range []bool{true, false, true} {
		file := filepath.Join(extra, fmt.Sprintf("request-%d.txt", i))
		spec := CommandSpec{ProcessID: fmt.Sprintf("proc-%024x", i+1), Executable: `C:\Windows\System32\cmd.exe`, Argv: []string{"/d", "/c", fmt.Sprintf(`echo ROOT_OK>..\external\request-%d.txt`, i)}, CWD: repo, WritableRoot: repo, Sandbox: CommandSandboxWorkspaceWrite, Environment: []string{`SystemRoot=C:\Windows`, `ComSpec=C:\Windows\System32\cmd.exe`, `PATH=C:\Windows\System32`, "TEMP=" + repo, "TMP=" + repo}}
		if granted {
			spec.WritableRoots = []string{extra}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		handle, err := service.StartCommand(ctx, spec)
		if err != nil {
			cancel()
			t.Fatal("pre-launch failure:", err)
		}
		var result CommandResult
		select {
		case result = <-handle.Done():
		case <-ctx.Done():
			_ = handle.Stop()
			cancel()
			t.Fatal("command timeout")
		}
		cancel()
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		_, fileErr := os.Stat(file)
		if granted && (result.ExitCode != 0 || fileErr != nil) {
			t.Fatal("granted root write failed", result, fileErr)
		}
		if !granted && (result.ExitCode == 0 || !os.IsNotExist(fileErr)) {
			t.Fatal("omitted root retained write capability", result, fileErr)
		}
	}
	t.Log("same workspace: grant external root -> omit (native denial) -> regrant passed")
}

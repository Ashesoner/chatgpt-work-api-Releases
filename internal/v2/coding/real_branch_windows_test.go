//go:build windows

package coding

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/v2/codextoolhost"
	v2config "github.com/AAAYNMMM/CWapi/internal/v2/config"
	"github.com/AAAYNMMM/CWapi/internal/v2/mcpserver"
	"github.com/AAAYNMMM/CWapi/internal/v2/workspace"
)

// Real local Git clones are resumed through the production Manager, then
// routed through the production Coding Service and pinned Windows toolhost.
// Only the remote transport is replaced by preparing local fixture clones;
// Git branch/HEAD/status and execution/process ownership are not mocked.
func TestWindowsRealGitBranchLifecycle(t *testing.T) {
	executable, parent := os.Getenv("CWAPI_CODEX_TEST_EXECUTABLE"), os.Getenv("CWAPI_TEST_ROOT")
	if executable == "" || parent == "" {
		t.Skip("native runtime opt-in not configured")
	}
	for _, profile := range []string{"safe", "full"} {
		t.Run(profile, func(t *testing.T) {
			root, err := os.MkdirTemp(parent, "real-branches-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			gitExe, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			hooks := filepath.Join(root, "empty-hooks")
			if err := os.Mkdir(hooks, 0700); err != nil {
				t.Fatal(err)
			}
			git := func(args ...string) string {
				t.Helper()
				prefix := []string{"-c", "core.autocrlf=false", "-c", "core.hooksPath=" + hooks, "-c", "commit.gpgsign=false", "-c", "user.name=CWapi fixture", "-c", "user.email=fixture@example.invalid"}
				command := exec.Command(gitExe, append(prefix, args...)...)
				command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("fixture git %v: %v: %s", args, err, output)
				}
				return strings.TrimSpace(string(output))
			}
			remote := filepath.Join(root, "local-remote")
			git("init", "-b", "branch-a", remote)
			write := func(path, value string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(remote, ".gitignore"), "started.txt\nbusy.txt\n")
			write(filepath.Join(remote, "branch.txt"), "branch-a\n")
			git("-C", remote, "add", ".")
			git("-C", remote, "commit", "-m", "fixture branch A")
			git("-C", remote, "checkout", "-b", "branch-b")
			write(filepath.Join(remote, "branch.txt"), "branch-b\n")
			git("-C", remote, "commit", "-am", "fixture branch B")
			data := filepath.Join(root, "data")
			paths, heads := map[string]string{}, map[string]string{}
			for _, branch := range []string{"branch-a", "branch-b"} {
				identity, err := workspace.NewWorkspaceIdentity(branchTestRepository, branch)
				if err != nil {
					t.Fatal(err)
				}
				container := filepath.Join(data, "workspaces", workspace.WorkspaceDirectoryKey(identity))
				if err := os.MkdirAll(container, 0700); err != nil {
					t.Fatal(err)
				}
				paths[branch] = filepath.Join(container, "repo")
				git("clone", "--no-hardlinks", "--branch", branch, remote, paths[branch])
				heads[branch] = git("-C", paths[branch], "rev-parse", "HEAD")
				git("-C", paths[branch], "remote", "set-url", "origin", branchTestRepositoryURL)
				metadata, err := json.Marshal(map[string]any{"schema": "cwapi.workspace.v1", "repository": branchTestRepository, "normalized_url": branchTestRepositoryURL, "target_ref": identity.TargetRef, "resolved_commit": heads[branch], "updated_at": time.Now().UTC().Format(time.RFC3339Nano)})
				if err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(container, "workspace.json"), string(metadata))
			}
			if heads["branch-a"] == heads["branch-b"] {
				t.Fatal("fixture branches must have distinct commits")
			}
			manager, err := workspace.NewManager(data, gitExe)
			if err != nil {
				t.Fatal(err)
			}
			host, err := codextoolhost.New(data, v2config.CodexConfig{Executable: executable, AccessProfile: profile})
			if err != nil {
				t.Fatal(err)
			}
			service, err := New(manager, host)
			if err != nil {
				_ = host.Close(context.Background())
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := service.CloseAll(ctx); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			for _, branch := range []string{"branch-a", "branch-b"} {
				result, err := service.Open(ctx, mcpserver.CodingOpenInput{RepositoryURL: branchTestRepositoryURL, TargetRef: branch, ExpectedCommit: heads[branch], Resume: true})
				if err != nil || result.CurrentBranch != branch || result.CurrentHead != heads[branch] {
					t.Fatal("real Git open:", result, err)
				}
			}
			if service.RuntimeSnapshot().Active != 2 {
				t.Fatal("both branches must be active")
			}
			status := func(branch string) mcpserver.CodingStatusOutput {
				t.Helper()
				result, err := service.Status(ctx, mcpserver.CodingStatusInput{RepositoryURL: branchTestRepositoryURL, TargetRef: branch})
				if err != nil || result.CurrentBranch != branch || result.CurrentHead != heads[branch] {
					t.Fatal("independent real Git status:", result, err)
				}
				return result
			}
			echoBranch := func(branch string) {
				t.Helper()
				result, err := service.Exec(ctx, mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "refs/heads/" + branch, Command: "C:/Windows/System32/cmd.exe", Argv: []string{"/d", "/c", "type branch.txt"}, TimeoutSeconds: 20})
				if err != nil || result.State != "completed" || strings.TrimSpace(result.Stdout) != branch {
					t.Fatal("target_ref routed to wrong Git clone:", result, err)
				}
			}
			for _, branch := range []string{"branch-a", "branch-b"} {
				status(branch)
				echoBranch(branch)
			}
			_, err = service.Exec(ctx, mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, Command: "cmd"})
			if err == nil || !strings.Contains(err.Error(), "CODING_SESSION_AMBIGUOUS") {
				t.Fatal("ambiguous exec:", err)
			}
			_, err = service.Status(ctx, mcpserver.CodingStatusInput{RepositoryURL: branchTestRepositoryURL})
			if err == nil || !strings.Contains(err.Error(), "CODING_SESSION_AMBIGUOUS") {
				t.Fatal("ambiguous status:", err)
			}
			_, err = service.Close(ctx, mcpserver.CodingCloseInput{RepositoryURL: branchTestRepositoryURL})
			if err == nil || !strings.Contains(err.Error(), "CODING_SESSION_AMBIGUOUS") {
				t.Fatal("ambiguous close:", err)
			}
			_, err = service.Exec(ctx, mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "missing", Command: "cmd"})
			if err == nil || !strings.Contains(err.Error(), "CODING_SESSION_NOT_ACTIVE") {
				t.Fatal("inactive branch:", err)
			}
			longCommand := "C:/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
			longArgv := func(marker string) []string {
				return []string{"-NoProfile", "-NonInteractive", "-Command", `[IO.File]::WriteAllText((Join-Path (Get-Location) '` + marker + `'),'ready'); Start-Sleep -Seconds 60`}
			}
			ids := map[string]string{}
			for _, branch := range []string{"branch-a", "branch-b"} {
				result, err := service.Exec(ctx, mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: branch, Action: "start", Command: longCommand, Argv: longArgv("started.txt")})
				if err != nil || result.State != "running" {
					t.Fatal("persistent start:", result, err)
				}
				ids[branch] = result.ProcessID
				waitForMarker(t, paths[branch])
			}
			for _, branch := range []string{"branch-a", "branch-b"} {
				processes := status(branch).PersistentProcesses
				if len(processes) != 1 || processes[0].ProcessID != ids[branch] || processes[0].State != "running" {
					t.Fatal("persistent process crossed workspace:", processes)
				}
			}
			foregroundCtx, stopForeground := context.WithCancel(ctx)
			defer stopForeground()
			foreground := make(chan error, 1)
			go func() {
				_, err := service.Exec(foregroundCtx, mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a", Command: longCommand, Argv: longArgv("busy.txt"), TimeoutSeconds: 60})
				foreground <- err
			}()
			deadline := time.Now().Add(20 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(paths["branch-a"], "busy.txt")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("foreground did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if status("branch-a").State != "busy" || status("branch-b").State != "ready" {
				t.Fatal("branch busy status leaked")
			}
			_, err = service.Exec(ctx, mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a", Command: "cmd"})
			if err == nil || !strings.Contains(err.Error(), "CODING_COMMAND_ACTIVE") {
				t.Fatal("same branch concurrency:", err)
			}
			echoBranch("branch-b")
			stopForeground()
			select {
			case <-foreground:
			case <-time.After(8 * time.Second):
				t.Fatal("foreground cancellation did not finish")
			}
			_, err = service.Close(ctx, mcpserver.CodingCloseInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a"})
			if err != nil {
				t.Fatal(err)
			}
			assertRenameReleased(t, paths["branch-a"])
			processes := status("branch-b").PersistentProcesses
			if len(processes) != 1 || processes[0].ProcessID != ids["branch-b"] || processes[0].State != "running" {
				t.Fatal("close A affected B:", processes)
			}
			echoBranch("branch-b")
			if err := service.CloseAll(ctx); err != nil {
				t.Fatal(err)
			}
			assertRenameReleased(t, paths["branch-b"])
			if len(host.WorkspaceProcesses(paths["branch-b"])) != 0 {
				t.Fatal("CloseAll left persistent process")
			}
			t.Logf("real Git distinct heads %s / %s; active=2; routes/status/concurrency/selectors/persistent isolation/close A/CloseAll/rename PASS", heads["branch-a"], heads["branch-b"])
		})
	}
}

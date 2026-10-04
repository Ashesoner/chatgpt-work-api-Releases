//go:build windows

package coding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/v2/codextoolhost"
	"github.com/AAAYNMMM/CWapi/internal/v2/commandproxy"
	v2config "github.com/AAAYNMMM/CWapi/internal/v2/config"
	"github.com/AAAYNMMM/CWapi/internal/v2/mcpserver"
	"github.com/AAAYNMMM/CWapi/internal/v2/workspace"
)

func TestMain(m *testing.M) {
	if path, ok := commandproxy.IsInvocation(os.Args); ok {
		os.Exit(commandproxy.Run(path))
	}
	os.Exit(m.Run())
}

func TestWindowsCodingCloseAndShutdownReleaseTrees(t *testing.T) {
	executable, parent := os.Getenv("CWAPI_CODEX_TEST_EXECUTABLE"), os.Getenv("CWAPI_TEST_ROOT")
	if executable == "" || parent == "" {
		t.Skip("native runtime opt-in not configured")
	}
	for _, profile := range []string{"safe", "full"} {
		t.Run(profile, func(t *testing.T) {
			root, err := os.MkdirTemp(parent, "lifecycle-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			host, err := codextoolhost.New(filepath.Join(root, "data"), v2config.CodexConfig{Executable: executable, AccessProfile: profile})
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			paths := map[string]string{"refs/heads/branch-a": filepath.Join(root, "repo-a"), "refs/heads/branch-b": filepath.Join(root, "repo-b")}
			for _, path := range paths {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			prepare := func(_ context.Context, input workspace.PrepareInput) (workspace.Result, error) {
				ref, _, err := workspace.CanonicalTargetRef(input.TargetRef)
				return workspace.Result{Repository: branchTestRepository, TargetRef: ref, Path: paths[ref]}, err
			}
			service, err := newService(prepare, host.Exec, nil)
			if err != nil {
				t.Fatal(err)
			}
			service.stopProcesses, service.closeRuntime, service.listProcesses = host.StopWorkspace, host.Close, host.WorkspaceProcesses
			for _, ref := range []string{"branch-a", "branch-b"} {
				if _, err := service.Open(context.Background(), mcpserver.CodingOpenInput{RepositoryURL: branchTestRepositoryURL, TargetRef: ref}); err != nil {
					t.Fatal(err)
				}
			}
			command := "C:/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
			argv := []string{"-NoProfile", "-NonInteractive", "-Command", `[IO.File]::WriteAllText((Join-Path (Get-Location) 'started.txt'),'ready'); Start-Sleep -Seconds 60`}
			for _, ref := range []string{"branch-a", "branch-b"} {
				result, err := service.Exec(context.Background(), mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: ref, Action: "start", Command: command, Argv: argv})
				if err != nil || result.State != "running" {
					t.Fatal(result, err)
				}
			}
			for _, path := range paths {
				waitForMarker(t, path)
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err = service.Close(closeCtx, mcpserver.CodingCloseInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-a"})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			assertRenameReleased(t, paths["refs/heads/branch-a"])
			if processes := host.WorkspaceProcesses(paths["refs/heads/branch-b"]); len(processes) != 1 {
				t.Fatalf("close killed other branch: %+v", processes)
			}
			// Also cancel an active foreground command during service shutdown.
			if err := os.Remove(filepath.Join(paths["refs/heads/branch-b"], "started.txt")); err != nil {
				t.Fatal(err)
			}
			foreground := make(chan error, 1)
			go func() {
				_, err := service.Exec(context.Background(), mcpserver.CodingExecInput{RepositoryURL: branchTestRepositoryURL, TargetRef: "branch-b", Command: command, Argv: argv, TimeoutSeconds: 120})
				foreground <- err
			}()
			waitForMarker(t, paths["refs/heads/branch-b"])
			shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			err = service.CloseAll(shutdownCtx)
			stop()
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-foreground:
			case <-time.After(time.Second):
				t.Fatal("foreground not released")
			}
			assertRenameReleased(t, paths["refs/heads/branch-b"])
			if len(host.WorkspaceProcesses(paths["refs/heads/branch-b"])) != 0 {
				t.Fatal("shutdown left persistent process")
			}
			t.Log("persistent start -> branch close; foreground + persistent -> CloseAll; both immediate renames passed")
		})
	}
}

func waitForMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(path, "started.txt")); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("target command never wrote startup marker")
}

func assertRenameReleased(t *testing.T, path string) {
	t.Helper()
	moved := path + "-closed"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal("owned directory still locked:", err)
	}
	if err := os.Rename(moved, path); err != nil {
		t.Fatal(err)
	}
}

//go:build windows

package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/processlaunch"
	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if os.Getenv("CWAPI_PRE_COMMAND_HELPER") == "1" {
		if len(os.Args) > 1 && os.Args[1] == "app-server" {
			os.Exit(runPreCommandHelper(false))
		}
		if len(os.Args) > 1 && os.Args[1] == "--pre-command-child" {
			os.Exit(runPreCommandHelper(true))
		}
	}
	os.Exit(m.Run())
}

// Exercise real Windows jobs and directory handles, using a deterministic
// app-server fixture that stalls readiness before command/exec can be sent.
// This does not require a logged-in Codex session or modify a user workspace.
func TestWindowsPreCommandReadinessCancelReleasesWorkspace(t *testing.T) {
	testWindowsPreCommandCancelReleasesWorkspace(t, "readiness")
}

func TestWindowsPreCommandSetupCancelReleasesWorkspace(t *testing.T) {
	testWindowsPreCommandCancelReleasesWorkspace(t, "setup")
}

func testWindowsPreCommandCancelReleasesWorkspace(t *testing.T, phase string) {
	parent := os.Getenv("CWAPI_TEST_ROOT")
	if parent == "" {
		t.Skip("set CWAPI_TEST_ROOT for native owned-process checks")
	}
	for _, corruptScope := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup_error_%t", corruptScope), func(t *testing.T) {
			root, err := os.MkdirTemp(parent, "pre-command-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			repo, home := filepath.Join(root, "repo"), filepath.Join(root, "home")
			if err := os.Mkdir(repo, 0700); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			service := &Service{codexExe: executable}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				client, err := service.startCommandClient(ctx, home, repo, []string{
					"CWAPI_PRE_COMMAND_HELPER=1", "CWAPI_PRE_COMMAND_ROOT=" + repo,
					"CWAPI_PRE_COMMAND_PHASE=" + phase,
					"SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + root, "TMP=" + root,
				})
				if client != nil {
					err = errors.Join(err, errors.New("unexpected successful readiness"), closeCommandClient(client))
				}
				done <- err
			}()
			var pids struct{ Server, Child int }
			deadline := time.Now().Add(10 * time.Second)
			for {
				payload, err := os.ReadFile(filepath.Join(repo, "readiness.json"))
				if err == nil && json.Unmarshal(payload, &pids) == nil {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("failed before readiness fixture: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("readiness fixture did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			var owned *Client
			var scope *processScope
			clientProcessScopes.Range(func(key, value any) bool {
				client := key.(*Client)
				if client.home == home {
					owned, scope = client, value.(*processScope)
					return false
				}
				return true
			})
			if owned == nil || scope == nil {
				t.Fatal("readiness ran without an owned process tree")
			}
			t.Cleanup(func() {
				_ = scope.Close()
				_ = closeCommandClient(owned)
			})
			var processes []windows.Handle
			for _, pid := range []int{pids.Server, pids.Child} {
				process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
				if err != nil {
					t.Fatal(err)
				}
				processes = append(processes, process)
				defer windows.CloseHandle(process)
			}
			// Prove the child really holds the workspace before testing release.
			if err := os.Rename(repo, repo+"-blocked"); err == nil {
				_ = os.Rename(repo+"-blocked", repo)
				t.Fatal("fixture did not hold the workspace")
			}
			if corruptScope {
				// Keep the real job for fixture cleanup while injecting a failed
				// release. The original cancellation and cleanup error must survive.
				clientProcessScopes.Store(owned, "invalid test scope")
			}
			started := time.Now()
			cancel()
			select {
			case err := <-done:
				code := "CODEX_SANDBOX_READINESS_FAILED"
				if phase == "setup" {
					code = "CODEX_SANDBOX_SETUP_TIMEOUT"
				}
				if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), code) {
					t.Fatalf("original %s cancellation lost: %v", phase, err)
				}
				if corruptScope != strings.Contains(err.Error(), "CODEX_PROCESS_SCOPE_INVALID") {
					t.Fatalf("cleanup error propagation: %v", err)
				}
				if !corruptScope && strings.Contains(err.Error(), "CODEX_COMMAND_PROCESS_EXIT_TIMEOUT") {
					t.Fatal(err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("pre-handle cleanup exceeded bounded wait")
			}
			if corruptScope {
				if err := scope.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for _, process := range processes {
				waitMS := uint32(0)
				if corruptScope {
					// This case intentionally prevented production job cleanup.
					// Allow the fixture's manual recovery a bounded signal wait.
					waitMS = 1000
				}
				state, err := windows.WaitForSingleObject(process, waitMS)
				if err != nil || state != windows.WAIT_OBJECT_0 {
					t.Fatalf("process still running after cleanup: state=%d err=%v", state, err)
				}
			}
			select {
			case <-owned.done:
			default:
				t.Fatal("app-server Wait has not completed")
			}
			if _, err := os.Stat(filepath.Join(repo, "target-started")); !os.IsNotExist(err) {
				t.Fatal("target command was started before cancellation")
			}
			if err := os.Rename(repo, repo+"-released"); err != nil {
				t.Fatal("immediate workspace rename:", err)
			}
			if corruptScope {
				t.Logf("%s cancellation and cleanup failure both propagated; fixture job manually reaped", phase)
			} else {
				t.Logf("%s canceled before target; server and child already exited; immediate rename passed; elapsed=%s", phase, time.Since(started))
			}
		})
	}
}

func TestCloseCommandClientReportsUnconfirmedExit(t *testing.T) {
	if err := closeCommandClient(nil); err != nil {
		t.Fatal(err)
	}
	if err := closeCommandClient(NewClient("not-started", t.TempDir(), "", nil, 0, nil)); err != nil {
		t.Fatal(err)
	}
	// Model a previously closed client whose Wait never confirmed exit. The
	// synthetic Process is never passed to Kill because Close already ran.
	client := &Client{cmd: &exec.Cmd{Process: new(os.Process)}, done: make(chan struct{}), pending: make(map[int64]chan rpcResponse)}
	client.closed.Store(true)
	clientProcessScopes.Store(client, "invalid test scope")
	started := time.Now()
	err := closeCommandClient(client)
	if err == nil || !strings.Contains(err.Error(), "CODEX_PROCESS_SCOPE_INVALID") || !strings.Contains(err.Error(), "CODEX_COMMAND_PROCESS_EXIT_TIMEOUT") {
		t.Fatalf("missing joined cleanup errors: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 3*time.Second || elapsed > 5*time.Second {
		t.Fatalf("cleanup wait was not bounded: %s", elapsed)
	}
}

func runPreCommandHelper(child bool) int {
	root := os.Getenv("CWAPI_PRE_COMMAND_ROOT")
	// Prevent a broken test from leaving long-lived fixture processes.
	go func() { time.Sleep(20 * time.Second); os.Exit(90) }()
	if err := os.Chdir(root); err != nil {
		return 91
	}
	if child {
		path, err := windows.UTF16PtrFromString(root)
		if err != nil {
			return 92
		}
		handle, err := windows.CreateFile(path, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			return 93
		}
		defer windows.CloseHandle(handle)
		if os.WriteFile(filepath.Join(root, "child-ready"), []byte("ready"), 0600) != nil {
			return 94
		}
		select {}
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return 95
		}
		switch request["method"] {
		case "initialize":
			if encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"codexHome": os.Getenv("CODEX_HOME")}}) != nil {
				return 96
			}
		case "windowsSandbox/readiness", "windowsSandbox/setupStart":
			if request["method"] == "windowsSandbox/readiness" && os.Getenv("CWAPI_PRE_COMMAND_PHASE") == "setup" {
				if encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{"status": "notReady"}}) != nil {
					return 96
				}
				continue
			}
			executable, _ := os.Executable()
			process := processlaunch.Command(executable, "--pre-command-child")
			process.Env, process.Dir = os.Environ(), root
			if process.Start() != nil {
				return 97
			}
			for i := 0; ; i++ {
				if _, err := os.Stat(filepath.Join(root, "child-ready")); err == nil {
					break
				}
				if i > 1000 {
					return 98
				}
				time.Sleep(5 * time.Millisecond)
			}
			payload, _ := json.Marshal(struct{ Server, Child int }{os.Getpid(), process.Process.Pid})
			if os.WriteFile(filepath.Join(root, "readiness.json"), payload, 0600) != nil {
				return 99
			}
			if request["method"] == "windowsSandbox/setupStart" {
				if encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{}}) != nil {
					return 96
				}
			}
			// Deliberately do not answer: cancellation must reap the owned tree.
		case "command/exec":
			_ = os.WriteFile(filepath.Join(root, "target-started"), []byte("unexpected"), 0600)
			return 100
		}
	}
	return 0
}

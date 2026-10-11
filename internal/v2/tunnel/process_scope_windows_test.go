//go:build windows

package tunnel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const tunnelScopeHelperEnv = "CWAPI_TUNNEL_SCOPE_HELPER"

func TestTunnelChildDoesNotOutliveAbruptParent(t *testing.T) {
	if os.Getenv(tunnelScopeHelperEnv) == "1" {
		runTunnelScopeHelper(t)
		return
	}

	root := t.TempDir()
	pidFile := filepath.Join(root, "child.pid")
	helper := exec.Command(os.Args[0], "-test.run=^TestTunnelChildDoesNotOutliveAbruptParent$")
	helper.Env = append(os.Environ(), tunnelScopeHelperEnv+"=1", "CWAPI_TUNNEL_SCOPE_PIDFILE="+pidFile)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if helper.Process != nil {
			_ = helper.Process.Kill()
		}
		_ = helper.Wait()
	}()

	childPID := waitForTunnelScopeChildPID(t, pidFile, helper)
	childHandle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(childPID))
	if err != nil {
		t.Fatalf("open child process %d: %v", childPID, err)
	}
	defer windows.CloseHandle(childHandle)
	defer func() { _ = windows.TerminateProcess(childHandle, 1) }()

	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("abruptly terminate helper parent: %v", err)
	}
	_ = helper.Wait()

	result, err := windows.WaitForSingleObject(childHandle, 5_000)
	if err != nil {
		t.Fatalf("wait for tunnel child termination: %v", err)
	}
	if result != windows.WAIT_OBJECT_0 {
		t.Fatalf("tunnel child %d outlived abruptly terminated parent; wait result=%d", childPID, result)
	}
}

func runTunnelScopeHelper(t *testing.T) {
	pidFile := os.Getenv("CWAPI_TUNNEL_SCOPE_PIDFILE")
	if pidFile == "" {
		t.Fatal("missing helper pid file")
	}
	child, err := startProcess(
		"powershell.exe",
		[]string{"-NoProfile", "-Command", "Start-Sleep -Seconds 30"},
		nil,
		os.TempDir(),
	)
	if err != nil {
		t.Fatalf("start scoped tunnel child: %v", err)
	}
	process, ok := child.(*commandProcess)
	if !ok || process.command == nil || process.command.Process == nil {
		t.Fatal("unexpected tunnel process implementation")
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(process.command.Process.Pid)), 0o600); err != nil {
		_ = child.Kill()
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func waitForTunnelScopeChildPID(t *testing.T, pidFile string, helper *exec.Cmd) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(pidFile)
		if err == nil {
			pid, convErr := strconv.Atoi(string(payload))
			if convErr != nil || pid <= 0 {
				t.Fatalf("invalid child pid %q", payload)
			}
			return pid
		}
		if helper.ProcessState != nil && helper.ProcessState.Exited() {
			t.Fatal("helper parent exited before publishing child pid")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for helper child pid")
	return 0
}

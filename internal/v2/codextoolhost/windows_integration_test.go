//go:build windows

package codextoolhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/v2/commandproxy"
	v2config "github.com/AAAYNMMM/CWapi/internal/v2/config"
	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if path, ok := commandproxy.IsInvocation(os.Args); ok {
		os.Exit(commandproxy.Run(path))
	}
	os.Exit(m.Run())
}

// Opt-in: the pinned runtime applies Windows ACLs inside freshly generated
// fixtures. No user workspace or native CODEX_HOME is used.
func TestWindowsWorkspaceMatrix(t *testing.T) {
	executable := os.Getenv("CWAPI_CODEX_TEST_EXECUTABLE")
	parent := os.Getenv("CWAPI_TEST_ROOT")
	if executable == "" || parent == "" {
		t.Skip("set CWAPI_CODEX_TEST_EXECUTABLE and CWAPI_TEST_ROOT for native runtime checks")
	}
	root, err := os.MkdirTemp(parent, "matrix-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".local-tools/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	host, err := New(filepath.Join(root, "data"), v2config.CodexConfig{Executable: executable, AccessProfile: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close(context.Background()) }()
	counts := []int{0, 10000}
	if value := os.Getenv("CWAPI_TEST_COUNTS"); value != "" {
		counts = nil
		for _, field := range strings.Split(value, ",") {
			n, err := strconv.Atoi(field)
			if err != nil || n < 0 {
				t.Fatal("invalid CWAPI_TEST_COUNTS")
			}
			counts = append(counts, n)
		}
	}
	created := 0
	layout := os.Getenv("CWAPI_TEST_LAYOUT")
	content := []byte("x")
	if value := os.Getenv("CWAPI_TEST_FILE_BYTES"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 32*1024*1024 {
			t.Fatal("invalid file size")
		}
		content = make([]byte, n)
	}
	var previousACECount uint16
	for _, count := range counts {
		if count > 100000 || int64(count)*int64(len(content)) > 2*1024*1024*1024 {
			t.Fatal("fixture exceeds test budget")
		}
		for created < count {
			dir := filepath.Join(repo, ".local-tools", fmt.Sprintf("pkg-%04d", created/100))
			if layout == "deep" {
				for i := 0; i < 12; i++ {
					dir = filepath.Join(dir, "nested-dir")
				}
			}
			if layout == "wide" {
				dir = filepath.Join(repo, ".local-tools", fmt.Sprintf("pkg-%05d", created))
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%05d.txt", created)), content, 0600); err != nil {
				t.Fatal(err)
			}
			created++
		}
		if source := os.Getenv("CWAPI_TEST_SEED_FROM"); source != "" {
			sd, err := windows.GetNamedSecurityInfo(source, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			dacl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(repo, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
				t.Fatal(err)
			}
			t.Log("Seeded only the generated fixture DACL from read-only source")
		}
		for _, profile := range []string{"safe", "safe", "full"} {
			if os.Getenv("CWAPI_TEST_REOPEN") == "1" {
				if err := host.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				host, err = New(filepath.Join(root, "data"), v2config.CodexConfig{Executable: executable, AccessProfile: profile})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := host.SetAccessProfile(profile); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			marker := filepath.Join(repo, "started.txt")
			_ = os.Remove(marker)
			result, execErr := host.Exec(context.Background(), repo, ExecInput{Command: "C:/Windows/System32/cmd.exe", Argv: []string{"/d", "/c", "echo MATRIX_OK>started.txt & echo MATRIX_OK"}, TimeoutSeconds: 30})
			entry := map[string]any{"files": created, "profile": profile, "elapsed_ms": time.Since(started).Milliseconds(), "result": result}
			entry["layout"], entry["file_bytes"] = layout, len(content)
			_, markerErr := os.Stat(marker)
			entry["target_started"] = markerErr == nil
			if sd, err := windows.GetNamedSecurityInfo(repo, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION); err == nil {
				if dacl, _, err := sd.DACL(); err == nil && dacl != nil {
					entry["root_ace_count"] = dacl.AceCount
					if os.Getenv("CWAPI_TEST_EXPECT_SUCCESS") == "1" && profile == "safe" && previousACECount != 0 && previousACECount != dacl.AceCount {
						t.Errorf("ACL grew: %d -> %d", previousACECount, dacl.AceCount)
					}
					previousACECount = dacl.AceCount
				}
			}
			if execErr != nil {
				entry["error"] = execErr.Error()
			}
			payload, _ := json.Marshal(entry)
			t.Log(string(payload))
			if os.Getenv("CWAPI_TEST_EXPECT_SUCCESS") == "1" && (execErr != nil || result.State != "completed" || !strings.Contains(result.Stdout, "MATRIX_OK")) {
				t.Errorf("echo failed: %s", payload)
			}
			// Immediate rename verifies directory release, not merely process metadata.
			moved := repo + "-closed"
			if err := os.Rename(repo, moved); err != nil {
				t.Errorf("rename after %s/%d: %v", profile, count, err)
			} else if err := os.Rename(moved, repo); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWindowsCapabilityIsolation(t *testing.T) {
	executable, parent := os.Getenv("CWAPI_CODEX_TEST_EXECUTABLE"), os.Getenv("CWAPI_TEST_ROOT")
	if executable == "" || parent == "" {
		t.Skip("native runtime opt-in not configured")
	}
	root, err := os.MkdirTemp(parent, "isolation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	a, b := filepath.Join(root, "repo-a"), filepath.Join(root, "repo-b")
	for _, path := range []string{a, b} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	host, err := New(filepath.Join(root, "data"), v2config.CodexConfig{Executable: executable, AccessProfile: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	for _, path := range []string{a, b} {
		result, err := host.Exec(context.Background(), path, ExecInput{Command: "C:/Windows/System32/cmd.exe", Argv: []string{"/d", "/c", "echo OWNED>owned.txt"}, TimeoutSeconds: 30})
		if err != nil || result.State != "completed" {
			t.Fatal(result, err)
		}
	}
	result, err := host.Exec(context.Background(), a, ExecInput{Command: "C:/Windows/System32/cmd.exe", Argv: []string{"/d", "/c", `echo BAD>"` + filepath.Join(b, "escape.txt") + `"`}, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal("sandbox isolation was not exercised (pre-launch error):", err)
	}
	if result.ExitCode == 0 {
		t.Fatal("SAFE wrote another workspace")
	}
	if _, err := os.Stat(filepath.Join(b, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("other workspace was modified:", err)
	}
	t.Log("own-root writes succeeded; native sandbox denied cross-workspace write")
}

func TestWindowsForegroundTimeoutReleasesWorkspace(t *testing.T) {
	executable, parent := os.Getenv("CWAPI_CODEX_TEST_EXECUTABLE"), os.Getenv("CWAPI_TEST_ROOT")
	if executable == "" || parent == "" {
		t.Skip("native runtime opt-in not configured")
	}
	root, err := os.MkdirTemp(parent, "timeout-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	host, err := New(filepath.Join(root, "data"), v2config.CodexConfig{Executable: executable, AccessProfile: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	for _, profile := range []string{"safe", "full"} {
		if err := host.SetAccessProfile(profile); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(repo, "timeout-started.txt")
		_ = os.Remove(marker)
		result, err := host.Exec(context.Background(), repo, ExecInput{Command: "C:/Windows/System32/WindowsPowerShell/v1.0/powershell.exe", Argv: []string{"-NoProfile", "-NonInteractive", "-Command", "Set-Content -LiteralPath timeout-started.txt -Value STARTED; Start-Sleep -Seconds 60"}, TimeoutSeconds: 5})
		if err == nil || !strings.Contains(err.Error(), "CODEX_TOOLHOST_COMMAND_TIMEOUT") {
			t.Fatal("timeout classification:", result, err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("target did not start before timeout:", err)
		}
		cleanupSeen := false
		if result.Diagnostics == nil {
			t.Fatal("missing timeout diagnostics")
		}
		for _, phase := range result.Diagnostics.Phases {
			if phase.Name == "command_cleanup" {
				cleanupSeen = true
				if phase.State != "completed" {
					t.Fatal("cleanup not finished:", phase)
				}
			}
		}
		if !cleanupSeen {
			t.Fatal("missing cleanup phase")
		}
		if err := os.Rename(repo, repo+"-released"); err != nil {
			t.Fatal("timeout left directory locked:", err)
		}
		if err := os.Rename(repo+"-released", repo); err != nil {
			t.Fatal(err)
		}
		t.Log(profile, "timeout -> owned job empty -> immediate rename passed")
	}
}

func TestWindowsForegroundCancelReleasesWorkspace(t *testing.T) {
	executable, parent := os.Getenv("CWAPI_CODEX_TEST_EXECUTABLE"), os.Getenv("CWAPI_TEST_ROOT")
	if executable == "" || parent == "" {
		t.Skip("native runtime opt-in not configured")
	}
	root, err := os.MkdirTemp(parent, "cancel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	host, err := New(filepath.Join(root, "data"), v2config.CodexConfig{Executable: executable, AccessProfile: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	for _, profile := range []string{"safe", "full"} {
		if err := host.SetAccessProfile(profile); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(repo, "cancel-started.txt")
		_ = os.Remove(marker)
		ctx, cancel := context.WithCancel(context.Background())
		type outcome struct {
			result ExecResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := host.Exec(ctx, repo, ExecInput{Command: "C:/Windows/System32/WindowsPowerShell/v1.0/powershell.exe", Argv: []string{"-NoProfile", "-NonInteractive", "-Command", "Set-Content -LiteralPath cancel-started.txt -Value STARTED; Start-Sleep -Seconds 60"}, TimeoutSeconds: 30})
			done <- outcome{result, err}
		}()
		deadline := time.Now().Add(20 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			}
			select {
			case early := <-done:
				cancel()
				t.Fatal("command ended before target marker", early)
			default:
			}
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("target did not start")
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel() // Explicit caller cancellation, well before the 30-second timeout.
		select {
		case output := <-done:
			if output.err == nil || !strings.Contains(output.err.Error(), "CODEX_TOOLHOST_COMMAND_CANCELED") || strings.Contains(output.err.Error(), "CODEX_TOOLHOST_COMMAND_TIMEOUT") {
				t.Fatal("cancel classification", output)
			}
			cleanupSeen := false
			if output.result.Diagnostics == nil {
				t.Fatal("missing diagnostics")
			}
			for _, phase := range output.result.Diagnostics.Phases {
				if phase.Name == "command_cleanup" {
					cleanupSeen = true
					if phase.State != "completed" {
						t.Fatal("cleanup incomplete", phase)
					}
				}
			}
			if !cleanupSeen {
				t.Fatal("missing cleanup phase")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("cancel did not return within cleanup budget")
		}
		if err := os.Rename(repo, repo+"-released"); err != nil {
			t.Fatal("cancel left directory locked", err)
		}
		if err := os.Rename(repo+"-released", repo); err != nil {
			t.Fatal(err)
		}
		t.Log(profile, "caller cancel -> CANCELED (not TIMEOUT) -> cleanup complete -> immediate rename passed")
	}
}

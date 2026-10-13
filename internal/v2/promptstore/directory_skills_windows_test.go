//go:build windows

package promptstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryJunctionIsRejected(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "skills")
	outside := t.TempDir()
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("# Outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(skills, "junction")
	cmd := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, outside)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
	}

	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Skills["junction"]; ok {
		t.Fatal("junction-backed directory skill should not load")
	}
	if len(warnings) == 0 {
		t.Fatal("junction-backed directory skill should emit a warning")
	}
}

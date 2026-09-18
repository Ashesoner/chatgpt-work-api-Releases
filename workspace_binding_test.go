package main

import "testing"

func TestWorkspaceFolderCommandUsesVisibleProcessDefaults(t *testing.T) {
	repoPath := `C:\workspace\repo`
	command := workspaceFolderCommand(repoPath)
	if command == nil {
		t.Fatal("workspace folder command is nil")
	}
	if command.SysProcAttr != nil {
		t.Fatal("workspace folder command must not inherit hidden/background process attributes")
	}
	if len(command.Args) != 2 || command.Args[1] != repoPath {
		t.Fatalf("unexpected explorer args: %#v", command.Args)
	}
}

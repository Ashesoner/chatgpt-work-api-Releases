package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/processlaunch"
	v2service "github.com/AAAYNMMM/CWapi/internal/v2/service"
	"github.com/AAAYNMMM/CWapi/internal/v2/workspace"
)

// DeleteWorkspace is a desktop-only maintenance action. It is deliberately not
// part of either MCP app. The next coding_open for the same repository + branch
// recreates it. The existing global maintenance-busy policy is preserved.
func (a *App) DeleteWorkspace(repositoryName, targetRef string) (v2service.Snapshot, error) {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()

	current, err := a.core()
	if err != nil {
		return a.RuntimeSnapshot(), err
	}
	before := current.Snapshot()
	if before.Coding.Active > 0 {
		return before, errors.New("WORKSPACE_MAINTENANCE_BUSY_CODING")
	}
	if before.Agent.Pending > 0 || before.Agent.Claimed > 0 {
		return before, errors.New("WORKSPACE_MAINTENANCE_BUSY_AGENT")
	}
	repositoryName = strings.ToLower(strings.TrimSpace(repositoryName))
	targetRef = strings.TrimSpace(targetRef)
	if repositoryName == "" {
		return before, errors.New("WORKSPACE_REPOSITORY_REQUIRED")
	}
	if targetRef == "" {
		return before, errors.New("WORKSPACE_TARGET_REF_REQUIRED")
	}

	a.mu.RLock()
	configPath, runtimeCtx := a.configPath, a.ctx
	a.mu.RUnlock()
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := current.Close(closeCtx)
	cancel()
	if closeErr != nil {
		restartErr := a.restartV2Service(configPath, runtimeCtx)
		return a.RuntimeSnapshot(), errors.Join(closeErr, restartErr)
	}

	deleteErr := workspace.DeleteAt(workspaceDataRoot(configPath), repositoryName, targetRef)
	restartErr := a.restartV2Service(configPath, runtimeCtx)
	return a.RuntimeSnapshot(), errors.Join(deleteErr, restartErr)
}

// OpenWorkspaceFolder resolves the actual durable workspace entirely on the
// backend and opens its repo directory. The frontend never receives or derives
// workspace hashes or local paths.
func (a *App) OpenWorkspaceFolder(repositoryName, targetRef string) error {
	a.reconfigureMu.Lock()
	defer a.reconfigureMu.Unlock()

	repositoryName = strings.ToLower(strings.TrimSpace(repositoryName))
	targetRef = strings.TrimSpace(targetRef)
	if repositoryName == "" {
		return errors.New("WORKSPACE_REPOSITORY_REQUIRED")
	}
	if targetRef == "" {
		return errors.New("WORKSPACE_TARGET_REF_REQUIRED")
	}
	a.mu.RLock()
	configPath := a.configPath
	a.mu.RUnlock()
	repoPath, err := workspace.ResolveAt(workspaceDataRoot(configPath), repositoryName, targetRef)
	if err != nil {
		return err
	}
	command := processlaunch.Command("explorer.exe", repoPath)
	if err := command.Start(); err != nil {
		return errors.New("WORKSPACE_OPEN_FOLDER_FAILED")
	}
	go func() { _ = command.Wait() }()
	return nil
}

func workspaceDataRoot(configPath string) string {
	dataRoot := filepath.Dir(configPath)
	if strings.EqualFold(filepath.Base(dataRoot), "config") {
		dataRoot = filepath.Dir(dataRoot)
	}
	return filepath.Clean(dataRoot)
}

func (a *App) restartV2Service(configPath string, runtimeCtx context.Context) error {
	next, err := v2service.NewDefault(configPath)
	if err == nil {
		err = next.Start(runtimeCtx)
	}
	a.mu.Lock()
	if err == nil {
		a.service = next
		a.startupErr = nil
	} else {
		a.service = nil
		a.startupErr = err
	}
	a.mu.Unlock()
	return err
}

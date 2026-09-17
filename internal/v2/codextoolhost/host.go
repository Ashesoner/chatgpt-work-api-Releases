package codextoolhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/AAAYNMMM/CWapi/internal/childenv"
	"github.com/AAAYNMMM/CWapi/internal/codex"
	"github.com/AAAYNMMM/CWapi/internal/invocation"
	"github.com/AAAYNMMM/CWapi/internal/processcontract"
	"github.com/AAAYNMMM/CWapi/internal/security"
	"github.com/AAAYNMMM/CWapi/internal/v2/commandproxy"
	v2config "github.com/AAAYNMMM/CWapi/internal/v2/config"
)

const (
	defaultTimeout         = 120 * time.Second
	maxTimeout             = 600 * time.Second
	maxOutputBytes         = 64 * 1024
	maxPersistentProcesses = 16
	maxPersistentRecords   = 64
	persistentReadBytes    = 64 * 1024
	persistentArchiveBytes = 512 * 1024
)

// Host is a model-free bridge to the private Codex app-server command/exec
// development tool. It does not create Codex threads or turns and therefore
// does not use Codex account or model functionality.
type Host struct {
	service              *codex.Service
	resolver             *invocation.Resolver
	dataRoot             string
	git                  string
	gitSafety            *security.GitSafetyManager
	protectedExecutables []string
	proxyExecutable      string

	policyMu         sync.RWMutex
	accessProfile    string
	networkAccess    bool
	remoteGitRewrite bool

	processMu sync.RWMutex
	processes map[string]*persistentProcess
	closed    bool
}

type ExecInput struct {
	Action         string
	ProcessID      string
	Command        string
	Argv           []string
	CWD            string
	TimeoutSeconds int
	StdoutCursor   int64
	StderrCursor   int64
}

type ExecResult struct {
	State           string
	ProcessID       string
	PID             int
	StartedAt       string
	ExitCode        int
	Stdout          string
	Stderr          string
	StdoutCursor    int64
	StderrCursor    int64
	StdoutTruncated bool
	StderrTruncated bool
	Truncated       bool
}

type ProcessSummary struct {
	ProcessID      string
	State          string
	Command        string
	PID            int
	StartedAt      string
	ElapsedSeconds int64
}

type persistentProcess struct {
	mu sync.RWMutex

	id            string
	pid           int
	workspace     string
	command       string
	argv          []string
	startedAt     time.Time
	state         string
	result        ExecResult
	handle        *codex.CommandHandle
	runtime       *security.CommandRuntime
	done          chan struct{}
	stopRequested bool
	stdoutDir     string
	stderrDir     string
	stdoutArchive commandproxy.ReadResult
	stderrArchive commandproxy.ReadResult
}

func New(dataRoot string, cfg v2config.CodexConfig) (*Host, error) {
	dataRoot = strings.TrimSpace(dataRoot)
	if dataRoot == "" || !filepath.IsAbs(dataRoot) {
		return nil, errors.New("CODEX_TOOLHOST_DATA_ROOT_INVALID")
	}
	if err := v2config.ValidateCodex(cfg); err != nil {
		return nil, err
	}
	service, err := codex.NewCommandService(filepath.Clean(dataRoot), cfg.Executable)
	if err != nil {
		return nil, err
	}
	snapshot := service.Snapshot()
	if !snapshot.Configured {
		return nil, errors.New("CODEX_TOOLHOST_RUNTIME_UNAVAILABLE")
	}
	if !strings.EqualFold(snapshot.ExecutableSHA, codex.PinnedExecutableSHA256) {
		return nil, fmt.Errorf("CODEX_EXECUTABLE_SHA256_MISMATCH: expected=%s actual=%s", codex.PinnedExecutableSHA256, snapshot.ExecutableSHA)
	}
	resolver, err := invocation.New(commandEnvironment(filepath.Clean(dataRoot), snapshot.ExecutablePath))
	if err != nil {
		return nil, err
	}
	gitExecutable, err := resolver.ResolvePATHExecutable("git")
	if err != nil {
		return nil, fmt.Errorf("CODEX_TOOLHOST_GIT_UNAVAILABLE: %w", err)
	}
	appExecutable, _ := os.Executable()
	appRoot := filepath.Dir(filepath.Clean(dataRoot))
	return &Host{
		service: service, resolver: resolver, dataRoot: filepath.Clean(dataRoot), git: gitExecutable,
		gitSafety:            security.NewGitSafetyManager(gitExecutable, filepath.Clean(dataRoot)),
		protectedExecutables: []string{snapshot.ExecutablePath, appExecutable, filepath.Join(appRoot, "runtime", "tunnel", "current", "tunnel-client.exe")},
		proxyExecutable:      appExecutable,
		accessProfile:        cfg.AccessProfile, networkAccess: cfg.NetworkAccess, remoteGitRewrite: cfg.RemoteGitRewrite,
		processes: make(map[string]*persistentProcess),
	}, nil
}

func (h *Host) SetAccessProfile(profile string) error {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile != v2config.AccessProfileSafe && profile != v2config.AccessProfileFull {
		return errors.New("CONFIG_CODEX_ACCESS_PROFILE_INVALID")
	}
	if h == nil {
		return errors.New("CODEX_TOOLHOST_UNAVAILABLE")
	}
	h.policyMu.Lock()
	h.accessProfile = profile
	h.policyMu.Unlock()
	return nil
}

func (h *Host) SetNetworkAccess(allowed bool) error {
	if h == nil {
		return errors.New("CODEX_TOOLHOST_UNAVAILABLE")
	}
	h.policyMu.Lock()
	h.networkAccess = allowed
	h.policyMu.Unlock()
	return nil
}

func (h *Host) SetRemoteGitRewrite(allowed bool) error {
	if h == nil {
		return errors.New("CODEX_TOOLHOST_UNAVAILABLE")
	}
	h.policyMu.Lock()
	h.remoteGitRewrite = allowed
	h.policyMu.Unlock()
	return nil
}

func (h *Host) currentPolicy() (string, bool, bool) {
	h.policyMu.RLock()
	defer h.policyMu.RUnlock()
	return h.accessProfile, h.networkAccess, h.remoteGitRewrite
}

func (h *Host) Exec(ctx context.Context, workspaceRoot string, input ExecInput) (ExecResult, error) {
	if h == nil || h.service == nil || h.resolver == nil {
		return ExecResult{}, errors.New("CODEX_TOOLHOST_UNAVAILABLE")
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" || !filepath.IsAbs(workspaceRoot) {
		return ExecResult{}, errors.New("CODEX_TOOLHOST_WORKSPACE_INVALID")
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		action = "run"
	}
	h.processMu.RLock()
	closed := h.closed
	h.processMu.RUnlock()
	if closed && (action == "run" || action == "start") {
		return ExecResult{}, errors.New("CODEX_TOOLHOST_CLOSED")
	}
	switch action {
	case "run", "start":
		return h.execute(ctx, filepath.Clean(workspaceRoot), input, action == "start")
	case "status":
		return h.persistentStatus(filepath.Clean(workspaceRoot), input.ProcessID, input.StdoutCursor, input.StderrCursor)
	case "stop":
		return h.stopPersistent(ctx, filepath.Clean(workspaceRoot), input.ProcessID)
	default:
		return ExecResult{}, errors.New("CODING_EXEC_ACTION_INVALID")
	}
}

func (h *Host) execute(ctx context.Context, workspaceRoot string, input ExecInput, persistent bool) (ExecResult, error) {
	if persistent && input.TimeoutSeconds != 0 {
		return ExecResult{}, errors.New("CODING_PERSISTENT_TIMEOUT_UNSUPPORTED")
	}
	arguments, err := decodeArguments(input)
	if err != nil {
		return ExecResult{}, err
	}
	profile, networkAccess, remoteGitRewrite := h.currentPolicy()
	processID, err := randomProcessID()
	if err != nil {
		return ExecResult{}, err
	}
	if persistent {
		h.processMu.RLock()
		running := 0
		for _, process := range h.processes {
			process.mu.RLock()
			if process.state == "running" || process.state == "stopping" {
				running++
			}
			process.mu.RUnlock()
		}
		closed := h.closed
		h.processMu.RUnlock()
		if closed {
			return ExecResult{}, errors.New("CODEX_TOOLHOST_CLOSED")
		}
		if running >= maxPersistentProcesses {
			return ExecResult{}, errors.New("CODING_PERSISTENT_PROCESS_LIMIT")
		}
	}
	runtime, err := security.PrepareCommandRuntime(h.dataRoot, workspaceRoot, processID, profile)
	if err != nil {
		return ExecResult{}, err
	}
	keepRuntime := false
	defer func() {
		if !keepRuntime {
			runtime.Cleanup()
		}
	}()
	final, err := h.resolver.Resolve(workspaceRoot, arguments, runtime.BridgeRoot)
	if err != nil {
		return ExecResult{}, err
	}
	policyInvocation := security.Invocation{
		Executable: final.TargetExecutable, Argv: final.TargetArgv, CWD: final.CWD, AccessProfile: profile,
		TrustedGitExecutable: h.git, RemoteGitRewrite: remoteGitRewrite, ProtectedExecutables: h.protectedExecutables,
	}
	if err := security.Check(policyInvocation, workspaceRoot, h.dataRoot); err != nil {
		return ExecResult{}, err
	}
	if err := h.gitSafety.Before(ctxOrBackground(ctx), policyInvocation, workspaceRoot); err != nil {
		return ExecResult{}, err
	}
	sandbox := codex.CommandSandboxWorkspaceWrite
	if security.IsFull(profile) {
		sandbox = codex.CommandSandboxFullAccess
	}
	environment, err := runtime.Environment(final.Environment, sandbox)
	if err != nil {
		return ExecResult{}, err
	}
	commandCtx := ctxOrBackground(ctx)
	var cancel context.CancelFunc = func() {}
	if !persistent {
		timeout, timeoutErr := commandTimeout(input.TimeoutSeconds)
		if timeoutErr != nil {
			return ExecResult{}, timeoutErr
		}
		commandCtx, cancel = context.WithTimeout(commandCtx, timeout)
	} else {
		commandCtx, cancel = context.WithCancel(context.Background())
	}
	cancelTransferred := false
	defer func() {
		if !cancelTransferred {
			cancel()
		}
	}()
	launchExecutable := final.Executable
	launchArgv := final.Argv
	stdoutDir, stderrDir := "", ""
	if persistent {
		stdoutDir = filepath.Join(runtime.ProcessRoot, "output", "stdout")
		stderrDir = filepath.Join(runtime.ProcessRoot, "output", "stderr")
		payloadPath := filepath.Join(runtime.ProcessRoot, "command-proxy.json")
		if err := commandproxy.WritePayload(payloadPath, commandproxy.Payload{
			Executable: final.Executable, Argv: append([]string(nil), final.Argv...), CWD: final.CWD,
			StdoutDir: stdoutDir, StderrDir: stderrDir, MirrorOutput: false,
		}); err != nil {
			return ExecResult{}, err
		}
		launchExecutable = h.proxyExecutable
		launchArgv = []string{commandproxy.Argument, payloadPath}
	}
	handle, err := h.service.StartCommand(commandCtx, codex.CommandSpec{
		ProcessID: processID, Executable: launchExecutable, Argv: launchArgv,
		CWD: final.CWD, WritableRoot: workspaceRoot,
		WritableRoots: []string{runtime.ProcessRoot, filepath.Join(runtime.WorkspaceRuntime, "cache"), runtime.AuthRoot},
		Environment:   environment,
		Sandbox:       sandbox, NetworkAccess: networkAccess,
	})
	if err != nil {
		cancel()
		return ExecResult{}, err
	}
	if persistent {
		startedAt := time.Now().UTC()
		process := &persistentProcess{
			id: processID, pid: handle.PID(), workspace: workspaceRoot, command: input.Command,
			argv: append([]string(nil), input.Argv...), startedAt: startedAt, state: "running",
			handle: handle, runtime: runtime, done: make(chan struct{}), stdoutDir: stdoutDir, stderrDir: stderrDir,
		}
		state := map[string]any{
			"schema": "cwapi.persistent-process.v1", "process_id": processID, "workspace": workspaceRoot,
			"command": input.Command, "argv": input.Argv, "pid": process.pid,
			"started_at": startedAt.Format(time.RFC3339Nano), "state": "running",
		}
		if err := runtime.WriteProcessState(state); err != nil {
			_ = handle.Stop()
			cancel()
			return ExecResult{}, fmt.Errorf("CODING_PERSISTENT_STATE_WRITE_FAILED: %w", err)
		}
		h.processMu.Lock()
		if h.closed {
			h.processMu.Unlock()
			_ = handle.Stop()
			cancel()
			return ExecResult{}, errors.New("CODEX_TOOLHOST_CLOSED")
		}
		h.processes[processID] = process
		h.processMu.Unlock()
		keepRuntime = true
		cancelTransferred = true
		go h.watchPersistent(process, cancel)
		return process.snapshot(), nil
	}

	select {
	case result := <-handle.Done():
		return commandResult(result, "", 0, time.Time{}), nil
	case <-commandCtx.Done():
		_ = handle.Stop()
		return ExecResult{}, fmt.Errorf("CODEX_TOOLHOST_COMMAND_TIMEOUT: %w", commandCtx.Err())
	}
}

func (h *Host) watchPersistent(process *persistentProcess, cancel context.CancelFunc) {
	result := <-process.handle.Done()
	cancel()
	output := commandResult(result, process.id, process.pid, process.startedAt)
	stdoutArchive, _ := commandproxy.ReadTail(process.stdoutDir, persistentArchiveBytes)
	stderrArchive, _ := commandproxy.ReadTail(process.stderrDir, persistentArchiveBytes)
	if output.Stdout == "" {
		output.Stdout, output.StdoutCursor, output.StdoutTruncated = stdoutArchive.Data, stdoutArchive.Cursor, stdoutArchive.Truncated
	}
	if output.Stderr == "" {
		output.Stderr, output.StderrCursor, output.StderrTruncated = stderrArchive.Data, stderrArchive.Cursor, stderrArchive.Truncated
	}
	output.Truncated = output.Truncated || output.StdoutTruncated || output.StderrTruncated
	process.mu.Lock()
	if process.stopRequested {
		output.State = "stopped"
	}
	process.state = output.State
	process.result = output
	process.stdoutArchive = stdoutArchive
	process.stderrArchive = stderrArchive
	process.mu.Unlock()
	process.runtime.Cleanup()
	close(process.done)
	h.pruneTerminalProcesses()
}

func (h *Host) pruneTerminalProcesses() {
	h.processMu.Lock()
	defer h.processMu.Unlock()
	for len(h.processes) > maxPersistentRecords {
		var oldest *persistentProcess
		for _, candidate := range h.processes {
			candidate.mu.RLock()
			terminal := candidate.state != "running" && candidate.state != "stopping"
			startedAt := candidate.startedAt
			candidate.mu.RUnlock()
			if terminal && (oldest == nil || startedAt.Before(oldest.startedAt)) {
				oldest = candidate
			}
		}
		if oldest == nil {
			return
		}
		delete(h.processes, oldest.id)
	}
}

func (h *Host) persistentStatus(workspaceRoot, processID string, stdoutCursor, stderrCursor int64) (ExecResult, error) {
	if stdoutCursor < 0 || stderrCursor < 0 {
		return ExecResult{}, errors.New("CODING_OUTPUT_CURSOR_INVALID")
	}
	process, err := h.lookupPersistent(workspaceRoot, processID)
	if err != nil {
		return ExecResult{}, err
	}
	return process.snapshotWithOutput(stdoutCursor, stderrCursor), nil
}

func (h *Host) stopPersistent(ctx context.Context, workspaceRoot, processID string) (ExecResult, error) {
	process, err := h.lookupPersistent(workspaceRoot, processID)
	if err != nil {
		return ExecResult{}, err
	}
	process.mu.Lock()
	if process.state != "running" && process.state != "stopping" {
		result := process.snapshotLocked()
		process.mu.Unlock()
		return result, nil
	}
	process.stopRequested = true
	process.state = "stopping"
	handle := process.handle
	done := process.done
	process.mu.Unlock()
	if err := handle.Stop(); err != nil {
		return ExecResult{}, err
	}
	waitCtx := ctxOrBackground(ctx)
	select {
	case <-done:
		return process.snapshot(), nil
	case <-waitCtx.Done():
		return ExecResult{}, fmt.Errorf("CODING_PERSISTENT_STOP_TIMEOUT: %w", waitCtx.Err())
	case <-time.After(5 * time.Second):
		return ExecResult{}, errors.New("CODING_PERSISTENT_STOP_TIMEOUT")
	}
}

func (h *Host) lookupPersistent(workspaceRoot, processID string) (*persistentProcess, error) {
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return nil, errors.New("CODING_PROCESS_ID_REQUIRED")
	}
	h.processMu.RLock()
	process := h.processes[processID]
	h.processMu.RUnlock()
	if process == nil {
		return nil, errors.New("CODING_PROCESS_NOT_FOUND")
	}
	if !strings.EqualFold(process.workspace, workspaceRoot) {
		return nil, errors.New("CODING_PROCESS_WORKSPACE_MISMATCH")
	}
	return process, nil
}

func (p *persistentProcess) snapshot() ExecResult {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshotLocked()
}

func (p *persistentProcess) snapshotLocked() ExecResult {
	if p.result.State != "" {
		return p.result
	}
	return ExecResult{
		State: p.state, ProcessID: p.id, PID: p.pid,
		StartedAt: p.startedAt.Format(time.RFC3339Nano),
	}
}

func (p *persistentProcess) snapshotWithOutput(stdoutCursor, stderrCursor int64) ExecResult {
	p.mu.RLock()
	result := p.snapshotLocked()
	terminal := p.result.State != ""
	stdoutDir, stderrDir := p.stdoutDir, p.stderrDir
	stdoutArchive, stderrArchive := p.stdoutArchive, p.stderrArchive
	p.mu.RUnlock()

	var stdoutChunk, stderrChunk commandproxy.ReadResult
	if terminal {
		stdoutChunk = archiveSince(stdoutArchive, stdoutCursor, persistentReadBytes)
		stderrChunk = archiveSince(stderrArchive, stderrCursor, persistentReadBytes)
	} else {
		stdoutChunk, _ = commandproxy.ReadSince(stdoutDir, stdoutCursor, persistentReadBytes)
		stderrChunk, _ = commandproxy.ReadSince(stderrDir, stderrCursor, persistentReadBytes)
	}
	result.Stdout, result.Stderr = stdoutChunk.Data, stderrChunk.Data
	result.StdoutCursor, result.StderrCursor = stdoutChunk.Cursor, stderrChunk.Cursor
	result.StdoutTruncated, result.StderrTruncated = stdoutChunk.Truncated, stderrChunk.Truncated
	result.Truncated = result.StdoutTruncated || result.StderrTruncated
	return result
}

func archiveSince(archive commandproxy.ReadResult, cursor int64, limit int) commandproxy.ReadResult {
	end := archive.Cursor
	base := end - int64(len(archive.Data))
	if base < 0 {
		base = 0
	}
	result := commandproxy.ReadResult{Cursor: cursor}
	if cursor < base {
		cursor = base
		result.Truncated = true
	}
	if cursor >= end {
		result.Cursor = end
		return result
	}
	start := int(cursor - base)
	stop := start + limit
	if stop > len(archive.Data) {
		stop = len(archive.Data)
	}
	result.Data = archive.Data[start:stop]
	result.Cursor = base + int64(stop)
	result.Truncated = result.Truncated || archive.Truncated && cursor == base
	return result
}

func (h *Host) WorkspaceProcesses(workspaceRoot string) []ProcessSummary {
	if h == nil {
		return nil
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	h.processMu.RLock()
	processes := make([]*persistentProcess, 0, len(h.processes))
	for _, process := range h.processes {
		if strings.EqualFold(process.workspace, workspaceRoot) {
			processes = append(processes, process)
		}
	}
	h.processMu.RUnlock()
	now := time.Now().UTC()
	result := make([]ProcessSummary, 0, len(processes))
	for _, process := range processes {
		process.mu.RLock()
		state := process.state
		if state != "running" && state != "stopping" {
			process.mu.RUnlock()
			continue
		}
		elapsed := int64(now.Sub(process.startedAt) / time.Second)
		if elapsed < 0 {
			elapsed = 0
		}
		result = append(result, ProcessSummary{
			ProcessID: process.id, State: state, Command: process.command, PID: process.pid,
			StartedAt: process.startedAt.Format(time.RFC3339Nano), ElapsedSeconds: elapsed,
		})
		process.mu.RUnlock()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt < result[j].StartedAt })
	return result
}

func (h *Host) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.processMu.Lock()
	if h.closed {
		h.processMu.Unlock()
		return nil
	}
	h.closed = true
	processes := make([]*persistentProcess, 0, len(h.processes))
	for _, process := range h.processes {
		processes = append(processes, process)
	}
	h.processMu.Unlock()
	var joined error
	for _, process := range processes {
		if _, err := h.stopPersistent(ctx, process.workspace, process.id); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (h *Host) StopWorkspace(ctx context.Context, workspaceRoot string) error {
	if h == nil {
		return nil
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	h.processMu.RLock()
	processes := make([]*persistentProcess, 0)
	for _, process := range h.processes {
		if strings.EqualFold(process.workspace, workspaceRoot) {
			processes = append(processes, process)
		}
	}
	h.processMu.RUnlock()
	var joined error
	for _, process := range processes {
		if _, err := h.stopPersistent(ctx, workspaceRoot, process.id); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func decodeArguments(input ExecInput) (processcontract.StartArguments, error) {
	argv := make([]any, len(input.Argv))
	for index, value := range input.Argv {
		argv[index] = value
	}
	value := map[string]any{"command": input.Command}
	if len(argv) > 0 {
		value["argv"] = argv
	}
	if input.CWD != "" {
		value["cwd"] = input.CWD
	}
	return processcontract.DecodeStart(value)
}

func commandTimeout(seconds int) (time.Duration, error) {
	if seconds == 0 {
		return defaultTimeout, nil
	}
	timeout := time.Duration(seconds) * time.Second
	if timeout < time.Second || timeout > maxTimeout {
		return 0, errors.New("CODING_EXEC_TIMEOUT_INVALID")
	}
	return timeout, nil
}

func commandEnvironment(dataRoot, executable string) []string {
	base := childenv.Canonical()
	privateRoot := filepath.Dir(filepath.Dir(filepath.Clean(executable)))
	appRoot := filepath.Dir(filepath.Clean(dataRoot))
	paths := []string{
		filepath.Join(privateRoot, "bin"), filepath.Join(privateRoot, "codex-resources"), filepath.Join(privateRoot, "codex-path"),
		filepath.Join(appRoot, "runtime", "git", "cmd"),
	}
	available := make([]string, 0, len(paths)+1)
	for _, candidate := range paths {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			available = append(available, candidate)
		}
	}
	if inherited := childenv.Value(base, "PATH"); inherited != "" {
		available = append(available, inherited)
	}
	pathValue := strings.Join(available, string(os.PathListSeparator))
	return childenv.Merge(base, map[string]*string{"PATH": &pathValue})
}

func commandResult(result codex.CommandResult, processID string, pid int, startedAt time.Time) ExecResult {
	if result.Err != nil {
		stderr, trimmed := boundedTail(result.Err.Error(), maxOutputBytes)
		return ExecResult{State: "failed", ProcessID: processID, PID: pid, StartedAt: formatStartedAt(startedAt), ExitCode: -1, Stderr: stderr, Truncated: trimmed}
	}
	stdout, stdoutTrimmed := boundedTail(result.Stdout, maxOutputBytes)
	stderr, stderrTrimmed := boundedTail(result.Stderr, maxOutputBytes)
	state := "completed"
	if result.ExitCode != 0 {
		state = "failed"
	}
	return ExecResult{
		State: state, ProcessID: processID, PID: pid, StartedAt: formatStartedAt(startedAt),
		ExitCode: result.ExitCode, Stdout: stdout, Stderr: stderr, Truncated: stdoutTrimmed || stderrTrimmed,
	}
}

func formatStartedAt(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func randomProcessID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("CODEX_TOOLHOST_PROCESS_ID_FAILED: %w", err)
	}
	return "proc-" + hex.EncodeToString(value[:]), nil
}

func boundedTail(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	payload := []byte(value)
	start := len(payload) - limit
	for start < len(payload) && !utf8.RuneStart(payload[start]) {
		start++
	}
	return string(payload[start:]), true
}

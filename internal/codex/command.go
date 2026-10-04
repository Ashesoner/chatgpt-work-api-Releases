package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AAAYNMMM/CWapi/internal/childenv"
	"github.com/AAAYNMMM/CWapi/internal/executiondiag"
	"github.com/AAAYNMMM/CWapi/internal/processcontract"
)

type CommandSpec struct {
	ProcessID     string
	Executable    string
	Argv          []string
	CWD           string
	WritableRoot  string
	WritableRoots []string
	Environment   []string
	Sandbox       string
	NetworkAccess bool
}

const (
	CommandSandboxWorkspaceWrite = "workspace-write"
	CommandSandboxFullAccess     = "danger-full-access"
)

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

type CommandHandle struct {
	done        chan CommandResult
	cleaned     chan struct{}
	cancel      context.CancelFunc
	client      *Client
	home        string
	cleanupOnce sync.Once
	stopOnce    sync.Once
	stopErr     error
	cleanupErr  error
}

func (h *CommandHandle) Done() <-chan CommandResult {
	if h == nil {
		return nil
	}
	return h.done
}

// PID is the owned private app-server process for this command. The user
// command remains a child in the same managed process tree.
func (h *CommandHandle) PID() int {
	if h == nil || h.client == nil || h.client.cmd == nil || h.client.cmd.Process == nil {
		return 0
	}
	return h.client.cmd.Process.Pid
}

func (h *CommandHandle) Stop() error {
	if h == nil {
		return nil
	}
	h.stopOnce.Do(func() {
		h.cancel()
		h.stopErr = h.client.releaseProcessTree()
		h.client.Close()
	})
	return h.stopErr
}

func (s *Service) StartCommand(ctx context.Context, spec CommandSpec) (*CommandHandle, error) {
	if s == nil {
		return nil, errors.New("CODEX_COMMAND_SERVICE_UNAVAILABLE")
	}
	if err := validateCommandSpec(spec); err != nil {
		return nil, err
	}
	endPhase := executiondiag.Start(ctx, "runtime_integrity")
	actualHash, err := hashFile(s.codexExe)
	endPhase(err)
	if err != nil {
		return nil, fmt.Errorf("CODEX_RUNTIME_UNAVAILABLE: %w", err)
	}
	if !strings.EqualFold(actualHash, PinnedExecutableSHA256) {
		return nil, fmt.Errorf("CODEX_EXECUTABLE_SHA256_MISMATCH: expected=%s actual=%s", PinnedExecutableSHA256, actualHash)
	}

	executionRoot := filepath.Join(s.dataRoot, "temp", "codex-executions")
	home := filepath.Join(executionRoot, spec.ProcessID)
	if err := os.MkdirAll(executionRoot, 0o700); err != nil {
		return nil, fmt.Errorf("CODEX_EXECUTION_ROOT_CREATE_FAILED: %w", err)
	}
	if err := os.Mkdir(home, 0o700); err != nil {
		return nil, fmt.Errorf("CODEX_EXECUTION_HOME_CREATE_FAILED: %w", err)
	}
	removeHome := true
	defer func() {
		if removeHome {
			_ = os.RemoveAll(home)
		}
	}()
	if err := ensureCommandHome(home); err != nil {
		return nil, err
	}
	endPhase = executiondiag.Start(ctx, "sandbox_identity")
	observeCommandACL(ctx, spec)
	err = prepareCommandIdentity(s.dataRoot, home, spec)
	endPhase(err)
	if err != nil {
		return nil, fmt.Errorf("CODEX_SANDBOX_IDENTITY_FAILED: %w", err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	commandCtx, cancel := context.WithCancel(ctx)
	client, err := s.startCommandClient(commandCtx, home, spec.CWD, commandServerEnvironment(spec.Environment))
	if err != nil {
		cancel()
		return nil, err
	}
	handle := &CommandHandle{done: make(chan CommandResult, 1), cleaned: make(chan struct{}), cancel: cancel, client: client, home: home}
	removeHome = false
	go handle.run(commandCtx, commandParams(spec))
	return handle, nil
}

func (s *Service) startCommandClient(ctx context.Context, home, cwd string, environment []string) (output *Client, setupErr error) {
	notifications := make(chan map[string]any, 8)
	newClient := func() *Client {
		return NewClient(s.codexExe, home, "", environment, 30*time.Second, func(message map[string]any) {
			select {
			case notifications <- message:
			default:
			}
		})
	}
	client := newClient()
	endPhase := executiondiag.Start(ctx, "app_server_start")
	startErr := startOwnedCommandClient(ctx, client)
	endPhase(startErr)
	if startErr != nil {
		return nil, startErr
	}
	readinessCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endPhase = executiondiag.Start(ctx, "sandbox_readiness")
	readiness, err := client.request(readinessCtx, "windowsSandbox/readiness", nil, true)
	endPhase(err)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("CODEX_SANDBOX_READINESS_FAILED: %w", err), closeCommandClient(client))
	}
	if objectString(readiness, "status") == "ready" {
		return client, nil
	}
	endSetup := executiondiag.Start(ctx, "sandbox_setup")
	defer func() { endSetup(setupErr) }()
	if _, err := client.request(readinessCtx, "windowsSandbox/setupStart", map[string]any{"mode": "unelevated", "cwd": cwd}, true); err != nil {
		return nil, errors.Join(fmt.Errorf("CODEX_SANDBOX_SETUP_START_FAILED: %w", err), closeCommandClient(client))
	}
	for {
		select {
		case message := <-notifications:
			if objectString(message, "method") != "windowsSandbox/setupCompleted" {
				continue
			}
			params, _ := message["params"].(map[string]any)
			if success, _ := params["success"].(bool); !success {
				return nil, errors.Join(errors.New("CODEX_SANDBOX_SETUP_FAILED"), closeCommandClient(client))
			}
			if err := closeCommandClient(client); err != nil {
				return nil, fmt.Errorf("CODEX_SANDBOX_SETUP_CLEANUP_FAILED: %w", err)
			}
			client = newClient()
			if err := startOwnedCommandClient(ctx, client); err != nil {
				return nil, err
			}
			verify, verifyErr := client.request(readinessCtx, "windowsSandbox/readiness", nil, true)
			if verifyErr != nil || objectString(verify, "status") != "ready" {
				return nil, errors.Join(errors.New("CODEX_SANDBOX_NOT_READY"), verifyErr, closeCommandClient(client))
			}
			return client, nil
		case <-readinessCtx.Done():
			return nil, errors.Join(fmt.Errorf("CODEX_SANDBOX_SETUP_TIMEOUT: %w", readinessCtx.Err()), closeCommandClient(client))
		}
	}
}

func startOwnedCommandClient(ctx context.Context, client *Client) error {
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.Start(startCtx); err != nil {
		return errors.Join(err, closeCommandClient(client))
	}
	if err := client.ownProcessTree(); err != nil {
		return errors.Join(fmt.Errorf("CODEX_COMMAND_PROCESS_SCOPE_FAILED: %w", err), closeCommandClient(client))
	}
	return nil
}

func (h *CommandHandle) run(ctx context.Context, params map[string]any) {
	endPhase := executiondiag.Start(ctx, "command_exec")
	value, err := h.client.request(ctx, "command/exec", params, true)
	endPhase(err)
	result := decodeCommandResult(value, err)
	endCleanup := executiondiag.Start(ctx, "command_cleanup")
	h.finish()
	endCleanup(errors.Join(h.stopErr, h.cleanupErr))
	result.Err = errors.Join(result.Err, h.stopErr, h.cleanupErr)
	close(h.cleaned)
	h.done <- result
	close(h.done)
}

func (h *CommandHandle) finish() {
	h.cleanupOnce.Do(func() {
		_ = h.Stop()
		select {
		case <-h.client.done:
		case <-time.After(3 * time.Second):
			h.cleanupErr = errors.New("CODEX_COMMAND_PROCESS_EXIT_TIMEOUT")
		}
		if err := os.RemoveAll(h.home); err != nil {
			h.cleanupErr = errors.Join(h.cleanupErr, fmt.Errorf("CODEX_COMMAND_HOME_CLEANUP_FAILED: %w", err))
		}
	})
}

// WaitCleanup does not consume Done, whose result belongs to the foreground
// executor or persistent-process watcher.
func (h *CommandHandle) WaitCleanup(ctx context.Context) error {
	select {
	case <-h.cleaned:
		return errors.Join(h.stopErr, h.cleanupErr)
	case <-ctx.Done():
		return fmt.Errorf("CODEX_COMMAND_CLEANUP_TIMEOUT: %w", ctx.Err())
	}
}

// Before a CommandHandle exists, the caller still owns cleanup. Preserve both
// job termination failures and an unconfirmed app-server exit for that caller.
func closeCommandClient(client *Client) error {
	if client == nil {
		return nil
	}
	cleanupErr := client.releaseProcessTree()
	client.Close()
	// A failed launch has no Wait goroutine and therefore never closes done.
	if client.cmd == nil || client.cmd.Process == nil {
		return cleanupErr
	}
	select {
	case <-client.done:
	case <-time.After(3 * time.Second):
		cleanupErr = errors.Join(cleanupErr, errors.New("CODEX_COMMAND_PROCESS_EXIT_TIMEOUT"))
	}
	return cleanupErr
}

func commandParams(spec CommandSpec) map[string]any {
	command := make([]string, 0, len(spec.Argv)+1)
	command = append(command, spec.Executable)
	command = append(command, spec.Argv...)
	environment := make(map[string]any, len(spec.Environment)+3)
	for _, entry := range spec.Environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			environment[key] = value
		}
	}
	for _, key := range []string{"CODEX_HOME", "RUST_LOG", "LOG_FORMAT"} {
		environment[key] = nil
	}
	writableRoots := make([]string, 0, len(spec.WritableRoots)+1)
	writableRoots = append(writableRoots, spec.WritableRoot)
	for _, root := range spec.WritableRoots {
		duplicate := false
		for _, existing := range writableRoots {
			if strings.EqualFold(filepath.Clean(existing), filepath.Clean(root)) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			writableRoots = append(writableRoots, root)
		}
	}
	sandboxPolicy := map[string]any{
		"type": "workspaceWrite", "writableRoots": writableRoots,
		"networkAccess": spec.NetworkAccess, "excludeSlashTmp": true, "excludeTmpdirEnvVar": true,
	}
	if spec.Sandbox == CommandSandboxFullAccess {
		sandboxPolicy = map[string]any{"type": "dangerFullAccess", "networkAccess": spec.NetworkAccess}
	}
	return map[string]any{
		"command":        command,
		"cwd":            spec.CWD,
		"env":            environment,
		"sandboxPolicy":  sandboxPolicy,
		"disableTimeout": true,
	}
}

func decodeCommandResult(value any, requestErr error) CommandResult {
	if requestErr != nil {
		return CommandResult{Err: requestErr}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return CommandResult{Err: errors.New("CODEX_COMMAND_RESPONSE_INVALID")}
	}
	exitCode, ok := numberToInt64(object["exitCode"])
	if !ok {
		return CommandResult{Err: errors.New("CODEX_COMMAND_EXIT_CODE_MISSING")}
	}
	stdout, _ := object["stdout"].(string)
	stderr, _ := object["stderr"].(string)
	return CommandResult{ExitCode: int(exitCode), Stdout: stdout, Stderr: stderr}
}

func commandServerEnvironment(entries []string) []string {
	overrides := make(map[string]*string)
	for _, entry := range entries {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(strings.ToUpper(key), "CWAPI_INTERNAL_") {
			overrides[key] = nil
		}
	}
	return childenv.Merge(entries, overrides)
}

func validateCommandSpec(spec CommandSpec) error {
	if !processcontract.ProcessIDPattern.MatchString(spec.ProcessID) {
		return errors.New("CODEX_COMMAND_PROCESS_ID_INVALID")
	}
	for _, path := range []string{spec.Executable, spec.CWD, spec.WritableRoot} {
		if !filepath.IsAbs(path) {
			return errors.New("CODEX_COMMAND_PATH_INVALID")
		}
	}
	for _, path := range spec.WritableRoots {
		if !filepath.IsAbs(path) {
			return errors.New("CODEX_COMMAND_PATH_INVALID")
		}
	}
	if !pathWithin(spec.CWD, spec.WritableRoot) {
		return errors.New("CODEX_COMMAND_CWD_OUTSIDE_ROOT")
	}
	info, err := os.Stat(spec.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("CODEX_COMMAND_EXECUTABLE_INVALID")
	}
	if len(spec.Environment) == 0 {
		return errors.New("CODEX_COMMAND_ENVIRONMENT_REQUIRED")
	}
	if spec.Sandbox != "" && spec.Sandbox != CommandSandboxWorkspaceWrite && spec.Sandbox != CommandSandboxFullAccess {
		return errors.New("CODEX_COMMAND_SANDBOX_INVALID")
	}
	return nil
}

func objectString(value any, key string) string {
	object, _ := value.(map[string]any)
	text, _ := object[key].(string)
	return text
}

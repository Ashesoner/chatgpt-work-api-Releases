//go:build windows

package codex

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AAAYNMMM/CWapi/internal/executiondiag"
	"github.com/AAAYNMMM/CWapi/internal/security"
	"golang.org/x/sys/windows"
)

// The cap_sid wire layout and canonical keys are coupled to the SHA-pinned
// Codex 0.150.1 windows-sandbox-rs/src/{cap,path_normalization}.rs. Keep command
// homes ephemeral; persist only an identity seed so ACL capabilities for durable
// roots do not change on every invocation. No Codex accounts/config are shared.
const commandIdentitySchema = "cwapi.windows-sandbox-identity.v1"

var commandIdentityMu sync.Mutex

func observeCommandACL(ctx context.Context, spec CommandSpec) {
	if spec.Sandbox == CommandSandboxFullAccess {
		return
	}
	sd, err := windows.GetNamedSecurityInfo(spec.WritableRoot, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return
	}
	dacl, _, err := sd.DACL()
	if err == nil && dacl != nil {
		executiondiag.WorkspaceACL(ctx, int(dacl.AceCount))
	}
}

type commandIdentity struct {
	Schema    string `json:"schema"`
	Workspace string `json:"workspace"`
	Seed      string `json:"seed"`
}

type commandCapabilities struct {
	Workspace          string            `json:"workspace"`
	Readonly           string            `json:"readonly"`
	WorkspaceByCWD     map[string]string `json:"workspace_by_cwd"`
	WritableRootByPath map[string]string `json:"writable_root_by_path"`
}

func prepareCommandIdentity(dataRoot, home string, spec CommandSpec) error {
	if spec.Sandbox == CommandSandboxFullAccess {
		return nil
	}
	rootKey, err := commandPathKey(spec.WritableRoot)
	if err != nil {
		return err
	}
	runtimeRoot, err := security.WorkspaceRuntimeRoot(dataRoot, spec.WritableRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(runtimeRoot)
	resolvedData, dataErr := filepath.EvalSymlinks(dataRoot)
	if err != nil || dataErr != nil || !security.PathWithin(resolvedRoot, resolvedData) {
		return errors.New("CODEX_SANDBOX_IDENTITY_ROOT_INVALID")
	}
	seed, err := loadCommandIdentity(filepath.Join(runtimeRoot, "sandbox-identity.json"), rootKey)
	if err != nil {
		return err
	}
	caps := commandCapabilities{
		Workspace: capabilitySID(seed, "workspace"), Readonly: capabilitySID(seed, "readonly"),
		WorkspaceByCWD: make(map[string]string), WritableRootByPath: make(map[string]string),
	}
	// Seed both lookup maps: Codex chooses a different map when a writable root
	// equals CWD. The SID for that root must stay the same when CWD changes.
	roots := append([]string{spec.WritableRoot, spec.CWD}, spec.WritableRoots...)
	for _, root := range roots {
		key, err := commandPathKey(root)
		if err != nil {
			return err
		}
		sid := capabilitySID(seed, "root:"+key)
		caps.WorkspaceByCWD[key] = sid
		caps.WritableRootByPath[key] = sid
	}
	payload, err := json.Marshal(caps)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "cap_sid"), payload, 0600)
}

func loadCommandIdentity(path, workspace string) ([]byte, error) {
	commandIdentityMu.Lock()
	defer commandIdentityMu.Unlock()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		seed := make([]byte, 32)
		if _, err := rand.Read(seed); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(commandIdentity{Schema: commandIdentitySchema, Workspace: workspace, Seed: hex.EncodeToString(seed)})
		if err != nil {
			return nil, err
		}
		if err := writeAtomic(path, payload, 0600); err != nil {
			return nil, err
		}
		return seed, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
		return nil, errors.New("CODEX_SANDBOX_IDENTITY_INVALID")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var identity commandIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return nil, errors.New("CODEX_SANDBOX_IDENTITY_INVALID")
	}
	seed, err := hex.DecodeString(identity.Seed)
	if err != nil || len(seed) != 32 || identity.Schema != commandIdentitySchema || identity.Workspace != workspace {
		return nil, errors.New("CODEX_SANDBOX_IDENTITY_INVALID")
	}
	return seed, nil
}

func capabilitySID(seed []byte, label string) string {
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte(label))
	digest := mac.Sum(nil)
	return fmt.Sprintf("S-1-5-21-%d-%d-%d-%d", binary.LittleEndian.Uint32(digest[0:4]), binary.LittleEndian.Uint32(digest[4:8]), binary.LittleEndian.Uint32(digest[8:12]), binary.LittleEndian.Uint32(digest[12:16]))
}

func commandPathKey(path string) (string, error) {
	resolved, err := security.CanonicalPath(path, path)
	if err != nil {
		return "", err
	}
	// Match dunce::canonicalize + slash replacement + to_ascii_lowercase.
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		resolved = `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`)
	} else {
		resolved = strings.TrimPrefix(resolved, `\\?\`)
	}
	key := []byte(filepath.ToSlash(resolved))
	for i, ch := range key {
		if ch >= 'A' && ch <= 'Z' {
			key[i] = ch + ('a' - 'A')
		}
	}
	return string(key), nil
}

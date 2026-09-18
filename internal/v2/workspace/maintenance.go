package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/AAAYNMMM/CWapi/internal/repository"
	"github.com/AAAYNMMM/CWapi/internal/security"
)

type resolvedWorkspace struct {
	container string
	repoPath  string
}

// ResolveAt returns the repository directory for exactly one repository +
// canonical target ref. It never exposes or depends on the workspace key at
// the caller boundary. V1 branch-aware storage is preferred. A legacy
// repository-only container is considered only when the V1 container does not
// exist, and only when legacy metadata exactly matches repository + target_ref.
func ResolveAt(dataRoot, repositoryName, targetRef string) (string, error) {
	resolved, err := resolveWorkspaceAt(dataRoot, repositoryName, targetRef)
	if err != nil {
		return "", err
	}
	return resolved.repoPath, nil
}

// DeleteAt removes exactly one durable repository + branch workspace. It does
// not try to repair Git and is intentionally kept outside MCP Coding tools.
// Callers must first stop the owning Coding service so no session can race
// deletion.
func DeleteAt(dataRoot, repositoryName, targetRef string) error {
	resolved, err := resolveWorkspaceAt(dataRoot, repositoryName, targetRef)
	if err != nil {
		return err
	}
	dataRoot = filepath.Clean(strings.TrimSpace(dataRoot))
	runtimeRoot, err := security.WorkspaceRuntimeRoot(dataRoot, resolved.repoPath)
	if err != nil {
		return err
	}
	legacyRuntimeRoot, err := security.LegacyWorkspaceRuntimeRoot(dataRoot, resolved.repoPath)
	if err != nil {
		return err
	}
	runtimeBase := filepath.Join(dataRoot, "runtime", "workspaces")
	for _, root := range []string{runtimeRoot, legacyRuntimeRoot} {
		if !security.PathWithin(root, runtimeBase) {
			return errors.New("WORKSPACE_RUNTIME_PATH_INVALID")
		}
	}
	if err := os.RemoveAll(resolved.container); err != nil {
		return errors.New("WORKSPACE_DELETE_FAILED")
	}
	for _, root := range []string{runtimeRoot, legacyRuntimeRoot} {
		if runtimeInfo, runtimeErr := os.Lstat(root); runtimeErr == nil {
			if runtimeInfo.Mode()&os.ModeSymlink != 0 || !runtimeInfo.IsDir() {
				return errors.New("WORKSPACE_RUNTIME_PATH_INVALID")
			}
			if err := os.RemoveAll(root); err != nil {
				return errors.New("WORKSPACE_RUNTIME_DELETE_FAILED")
			}
		} else if !errors.Is(runtimeErr, os.ErrNotExist) {
			return errors.New("WORKSPACE_RUNTIME_DELETE_FAILED")
		}
	}
	return nil
}

func resolveWorkspaceAt(dataRoot, repositoryName, targetRef string) (resolvedWorkspace, error) {
	dataRoot = strings.TrimSpace(dataRoot)
	if dataRoot == "" || !filepath.IsAbs(dataRoot) {
		return resolvedWorkspace{}, errors.New("WORKSPACE_DATA_ROOT_INVALID")
	}
	repositoryName = strings.ToLower(strings.TrimSpace(repositoryName))
	parsed, err := repository.Parse("https://github.com/" + repositoryName)
	if err != nil || parsed.Repository != repositoryName {
		return resolvedWorkspace{}, errors.New("WORKSPACE_REPOSITORY_INVALID")
	}
	identity, err := NewWorkspaceIdentity(parsed.Repository, targetRef)
	if err != nil {
		return resolvedWorkspace{}, err
	}
	root := filepath.Join(filepath.Clean(dataRoot), "workspaces")
	primary := filepath.Join(root, WorkspaceDirectoryKey(identity))
	if exists, err := workspaceContainerExists(primary); err != nil {
		return resolvedWorkspace{}, err
	} else if exists {
		return validateResolvedContainer(primary, identity)
	}

	previousV1 := filepath.Join(root, legacyBranchAwareWorkspaceKey(identity))
	if exists, err := workspaceContainerExists(previousV1); err != nil {
		return resolvedWorkspace{}, err
	} else if exists {
		return validateResolvedContainer(previousV1, identity)
	}

	legacy := filepath.Join(root, legacyWorkspaceKey(identity.Repository))
	if exists, err := workspaceContainerExists(legacy); err != nil {
		return resolvedWorkspace{}, err
	} else if !exists {
		return resolvedWorkspace{}, errors.New("WORKSPACE_NOT_FOUND")
	}
	resolved, err := validateResolvedContainer(legacy, identity)
	if err != nil {
		// Repository-only fallback is intentionally indistinguishable from
		// not-found when its metadata names another branch. Never fall through
		// to a different target.
		if errors.Is(err, errWorkspaceIdentityMismatch) {
			return resolvedWorkspace{}, errors.New("WORKSPACE_NOT_FOUND")
		}
		return resolvedWorkspace{}, err
	}
	return resolved, nil
}

var errWorkspaceIdentityMismatch = errors.New("WORKSPACE_METADATA_IDENTITY_MISMATCH")

func workspaceContainerExists(container string) (bool, error) {
	info, err := os.Lstat(container)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, errors.New("WORKSPACE_CONTAINER_INVALID")
	}
	return true, nil
}

func validateResolvedContainer(container string, identity WorkspaceIdentity) (resolvedWorkspace, error) {
	meta, err := loadMetadata(filepath.Join(container, "workspace.json"))
	if err != nil {
		return resolvedWorkspace{}, err
	}
	if meta.Repository != identity.Repository || meta.TargetRef != identity.TargetRef {
		return resolvedWorkspace{}, errWorkspaceIdentityMismatch
	}
	repoPath := filepath.Join(container, "repo")
	info, err := os.Lstat(repoPath)
	if errors.Is(err, os.ErrNotExist) {
		return resolvedWorkspace{}, errors.New("WORKSPACE_NOT_FOUND")
	}
	if err != nil {
		return resolvedWorkspace{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return resolvedWorkspace{}, errors.New("WORKSPACE_REPOSITORY_PATH_INVALID")
	}
	return resolvedWorkspace{container: container, repoPath: repoPath}, nil
}

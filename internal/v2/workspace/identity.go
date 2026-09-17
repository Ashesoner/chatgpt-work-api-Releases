package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// WorkspaceIdentity is the single durable/runtime identity for one Coding workspace.
// Repository must already be the normalized owner/repository value. TargetRef is
// always stored as refs/heads/<branch>.
type WorkspaceIdentity struct {
	Repository string
	TargetRef  string
}

// NewWorkspaceIdentity normalizes the repository and target ref without doing
// network I/O. Git's full branch-name validation remains in Manager.Prepare.
func NewWorkspaceIdentity(repositoryName, targetRef string) (WorkspaceIdentity, error) {
	repositoryName = strings.ToLower(strings.TrimSpace(repositoryName))
	if repositoryName == "" {
		return WorkspaceIdentity{}, errors.New("WORKSPACE_REPOSITORY_INVALID")
	}
	canonical, _, err := CanonicalTargetRef(targetRef)
	if err != nil {
		return WorkspaceIdentity{}, err
	}
	return WorkspaceIdentity{Repository: repositoryName, TargetRef: canonical}, nil
}

// CanonicalTargetRef makes the two accepted branch spellings equivalent.
func CanonicalTargetRef(raw string) (string, string, error) {
	branch := strings.TrimSpace(raw)
	if strings.HasPrefix(branch, "refs/heads/") {
		branch = strings.TrimPrefix(branch, "refs/heads/")
	}
	if branch == "" {
		return "", "", errors.New("WORKSPACE_TARGET_REF_REQUIRED")
	}
	return "refs/heads/" + branch, branch, nil
}

// WorkspaceKey is the stable on-disk key for exactly one repository + branch.
// The NUL separator prevents ambiguous concatenations. Existing repository-only
// workspace keys are intentionally not reused or migrated.
func WorkspaceKey(identity WorkspaceIdentity) string {
	sum := sha256.Sum256([]byte(identity.Repository + "\x00" + identity.TargetRef))
	return hex.EncodeToString(sum[:])
}

// legacyWorkspaceKey returns the upstream 2.0.5 repository-only key. It is
// retained only so pre-V1 maintenance code can still address old workspaces
// until the branch-aware GUI/maintenance stage is implemented. New workspaces
// must never use this key.
func legacyWorkspaceKey(repositoryName string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(repositoryName))))
	return hex.EncodeToString(sum[:])
}

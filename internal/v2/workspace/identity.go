package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const workspaceKeyHexLength = 24

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

func branchAwareWorkspaceDigest(identity WorkspaceIdentity) string {
	sum := sha256.Sum256([]byte(identity.Repository + "\x00" + identity.TargetRef))
	return hex.EncodeToString(sum[:])
}

// WorkspaceKey is the full stable runtime identity key for exactly one
// repository + branch. Runtime Coding ownership keeps the full digest; path
// shortening must not weaken active/opening/close identity semantics.
func WorkspaceKey(identity WorkspaceIdentity) string {
	return branchAwareWorkspaceDigest(identity)
}

// WorkspaceDirectoryKey is the stable short on-disk directory key. The full
// repository + target_ref identity is always verified from workspace.json.
func WorkspaceDirectoryKey(identity WorkspaceIdentity) string {
	return branchAwareWorkspaceDigest(identity)[:workspaceKeyHexLength]
}

// legacyBranchAwareWorkspaceKey is the original V1 64-hex on-disk key.
// Existing directories using it remain readable but are never renamed or
// migrated automatically.
func legacyBranchAwareWorkspaceKey(identity WorkspaceIdentity) string {
	return branchAwareWorkspaceDigest(identity)
}

// legacyWorkspaceKey returns the upstream 2.0.5 repository-only 64-hex key.
// Existing directories using it are considered only when workspace metadata
// exactly matches the requested repository + target ref.
func legacyWorkspaceKey(repositoryName string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(repositoryName))))
	return hex.EncodeToString(sum[:])
}

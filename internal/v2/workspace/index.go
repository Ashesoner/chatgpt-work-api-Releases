package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AAAYNMMM/CWapi/internal/repository"
)

type WorkspaceEntry struct {
	Repository string `json:"repository"`
	TargetRef  string `json:"target_ref"`
	Branch     string `json:"branch"`
}

type IndexSnapshot struct {
	RepositoryCount int              `json:"repository_count"`
	Repositories    []string         `json:"repositories,omitempty"`
	Workspaces      []WorkspaceEntry `json:"workspaces,omitempty"`
	InvalidEntries  int              `json:"invalid_entries,omitempty"`
}

// Index reads durable workspace metadata only. It never fetches or mutates Git.
// Short-key V1, original 64-hex branch-aware V1, and legacy repository-only
// containers are recognized, but only when metadata is valid and the directory
// key matches that metadata. Duplicate copies of the same identity collapse to
// one public entry; maintenance resolution prefers the newest short-key form.
func (m *Manager) Index() IndexSnapshot {
	if m == nil {
		return IndexSnapshot{}
	}
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return IndexSnapshot{}
	}
	repositoriesSeen := map[string]struct{}{}
	workspacesSeen := map[string]WorkspaceEntry{}
	invalid := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := loadMetadata(filepath.Join(m.root, entry.Name(), "workspace.json"))
		if err != nil {
			invalid++
			continue
		}
		workspaceEntry, identity, ok := indexEntry(meta)
		if !ok {
			invalid++
			continue
		}
		if entry.Name() != WorkspaceDirectoryKey(identity) &&
			entry.Name() != legacyBranchAwareWorkspaceKey(identity) &&
			entry.Name() != legacyWorkspaceKey(identity.Repository) {
			invalid++
			continue
		}
		repositoriesSeen[workspaceEntry.Repository] = struct{}{}
		workspacesSeen[WorkspaceKey(identity)] = workspaceEntry
	}

	repositories := make([]string, 0, len(repositoriesSeen))
	for repositoryName := range repositoriesSeen {
		repositories = append(repositories, repositoryName)
	}
	sort.Strings(repositories)

	workspaces := make([]WorkspaceEntry, 0, len(workspacesSeen))
	for _, entry := range workspacesSeen {
		workspaces = append(workspaces, entry)
	}
	sort.Slice(workspaces, func(i, j int) bool {
		if workspaces[i].Repository != workspaces[j].Repository {
			return workspaces[i].Repository < workspaces[j].Repository
		}
		return workspaces[i].TargetRef < workspaces[j].TargetRef
	})
	return IndexSnapshot{
		RepositoryCount: len(repositories),
		Repositories:    repositories,
		Workspaces:      workspaces,
		InvalidEntries:  invalid,
	}
}

func indexEntry(meta metadata) (WorkspaceEntry, WorkspaceIdentity, bool) {
	repositoryName := strings.ToLower(strings.TrimSpace(meta.Repository))
	parsed, err := repository.Parse("https://github.com/" + repositoryName)
	if err != nil || parsed.Repository != repositoryName {
		return WorkspaceEntry{}, WorkspaceIdentity{}, false
	}
	identity, err := NewWorkspaceIdentity(repositoryName, meta.TargetRef)
	if err != nil || identity.TargetRef != strings.TrimSpace(meta.TargetRef) {
		return WorkspaceEntry{}, WorkspaceIdentity{}, false
	}
	branch := strings.TrimPrefix(identity.TargetRef, "refs/heads/")
	if branch == "" || branch == identity.TargetRef {
		return WorkspaceEntry{}, WorkspaceIdentity{}, false
	}
	return WorkspaceEntry{Repository: identity.Repository, TargetRef: identity.TargetRef, Branch: branch}, identity, true
}

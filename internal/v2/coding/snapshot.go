package coding

import (
	"sort"

	"github.com/AAAYNMMM/CWapi/internal/executiondiag"
)

type RuntimeSnapshot struct {
	State               string                 `json:"state"`
	Active              int                    `json:"active"`
	Repositories        []string               `json:"repositories,omitempty"`
	LastExecution       *executiondiag.Outcome `json:"last_execution,omitempty"`
	LastExecutionTarget string                 `json:"last_execution_target_ref,omitempty"`
	LastExecutionBranch string                 `json:"last_execution_branch,omitempty"`
}

func (s *Service) RuntimeSnapshot() RuntimeSnapshot {
	if s == nil {
		return RuntimeSnapshot{State: "unavailable"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	active := 0
	var lastExecution *executiondiag.Outcome
	var lastExecutionTarget, lastExecutionBranch string
	for key, owner := range s.active {
		if owner == "" {
			continue
		}
		active++
		if owner == openingRepository {
			if opening := s.opening[key]; opening != nil {
				seen[opening.identity.Repository] = struct{}{}
			}
			continue
		}
		if record := s.sessions[owner]; record != nil {
			seen[record.repository] = struct{}{}
			record.mu.Lock()
			if record.lastExecution != nil && (lastExecution == nil || record.lastExecution.At > lastExecution.At) {
				lastExecution = record.lastExecution
				lastExecutionTarget = record.targetRef
				lastExecutionBranch = record.currentBranch
			}
			record.mu.Unlock()
		}
	}
	repositories := make([]string, 0, len(seen))
	for repository := range seen {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	state := "ready"
	if s.closed {
		state = "closed"
	} else if active > 0 {
		state = "active"
	}
	return RuntimeSnapshot{
		State: state, Active: active, Repositories: repositories, LastExecution: lastExecution,
		LastExecutionTarget: lastExecutionTarget, LastExecutionBranch: lastExecutionBranch,
	}
}

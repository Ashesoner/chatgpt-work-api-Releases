package coding

import "sort"

type RuntimeSnapshot struct {
	State        string   `json:"state"`
	Active       int      `json:"active"`
	Repositories []string `json:"repositories,omitempty"`
}

func (s *Service) RuntimeSnapshot() RuntimeSnapshot {
	if s == nil {
		return RuntimeSnapshot{State: "unavailable"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	active := 0
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
	return RuntimeSnapshot{State: state, Active: active, Repositories: repositories}
}

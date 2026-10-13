package promptstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Skill struct {
	ID          string
	Name        string
	Description string
	Content     string
	Hidden      bool
}

type Store struct {
	CodingCore  string
	CodingRules string
	AgentCore   string
	AgentRules  string
	Skills      map[string]Skill
}

func DiscoverRoot() (string, error) {

	candidates := make([]string, 0, 10)
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "prompts"))
	}
	if cwd, err := os.Getwd(); err == nil {
		dir := filepath.Clean(cwd)
		for i := 0; i < 8; i++ {
			candidates = append(candidates, filepath.Join(dir, "prompts"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("PROMPT_ROOT_NOT_FOUND")
}

func Load(root string, requireCoding, requireAgent bool) (*Store, []string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || root == "" {
		return nil, nil, errors.New("PROMPT_ROOT_REQUIRED")
	}
	store := &Store{Skills: map[string]Skill{}}
	var err error
	if requireCoding {
		if store.CodingCore, err = readRequired(filepath.Join(root, "coding", "core.md"), "CODING_CORE_LOAD_FAILED"); err != nil {
			return nil, nil, err
		}
		if store.CodingRules, err = readRequired(filepath.Join(root, "coding", "rules.md"), "CODING_RULES_LOAD_FAILED"); err != nil {
			return nil, nil, err
		}
	}
	if requireAgent {
		if store.AgentCore, err = readRequired(filepath.Join(root, "agent", "core.md"), "AGENT_CORE_LOAD_FAILED"); err != nil {
			return nil, nil, err
		}
		if store.AgentRules, err = readRequired(filepath.Join(root, "agent", "rules.md"), "AGENT_RULES_LOAD_FAILED"); err != nil {
			return nil, nil, err
		}
	}

	warnings := []string{}
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, warnings, nil
		}
		return nil, nil, fmt.Errorf("SKILL_DIRECTORY_READ_FAILED: %w", err)
	}
	skillsRoot := filepath.Join(root, "skills")
	legacyFiles := make([]string, 0, len(entries))
	directories := make([]string, 0, len(entries))
	for _, entry := range entries {
		entryPath := filepath.Join(skillsRoot, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			warnings = append(warnings, "Failed to load skill: "+entry.Name()+" (linked path not allowed)")
			continue
		}
		if entry.IsDir() {
			directories = append(directories, entry.Name())
			continue
		}
		// Windows directory junctions may not report IsDir through DirEntry even
		// though os.Stat follows them as directories. Report and reject instead
		// of silently treating a linked directory as an ordinary file.
		if info, statErr := os.Stat(entryPath); statErr == nil && info.IsDir() {
			warnings = append(warnings, "Failed to load skill: "+entry.Name()+" (linked directory not allowed)")
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			legacyFiles = append(legacyFiles, entry.Name())
		}
	}
	// Preserve backward compatibility: existing single-file IDs claim their IDs
	// before directory Skills expose main IDs or hidden reference aliases.
	for _, fileName := range legacyFiles {
		loadSingleFileSkill(store, &warnings, skillsRoot, fileName)
	}
	for _, directoryName := range directories {
		loadDirectorySkill(store, &warnings, skillsRoot, directoryName)
	}
	return store, warnings, nil
}

func (s *Store) Instructions(mode string) (string, error) {
	if s == nil {
		return "", errors.New("PROMPT_STORE_UNAVAILABLE")
	}
	var core, rules string
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "coding":
		core, rules = s.CodingCore, s.CodingRules
	case "agent":
		core, rules = s.AgentCore, s.AgentRules
	default:
		return "", errors.New("PROMPT_MODE_INVALID")
	}
	if strings.TrimSpace(core) == "" || strings.TrimSpace(rules) == "" {
		return "", errors.New("PROMPT_MODE_RESOURCES_MISSING")
	}
	return strings.TrimSpace(core) + "\n\n" + strings.TrimSpace(rules) + "\n\n" + s.SkillList(), nil
}

func (s *Store) SkillList() string {
	lines := []string{"Available Skills:"}
	if s == nil || len(s.Skills) == 0 {
		return strings.Join(lines, "\n") + "\n(none)"
	}
	ids := make([]string, 0, len(s.Skills))
	for id, skill := range s.Skills {
		if skill.Hidden {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return strings.Join(lines, "\n") + "\n(none)"
	}
	for _, id := range ids {
		skill := s.Skills[id]
		lines = append(lines, fmt.Sprintf("%s - %s: %s", skill.ID, skill.Name, skill.Description))
	}
	return strings.Join(lines, "\n")
}

func (s *Store) LoadSkill(name string) (Skill, error) {
	name = strings.TrimSpace(name)
	if s == nil || name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return Skill{}, errors.New("SKILL_NAME_INVALID")
	}
	skill, ok := s.Skills[name]
	if !ok {
		return Skill{}, fmt.Errorf("SKILL_NOT_FOUND: %s", name)
	}
	return skill, nil
}

func readRequired(path, code string) (string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", code, err)
	}
	if !utf8.Valid(payload) || strings.TrimSpace(string(payload)) == "" {
		return "", fmt.Errorf("%s: invalid or empty UTF-8 markdown", code)
	}
	return string(payload), nil
}

func parseSkill(id, content string) Skill {
	id = strings.TrimSpace(id)
	name, description := frontMatterMetadata(content)
	if name == "" {
		name = id
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && name == id {
			if candidate := strings.TrimSpace(strings.TrimPrefix(trimmed, "# ")); candidate != "" {
				name = candidate
			}
		}
		if description == "" && strings.HasPrefix(strings.ToLower(trimmed), "description:") {
			candidate := strings.TrimSpace(trimmed[len("Description:"):])
			if candidate != "" && candidate != ">" && candidate != "|" {
				description = candidate
			}
		}
	}
	if description == "" {
		description = name
	}
	return Skill{ID: id, Name: name, Description: description, Content: strings.TrimSpace(content)}
}

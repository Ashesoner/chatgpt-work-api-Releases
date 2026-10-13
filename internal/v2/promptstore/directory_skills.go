package promptstore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxSkillMarkdownBytes       int64 = 1 << 20
	maxDirectorySkillReferences       = 64
)

func loadSingleFileSkill(store *Store, warnings *[]string, skillsRoot, fileName string) {
	path := filepath.Join(skillsRoot, fileName)
	content, err := readSkillMarkdown(path, skillsRoot)
	if err != nil {
		*warnings = append(*warnings, "Failed to load skill: "+fileName+" ("+err.Error()+")")
		return
	}
	id := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	registerSkill(store, warnings, parseSkill(id, content), fileName)
}

func loadDirectorySkill(store *Store, warnings *[]string, skillsRoot, directoryName string) {
	id := strings.TrimSpace(directoryName)
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		*warnings = append(*warnings, "Failed to load skill directory: "+directoryName)
		return
	}
	if _, exists := store.Skills[id]; exists {
		*warnings = append(*warnings, "Duplicate skill ID: "+id)
		return
	}

	directoryPath := filepath.Join(skillsRoot, directoryName)
	directory, err := safeSkillPath(skillsRoot, directoryPath, true)
	if err != nil {
		*warnings = append(*warnings, "Failed to load skill directory: "+directoryName+" ("+err.Error()+")")
		return
	}

	mainContent, err := readSkillMarkdown(filepath.Join(directory, "SKILL.md"), directory)
	if err != nil {
		*warnings = append(*warnings, "Failed to load skill directory: "+directoryName+" (SKILL.md missing or invalid: "+err.Error()+")")
		return
	}
	mainSkill := parseSkill(id, mainContent)

	overlayPath := filepath.Join(directory, "CWAPI.md")
	if _, statErr := os.Lstat(overlayPath); statErr == nil {
		overlay, overlayErr := readSkillMarkdown(overlayPath, directory)
		if overlayErr != nil {
			*warnings = append(*warnings, "Failed to load skill directory: "+directoryName+" (CWAPI.md invalid: "+overlayErr.Error()+")")
			return
		}
		mainSkill.Content = strings.TrimSpace(mainSkill.Content) + "\n\n" + strings.TrimSpace(overlay)
	} else if !os.IsNotExist(statErr) {
		*warnings = append(*warnings, "Failed to inspect skill overlay: "+directoryName+" ("+statErr.Error()+")")
		return
	}

	type reference struct {
		path  string
		skill Skill
	}
	references := []reference{}
	referenceRootPath := filepath.Join(directory, "references")
	if _, statErr := os.Lstat(referenceRootPath); statErr == nil {
		referenceRoot, pathErr := safeSkillPath(directory, referenceRootPath, true)
		if pathErr != nil {
			*warnings = append(*warnings, "Failed to read skill references: "+directoryName+" ("+pathErr.Error()+")")
			return
		}
		entries, readErr := os.ReadDir(referenceRoot)
		if readErr != nil {
			*warnings = append(*warnings, "Failed to read skill references: "+directoryName+" ("+readErr.Error()+")")
			return
		}
		markdownCount := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				markdownCount++
			}
		}
		if markdownCount > maxDirectorySkillReferences {
			*warnings = append(*warnings, fmt.Sprintf(
				"Failed to load skill directory: %s (too many references: %d > %d)",
				directoryName, markdownCount, maxDirectorySkillReferences,
			))
			return
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				continue
			}
			content, readErr := readSkillMarkdown(filepath.Join(referenceRoot, entry.Name()), referenceRoot)
			if readErr != nil {
				*warnings = append(*warnings, "Failed to load skill reference: "+directoryName+"/references/"+entry.Name()+" ("+readErr.Error()+")")
				continue
			}
			refName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			aliasID := id + "." + refName
			if _, exists := store.Skills[aliasID]; exists {
				*warnings = append(*warnings, "Duplicate skill ID: "+aliasID)
				continue
			}
			refSkill := parseSkill(aliasID, content)
			refSkill.Hidden = true
			if refSkill.Name == aliasID {
				refSkill.Name = mainSkill.Name + " / " + refName
			}
			references = append(references, reference{path: "references/" + entry.Name(), skill: refSkill})
		}
	} else if !os.IsNotExist(statErr) {
		*warnings = append(*warnings, "Failed to inspect skill references: "+directoryName+" ("+statErr.Error()+")")
		return
	}

	sort.Slice(references, func(i, j int) bool { return references[i].path < references[j].path })
	if len(references) > 0 {
		var aliases strings.Builder
		aliases.WriteString("\n\n## CWapi reference aliases\n")
		aliases.WriteString("This directory Skill uses the existing `load_skill(name)` tool. Load referenced Markdown files with these aliases:\n")
		for _, ref := range references {
			fmt.Fprintf(&aliases, "- `%s` -> `load_skill(name=\"%s\")`\n", ref.path, ref.skill.ID)
		}
		mainSkill.Content = strings.TrimSpace(mainSkill.Content) + aliases.String()
	}

	registerSkill(store, warnings, mainSkill, directoryName+"/SKILL.md")
	for _, ref := range references {
		registerSkill(store, warnings, ref.skill, directoryName+"/"+ref.path)
	}
}

func readSkillMarkdown(path, allowedRoot string) (string, error) {
	resolved, err := safeSkillPath(allowedRoot, path, false)
	if err != nil {
		return "", err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	if info.Size() > maxSkillMarkdownBytes {
		return "", fmt.Errorf("file too large: %d > %d bytes", info.Size(), maxSkillMarkdownBytes)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxSkillMarkdownBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(payload)) > maxSkillMarkdownBytes {
		return "", fmt.Errorf("file too large")
	}
	if !utf8.Valid(payload) || strings.TrimSpace(string(payload)) == "" {
		return "", fmt.Errorf("invalid or empty UTF-8 markdown")
	}
	return string(payload), nil
}

func safeSkillPath(allowedRoot, candidate string, requireDir bool) (string, error) {
	rootAbs, err := filepath.Abs(allowedRoot)
	if err != nil {
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	candidateResolved, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(rootResolved, candidateResolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes skill root")
	}
	if !sameFilesystemPath(candidateAbs, candidateResolved) {
		return "", fmt.Errorf("linked path not allowed")
	}

	info, err := os.Stat(candidateResolved)
	if err != nil {
		return "", err
	}
	if requireDir && !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	if !requireDir && info.IsDir() {
		return "", fmt.Errorf("expected file, got directory")
	}
	return candidateResolved, nil
}

func sameFilesystemPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func registerSkill(store *Store, warnings *[]string, skill Skill, source string) {
	if store == nil || strings.TrimSpace(skill.ID) == "" {
		return
	}
	if _, exists := store.Skills[skill.ID]; exists {
		*warnings = append(*warnings, "Duplicate skill ID: "+skill.ID+" from "+source)
		return
	}
	store.Skills[skill.ID] = skill
}

func frontMatterMetadata(content string) (string, string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	name, description := "", ""
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "name:") && name == "" {
			name = strings.TrimSpace(trimmed[len("name:"):])
			continue
		}
		if strings.HasPrefix(lower, "description:") && description == "" {
			value := strings.TrimSpace(trimmed[len("description:"):])
			if value != ">" && value != "|" {
				description = value
				continue
			}
			parts := []string{}
			for j := i + 1; j < len(lines); j++ {
				next := lines[j]
				if strings.TrimSpace(next) == "---" || (len(next) > 0 && next[0] != ' ' && next[0] != '\t') {
					break
				}
				if part := strings.TrimSpace(next); part != "" {
					parts = append(parts, part)
				}
				i = j
			}
			description = strings.Join(parts, " ")
		}
	}
	return name, description
}

package promptstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDirectorySkillKeepsLoadSkillNameShape(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("coding/core.md", "core")
	mustWrite("coding/rules.md", "rules")
	mustWrite("skills/legacy.md", "# Legacy\nDescription: legacy single-file skill\n")
	mustWrite("skills/agent-reach/SKILL.md", "---\nname: agent-reach\ndescription: >\n  Research and read internet sources.\n  Route to the matching backend.\n---\n# Agent Reach\nSee [social](references/social.md).\n")
	mustWrite("skills/agent-reach/CWAPI.md", "# CWapi adapter\nUse SAFE execution.\n")
	mustWrite("skills/agent-reach/references/social.md", "# Social\nSocial platform runbook.\n")
	mustWrite("skills/agent-reach/references/web.md", "# Web\nWeb reading runbook.\n")
	mustWrite("skills/agent-reach/SKILL_en.md", "# Ignored locale variant\n")

	store, warnings, err := Load(root, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if got := store.Skills["agent-reach"].Description; got != "Research and read internet sources. Route to the matching backend." {
		t.Fatalf("front matter description mismatch: %q", got)
	}
	main, err := store.LoadSkill("agent-reach")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(main.Content, "Use SAFE execution.") {
		t.Fatalf("main skill missing CWAPI.md overlay:\n%s", main.Content)
	}
	if !strings.Contains(main.Content, `load_skill(name="agent-reach.social")`) || !strings.Contains(main.Content, `load_skill(name="agent-reach.web")`) {
		t.Fatalf("main skill missing reference aliases:\n%s", main.Content)
	}
	social, err := store.LoadSkill("agent-reach.social")
	if err != nil {
		t.Fatal(err)
	}
	if !social.Hidden || !strings.Contains(social.Content, "Social platform runbook") {
		t.Fatalf("reference skill mismatch: %#v", social)
	}
	list := store.SkillList()
	if !strings.Contains(list, "agent-reach - Agent Reach") || !strings.Contains(list, "legacy - Legacy") {
		t.Fatalf("top-level skills missing from list:\n%s", list)
	}
	if strings.Contains(list, "agent-reach.social") || strings.Contains(list, "agent-reach.web") {
		t.Fatalf("reference aliases should stay out of top-level skill list:\n%s", list)
	}
}

func TestLoadDirectorySkillRequiresSkillMarkdown(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "broken", "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Skills) != 0 {
		t.Fatalf("unexpected skills: %#v", store.Skills)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "SKILL.md missing or invalid") {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestSkillMarkdownSizeLimit(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", int(maxSkillMarkdownBytes)+1)
	if err := os.WriteFile(filepath.Join(skills, "huge.md"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Skills["huge"]; ok {
		t.Fatal("oversized skill should not load")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "file too large") {
		t.Fatalf("expected size warning, got %v", warnings)
	}
}

func TestDirectorySkillReferenceLimitFailsClosed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "many")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Many\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Use deterministic numeric names without relying on formatting helpers.
	for i := 0; i < maxDirectorySkillReferences+1; i++ {
		name := filepath.Join(dir, "references", string(rune('a'+(i%26)))+"-"+strconvItoa(i)+".md")
		if err := os.WriteFile(name, []byte("# Ref\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Skills["many"]; ok {
		t.Fatal("directory skill should fail closed when reference limit is exceeded")
	}
	if !warningsContain(warnings, "too many references") {
		t.Fatalf("expected reference-limit warning, got %v", warnings)
	}
}

func TestDirectorySkillAliasCollisionIsReported(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "foo")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "bar.md"), []byte("# Ref Bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "foo.bar.md"), []byte("# Flat Bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	collision, ok := store.Skills["foo.bar"]
	if !ok {
		t.Fatal("one deterministic foo.bar skill should remain available")
	}
	if !strings.Contains(collision.Content, "Flat Bar") {
		t.Fatalf("legacy single-file skill should win alias collision for backward compatibility: %#v", collision)
	}
	if !warningsContain(warnings, "Duplicate skill ID: foo.bar") {
		t.Fatalf("expected alias collision warning, got %v", warnings)
	}
}

func TestLinkedSkillFileIsRejected(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "skills")
	outside := t.TempDir()
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(target, []byte("# Outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(skills, "linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Skills["linked"]; ok {
		t.Fatal("linked skill should not load")
	}
	if len(warnings) == 0 {
		t.Fatal("linked skill should emit a warning")
	}
}

func TestBundledAgentReachSkillLoads(t *testing.T) {
	root := filepath.Join("..", "..", "..", "prompts")
	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected bundled skill warnings: %v", warnings)
	}
	for _, id := range []string{
		"agent-reach", "agent-reach.search", "agent-reach.social", "agent-reach.career",
		"agent-reach.dev", "agent-reach.web", "agent-reach.video", "agent-reach.finance",
	} {
		if _, err := store.LoadSkill(id); err != nil {
			t.Fatalf("bundled skill %s not loadable: %v", id, err)
		}
	}
	main, _ := store.LoadSkill("agent-reach")
	if !strings.Contains(main.Content, `load_skill(name="agent-reach.social")`) {
		t.Fatal("bundled Agent Reach main skill is missing CWapi reference aliases")
	}
	if !strings.Contains(main.Content, "CWapi integration override") {
		t.Fatal("bundled Agent Reach main skill is missing CWAPI.md overlay")
	}
}

func TestBundledAgentReachUpstreamManifest(t *testing.T) {
	root := filepath.Join("..", "..", "..", "prompts", "skills", "agent-reach")
	payload, err := os.ReadFile(filepath.Join(root, "UPSTREAM.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) == 0 {
		t.Fatal("UPSTREAM.json has no tracked upstream files")
	}
	for rel, expected := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if !strings.EqualFold(actual, expected) {
			t.Fatalf("%s differs from upstream manifest: got %s want %s", rel, actual, expected)
		}
	}
}

func warningsContain(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}

func TestLegacyMainIDWinsDirectoryCollision(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Directory Foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "foo.md"), []byte("# Legacy Foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, warnings, err := Load(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	skill, ok := store.Skills["foo"]
	if !ok || !strings.Contains(skill.Content, "Legacy Foo") {
		t.Fatalf("legacy single-file skill should keep the main ID: %#v", skill)
	}
	if !warningsContain(warnings, "Duplicate skill ID: foo") {
		t.Fatalf("expected directory collision warning, got %v", warnings)
	}
}

func TestSkillListShowsNoneWhenOnlyHiddenAliasesExist(t *testing.T) {
	store := &Store{Skills: map[string]Skill{
		"hidden.ref": {ID: "hidden.ref", Name: "Hidden", Description: "Hidden", Hidden: true},
	}}
	if got := store.SkillList(); got != "Available Skills:\n(none)" {
		t.Fatalf("unexpected hidden-only skill list: %q", got)
	}
}

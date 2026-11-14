package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, File), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const simple = "---\nname: code-review\ndescription: Review code for bugs and security issues.\n---\n# Code Review\nCheck things.\n"

func TestParseSimpleSkill(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "code-review", simple)
	s, body, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "code-review" || s.Description != "Review code for bugs and security issues." || !s.Enabled {
		t.Fatalf("%+v", s)
	}
	if !strings.HasPrefix(body, "# Code Review") {
		t.Fatalf("body=%q", body)
	}
}

func TestParseYAMLShapes(t *testing.T) {
	src := `---
name: pdf-tools
description: >
  Work with PDF files.
  Use it when the user mentions PDFs.
license: "MIT"
compatibility: 'needs "python3"; ok'
allowed-tools: Read Grep   # informational
metadata:
  author: ada
  version: "1.2"
---
Body
`
	dir := writeSkill(t, t.TempDir(), "pdf-tools", src)
	s, _, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Description != "Work with PDF files. Use it when the user mentions PDFs." {
		t.Errorf("description=%q", s.Description)
	}
	if s.License != "MIT" || s.Compatibility != `needs "python3"; ok` {
		t.Errorf("license=%q compat=%q", s.License, s.Compatibility)
	}
	if len(s.AllowedTools) != 2 || s.AllowedTools[0] != "Read" {
		t.Errorf("allowed=%v", s.AllowedTools)
	}
	if s.Metadata["author"] != "ada" || s.Metadata["version"] != "1.2" {
		t.Errorf("metadata=%v", s.Metadata)
	}
}

func TestParseLenientAboutColonsInDescription(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "x", "---\nname: x\ndescription: Use for tasks like: reviews and audits\n---\n")
	s, _, err := Parse(dir)
	if err != nil || s.Description != "Use for tasks like: reviews and audits" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestParseErrors(t *testing.T) {
	root := t.TempDir()
	for name, c := range map[string]struct{ folder, src, want string }{
		"no header":        {"a", "# just text\n", "must start with"},
		"no name":          {"b", "---\ndescription: d\n---\n", "name"},
		"no description":   {"c", "---\nname: c\n---\n", "description"},
		"name differs":     {"d", "---\nname: other\ndescription: d\n---\n", "same as the folder"},
		"bad name":         {"Bad_Name", "---\nname: Bad_Name\ndescription: d\n---\n", "not valid"},
		"unclosed header":  {"e", "---\nname: e\ndescription: d\n", "must start with"},
		"stray indentation": {"f", "---\n  name: f\ndescription: d\n---\n", "indentation"},
	} {
		dir := writeSkill(t, root, c.folder, c.src)
		if _, _, err := Parse(dir); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want it to contain %q", name, err, c.want)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "code-review", "x1", "a-b-c"} {
		if !ValidName(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "a--b", "A", "a_b", "a b", "../x", strings.Repeat("a", 65)} {
		if ValidName(bad) {
			t.Errorf("%q must be invalid", bad)
		}
	}
}

func TestReadInsideStaysInsideTheSkill(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "s", "---\nname: s\ndescription: d\n---\n")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "a.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := readInside(dir, "references/a.md"); err != nil || got != "hello" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, bad := range []string{"../secret.txt", "references/../../secret.txt", secret, "/etc/passwd", `\windows\x`, "", "references", "missing.md"} {
		if _, err := readInside(dir, bad); err == nil {
			t.Errorf("%q must not be readable", bad)
		}
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link.txt")); err == nil { // links need rights on Windows
		if got, err := readInside(dir, "link.txt"); err == nil {
			t.Errorf("a link out of the skill must not be followed, got %q", got)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readInside(dir, "bin.dat"); err == nil {
		t.Error("a binary file is not text")
	}
}

func TestManagerListEnableAndPrompt(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "code-review", simple)
	writeSkill(t, root, "broken", "no header here")
	m := NewManager(root, filepath.Join(t.TempDir(), "skills.json"))
	m.Reload()

	if l := m.List(); len(l) != 1 || l[0].Name != "code-review" {
		t.Fatalf("list=%v", l)
	}
	if p := m.Problems(); len(p) != 1 || p[0].Dir != "broken" {
		t.Fatalf("problems=%v", p)
	}
	if !strings.Contains(m.Prompt(), "- code-review: Review code") {
		t.Fatalf("prompt=%q", m.Prompt())
	}
	if body, err := m.Body("code-review"); err != nil || !strings.Contains(body, "Check things") {
		t.Fatalf("body=%q err=%v", body, err)
	}

	if err := m.SetEnabled("code-review", false); err != nil {
		t.Fatal(err)
	}
	if m.Prompt() != "" {
		t.Error("a skill that is off is not announced")
	}
	if _, err := m.Body("code-review"); err == nil {
		t.Error("a skill that is off cannot be loaded")
	}
	// the choice survives a new manager (it is in a file)
	m2 := NewManager(root, m.statePath)
	m2.Reload()
	if l := m2.List(); len(l) != 1 || l[0].Enabled {
		t.Fatalf("list=%v", l)
	}
	if err := m.SetEnabled("nope", true); err == nil {
		t.Error("an unknown skill is an error")
	}

	// a skill that is copied in appears after a reload
	writeSkill(t, root, "git-flow", "---\nname: git-flow\ndescription: Branching rules.\n---\n")
	m.Reload()
	if len(m.List()) != 2 {
		t.Fatalf("list=%v", m.List())
	}
}

func TestInstallCopiesChecksAndRefusesDuplicates(t *testing.T) {
	src := writeSkill(t, t.TempDir(), "my-folder", "---\nname: renamed-skill\ndescription: Does things.\n---\nBody\n")
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "scripts", "run.sh"), []byte("echo hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()

	sk, rep, err := Install(src, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "renamed-skill" || sk.Dir != filepath.Join(dest, "renamed-skill") {
		t.Fatalf("%+v", sk)
	}
	if len(rep.Scripts) != 1 || rep.Scripts[0] != "scripts/run.sh" {
		t.Fatalf("scripts=%v", rep.Scripts)
	}
	if _, err := os.Stat(filepath.Join(sk.Dir, ".git")); err == nil {
		t.Error(".git must not be copied")
	}
	if _, err := os.Stat(filepath.Join(sk.Dir, "scripts", "run.sh")); err != nil {
		t.Error("the script must be copied")
	}
	if _, _, err := Install(src, dest, false); err == nil {
		t.Error("a second install of the same name needs --force")
	}
	if _, _, err := Install(src, dest, true); err != nil {
		t.Errorf("force: %v", err)
	}
	if _, err := Remove(dest, "renamed-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(dest, "../x"); err == nil {
		t.Error("a path is not a skill name")
	}
	if _, _, err := Install(t.TempDir(), dest, false); err == nil {
		t.Error("a folder without SKILL.md is not a skill")
	}
}

func TestBuiltinSkillIsThereWithoutAnyFiles(t *testing.T) {
	m := NewManager(t.TempDir(), filepath.Join(t.TempDir(), "skills.json"))
	m.Reload()
	s, ok := m.Get("relay-builder")
	if !ok || !s.Builtin || !s.Enabled {
		t.Fatalf("the builder skill must exist right after the build: %+v", s)
	}
	if !strings.Contains(m.Prompt(), "relay-builder") {
		t.Fatal("it must be announced to the model")
	}
	for _, trigger := range []string{"send this to my Telegram", "open YouTube", "calendar tomorrow", "fixed"} {
		if !strings.Contains(m.Prompt(), trigger) {
			t.Errorf("an indirect request like %q must be among the triggers the model sees in the list", trigger)
		}
	}
	body, err := m.Body("relay-builder")
	if err != nil || !strings.Contains(body, "create_mcp_server") {
		t.Fatalf("body: %v", err)
	}
	// every file that the instructions point to exists, and nothing escapes
	for _, f := range regexp.MustCompile(`references/[A-Za-z0-9._-]+`).FindAllString(body, -1) {
		if _, err := m.ReadFile("relay-builder", f); err != nil {
			t.Errorf("SKILL.md mentions %s: %v", f, err)
		}
	}
	for _, bad := range []string{"../x", "/etc/passwd", `..\x`, "", "references"} {
		if _, err := m.ReadFile("relay-builder", bad); err == nil {
			t.Errorf("%q must not be readable", bad)
		}
	}
	if files := m.Files("relay-builder", 50); len(files) < 5 {
		t.Errorf("files: %v", files)
	}
	// it can be switched off, and a skill of the same name in the folder replaces it
	if err := m.SetEnabled("relay-builder", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Prompt(), "relay-builder") {
		t.Error("a skill that is off is not announced")
	}
	root := t.TempDir()
	writeSkill(t, root, "relay-builder", "---\nname: relay-builder\ndescription: My own version of it.\n---\nMine\n")
	m2 := NewManager(root, filepath.Join(t.TempDir(), "s.json"))
	m2.Reload()
	if s, _ := m2.Get("relay-builder"); s.Builtin || s.Description != "My own version of it." {
		t.Fatalf("the user's skill must win: %+v", s)
	}
}

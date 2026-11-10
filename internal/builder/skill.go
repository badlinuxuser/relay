package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"relay/internal/provider"
	"relay/internal/skills"
)

type createSkill struct{ b *Builder }

type skillArgs struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Instructions string            `json:"instructions"`
	Files        map[string]string `json:"files"`
	Replace      bool              `json:"replace"`
}

func (t *createSkill) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: ToolCreateSkill,
		Description: "Create a skill: written instructions that Relay offers to the model for a kind of task. " +
			"Use it when the user wants Relay to follow a procedure, a style or house rules. " +
			"Read the skill relay-builder (load_skill) first for how to write a good one.",
		Schema: mkSchema(map[string]any{
			"name":         str("Lowercase letters, digits and single hyphens, up to 64: release-notes."),
			"description":  str("What the skill does AND when to use it (1-2 sentences). Only this line is shown in the list of skills, so name the situations."),
			"instructions": str("The body of SKILL.md in Markdown: concrete steps, formats, examples; short. Never put secrets here."),
			"files":        obj("Optional extra files: {\"references/style.md\": \"...\"}. They are read only when the instructions point to them."),
			"replace":      flag("true: replace the skill of this name."),
		}, "name", "description", "instructions"),
	}
}

func (t *createSkill) NeedsApproval() bool { return true }

// OnceOnly: the approval is asked every time, "always" is not remembered.
func (t *createSkill) OnceOnly() bool { return true }

func (t *createSkill) Describe(a json.RawMessage) string {
	var v skillArgs
	_ = json.Unmarshal(a, &v)
	return fmt.Sprintf("create the skill %q (%d lines of instructions, %d extra files): %s", v.Name,
		strings.Count(v.Instructions, "\n")+1, len(v.Files), oneLine(v.Description, 120))
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func (t *createSkill) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v skillArgs
	if err := decode(a, &v); err != nil {
		return "", err
	}
	b := t.b
	v.Name = strings.TrimSpace(v.Name)
	if !skills.ValidName(v.Name) {
		return "", fmt.Errorf("the name %q is not valid: use lowercase letters, digits and single hyphens (like release-notes)", v.Name)
	}
	if utf8.RuneCountInString(strings.Join(strings.Fields(v.Description), " ")) < 10 {
		return "", errors.New("the description is too short: say what the skill does and when to use it")
	}
	if strings.TrimSpace(v.Instructions) == "" {
		return "", errors.New("the instructions are empty")
	}
	if len(v.Files) > maxFiles || len(v.Instructions) > maxFileSize {
		return "", fmt.Errorf("too big: at most %d KB of instructions and %d extra files", maxFileSize/1024, maxFiles)
	}
	if s, ok := b.Skills.Get(v.Name); ok && s.Builtin {
		return "", fmt.Errorf("%q is a built-in skill; choose another name", v.Name)
	}
	skillsDir := b.Skills.Dir()
	dest := filepath.Join(skillsDir, v.Name)
	if _, err := os.Lstat(dest); err == nil && !v.Replace {
		return "", fmt.Errorf("a skill %q already exists; call again with replace=true to replace it", v.Name)
	}

	md := "---\nname: " + v.Name + "\ndescription: " + strconv.Quote(strings.Join(strings.Fields(v.Description), " ")) +
		"\n---\n\n" + strings.TrimSpace(v.Instructions) + "\n"
	files := map[string][]byte{skills.File: []byte(md)}
	for name, text := range v.Files {
		rel, err := cleanRel(name, skills.File)
		if err != nil {
			return "", err
		}
		if len(text) > maxFileSize {
			return "", fmt.Errorf("the file %q is larger than %d KB", name, maxFileSize/1024)
		}
		files[rel] = []byte(text)
	}

	tmp := filepath.Join(skillsDir, "."+v.Name+".installing") // a leading dot: the scanner skips it
	_ = os.RemoveAll(tmp)
	if err := writeTree(tmp, files); err != nil {
		return "", err
	}
	if _, _, err := skills.ParseAs(tmp, ""); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("the skill is not valid: %v", err)
	}
	if err := os.RemoveAll(dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	b.Skills.Reload()
	return fmt.Sprintf("Created the skill %q in %s with %d files. It is in the list of skills from the next step on; the user sees it with /skills.", v.Name, dest, len(files)), nil
}

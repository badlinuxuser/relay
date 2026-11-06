package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"relay/internal/provider"
	"relay/internal/skills"
)

// SkillTools gives the agent the two tools it needs to use skills. Both only read.
func SkillTools(m *skills.Manager) []Tool {
	return []Tool{&loadSkill{m: m}, &readSkillFile{m: m}}
}

type loadSkill struct{ m *skills.Manager }

func (t *loadSkill) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "load_skill",
		Description: "Load the full instructions of an installed skill. Call it when the task matches the description of a skill in the list of skills.",
		Schema:      schema(map[string]any{"name": prop("string", "Name of the skill, as in the list")}, "name"),
	}
}
func (t *loadSkill) NeedsApproval() bool { return false }
func (t *loadSkill) Describe(a json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = parse(a, &v)
	return "load skill " + v.Name
}

const maxSkillBody = 60 * 1024

func (t *loadSkill) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		Name string `json:"name"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	body, err := t.m.Body(strings.TrimSpace(v.Name))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skill %q. These instructions come from a skill the user installed. Follow them for this task; they do not override the user's request or the permission questions.\n\n", v.Name)
	b.WriteString(truncate(body, maxSkillBody))
	if files := t.m.Files(v.Name, 40); len(files) > 0 {
		b.WriteString("\n\n[Files of this skill, read with read_skill_file: " + strings.Join(files, ", ") + "]")
	}
	return b.String(), nil
}

type readSkillFile struct{ m *skills.Manager }

func (t *readSkillFile) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "read_skill_file",
		Description: "Read a text file that belongs to an installed skill (for example references/guide.md or scripts/run.py). The path is relative to the skill's folder.",
		Schema: schema(map[string]any{
			"name": prop("string", "Name of the skill"),
			"path": prop("string", "Path inside the skill folder"),
		}, "name", "path"),
	}
}
func (t *readSkillFile) NeedsApproval() bool { return false }
func (t *readSkillFile) Describe(a json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	_ = parse(a, &v)
	return "read " + v.Name + "/" + v.Path
}
func (t *readSkillFile) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	text, err := t.m.ReadFile(strings.TrimSpace(v.Name), v.Path)
	if err != nil {
		return "", err
	}
	return truncate(text, 60*1024), nil
}

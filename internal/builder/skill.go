package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"relay/internal/provider"
	"relay/internal/tools"
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
			"If the requested capability needs tools or an external service, first check installed capabilities and create/reuse the required MCP server as part of the same skill-building task; do not stop at a text-only skill. " +
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

// NeedsOffer: building starts with the offer (propose_extension) that the user agreed to.
func (t *createSkill) NeedsOffer() bool { return true }

// NeedsRead: the model must have read the guide.
func (t *createSkill) NeedsRead(args []byte) []string { return []string{"guide"} }

// CancelsRequest: when the user refuses, the whole request is cancelled.
func (t *createSkill) CancelsRequest() bool { return true }

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
	tools.Progress(ctx, "Writing the skill", 1, 2)
	dest, n, err := t.b.writeSkill(skillSpec{Name: v.Name, Description: v.Description,
		Instructions: v.Instructions, Files: v.Files, Replace: v.Replace})
	if err != nil {
		return "", err
	}
	tools.Progress(ctx, "Checking the skill", 2, 2)
	return fmt.Sprintf("Created the skill %q in %s with %d files. It is in the list of skills from the next step on; the user sees it with /skills.", strings.TrimSpace(v.Name), dest, n), nil
}

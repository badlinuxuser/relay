package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"relay/internal/provider"
	"relay/internal/tools"
)

// ToolPropose is the name of the tool with which the agent offers to build what it lacks.
const ToolPropose = "propose_extension"

type propose struct{ b *Builder }

// Propose returns the offer tool. It is not among Tools(): the user can switch the offers off.
func (b *Builder) Propose() tools.Tool { return &propose{b} }

type proposeArgs struct {
	What string `json:"what"`
	Kind string `json:"kind"`
}

func (t *propose) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: ToolPropose,
		Description: "Use this BEFORE creating or changing any capability that Relay does not already have. Ask about the user-visible result, not the implementation. " +
			"A single request may require a skill, one or more MCP tools/connectors, or both; decide the components yourself after checking what already exists. " +
			"Nothing is created before the user agrees here. If they agree, load relay-builder and implement the complete requested capability, reusing existing components; if they refuse, cancel the request.",
		Schema: mkSchema(map[string]any{
			"what": str("What is missing, in one short sentence, from the user's point of view (e.g. \"send messages to your Telegram\", \"read your Google Calendar\")."),
			"kind": map[string]any{"type": "string", "enum": []string{"tool", "connector", "skill", "fix"},
				"description": "tool: works on this computer; connector: works with an outside service; skill: a written procedure; fix: an existing tool that you built does not work and needs to be repaired."},
		}, "what", "kind"),
	}
}

func (t *propose) NeedsApproval() bool { return true }

// OnceOnly: the question is asked every time.
func (t *propose) OnceOnly() bool { return true }

// IsBuildOffer: once the user agreed, building is allowed in this request.
func (t *propose) IsBuildOffer() bool { return true }

// CancelsRequest: a refusal ends the whole request.
func (t *propose) CancelsRequest() bool { return true }

func (t *propose) DeniedMessage() string {
	return "The user refused, so the request is cancelled. Do not continue the task and do not look for another way."
}

func (t *propose) Describe(a json.RawMessage) string {
	var v proposeArgs
	_ = json.Unmarshal(a, &v)
	kind := strings.TrimSpace(v.Kind)
	if kind == "fix" {
		return fmt.Sprintf("A tool does not work: %s. Let the agent repair it now? (If you refuse, the request is cancelled.)", oneLine(v.What, 160))
	}
	if kind == "" {
		kind = "tool"
	}
	return fmt.Sprintf("Relay has no %s for this: %s. Build one now? (If you refuse, the request is cancelled.)", kind, oneLine(v.What, 160))
}

func (t *propose) Run(ctx context.Context, a json.RawMessage) (string, error) {
	return "The user agreed. Now: (1) load the skill relay-builder (load_skill) and read references/sdk.md (read_skill_file) before you write anything; " +
		"(2) call list_extensions to see what exists and what this computer has; (3) for a fix, read the code with read_server; " +
		"(4) for a local Python MCP, design the project and use validate_mcp_server to syntax-check, launch it temporarily through Relay, and verify tool discovery; fix and validate again until it passes (remote MCPs go directly to connection setup); " +
		"(5) only then call create_mcp_server to install and register it, and write the linked skill. Ask the user at most one or two short questions if something decides the design. " +
		"When it works, finish the task that the user originally asked for.", nil
}

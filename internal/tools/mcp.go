package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"

	"relay/internal/mcp"
	"relay/internal/provider"
)

var reBadName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// MCPCaller runs a tool of an MCP server (the mcp.Manager).
type MCPCaller interface {
	CallTool(ctx context.Context, server, tool string, args json.RawMessage) (string, error)
}

type mcpTool struct {
	caller MCPCaller
	ref    mcp.ToolRef
	name   string
}

// mcpToolName makes the name of a tool for the model: mcp__<server>__<tool>. Providers allow
// 64 characters of letters, digits, '_' and '-', so a long or odd name is cut and made
// unique with a short hash of the original.
func mcpToolName(server, tool string, used map[string]bool) string {
	full := "mcp__" + server + "__" + tool
	name := reBadName.ReplaceAllString(full, "_")
	if len(name) > 64 {
		sum := sha256.Sum256([]byte(full))
		name = name[:57] + "_" + hex.EncodeToString(sum[:])[:6]
	}
	base := name
	for n := 2; used[name]; n++ { // two different tools can end up with the same name
		suffix := "_" + strconv.Itoa(n)
		name = base[:min(len(base), 64-len(suffix))] + suffix
	}
	used[name] = true
	return name
}

// MCPTools turns the tools of the connected MCP servers into agent tools. Every call asks
// the user first: a server is not trusted just because the user added it.
func MCPTools(refs []mcp.ToolRef, c MCPCaller) []Tool {
	used := map[string]bool{}
	out := make([]Tool, 0, len(refs))
	for _, ref := range refs {
		out = append(out, &mcpTool{caller: c, ref: ref, name: mcpToolName(ref.Server, ref.Name, used)})
	}
	return out
}

func (t *mcpTool) Spec() provider.ToolSpec {
	sch := t.ref.Schema
	if len(sch) == 0 {
		sch = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return provider.ToolSpec{Name: t.name, Description: "[MCP server " + t.ref.Server + "] " + t.ref.Description, Schema: sch}
}
func (t *mcpTool) NeedsApproval() bool { return true }
func (t *mcpTool) Describe(a json.RawMessage) string {
	s := string(a)
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return t.ref.Server + "/" + t.ref.Name + " " + s
}
func (t *mcpTool) Run(ctx context.Context, a json.RawMessage) (string, error) {
	return t.caller.CallTool(ctx, t.ref.Server, t.ref.Name, a)
}

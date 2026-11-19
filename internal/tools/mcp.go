package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

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

// Every MCP invocation is an external capability boundary. Even sign-in changes account
// authorization state, so it is confirmed like any other MCP operation.
func (t *mcpTool) NeedsApproval() bool { return true }

// "Always allow in this session" is remembered per MCP tool (the name includes the server),
// so one approval covers repeated calls of the same tool, not the whole connector.
func (t *mcpTool) OnceOnly() bool { return false }

func (t *mcpTool) HideRawArgs() bool { return true }
func (t *mcpTool) Describe(a json.RawMessage) string {
	// Approval previews should show the user which connector operation is about to
	// happen and its meaningful arguments, while never echoing common credential fields.
	var value any
	if json.Unmarshal(a, &value) == nil {
		redactMCPArguments(value)
		if b, err := json.Marshal(value); err == nil {
			a = b
		}
	}
	s := string(a)
	if len(s) > 1000 {
		s = s[:1000] + "…"
	}
	return t.ref.Server + "/" + t.ref.Name + " " + s
}

func redactMCPArguments(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			key := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(k, "-", "_"), " ", "_"))
			if strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "password") ||
				strings.Contains(key, "authorization") || strings.Contains(key, "api_key") || strings.Contains(key, "apikey") ||
				strings.Contains(key, "cookie") || strings.Contains(key, "credential") || strings.Contains(key, "session") {
				x[k] = "[redacted]"
				continue
			}
			redactMCPArguments(value)
		}
	case []any:
		for _, value := range x {
			redactMCPArguments(value)
		}
	}
}
func (t *mcpTool) Run(ctx context.Context, a json.RawMessage) (string, error) {
	return t.caller.CallTool(ctx, t.ref.Server, t.ref.Name, a)
}

package tools

import (
	"context"
	"encoding/json"
	"regexp"

	"relay/internal/mcp"
	"relay/internal/provider"
)

var reBadName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

type mcpTool struct {
	c    *mcp.Client
	info mcp.ToolInfo
	name string
}

// MCPTools превращает инструменты MCP-сервера в инструменты агента.
// Имя получает вид mcp__<сервер>__<инструмент>; каждый вызов требует подтверждения.
func MCPTools(c *mcp.Client) []Tool {
	var out []Tool
	for _, info := range c.Tools() {
		name := reBadName.ReplaceAllString("mcp__"+c.Name+"__"+info.Name, "_")
		if len(name) > 64 {
			name = name[:64]
		}
		out = append(out, &mcpTool{c: c, info: info, name: name})
	}
	return out
}

func (t *mcpTool) Spec() provider.ToolSpec {
	sch := t.info.InputSchema
	if len(sch) == 0 {
		sch = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return provider.ToolSpec{Name: t.name, Description: t.info.Description, Schema: sch}
}
func (t *mcpTool) NeedsApproval() bool { return true }
func (t *mcpTool) Describe(a json.RawMessage) string {
	s := string(a)
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return t.c.Name + "/" + t.info.Name + " " + s
}
func (t *mcpTool) Run(ctx context.Context, a json.RawMessage) (string, error) {
	return t.c.Call(ctx, t.info.Name, a)
}

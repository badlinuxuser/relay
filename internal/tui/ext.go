package tui

import (
	"fmt"
	"strings"

	"relay/internal/proto"
)

// MCP servers and skills in the side panel and the /mcp and /skills commands.

type extRow struct{ dot, name, note string }

func mcpDot(state string) string {
	switch state {
	case "connected":
		return "●"
	case "connecting":
		return "○"
	case "auth_required":
		return "!"
	case "error":
		return "✗"
	}
	return "·"
}

// mcpNote says in a few words what is going on with a server.
func mcpNote(s proto.MCPServerInfo) string {
	switch {
	case s.Type == "stdio" && !s.Trusted:
		return "approve it"
	case s.Disabled:
		return "off"
	case s.State == "connected":
		return fmt.Sprintf("%d tools", s.ToolCount)
	case s.State == "auth_required":
		return "sign in"
	case s.State == "error":
		return "error"
	case s.State == "connecting":
		return "connecting"
	}
	return s.State
}

// extRows lists what the side panel shows: the MCP servers and the skills.
func (m model) extRows() []extRow {
	if m.status == nil {
		return nil
	}
	const maxRows = 6
	var rows []extRow
	for _, s := range m.status.MCPServers {
		if len(rows) == maxRows-1 && len(m.status.MCPServers) > maxRows {
			rows = append(rows, extRow{"·", fmt.Sprintf("+%d more", len(m.status.MCPServers)-len(rows)), "/mcp"})
			break
		}
		rows = append(rows, extRow{mcpDot(s.State), s.Name, mcpNote(s)})
	}
	on := 0
	for _, s := range m.status.Skills {
		if s.Enabled {
			on++
		}
	}
	if on > 0 {
		rows = append(rows, extRow{"✦", "skills", fmt.Sprintf("%d on", on)})
	}
	return rows
}

// extHeight is the number of lines the block takes in the side panel (0: nothing to show).
func (m model) extHeight() int {
	if n := len(m.extRows()); n > 0 {
		return n + 2
	}
	return 0
}

// mcpLines is the answer to /mcp: one line per server.
func mcpLines(st *proto.StatusResp) []string {
	if st == nil || len(st.MCPServers) == 0 {
		return []string{"No MCP servers yet. Add one in a terminal: relay mcp add <id> --url URL  (or --command CMD), or edit mcp.json. More: relay mcp help"}
	}
	out := []string{"MCP servers:"}
	for _, s := range st.MCPServers {
		line := fmt.Sprintf("  %s %s · %s · %s", mcpDot(s.State), s.Name, s.Type, mcpNote(s))
		if s.State == "error" && s.Error != "" {
			line += ": " + shorten(s.Error, 100)
		}
		out = append(out, line)
	}
	if st.MCPError != "" {
		out = append(out, "mcp.json has a problem and is being ignored: "+shorten(st.MCPError, 160))
	}
	return out
}

// skillsLines is the answer to /skills.
func skillsLines(st *proto.StatusResp) []string {
	if st == nil || len(st.Skills) == 0 {
		return []string{"No skills yet. Add one in a terminal: relay skill add <folder>, or copy a skill folder into the skills folder. More: relay skill help"}
	}
	out := []string{"Skills (the agent loads one when a task matches it):"}
	for _, s := range st.Skills {
		state := ""
		if !s.Enabled {
			state = " (off)"
		}
		out = append(out, fmt.Sprintf("  ✦ %s%s · %s", s.Name, state, shorten(s.Description, 90)))
	}
	return out
}

// mcpHints are the notices shown when Relay connects: things that need the user.
func mcpHints(st *proto.StatusResp) []string {
	if st == nil {
		return nil
	}
	var out []string
	for _, s := range st.MCPServers {
		if s.Type == "stdio" && !s.Trusted && !s.Disabled {
			out = append(out, fmt.Sprintf("MCP server %q waits for your approval to run: %s. Approve it in a terminal: relay mcp trust %s", s.ID, shorten(s.Target, 80), s.ID))
		}
	}
	if st.MCPError != "" {
		out = append(out, "mcp.json has a problem and is being ignored: "+strings.TrimSpace(shorten(st.MCPError, 200)))
	}
	return out
}

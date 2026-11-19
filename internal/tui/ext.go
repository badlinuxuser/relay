package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/browser"
	"relay/internal/client"
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
	case "auth_required", "needs_setup":
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
	case s.State == "needs_setup":
		return "OAuth setup"
	case s.State == "auth_required" || (s.OAuth && !s.SignedIn):
		return "sign in"
	case s.State == "connected":
		return fmt.Sprintf("%d tools", s.ToolCount)
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
		if s.Builtin {
			state += " (built in)"
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
	for _, s := range st.MCPServers {
		if s.State == "needs_setup" {
			out = append(out, fmt.Sprintf("The connector %q needs OAuth client setup. Open /connections to enter its Client ID and optional secret.", s.Name))
		} else if s.State == "auth_required" || (s.OAuth && !s.SignedIn) {
			out = append(out, fmt.Sprintf("The connector %q needs you to sign in: /connectors auth %s (the agent also asks by itself when it needs it)", s.Name, s.ID))
		}
	}
	if st.MCPError != "" {
		out = append(out, "mcp.json has a problem and is being ignored: "+strings.TrimSpace(shorten(st.MCPError, 200)))
	}
	return out
}

// ---- /tools and /connectors ----------------------------------------------------

// toolNames lists tool names, a few of them.
func toolNames(tools []proto.MCPToolInfo) string {
	var names []string
	for _, t := range tools {
		if !t.Disabled {
			names = append(names, t.Name)
		}
	}
	if len(names) > 8 {
		return strings.Join(names[:8], ", ") + fmt.Sprintf(", +%d more", len(names)-8)
	}
	return strings.Join(names, ", ")
}

// serverLine is one line about a server for /tools and /connectors.
func serverLine(s proto.MCPServerInfo) []string {
	line := fmt.Sprintf("  %s %s · %s · %s", mcpDot(s.State), s.Name, s.State, mcpNote(s))
	if s.State == "needs_setup" {
		line += " · configure in /connections"
	} else if s.OAuth || s.State == "auth_required" {
		if s.SignedIn {
			line += " · signed in"
		} else {
			line += " · not signed in: /connectors auth " + s.ID
		}
	}
	if s.State == "error" && s.Error != "" {
		line += ": " + shorten(s.Error, 100)
	}
	out := []string{line}
	if names := toolNames(s.Tools); names != "" {
		out = append(out, "      "+names)
	}
	return out
}

// toolsLines is the answer to /tools: what the agent can do on this computer.
func toolsLines(r *proto.ToolsResp) []string {
	out := []string{"Tools of the agent (turn the agent on with ctrl+a; ✋ means it asks you first):"}
	for _, t := range r.Builtin {
		mark := "  "
		if t.Approval {
			mark = "✋"
		}
		out = append(out, fmt.Sprintf("  %s %s · %s", mark, t.Name, t.Description))
	}
	if len(r.Local) == 0 {
		out = append(out, "No local MCP servers yet. Ask the agent to build one (for example: \"make a tool that ...\"), it has the skill for it built in.")
		return out
	}
	out = append(out, "Local MCP servers (every call asks you first):")
	for _, s := range r.Local {
		out = append(out, serverLine(s)...)
	}
	return out
}

// connectorLines is the answer to /connectors: what works with outside services.
func connectorLines(r *proto.ToolsResp) []string {
	if len(r.Connectors) == 0 {
		return []string{"No connectors yet. Turn the agent on (ctrl+a) and ask for one, for example: \"connect my GitHub\". " +
			"Relay writes the connector itself and signs you in. Or add one by hand: relay mcp add <id> --url URL"}
	}
	out := []string{"Connectors (they work with outside services; every call asks you first):"}
	for _, s := range r.Connectors {
		out = append(out, serverLine(s)...)
	}
	if r.Callback != "" {
		out = append(out, "OAuth callback address for an OAuth app at a service: "+r.Callback)
	}
	return out
}

type extMsg struct {
	tools *proto.ToolsResp
	which string // tools | connectors
}

type authURLMsg struct{ id, url string }
type authConsentMsg struct{ id, description string }

func extCmd(cl *client.Client, which string) tea.Cmd {
	return func() tea.Msg {
		r, err := cl.Tools()
		if err != nil {
			return errMsg{err}
		}
		return extMsg{tools: r, which: which}
	}
}

func authCmd(cl *client.Client, id string) tea.Cmd {
	return func() tea.Msg {
		description, err := cl.MCPAuthorizationDescription(id)
		if err != nil {
			return errMsg{err}
		}
		return authConsentMsg{id: id, description: description}
	}
}

func authStartCmd(cl *client.Client, id string) tea.Cmd {
	return func() tea.Msg {
		url, err := cl.MCPAuthorize(id)
		if err != nil {
			return errMsg{err}
		}
		return authURLMsg{id: id, url: url}
	}
}

// openSignIn opens the sign-in page; when that does not work, it shows the address.
func (m *model) openSignIn(url string) {
	if err := browser.Open(url); err != nil {
		m.addNotice("Could not open the browser. Open this address yourself: " + url)
	}
	m.mc.react("noticed", 8*time.Second, "Sign in in the browser")
}

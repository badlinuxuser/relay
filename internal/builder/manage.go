package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"relay/internal/mcp"
	"relay/internal/provider"
	"relay/internal/skills"
)

// ---- remove_extension ---------------------------------------------------------

type removeExt struct{ b *Builder }

type removeArgs struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (t *removeExt) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        ToolRemove,
		Description: "Remove an MCP server (with the code and the sign-in that belong to it) or a skill. Built-in skills cannot be removed.",
		Schema: mkSchema(map[string]any{
			"kind": map[string]any{"type": "string", "enum": []string{"mcp_server", "skill"}, "description": "What to remove."},
			"name": str("The id of the server, or the name of the skill."),
		}, "kind", "name"),
	}
}

func (t *removeExt) NeedsApproval() bool { return true }

// OnceOnly: the approval is asked every time, "always" is not remembered.
func (t *removeExt) OnceOnly() bool { return true }

func (t *removeExt) Describe(a json.RawMessage) string {
	var v removeArgs
	_ = json.Unmarshal(a, &v)
	return fmt.Sprintf("remove the %s %q", strings.ReplaceAll(v.Kind, "_", " "), v.Name)
}

func (t *removeExt) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v removeArgs
	if err := decode(a, &v); err != nil {
		return "", err
	}
	b := t.b
	switch v.Kind {
	case "mcp_server":
		if !mcp.ValidID(v.Name) {
			return "", fmt.Errorf("%q is not a server id", v.Name)
		}
		file, err := mcp.LoadFile(b.MCP.Path())
		if err != nil {
			return "", fmt.Errorf("mcp.json has a problem, so nothing was changed: %v", err)
		}
		cfg, ok := file.Servers[v.Name]
		if !ok {
			return "", fmt.Errorf("there is no server %q in mcp.json (call list_extensions)", v.Name)
		}
		delete(file.Servers, v.Name)
		if err := mcp.Save(b.MCP.Path(), file); err != nil {
			return "", err
		}
		b.MCP.Forget(v.Name) // the token, the registration and the approval
		note := ""
		dir := filepath.Join(b.Home, "connectors", v.Name)
		if len(cfg.Args) > 0 && isInside(dir, cfg.Args[0]) {
			if err := os.RemoveAll(dir); err == nil {
				note = " Its code in " + dir + " was deleted."
			}
		}
		b.MCP.Reload()
		return fmt.Sprintf("Removed the server %q.%s", v.Name, note), nil
	case "skill":
		s, ok := b.Skills.Get(v.Name)
		if !ok {
			return "", fmt.Errorf("there is no skill %q (call list_extensions)", v.Name)
		}
		if s.Builtin {
			return "", fmt.Errorf("%q is built into Relay and cannot be removed; the user can switch it off with `relay skill disable %s`", v.Name, v.Name)
		}
		if _, err := skills.Remove(b.Skills.Dir(), v.Name); err != nil {
			return "", err
		}
		b.Skills.Reload()
		return fmt.Sprintf("Removed the skill %q.", v.Name), nil
	}
	return "", fmt.Errorf("kind must be mcp_server or skill, got %q", v.Kind)
}

// ---- list_extensions ----------------------------------------------------------

type listExt struct{ b *Builder }

func (t *listExt) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: ToolList,
		Description: "Show what extends Relay now: the MCP servers (kind, state, tools, whether a sign-in is needed), the skills, the folders where they live, " +
			"and the callback address that an OAuth app must list. Call it before building something.",
		Schema: mkSchema(map[string]any{}),
	}
}

func (t *listExt) NeedsApproval() bool                  { return false }
func (t *listExt) Describe(a json.RawMessage) string    { return "list the MCP servers and skills" }
func (t *listExt) Run(ctx context.Context, a json.RawMessage) (string, error) {
	b := t.b
	var out strings.Builder
	fmt.Fprintf(&out, "Folders:\n  home: %s\n  servers: %s\n  code of servers made by create_mcp_server: %s\n  skills: %s\n",
		b.Home, b.MCP.Path(), filepath.Join(b.Home, "connectors"), b.Skills.Dir())
	if cb := b.callback(); cb != "" {
		fmt.Fprintf(&out, "OAuth callback address (an OAuth app at a service must list exactly this): %s\n", cb)
	}
	out.WriteString("\nMCP servers:\n")
	servers := b.MCP.Status()
	if len(servers) == 0 {
		out.WriteString("  (none)\n")
	}
	for _, s := range servers {
		var tl []string
		for _, x := range s.Tools {
			if !x.Disabled {
				tl = append(tl, x.Name)
			}
		}
		sort.Strings(tl)
		fmt.Fprintf(&out, "  %s: %s, %s, %s", s.ID, s.Kind, s.Type, s.State)
		if s.Error != "" {
			fmt.Fprintf(&out, " (%s)", oneLine(s.Error, 160))
		}
		if len(tl) > 0 {
			fmt.Fprintf(&out, "; tools: %s", strings.Join(tl, ", "))
		}
		if b.MCP.NeedsSignIn(s.ID) {
			out.WriteString("; the user has not signed in")
		} else if s.OAuth {
			out.WriteString("; signed in")
		}
		out.WriteString("\n")
	}
	if e := b.MCP.LoadError(); e != "" {
		fmt.Fprintf(&out, "  mcp.json has a problem and is ignored: %s\n", oneLine(e, 200))
	}
	out.WriteString("\nSkills:\n")
	for _, s := range b.Skills.List() {
		state := "on"
		if !s.Enabled {
			state = "off"
		}
		origin := ""
		if s.Builtin {
			origin = ", built in"
		}
		fmt.Fprintf(&out, "  %s (%s%s): %s\n", s.Name, state, origin, oneLine(s.Description, 140))
	}
	return out.String(), nil
}

// ---- authorize_connector ------------------------------------------------------

type authConnector struct{ b *Builder }

func (t *authConnector) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: ToolAuthorize,
		Description: "Sign the user in to a connector. Relay pauses, opens the sign-in page in the browser, waits until the user is done, and then this call returns. " +
			"Use it after creating a connector with OAuth, or when a connector says that sign-in is needed.",
		Schema: mkSchema(map[string]any{"id": str("The id of the connector (see list_extensions).")}, "id"),
	}
}

func (t *authConnector) NeedsApproval() bool { return false }
func (t *authConnector) Describe(a json.RawMessage) string {
	var v struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(a, &v)
	return "sign in to " + v.ID
}

func (t *authConnector) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		ID string `json:"id"`
	}
	if err := decode(a, &v); err != nil {
		return "", err
	}
	var ids []string
	for _, s := range t.b.MCP.Status() {
		if s.ID == v.ID {
			if t.b.MCP.NeedsSignIn(v.ID) {
				return "", &mcp.AuthRequiredError{Server: v.ID} // the agent opens the browser and calls this tool again
			}
			if s.OAuth || s.Type == mcp.TypeHTTP {
				return fmt.Sprintf("The user is signed in to %q.", v.ID), nil
			}
			return fmt.Sprintf("%q has no sign-in settings (no \"oauth\" block): it does not use OAuth.", v.ID), nil
		}
		ids = append(ids, s.ID)
	}
	return "", fmt.Errorf("there is no server %q; known: %s", v.ID, strings.Join(ids, ", "))
}

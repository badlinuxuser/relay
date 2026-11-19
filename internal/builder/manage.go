package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

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
		for _, sk := range b.skillsOfServer(v.Name) { // the skill that was written with the server
			if _, err := skills.Remove(b.Skills.Dir(), sk.Name); err == nil {
				note += fmt.Sprintf(" Its skill %q was removed too.", sk.Name)
			}
		}
		b.Skills.Reload()
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

func (t *listExt) NeedsApproval() bool               { return false }
func (t *listExt) Describe(a json.RawMessage) string { return "list the MCP servers and skills" }
func (t *listExt) Run(ctx context.Context, a json.RawMessage) (string, error) {
	b := t.b
	var out strings.Builder
	out.WriteString(environmentInfo())
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
		for _, sk := range b.skillsOfServer(s.ID) {
			fmt.Fprintf(&out, "; skill: %s", sk.Name)
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
				return "", &mcp.AuthRequiredError{Server: v.ID, SafeToRetry: true} // the agent opens the browser and calls this tool again
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

// environmentInfo says where Relay runs, so that the model builds for this computer and does not
// think it is in a sandbox.
func environmentInfo() string {
	desk := "none found (a remote terminal?)"
	switch {
	case runtime.GOOS == "windows" || runtime.GOOS == "darwin":
		desk = "yes"
	case os.Getenv("WAYLAND_DISPLAY") != "":
		desk = "yes (Wayland)"
	case os.Getenv("DISPLAY") != "":
		desk = "yes (X11)"
	}
	py := "not found"
	if name, err := pythonCommand(); err == nil {
		py = name
	}
	return fmt.Sprintf("This computer (the one Relay runs on, and where your servers run, with the user's rights): %s/%s; graphical desktop: %s; Python: %s\n\n",
		runtime.GOOS, runtime.GOARCH, desk, py)
}

// listServerFiles skips generated environments and caches before applying the file limit, so
// a private .venv cannot crowd the actual project source out of read_server's result.
func listServerFiles(dir string, maxFiles int) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".venv", ".git", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "server.py" || rel == "relay_mcp.py" {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// ---- read_server ---------------------------------------------------------------

type readServer struct{ b *Builder }

func (t *readServer) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: ToolReadServer,
		Description: "Read the code of an MCP server that was built with create_mcp_server (server.py and its other files). " +
			"Use it before you repair a server that does not work: you must see the code to fix it, then call create_mcp_server again with replace=true.",
		Schema: mkSchema(map[string]any{"id": str("The id of the server (see list_extensions).")}, "id"),
	}
}

func (t *readServer) NeedsApproval() bool { return false }
func (t *readServer) Describe(a json.RawMessage) string {
	var v struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(a, &v)
	return "read the code of " + v.ID
}

const maxReadServer = 80 * 1024

func (t *readServer) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		ID string `json:"id"`
	}
	if err := decode(a, &v); err != nil {
		return "", err
	}
	if !mcp.ValidID(v.ID) {
		return "", fmt.Errorf("%q is not a server id", v.ID)
	}
	dir := filepath.Join(t.b.Home, "connectors", v.ID)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("there is no code for %q in %s: it was not built here (call list_extensions)", v.ID, filepath.Join(t.b.Home, "connectors"))
	}
	files := listServerFiles(dir, 60)
	order := append([]string{"server.py"}, files...)
	if len(order) == 0 {
		return "", fmt.Errorf("the folder of %q has no files", v.ID)
	}
	var out strings.Builder
	left := maxReadServer
	for _, f := range order {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil || !utf8.Valid(data) {
			fmt.Fprintf(&out, "### %s\n(not a text file)\n\n", f)
			continue
		}
		text := string(data)
		if len(text) > left {
			text = text[:max(left, 0)] + "\n[... cut]"
		}
		left -= len(text)
		fmt.Fprintf(&out, "### %s\n%s\n\n", f, text)
	}
	return strings.TrimSpace(out.String()), nil
}

package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"relay/internal/mcp"
	"relay/internal/provider"
)

type createServer struct{ b *Builder }

type serverArgs struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Code    string            `json:"code"`
	Files   map[string]string `json:"files"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Env     map[string]string `json:"env"`
	OAuth   *mcp.OAuthConfig  `json:"oauth"`
	Replace bool              `json:"replace"`
}

func (t *createServer) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: ToolCreateServer,
		Description: "Create an MCP server and plug it into Relay: its tools become available to you in the next step. " +
			"Either write a Python program (code) that uses the relay_mcp helper, or plug in a remote MCP server by address (url). " +
			"The tool saves the files, starts the program, and answers with its tools or with the error; if it failed, fix the code and call again with replace=true. " +
			"Read the skill relay-builder (load_skill) before the first use: it has the helper's API and two complete examples.",
		Schema: mkSchema(map[string]any{
			"id":   str("Short identifier: letters, digits, '-' or '_' (up to 32). Becomes part of the tool names: mcp__<id>__<tool>."),
			"name": str("Name shown to the user, e.g. \"GitHub\"."),
			"kind": map[string]any{"type": "string", "enum": []string{"tool", "connector"},
				"description": "tool: works on this computer. connector: works with an outside service. Default: connector when there is an oauth block or a url, else tool."},
			"code":    str("The complete source of server.py. Use `from relay_mcp import Server, ToolError, http, secret`, define tools with @srv.tool(...), and end with srv.run()."),
			"files":   obj("Optional extra files next to server.py: {\"helpers.py\": \"...\"}. Relative paths only."),
			"url":     str("Instead of code: the address of a remote MCP server (https://...). Relay signs the user in by itself if the server needs it."),
			"headers": obj("With url: extra HTTP headers, e.g. {\"X-Api-Key\": \"${SERVICE_KEY}\"}. Never write a secret itself: use ${ENV_NAME}."),
			"env": obj("Environment variables for the program: {\"NAME\": \"${NAME}\"} takes the value from the user's own environment. " +
				"Never write a secret itself."),
			"oauth": obj("Sign-in settings for a connector: {authorize_url, token_url, client_id, client_secret (write \"${ENV_NAME}\"), scopes: [...], params: {...}}. " +
				"The program then uses http(..., oauth=True)."),
			"replace": flag("true: replace the server with this id (use it to fix a server that you created)."),
		}, "id"),
	}
}

func (t *createServer) NeedsApproval() bool { return true }

// OnceOnly: the approval is asked every time, "always" is not remembered.
func (t *createServer) OnceOnly() bool { return true }

func (t *createServer) Describe(a json.RawMessage) string {
	var v serverArgs
	_ = json.Unmarshal(a, &v)
	if v.URL != "" {
		return fmt.Sprintf("connect the remote MCP server %q at %s", v.ID, v.URL)
	}
	kind := v.Kind
	if kind == "" {
		kind = "tool"
		if v.OAuth != nil {
			kind = "connector"
		}
	}
	extra := ""
	if len(v.Env) > 0 {
		extra += "; env: " + quoteList(v.Env)
	}
	if v.OAuth != nil {
		extra += "; OAuth sign-in"
	}
	return fmt.Sprintf("create the MCP %s %q: a Python program of %d lines that will run on this computer with your rights in %s (%s)%s",
		kind, v.ID, strings.Count(v.Code, "\n")+1, filepath.Join(t.b.Home, "connectors", v.ID), summarize(v.Code), extra)
}

func (t *createServer) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v serverArgs
	if err := decode(a, &v); err != nil {
		return "", err
	}
	b := t.b
	v.ID = strings.TrimSpace(v.ID)
	if !mcp.ValidID(v.ID) {
		return "", fmt.Errorf("the id %q is not valid: use 1-32 letters, digits, '-' or '_', starting with a letter or digit", v.ID)
	}
	if (strings.TrimSpace(v.Code) == "") == (strings.TrimSpace(v.URL) == "") {
		return "", errors.New("give exactly one of: code (a program written with relay_mcp) or url (a remote MCP server)")
	}
	file, err := mcp.LoadFile(b.MCP.Path())
	if err != nil {
		return "", fmt.Errorf("mcp.json has a problem, so nothing was changed: %v", err)
	}
	if _, exists := file.Servers[v.ID]; exists && !v.Replace {
		return "", fmt.Errorf("a server %q already exists. To change it, call again with replace=true; to look at it, call list_extensions", v.ID)
	}

	cfg := mcp.ServerConfig{Name: v.Name, Kind: v.Kind, Env: v.Env, OAuth: v.OAuth}
	remote := strings.TrimSpace(v.URL) != ""
	var dir string
	var files map[string][]byte
	if remote {
		cfg.URL, cfg.Headers = strings.TrimSpace(v.URL), v.Headers
	} else {
		py, err := pythonCommand()
		if err != nil {
			return "", err
		}
		if !strings.Contains(v.Code, "relay_mcp") {
			return "", errors.New("write the server with the helper: `from relay_mcp import Server, ToolError, http, secret` (see references/sdk.md of the skill relay-builder)")
		}
		if !strings.Contains(v.Code, ".run(") {
			return "", errors.New("the program must end with srv.run(), otherwise it exits at once")
		}
		if len(v.Code) > maxFileSize || len(v.Files) > maxFiles {
			return "", fmt.Errorf("too big: at most %d KB of code and %d extra files", maxFileSize/1024, maxFiles)
		}
		files = map[string][]byte{"server.py": []byte(v.Code), "relay_mcp.py": sdkSource}
		for name, text := range v.Files {
			rel, err := cleanRel(name, "server.py", "relay_mcp.py")
			if err != nil {
				return "", err
			}
			if len(text) > maxFileSize {
				return "", fmt.Errorf("the file %q is larger than %d KB", name, maxFileSize/1024)
			}
			files[rel] = []byte(text)
		}
		dir = filepath.Join(b.Home, "connectors", v.ID)
		cfg.Command, cfg.Args = py, []string{filepath.Join(dir, "server.py")}
	}
	cfg = mcp.Normalize(cfg)
	if err := mcp.Validate(v.ID, cfg); err != nil {
		return "", err
	}

	if !remote {
		_ = os.RemoveAll(dir) // leftovers of an earlier try with this id
		if err := writeTree(dir, files); err != nil {
			return "", err
		}
		if err := checkPython(ctx, cfg.Command, filepath.Join(dir, "server.py")); err != nil {
			return "", err
		}
	}

	file.Servers[v.ID] = cfg
	if err := mcp.Save(b.MCP.Path(), file); err != nil {
		return "", err
	}
	b.MCP.Reload()
	_ = b.MCP.Disconnect(v.ID) // a replaced server must start again with the new code
	if remote {
		err = b.MCP.Connect(v.ID)
	} else {
		err = b.MCP.Trust(v.ID) // the user approved this tool call: that is the approval of the program
	}
	return t.report(v, cfg, dir, err)
}

func (t *createServer) report(v serverArgs, cfg mcp.ServerConfig, dir string, startErr error) (string, error) {
	var auth *mcp.AuthError
	signIn := ""
	if t.b.MCP.NeedsSignIn(v.ID) || errors.As(startErr, &auth) {
		signIn = fmt.Sprintf("\nThe user has to sign in before the tools of %q work: call authorize_connector with id %q (it opens the browser and waits), or just call one of its tools, and Relay does the same.", v.ID, v.ID)
	}
	where := ""
	if dir != "" {
		where = " in " + dir
	}
	if startErr != nil && !errors.As(startErr, &auth) {
		return fmt.Sprintf("Saved the server %q%s, but it did not start.%s", v.ID, where, signIn),
			fmt.Errorf("%v\nFix server.py and call create_mcp_server again with replace=true", startErr)
	}
	var names []string
	for _, st := range t.b.MCP.Status() {
		if st.ID == v.ID {
			for _, tl := range st.Tools {
				names = append(names, tl.Name)
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Created the %s %q%s.", cfg.Kind, v.ID, where)
	if len(names) > 0 {
		fmt.Fprintf(&out, "\nIt started with %d tools: %s.\nThey are available to you from the next step as mcp__%s__<tool>. Test one that only reads, with real input, and check the result.", len(names), strings.Join(names, ", "), v.ID)
	}
	out.WriteString(signIn)
	if cfg.Kind == mcp.KindConnector && cfg.OAuth != nil {
		if cb := t.b.callback(); cb != "" {
			fmt.Fprintf(&out, "\nThe OAuth app of the service must list this callback address: %s", cb)
		}
	}
	return out.String(), nil
}

func (b *Builder) callback() string {
	if b.Callback == nil {
		return ""
	}
	return b.Callback()
}

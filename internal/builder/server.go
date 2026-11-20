package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"relay/internal/mcp"
	"relay/internal/provider"
	"relay/internal/tools"
)

type createServer struct{ b *Builder }

// The skill that must come with every server has the same shape as the arguments of create_skill (skillArgs).

type serverArgs struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	Code         string            `json:"code"`
	Files        map[string]string `json:"files"`
	Dependencies []string          `json:"dependencies"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	Env          map[string]string `json:"env"`
	OAuth        *mcp.OAuthConfig  `json:"oauth"`
	Skill        *skillArgs        `json:"skill"`
	Replace      bool              `json:"replace"`
}

func (t *createServer) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: ToolCreateServer,
		Description: "Create an MCP server and plug it into Relay: its tools become available to you in the next step. " +
			"Either write a Python project (code plus optional files and PyPI dependencies) that uses the relay_mcp helper, or plug in a remote MCP server by address (url). " +
			"A skill is required: it tells the model that uses these tools later when and how to use them. " +
			"For local projects, first use validate_mcp_server to test the project in a temporary environment; then this tool installs it, starts it, writes the skill, and reports the discovered tools. If it fails, fix the code and call again with replace=true. " +
			"Read the skill relay-builder (load_skill) before the first use: it has the helper's API and two complete examples.",
		Schema: mkSchema(map[string]any{
			"id":   str("Short identifier: letters, digits, '-' or '_' (up to 32). Becomes part of the tool names: mcp__<id>__<tool>."),
			"name": str("Name shown to the user, e.g. \"GitHub\"."),
			"kind": map[string]any{"type": "string", "enum": []string{"tool", "connector"},
				"description": "tool: works on this computer. connector: works with an outside service. Default: connector when there is an oauth block or a url, else tool."},
			"code":         str("The complete source of server.py. Use `from relay_mcp import Server, ToolError, http, secret`, define tools with @srv.tool(...), and end with srv.run()."),
			"files":        obj("Optional extra project files next to server.py: {\"helpers.py\": \"...\"}. Relative paths only."),
			"dependencies": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional PyPI packages with optional version pins, e.g. [\"requests>=2.31,<3\", \"beautifulsoup4==4.12.3\"]. Installed into a private .venv for this MCP. Package names and version constraints only; no URLs or pip flags."},
			"url":          str("Instead of code: the address of a remote MCP server (https://...). Relay signs the user in by itself if the server needs it."),
			"headers":      obj("With url: extra HTTP headers, e.g. {\"X-Api-Key\": \"${SERVICE_KEY}\"}. Never write a secret itself: use ${ENV_NAME}."),
			"env": obj("Environment variables for the program: {\"NAME\": \"${NAME}\"} takes the value from the user's own environment. " +
				"Never write a secret itself."),
			"oauth": obj("Sign-in settings for a connector: {authorize_url, token_url, client_id, client_secret (write \"${ENV_NAME}\"), registration_url (optional RFC 7591), resource (optional RFC 8707), scopes: [...], params: {...}}. " +
				"Use registration_url only when the provider supports dynamic client registration; otherwise ask the user to register an OAuth app and provide client_id. The program then uses http(..., oauth=True)."),
			"skill": map[string]any{"type": "object", "description": "REQUIRED. The skill that goes with these tools. " +
				"{description: what it is for AND when to use it (1-2 sentences, this is the line the model sees in the list of skills), " +
				"instructions: Markdown for the model that will use the tools: which tool for which job, how to fill the arguments, limits and pitfalls, what the user must set up (a variable, an OAuth app, a sign-in); no secrets; " +
				"name: optional, default is the id in lower case, files: optional extra files}. The list of the tools is added to it automatically.",
				"properties": map[string]any{
					"name": str("Optional. Lowercase letters, digits and single hyphens."), "description": str("What it is for and when to use it."),
					"instructions": str("Markdown instructions for using the tools."), "files": obj("Optional extra files, e.g. references/api.md."),
				}, "required": []string{"description", "instructions"}},
			"replace": flag("true: replace the server with this id (use it to fix a server that you created)."),
		}, "id", "skill"),
	}
}

func (t *createServer) NeedsApproval() bool { return true }
func (t *createServer) OnceOnly() bool      { return false }

// NeedsRead: before it builds, the model must have read the guide, and for a program also the SDK.
func (t *createServer) NeedsRead(args []byte) []string {
	var v struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(args, &v)
	if strings.TrimSpace(v.URL) != "" {
		return []string{"guide"} // a remote server has no code
	}
	return []string{"guide", "sdk"}
}

// NeedsOffer: building starts with the offer (propose_extension) that the user agreed to.
func (t *createServer) NeedsOffer() bool { return true }

// CancelsRequest: when the user refuses to run the new program, the whole request is cancelled.
func (t *createServer) CancelsRequest() bool { return true }

func (t *createServer) Describe(a json.RawMessage) string {
	var v serverArgs
	_ = json.Unmarshal(a, &v)
	if v.URL != "" {
		label := v.Name
		if label == "" {
			label = v.ID
		}
		return fmt.Sprintf("connect the service %q so Relay can use its capabilities, and add the matching instructions", label)
	}
	kind := v.Kind
	if kind == "" {
		kind = "tool"
		if v.OAuth != nil {
			kind = "connector"
		}
	}
	extra := ""
	if len(v.Dependencies) > 0 {
		extra += "; installs private Python dependencies: " + strings.Join(v.Dependencies, ", ")
	}
	if len(v.Env) > 0 {
		extra += "; env: " + quoteList(v.Env)
	}
	if v.OAuth != nil {
		extra += "; OAuth sign-in"
	}
	label := v.Name
	if label == "" {
		label = v.ID
	}
	return fmt.Sprintf("add the %s capability %q and its matching instructions; its helper program will run on this computer with your rights (%s)%s",
		kind, label, summarize(v.Code), extra)
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
	remote := strings.TrimSpace(v.URL) != ""
	if remote && (len(v.Dependencies) > 0 || len(v.Files) > 0) {
		return "", errors.New("files and dependencies are only supported for local Python MCP projects")
	}

	// the skill is part of the job: it is checked before anything is written
	if v.Skill == nil {
		return "", errors.New("the argument \"skill\" is required: write the skill that tells the model when and how to use these tools " +
			"(description, instructions), and call again")
	}
	sk := skillSpec{Name: v.Skill.Name, Description: v.Skill.Description, Instructions: v.Skill.Instructions,
		Files: v.Skill.Files, Replace: v.Replace || v.Skill.Replace, Server: v.ID}
	if strings.TrimSpace(sk.Name) == "" {
		sk.Name = deriveSkillName(v.ID)
	}
	if len(strings.TrimSpace(sk.Instructions)) < 40 {
		return "", errors.New("the skill instructions are too short to be useful: say which tool is for which job, how to fill the arguments, and what to watch for")
	}
	if err := b.checkSkill(&sk); err != nil {
		return "", err
	}

	file, err := mcp.LoadFile(b.MCP.Path())
	if err != nil {
		return "", fmt.Errorf("mcp.json has a problem, so nothing was changed: %v", err)
	}
	if _, exists := file.Servers[v.ID]; exists && !v.Replace {
		return "", fmt.Errorf("a server %q already exists. To change it, call again with replace=true; to look at it, call list_extensions", v.ID)
	}
	stages := 5
	if remote {
		stages = 3
	}

	cfg := mcp.ServerConfig{Name: v.Name, Kind: v.Kind, Env: v.Env, OAuth: v.OAuth}
	var dir string
	var py string
	var files map[string][]byte
	if remote {
		cfg.URL, cfg.Headers = strings.TrimSpace(v.URL), v.Headers
	} else {
		py, err = pythonCommand()
		if err != nil {
			return "", err
		}
		if !strings.Contains(v.Code, "relay_mcp") {
			return "", errors.New("write the server with the helper: `from relay_mcp import Server, ToolError, http, secret` (see references/sdk.md of the skill relay-builder)")
		}
		if !strings.Contains(v.Code, ".run(") {
			return "", errors.New("the program must end with srv.run(), otherwise it exits at once")
		}
		if len(v.Code) > maxFileSize || len(v.Files) > maxFiles || len(v.Dependencies) > 30 {
			return "", fmt.Errorf("too big: at most %d KB of code and %d extra files", maxFileSize/1024, maxFiles)
		}
		usesOAuth := strings.Contains(v.Code, "oauth=True") || strings.Contains(v.Code, "oauth_token(")
		if v.OAuth != nil && !usesOAuth {
			return "", errors.New("there is an oauth block, but the code never uses the signed-in token (http(..., oauth=True) or oauth_token()). " +
				"OAuth is only for services where the user signs in with their own account (GitHub, Google, Spotify ...). " +
				"A service with a bot token or an API key (a Telegram bot, a Slack bot token, most APIs) needs no oauth block: " +
				"read the key with secret(\"NAME\") and give it in env as {\"NAME\": \"${NAME}\"}")
		}
		if v.OAuth == nil && usesOAuth {
			return "", errors.New("the code uses oauth=True / oauth_token(), but there is no oauth block: give the sign-in settings in oauth, " +
				"or, if the service uses a key or a token, read it with secret(\"NAME\") and put it in env")
		}
		files = map[string][]byte{"server.py": []byte(v.Code), "relay_mcp.py": sdkSource}
		if len(v.Dependencies) > 0 {
			files["requirements.txt"] = []byte(strings.Join(normalizeDependencies(v.Dependencies), "\n") + "\n")
		}
		for name, text := range v.Files {
			reserved := []string{"server.py", "relay_mcp.py"}
			if len(v.Dependencies) > 0 {
				reserved = append(reserved, "requirements.txt")
			}
			rel, err := cleanRel(name, reserved...)
			if err != nil {
				return "", err
			}
			if len(text) > maxFileSize {
				return "", fmt.Errorf("the file %q is larger than %d KB", name, maxFileSize/1024)
			}
			files[rel] = []byte(text)
		}
		dir = filepath.Join(b.Home, "connectors", v.ID)
		cfg.Command = py
		if len(v.Dependencies) > 0 {
			if err := validateDependencies(v.Dependencies); err != nil {
				return "", err
			}
			cfg.Command = venvPythonPath(filepath.Join(dir, ".venv"))
		}
		cfg.Args = []string{filepath.Join(dir, "server.py")}
	}
	cfg = mcp.Normalize(cfg)
	if err := mcp.Validate(v.ID, cfg); err != nil {
		return "", err
	}

	if !remote {
		tools.Progress(ctx, "Preparing the project", 1, stages)
		_ = os.RemoveAll(dir) // leftovers of an earlier try with this id
		if err := writeTree(dir, files); err != nil {
			return "", err
		}
		if len(v.Dependencies) > 0 {
			tools.Progress(ctx, "Installing isolated dependencies", 2, stages)
			if _, err := createPythonEnv(ctx, py, filepath.Join(dir, ".venv"), v.Dependencies); err != nil {
				return "", fmt.Errorf("could not prepare the private Python environment: %w", err)
			}
		}
		tools.Progress(ctx, "Checking the code", 3, stages)
		if err := checkPython(ctx, cfg.Command, filepath.Join(dir, "server.py")); err != nil {
			return "", err
		}
	} else {
		tools.Progress(ctx, "Saving the connection", 1, stages)
	}

	file.Servers[v.ID] = cfg
	if err := mcp.Save(b.MCP.Path(), file); err != nil {
		return "", err
	}
	tools.Progress(ctx, "Starting the server", stages-1, stages)
	b.MCP.Reload()
	_ = b.MCP.Disconnect(v.ID) // a replaced server must start again with the new code
	if remote {
		err = b.MCP.Connect(v.ID)
	} else {
		err = b.MCP.Trust(v.ID) // the user approved this tool call: that is the approval of the program
	}
	var auth *mcp.AuthError
	if err != nil && !errors.As(err, &auth) {
		where := ""
		if dir != "" {
			where = " in " + dir
		}
		return fmt.Sprintf("Saved the server %q%s, but it did not start. The skill is written when it starts.", v.ID, where),
			fmt.Errorf("%v\nFix server.py and call create_mcp_server again with replace=true (with the skill again)", err)
	}

	// the skill: the text of the model plus the exact list of the tools, which Relay knows
	tools.Progress(ctx, "Writing the skill", stages, stages)
	var names []string
	for _, st := range b.MCP.Status() {
		if st.ID == v.ID {
			for _, tl := range st.Tools {
				names = append(names, tl.Name)
			}
		}
	}
	sk.Instructions = strings.TrimSpace(sk.Instructions) + toolsSection(v.ID, b.MCP.Status())
	skillDir, _, err := b.writeSkill(sk)
	if err != nil {
		return fmt.Sprintf("The server %q runs, but its skill could not be written.", v.ID), err
	}
	return t.report(v, cfg, dir, skillDir, sk.Name, names, auth != nil)
}

// toolsSection lists the tools of a server, exactly as the model sees them.
func toolsSection(id string, status []mcp.Status) string {
	var b strings.Builder
	for _, st := range status {
		if st.ID != id {
			continue
		}
		var lines []string
		for _, tl := range st.Tools {
			if tl.Disabled {
				continue
			}
			lines = append(lines, fmt.Sprintf("- `%s`: %s", tools.MCPToolName(id, tl.Name), oneLine(tl.Description, 160)))
		}
		if len(lines) > 0 {
			b.WriteString("\n\n## Tools of this server\n\n" + strings.Join(lines, "\n"))
		}
	}
	return b.String()
}

func (t *createServer) report(v serverArgs, cfg mcp.ServerConfig, dir, skillDir, skillName string, names []string, needsAuth bool) (string, error) {
	signIn := ""
	if needsAuth || t.b.MCP.NeedsSignIn(v.ID) {
		signIn = fmt.Sprintf("\nThe user has to sign in before the tools of %q work: call authorize_connector with id %q (it opens the browser and waits), or just call one of its tools, and Relay does the same.", v.ID, v.ID)
	}
	where := ""
	if dir != "" {
		where = " in " + dir
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Created the %s %q%s and its skill %q (%s).", cfg.Kind, v.ID, where, skillName, skillDir)
	if len(names) > 0 {
		fmt.Fprintf(&out, "\nIt started with %d tools: %s.\nThey are available to you from the next step as mcp__%s__<tool>. Test one that only reads, with real input, and check the result.", len(names), strings.Join(names, ", "), v.ID)
	}
	if missing := missingEnv(v); len(missing) > 0 {
		fmt.Fprintf(&out, "\nThese variables are not set in Relay's environment: %s. Tell the user to set them (for example `export %s=...` in the shell profile), "+
			"and to restart Relay (`relay stop`, then start it again): a running Relay does not see variables that are set later. Until then the tools that need them will say so.",
			strings.Join(missing, ", "), missing[0])
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

// missingEnv lists the ${NAME} references of the server (in env, headers and the OAuth secret) whose
// variable is empty in Relay's own environment.
func missingEnv(v serverArgs) []string {
	set := map[string]bool{}
	look := func(val string) {
		os.Expand(val, func(name string) string {
			if os.Getenv(name) == "" {
				set[name] = true
			}
			return ""
		})
	}
	for _, val := range v.Env {
		look(val)
	}
	for _, val := range v.Headers {
		look(val)
	}
	if v.OAuth != nil {
		look(v.OAuth.ClientSecret)
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

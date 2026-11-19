package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"relay/internal/browser"
	"relay/internal/builder"
	"relay/internal/client"
	"relay/internal/config"
	"relay/internal/mcp"
)

const mcpUsage = `relay mcp: MCP servers (tools that any model can use)

Usage:
  relay mcp list                        servers and their state
  relay mcp info <id>                   details of one configured server
  relay mcp validate [id]               check one or all server definitions
  relay mcp add <id> --url URL          add a remote server (Streamable HTTP)
  relay mcp add <id> --command CMD [--args "A B"] [--env KEY=VALUE]
  relay mcp add <id> --command CMD -- ARG1 ARG2     (arguments after --, kept as they are)
  relay mcp remove <id> [--yes] [--keep-files]
  relay mcp test <id>                   connect once and show the tools
  relay mcp trust <id>                  approve the command of a local server
  relay mcp connect <id> | disconnect <id> | reconnect <id>
  relay mcp sdk [folder]                write relay_mcp.py (the helper for writing a server in Python) into a folder

Servers live in mcp.json (edit it by hand if you like; Relay notices the change).
Options of add: --name TEXT  --kind tool|connector  --header "Name: value"  --yes  --force
  tool: works on this computer (/tools); connector: works with an outside service (/connectors).
  connect signs you in (opens the browser) to a server that needs it.
Example:
  relay mcp add files --command npx -- -y @modelcontextprotocol/server-filesystem /home/me/projects
`

func cmdMCP(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(mcpUsage)
		return 0
	}
	switch args[0] {
	case "list", "ls":
		return mcpList()
	case "info":
		return mcpInfo(args[1:])
	case "validate", "check":
		return mcpValidate(args[1:])
	case "add":
		return mcpAdd(args[1:])
	case "remove", "rm":
		return mcpRemove(args[1:])
	case "test":
		return mcpTest(args[1:])
	case "trust":
		return mcpTrust(args[1:])
	case "connect", "disconnect", "reconnect":
		return mcpAction(args[0], args[1:])
	case "sdk":
		return mcpSDK(args[1:])
	}
	fmt.Fprintf(os.Stderr, "relay mcp: unknown command %q\n\n%s", args[0], mcpUsage)
	return 2
}

type mcpFiles struct {
	home  string
	path  string
	file  *mcp.File
	trust *mcp.Trust
}

func openMCP() (*mcpFiles, error) {
	home, err := config.Home()
	if err != nil {
		return nil, err
	}
	m := &mcpFiles{home: home, path: config.MCPPath(home), trust: mcp.OpenTrust(config.MCPTrustPath(home))}
	m.file, err = mcp.LoadFile(m.path)
	if err != nil {
		return m, fmt.Errorf("%s has a problem, nothing was changed:\n%v", m.path, err)
	}
	return m, nil
}

func (m *mcpFiles) entry(id string) (mcp.ServerConfig, error) {
	c, ok := m.file.Servers[id]
	if !ok {
		return c, fmt.Errorf("there is no server %q in %s (see: relay mcp list)", id, m.path)
	}
	return c, nil
}

func mcpList() int {
	if cl, err := client.Connect(false); err == nil {
		if r, err := cl.MCP(); err == nil {
			if len(r.Servers) == 0 {
				fmt.Println("No MCP servers yet.\nAdd one:  relay mcp add <id> --url URL   or   relay mcp add <id> --command CMD\nOr edit:  " + r.Path)
				return 0
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tKIND\tTYPE\tSTATE\tTOOLS\tTARGET")
			for _, s := range r.Servers {
				tools := "-"
				if s.State == "connected" {
					tools = fmt.Sprint(s.ToolCount)
				}
				state := s.State
				if s.Disabled {
					state = "disabled"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Kind, s.Type, state, tools, oneLine(s.Target, 60))
			}
			tw.Flush()
			for _, s := range r.Servers {
				switch {
				case s.Type == mcp.TypeStdio && !s.Trusted:
					fmt.Printf("! %s waits for your approval to run: %s\n  approve it: relay mcp trust %s\n", s.ID, oneLine(s.Target, 80), s.ID)
				case s.State == "needs_setup":
					fmt.Printf("! %s needs OAuth client setup; open /connections\\n", s.ID)
				case s.State == "auth_required" || (s.OAuth && !s.SignedIn):
					fmt.Printf("! %s needs you to sign in (relay mcp connect %s)\n", s.ID, s.ID)
				case s.Error != "":
					fmt.Printf("! %s: %s\n", s.ID, oneLine(s.Error, 160))
				}
				if s.Warning != "" {
					fmt.Printf("! %s: %s\n", s.ID, s.Warning)
				}
			}
			if r.Error != "" {
				fmt.Printf("\n! %s has a problem and is being ignored (the last good version is in use):\n%s\n", r.Path, r.Error)
			}
			return 0
		}
	}
	// the backend is not running: show what is configured
	m, err := openMCP()
	if err != nil {
		return fail(err)
	}
	if len(m.file.Servers) == 0 {
		fmt.Println("No MCP servers yet.\nAdd one:  relay mcp add <id> --url URL   or   relay mcp add <id> --command CMD\nOr edit:  " + m.path)
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tSTATE\tTARGET")
	for _, id := range m.file.IDs() {
		st := mcp.StatusOf(id, m.file.Servers[id], mcp.SourceFile, m.trust)
		note := "not running"
		if !st.Trusted {
			note = "needs approval"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", id, st.Type, note, oneLine(st.Target, 60))
	}
	tw.Flush()
	fmt.Println("\nThe backend is not running, so no state is known. Start it with: relay")
	return 0
}

// mcpInfo shows the persisted definition, independent of whether the backend is running.
func mcpInfo(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "usage: relay mcp info <id>\n")
		return 2
	}
	m, err := openMCP()
	if err != nil {
		return fail(err)
	}
	cfg, err := m.entry(args[0])
	if err != nil {
		return fail(err)
	}
	fmt.Printf("ID:       %s\nName:     %s\nKind:     %s\nType:     %s\nTarget:   %s\nDisabled: %t\n",
		args[0], cfg.Name, cfg.Kind, cfg.Type, mcp.Target(cfg), cfg.Disabled)
	if len(cfg.Args) > 0 {
		fmt.Printf("Args:     %s\n", strings.Join(cfg.Args, " "))
	}
	if len(cfg.Env) > 0 {
		fmt.Printf("Env keys: %s\n", strings.Join(sortedKeys(cfg.Env), ", "))
	}
	if len(cfg.Headers) > 0 {
		fmt.Printf("Headers:  %s\n", strings.Join(sortedKeys(cfg.Headers), ", "))
	}
	if cfg.OAuth != nil {
		fmt.Printf("OAuth:    configured (client id %s)\n", cfg.OAuth.ClientID)
	}
	if len(cfg.DisabledTools) > 0 {
		fmt.Printf("Disabled tools: %s\n", strings.Join(cfg.DisabledTools, ", "))
	}
	if cfg.Type == mcp.TypeStdio && len(cfg.Args) > 0 {
		managed := filepath.Join(m.home, "connectors", args[0])
		if filepath.Clean(cfg.Args[0]) == filepath.Join(managed, "server.py") {
			if _, err := os.Stat(cfg.Args[0]); err != nil {
				fmt.Printf("Files:    MISSING (%s)\n", cfg.Args[0])
			} else {
				fmt.Printf("Files:    present (%s)\n", filepath.Dir(cfg.Args[0]))
			}
		}
	}
	if w := mcp.Warning(cfg); w != "" {
		fmt.Println("Warning: " + w)
	}
	if cl, connectErr := client.Connect(false); connectErr == nil {
		if live, liveErr := cl.MCP(); liveErr == nil {
			for _, server := range live.Servers {
				if server.ID != args[0] {
					continue
				}
				fmt.Printf("State:    %s\n", server.State)
				if server.Error != "" {
					fmt.Printf("Error:    %s\n", server.Error)
				}
				if server.ToolCount > 0 {
					fmt.Printf("Tools (%d):\n", server.ToolCount)
					for _, tool := range server.Tools {
						state := ""
						if tool.Disabled {
							state = " [disabled]"
						}
						fmt.Printf("  %s%s\n", tool.Name, state)
					}
				}
				break
			}
		}
	}
	return 0
}

func mcpValidate(args []string) int {
	if len(args) > 1 {
		fmt.Fprint(os.Stderr, "usage: relay mcp validate [id]\n")
		return 2
	}
	m, err := openMCP()
	if err != nil {
		return fail(err)
	}
	ids := m.file.IDs()
	if len(args) == 1 {
		ids = []string{args[0]}
	}
	if len(ids) == 0 {
		fmt.Println("No MCP servers configured.")
		return 0
	}
	bad := 0
	for _, id := range ids {
		cfg, ok := m.file.Servers[id]
		if !ok {
			fmt.Printf("✗ %s: not found\n", id)
			bad++
			continue
		}
		if err := mcp.Validate(id, mcp.Normalize(cfg)); err != nil {
			fmt.Printf("✗ %s: %v\n", id, err)
			bad++
			continue
		}
		if cfg.Type == mcp.TypeStdio && len(cfg.Args) > 0 {
			managed := filepath.Join(m.home, "connectors", id)
			if filepath.Clean(cfg.Args[0]) == filepath.Join(managed, "server.py") {
				if _, err := os.Stat(cfg.Args[0]); err != nil {
					fmt.Printf("✗ %s: server files are missing (%s)\n", id, cfg.Args[0])
					bad++
					continue
				}
			}
		}
		fmt.Printf("✓ %s\n", id)
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mcpAdd(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(os.Stderr, mcpUsage)
		return 2
	}
	id, rest := args[0], args[1:]
	var extra []string
	for i, a := range rest {
		if a == "--" {
			extra, rest = rest[i+1:], rest[:i]
			break
		}
	}
	fs := flag.NewFlagSet("relay mcp add", flag.ContinueOnError)
	name := fs.String("name", "", "display name")
	kind := fs.String("kind", "", "tool (works on this computer) or connector (works with an outside service)")
	url := fs.String("url", "", "address of a remote server")
	command := fs.String("command", "", "program to run")
	argStr := fs.String("args", "", "arguments, separated by spaces")
	var envs, headers multiFlag
	fs.Var(&envs, "env", "KEY=VALUE (can be repeated)")
	fs.Var(&headers, "header", "\"Name: value\" (can be repeated)")
	yes := fs.Bool("yes", false, "approve the command without asking")
	force := fs.Bool("force", false, "replace a server with the same id")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		return fail(fmt.Errorf("unexpected %q (put program arguments after --, or in --args \"...\")", fs.Arg(0)))
	}
	if (*url == "") == (*command == "") {
		return fail(fmt.Errorf("give exactly one of --url (a remote server) and --command (a local program)"))
	}
	cfg := mcp.ServerConfig{Name: *name, Kind: *kind, URL: *url, Command: *command}
	cfg.Args = append(strings.Fields(*argStr), extra...)
	for _, kv := range envs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fail(fmt.Errorf("--env wants KEY=VALUE, got %q", kv))
		}
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env[k] = v
	}
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return fail(fmt.Errorf("--header wants \"Name: value\", got %q", h))
		}
		if cfg.Headers == nil {
			cfg.Headers = map[string]string{}
		}
		cfg.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	cfg = mcp.Normalize(cfg)
	if err := mcp.Validate(id, cfg); err != nil {
		return fail(err)
	}
	m, err := openMCP()
	if err != nil {
		return fail(err)
	}
	if _, exists := m.file.Servers[id]; exists && !*force {
		return fail(fmt.Errorf("a server %q already exists in %s; use --force to replace it", id, m.path))
	}
	m.file.Servers[id] = cfg
	if err := mcp.Save(m.path, m.file); err != nil {
		return fail(err)
	}
	fmt.Printf("Added %q to %s\n", id, m.path)
	if w := mcp.Warning(cfg); w != "" {
		fmt.Println("! " + w)
	}
	if cfg.Type == mcp.TypeStdio {
		fmt.Printf("\nThis server is a program that will run on your computer:\n  %s\n", mcp.Target(cfg))
		if *yes || confirm("Trust it and let Relay run it? [y/N] ") {
			if err := m.trust.Allow(id, mcp.Fingerprint(cfg)); err != nil {
				return fail(err)
			}
			fmt.Println("Approved.")
		} else {
			fmt.Printf("Saved, but it will not run until you approve it:  relay mcp trust %s\n", id)
			return 0
		}
	}
	fmt.Printf("A running Relay picks the change up by itself. Check the server:  relay mcp test %s\n", id)
	return 0
}

func mcpRemove(args []string) int {
	fs := flag.NewFlagSet("relay mcp remove", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	keepFiles := fs.Bool("keep-files", false, "keep the Relay-created server source folder")
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "usage: relay mcp remove <id> [--yes] [--keep-files]\n")
		return 2
	}
	id := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return fail(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	m, err := openMCP()
	if err != nil {
		return fail(err)
	}
	cfg, err := m.entry(id)
	if err != nil {
		return fail(fmt.Errorf("%v\n(a server from the old [[mcp]] section of config.toml is removed there)", err))
	}
	var managedDir string
	if cfg.Type == mcp.TypeStdio && len(cfg.Args) > 0 {
		connectors := filepath.Join(m.home, "connectors")
		candidate := filepath.Join(connectors, id)
		if filepath.Clean(cfg.Args[0]) == filepath.Join(candidate, "server.py") {
			// The path is derived from a validated server id, and the entry point must
			// match the exact Relay-managed layout before recursive deletion is allowed.
			if rel, relErr := filepath.Rel(connectors, candidate); relErr == nil && rel == id {
				if _, statErr := os.Stat(candidate); statErr == nil {
					managedDir = candidate
				}
			}
		}
	}
	if !*yes {
		question := fmt.Sprintf("Remove MCP server %q from Relay?", id)
		if managedDir != "" && !*keepFiles {
			question += " Its Relay-created source folder will also be deleted."
		}
		if !confirm(question + " [y/N] ") {
			fmt.Println("Nothing was removed.")
			return 1
		}
	}
	delete(m.file.Servers, id)
	if err := mcp.Save(m.path, m.file); err != nil {
		return fail(err)
	}
	_ = m.trust.Forget(id)
	if managedDir != "" && !*keepFiles {
		if err := os.RemoveAll(managedDir); err != nil {
			return fail(fmt.Errorf("server was unregistered, but source folder could not be removed: %w", err))
		}
	}
	fmt.Printf("Removed %q. A running Relay stops it within a second.\n", id)
	return 0
}

func mcpTest(args []string) int {
	fs := flag.NewFlagSet("relay mcp test", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "run an unapproved program without asking")
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "usage: relay mcp test <id>\n")
		return 2
	}
	id := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	m, err := openMCP()
	if err != nil {
		return fail(err)
	}
	cfg, err := m.entry(id)
	if err != nil {
		return fail(err)
	}
	if cfg.Type == mcp.TypeStdio && !m.trust.Trusted(id, mcp.Fingerprint(cfg)) {
		fmt.Printf("%q is not approved yet. Testing runs this program once:\n  %s\n", id, mcp.Target(cfg))
		if !*yes && !confirm("Run it for this test? [y/N] ") {
			return 1
		}
	}
	fmt.Printf("Connecting to %s ...\n", mcp.Target(cfg))
	cl, err := mcp.Dial(cfg)
	if err != nil {
		var auth *mcp.AuthError
		if asAuth(err, &auth) {
			fmt.Println("The server wants you to sign in (OAuth). Signing in is not available in this version yet.")
			return 1
		}
		return fail(fmt.Errorf("could not connect: %v", err))
	}
	defer cl.Close()
	tools := cl.Tools()
	name := cl.Name
	if name == "" {
		name = id
	}
	fmt.Printf("Connected to %s: %d tools\n", name, len(tools))
	for i, t := range tools {
		if i == 25 {
			fmt.Printf("  ... and %d more\n", len(tools)-25)
			break
		}
		fmt.Printf("  %-28s %s\n", t.Name, oneLine(t.Description, 70))
	}
	return 0
}

func mcpTrust(args []string) int {
	fs := flag.NewFlagSet("relay mcp trust", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "approve without asking")
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "usage: relay mcp trust <id>\n")
		return 2
	}
	id := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	m, err := openMCP()
	if err != nil {
		return fail(err)
	}
	cfg, err := m.entry(id)
	if err != nil {
		return fail(err)
	}
	if cfg.Type != mcp.TypeStdio {
		fmt.Printf("%q is a remote server: there is no local program to approve.\n", id)
		return 0
	}
	fmt.Printf("Relay will run this program on your computer:\n  %s\n", mcp.Target(cfg))
	if len(cfg.Env) > 0 {
		keys := make([]string, 0, len(cfg.Env))
		for k := range cfg.Env {
			keys = append(keys, k)
		}
		fmt.Printf("with these extra environment variables: %s\n", strings.Join(keys, ", "))
	}
	if !*yes && !confirm("Trust it? [y/N] ") {
		return 1
	}
	if err := m.trust.Allow(id, mcp.Fingerprint(cfg)); err != nil {
		return fail(err)
	}
	fmt.Println("Approved. If you change the command later, Relay asks again.")
	return 0
}

func asAuth(err error, target **mcp.AuthError) bool { return errors.As(err, target) }

func mcpAction(action string, args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: relay mcp %s <id>\n", action)
		return 2
	}
	id := args[0]
	cl, err := client.Connect(true)
	if err != nil {
		return fail(err)
	}
	if cl.Spawned {
		fmt.Println("Backend started in the background (stop it with: relay stop)")
	}
	fmt.Printf("%s %s ...\n", strings.ToUpper(action[:1])+action[1:], id)
	r, err := cl.MCPAction(id, action)
	needSignIn := false
	if err != nil {
		if action == "disconnect" || !strings.Contains(err.Error(), "authorization required") {
			return fail(err)
		}
		needSignIn = true // a remote server that wants the user to sign in
	}
	if r == nil {
		if r, err = cl.MCP(); err != nil {
			return fail(err)
		}
	}
	for _, s := range r.Servers {
		if s.ID != id {
			continue
		}
		fmt.Printf("%s: %s", s.ID, s.State)
		if s.State == "connected" {
			fmt.Printf(", %d tools", s.ToolCount)
		}
		fmt.Println()
		if action != "disconnect" && (needSignIn || s.State == "auth_required" || (s.OAuth && !s.SignedIn)) {
			return signInCLI(cl, id)
		}
	}
	return 0
}

// signInCLI opens the sign-in page of a connector and waits until the user is done.
func signInCLI(cl *client.Client, id string) int {
	desc, err := cl.MCPAuthorizationDescription(id)
	if err != nil {
		return fail(err)
	}
	fmt.Println("\n" + desc)
	fmt.Print("\nStart this authorization? [y/N] ")
	answer, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
	if readErr != nil && strings.TrimSpace(answer) == "" {
		fmt.Println("Cancelled.")
		return 0
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		fmt.Println("Cancelled.")
		return 0
	}
	url, err := cl.MCPAuthorize(id)
	if err != nil {
		return fail(err)
	}
	fmt.Println("Opening the sign-in page in your browser. Waiting for you to finish (up to 5 minutes)...")
	if err := browser.Open(url); err != nil {
		fmt.Println("Could not open the browser. Open this address yourself:\n" + url)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		r, err := cl.MCP()
		if err != nil {
			return fail(err)
		}
		for _, s := range r.Servers {
			if s.ID == id && s.SignedIn && s.State == "connected" {
				fmt.Printf("Signed in to %s: %d tools\n", id, s.ToolCount)
				return 0
			}
		}
	}
	return fail(fmt.Errorf("the sign-in was not finished within 5 minutes"))
}

// mcpSDK writes the helper for Python servers into a folder, so that a server can be written by hand.
func mcpSDK(args []string) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}
	p := filepath.Join(dir, "relay_mcp.py")
	if err := os.WriteFile(p, builder.SDK(), 0o644); err != nil {
		return fail(err)
	}
	fmt.Printf("Wrote %s\nUse it in your server:  from relay_mcp import Server, ToolError, http, secret\nThen:  relay mcp add <id> --command python3 -- %s\nThe guide: docs/EXTENSIONS.md\n",
		p, filepath.Join(dir, "server.py"))
	return 0
}

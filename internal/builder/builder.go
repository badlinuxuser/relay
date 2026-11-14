// Package builder gives the agent the tools to extend Relay by itself: it can write an MCP
// server (a tool for this computer, or a connector to an outside service with OAuth) and plug
// it in, write a skill, look at what is installed, sign the user in, and remove what it made.
// They are built into Relay and work in agent mode with any model that can call tools.
package builder

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"relay/internal/mcp"
	"relay/internal/skills"
	"relay/internal/tools"
)

// sdkSource is relay_mcp.py, the helper that connector programs import. It is copied next to
// every program that create_mcp_server writes, so that programs always get the version that
// belongs to this Relay.
//
//go:embed sdk/relay_mcp.py
var sdkSource []byte

// SDK returns the source of relay_mcp.py.
func SDK() []byte { return sdkSource }

// Builder holds what the tools work with.
type Builder struct {
	Home     string          // the Relay configuration folder
	MCP      *mcp.Manager    // the servers
	Skills   *skills.Manager // the skills
	Callback func() string   // the OAuth callback address of this Relay (may be nil)
}

// Names of the tools, for the list of tools shown to the user.
const (
	ToolCreateServer = "create_mcp_server"
	ToolCreateSkill  = "create_skill"
	ToolRemove       = "remove_extension"
	ToolList         = "list_extensions"
	ToolReadServer   = "read_server"
	ToolAuthorize    = "authorize_connector"
)

// Tools returns the six tools.
func (b *Builder) Tools() []tools.Tool {
	return []tools.Tool{&createServer{b}, &createSkill{b}, &removeExt{b}, &listExt{b}, &readServer{b}, &authConnector{b}}
}

// ---- shared helpers -----------------------------------------------------------

func mkSchema(props map[string]any, required ...string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required})
	return b
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func flag(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func obj(desc string) map[string]any  { return map[string]any{"type": "object", "description": desc} }

func decode(args json.RawMessage, v any) error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("the arguments are not valid: %v", err)
	}
	return nil
}

// pythonCommand finds a Python 3 interpreter and returns the name to put into mcp.json.
func pythonCommand() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			return name, nil
		}
	}
	return "", errors.New("Python 3 was not found on this computer. Ask the user to install it (python.org) so that `python3` or `python` works, then try again")
}

// cleanRel checks the name of a file inside a folder that is being written.
func cleanRel(name string, reserved ...string) (string, error) {
	n := strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	if n == "" || strings.HasPrefix(n, "/") || filepath.VolumeName(n) != "" {
		return "", fmt.Errorf("the file name %q must be relative, like references/notes.md", name)
	}
	c := path.Clean(n)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("the file name %q leaves the folder", name)
	}
	for _, r := range reserved {
		if c == r {
			return "", fmt.Errorf("the file name %q is reserved", name)
		}
	}
	return filepath.FromSlash(c), nil
}

const (
	maxFiles    = 30
	maxFileSize = 300 * 1024
)

// writeTree writes the files into dir.
func writeTree(dir string, files map[string][]byte) error {
	for rel, data := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// isInside reports whether p is inside dir.
func isInside(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// checkPython parses the file with Python without running it.
func checkPython(ctx context.Context, py, file string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-c", "import ast,sys\nast.parse(open(sys.argv[1],encoding='utf-8').read(),sys.argv[1])", file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("server.py has a syntax error:\n%s", lastLines(string(out), 12))
	}
	return nil
}

var (
	reHost  = regexp.MustCompile(`https?://([A-Za-z0-9.-]+)`)
	reProc  = regexp.MustCompile(`\b(subprocess|os\.system|os\.popen|os\.exec\w*|pty\.spawn)\b|\beval\(|\bexec\(`)
	reWrite = regexp.MustCompile(`open\([^)]*['"][wax]b?\+?['"]|\.write_text\(|\.write_bytes\(|os\.(remove|unlink|rename|rmdir)\(|shutil\.(rmtree|move|copy\w*)\(`)
)

// summarize says in a line what a program can do, found by reading its text: the hosts it
// contacts, whether it starts programs, whether it writes or deletes files. The user sees it
// when asked to approve; it is a hint, not a guarantee.
func summarize(code string) string {
	hosts := map[string]bool{}
	for _, m := range reHost.FindAllStringSubmatch(code, -1) {
		hosts[strings.ToLower(m[1])] = true
	}
	var h []string
	for k := range hosts {
		h = append(h, k)
	}
	sort.Strings(h)
	if len(h) > 5 {
		h = append(h[:5], "…")
	}
	parts := []string{}
	if len(h) > 0 {
		parts = append(parts, "contacts: "+strings.Join(h, ", "))
	} else {
		parts = append(parts, "contacts no web address")
	}
	if reProc.MatchString(code) {
		parts = append(parts, "RUNS PROGRAMS")
	}
	if reWrite.MatchString(code) {
		parts = append(parts, "writes or deletes files")
	}
	return strings.Join(parts, "; ")
}

func quoteList(m map[string]string) string {
	var k []string
	for name := range m {
		k = append(k, name)
	}
	sort.Strings(k)
	return strings.Join(k, ", ")
}


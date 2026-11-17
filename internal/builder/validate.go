package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"relay/internal/mcp"
	"relay/internal/provider"
	"relay/internal/tools"
)

type validateServer struct{ b *Builder }

type validateArgs struct {
	ID           string            `json:"id"`
	Code         string            `json:"code"`
	Files        map[string]string `json:"files"`
	Env          map[string]string `json:"env"`
	Dependencies []string          `json:"dependencies"`
}

func (t *validateServer) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        ToolValidateServer,
		Description: "Build a temporary, isolated copy of an MCP project, install its declared PyPI dependencies into a private virtual environment, start it through Relay's MCP runtime, and verify that the MCP handshake and tool discovery succeed. Does not register the server or write to the user's connectors folder. Use repeatedly while developing before create_mcp_server.",
		Schema: mkSchema(map[string]any{
			"id":           str("Temporary server id, 1-32 letters, digits, '-' or '_'."),
			"code":         str("Complete server.py source using Relay's relay_mcp SDK."),
			"files":        obj(`Optional project files as relative path to source text, e.g. {"api.py": "..."}.`),
			"env":          obj("Environment references for the test process. Use ${NAME}; never include secret values."),
			"dependencies": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional PyPI packages with version constraints; installed in a disposable virtual environment."},
		}, "id", "code"),
	}
}
func (t *validateServer) NeedsApproval() bool            { return true }
func (t *validateServer) OnceOnly() bool                 { return true }
func (t *validateServer) NeedsOffer() bool               { return true }
func (t *validateServer) CancelsRequest() bool           { return true }
func (t *validateServer) NeedsRead(args []byte) []string { return []string{"guide", "sdk"} }
func (t *validateServer) Describe(a json.RawMessage) string {
	var v validateArgs
	_ = json.Unmarshal(a, &v)
	extra := ""
	if len(v.Dependencies) > 0 {
		extra = "; install into a disposable private Python environment: " + strings.Join(v.Dependencies, ", ")
	}
	return fmt.Sprintf("run the code of temporary MCP project %q locally and validate its MCP startup%s (without registering it)", v.ID, extra)
}

func (t *validateServer) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var v validateArgs
	if err := decode(raw, &v); err != nil {
		return "", err
	}
	v.ID = strings.TrimSpace(v.ID)
	if !mcp.ValidID(v.ID) {
		return "", fmt.Errorf("invalid MCP id %q", v.ID)
	}
	if strings.TrimSpace(v.Code) == "" {
		return "", fmt.Errorf("code is required")
	}
	if !strings.Contains(v.Code, "relay_mcp") || !strings.Contains(v.Code, ".run(") {
		return "", fmt.Errorf("server.py must import relay_mcp and finish with srv.run()")
	}
	if len(v.Code) > maxFileSize || len(v.Files) > maxFiles || len(v.Dependencies) > 30 {
		return "", fmt.Errorf("project exceeds the file or size limits")
	}
	if err := validateDependencies(v.Dependencies); err != nil {
		return "", err
	}
	py, err := pythonCommand()
	if err != nil {
		return "", err
	}
	root, err := os.MkdirTemp("", "relay-mcp-validation-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	files := map[string][]byte{"server.py": []byte(v.Code), "relay_mcp.py": sdkSource}
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
			return "", fmt.Errorf("file %q is too large", name)
		}
		files[rel] = []byte(text)
	}
	if err := writeTree(root, files); err != nil {
		return "", err
	}
	command := py
	if len(v.Dependencies) > 0 {
		tools.Progress(ctx, "Preparing an isolated test environment", 1, 3)
		command, err = createPythonEnv(ctx, py, filepath.Join(root, ".venv"), v.Dependencies)
		if err != nil {
			return "", err
		}
	}
	if err := checkPython(ctx, command, filepath.Join(root, "server.py")); err != nil {
		return "", err
	}
	cfg := mcp.Normalize(mcp.ServerConfig{Name: "Temporary validation", Kind: mcp.KindTool, Command: command, Args: []string{filepath.Join(root, "server.py")}, Env: v.Env})
	if err := mcp.Validate(v.ID, cfg); err != nil {
		return "", err
	}
	configPath := filepath.Join(root, "mcp.json")
	trustPath := filepath.Join(root, "trust.json")
	if err := mcp.Save(configPath, &mcp.File{Servers: map[string]mcp.ServerConfig{v.ID: cfg}}); err != nil {
		return "", err
	}
	manager := mcp.NewManager(configPath, trustPath, nil)
	defer manager.Close()
	manager.Reload()
	tools.Progress(ctx, "Starting the temporary MCP", 2, 3)
	if err := manager.Trust(v.ID); err != nil {
		for _, st := range manager.Status() {
			if st.ID == v.ID && st.Error != "" {
				return "", fmt.Errorf("MCP startup failed (%s): %s", st.State, st.Error)
			}
		}
		return "", fmt.Errorf("MCP startup failed: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var status mcp.Status
	for time.Now().Before(deadline) {
		for _, st := range manager.Status() {
			if st.ID == v.ID {
				status = st
			}
		}
		if status.State == mcp.StateConnected || status.State == mcp.StateError || status.State == mcp.StateDisconnected {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if status.State != mcp.StateConnected {
		return "", fmt.Errorf("MCP did not connect: state=%s error=%s", status.State, status.Error)
	}
	refs := manager.Tools()
	var names []string
	for _, ref := range refs {
		if ref.Server == v.ID {
			names = append(names, ref.Name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", fmt.Errorf("MCP connected but did not expose any tools")
	}
	tools.Progress(ctx, "MCP handshake and tool discovery passed", 3, 3)
	return fmt.Sprintf("Validation passed. The temporary MCP started successfully and exposed %d tools: %s. No server was registered and no files were written to Relay's connectors directory. You can now install it with create_mcp_server using the same source and dependencies.", len(names), strings.Join(names, ", ")), nil
}

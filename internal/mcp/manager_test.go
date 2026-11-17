package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary act as an MCP server on stdio: Dial runs os.Args[0] with
// RELAY_FAKE_MCP=1 in its "env" (which also proves that the "env" of an entry arrives).
func TestMain(m *testing.M) {
	if os.Getenv("RELAY_FAKE_MCP") == "1" {
		fakeStdioServer()
		return
	}
	os.Exit(m.Run())
}

func fakeStdioServer() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	fmt.Println("starting up (this line is not JSON-RPC)")
	for sc.Scan() {
		var msg rpcMsg
		if json.Unmarshal(sc.Bytes(), &msg) != nil || len(msg.ID) == 0 {
			continue
		}
		result := `{}`
		switch msg.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake-stdio"}}`
		case "tools/list":
			result = `{"tools":[{"name":"echo","description":"echoes","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			var call struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(msg.Params, &call)
			if call.Name == "revoked" { // a connector whose service answered 401
				result = `{"isError":true,"content":[{"type":"text","text":"relay:auth-required the service answered 401"}]}`
				break
			}
			text := "pong"
			if f := os.Getenv("RELAY_OAUTH_TOKEN_FILE"); f != "" {
				text += " " + f // lets a test see that the token file reaches the program
			}
			result = `{"content":[{"type":"text","text":"` + strings.ReplaceAll(text, `\`, `\\`) + `"}]}`
		}
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n", msg.ID, result)
	}
}

func fakeStdioConfig() ServerConfig {
	return Normalize(ServerConfig{Command: os.Args[0], Env: map[string]string{"RELAY_FAKE_MCP": "1"}})
}

func TestStdioHandshakeAndCall(t *testing.T) {
	cl, err := Dial(fakeStdioConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if cl.Name != "fake-stdio" || len(cl.Tools()) != 1 {
		t.Fatalf("name=%q tools=%v", cl.Name, cl.Tools())
	}
	out, err := cl.Call(context.Background(), "echo", nil)
	if err != nil || out != "pong" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestStdioProgramNotFoundSaysSo(t *testing.T) {
	_, err := Dial(Normalize(ServerConfig{Command: "relay-no-such-program-xyz"}))
	if err == nil {
		t.Fatal("want an error")
	}
}

func waitState(t *testing.T, m *Manager, id string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, s := range m.Status() {
			if s.ID == id && s.State == want {
				return s
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server %q did not reach %q: %+v", id, want, m.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManagerApprovalReloadAndDisconnect(t *testing.T) {
	dir := t.TempDir()
	path, trustPath := filepath.Join(dir, "mcp.json"), filepath.Join(dir, "trust.json")
	write := func(f *File) {
		if err := Save(path, f); err != nil {
			t.Fatal(err)
		}
	}
	write(&File{Servers: map[string]ServerConfig{"fake": fakeStdioConfig()}})
	m := NewManager(path, trustPath, nil)
	defer m.Close()

	m.Reload()
	st := m.Status()
	if len(st) != 1 || st[0].State != StateConfigured || st[0].Trusted {
		t.Fatalf("an unapproved program must not run: %+v", st)
	}
	if err := m.Connect("fake"); err == nil {
		t.Fatal("connecting an unapproved program must fail")
	}

	if err := m.Trust("fake"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "fake", StateConnected)
	if refs := m.Tools(); len(refs) != 1 || refs[0].Server != "fake" || refs[0].Name != "echo" {
		t.Fatalf("tools: %+v", refs)
	}
	if out, err := m.CallTool(context.Background(), "fake", "echo", nil); err != nil || out != "pong" {
		t.Fatalf("out=%q err=%v", out, err)
	}

	// turning a tool off in mcp.json takes effect without restarting the server
	c := fakeStdioConfig()
	c.DisabledTools = []string{"echo"}
	write(&File{Servers: map[string]ServerConfig{"fake": c}})
	m.Reload()
	waitState(t, m, "fake", StateConnected)
	if len(m.Tools()) != 0 {
		t.Fatal("a disabled tool must not be offered")
	}
	if _, err := m.CallTool(context.Background(), "fake", "echo", nil); err == nil {
		t.Fatal("a disabled tool must not run")
	}

	// a broken file is ignored as a whole
	if err := os.WriteFile(path, []byte(`{"servers": {`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	if m.LoadError() == "" || len(m.Status()) != 1 {
		t.Fatalf("the last good configuration must stay: err=%q status=%v", m.LoadError(), m.Status())
	}

	if err := m.Disconnect("fake"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "fake", StateDisconnected)
	if _, err := m.CallTool(context.Background(), "fake", "echo", nil); err == nil {
		t.Fatal("a server the user disconnected stays disconnected")
	}

	// removing the entry stops and forgets the server
	write(&File{Servers: map[string]ServerConfig{}})
	m.Reload()
	if len(m.Status()) != 0 || m.LoadError() != "" {
		t.Fatalf("status=%v err=%q", m.Status(), m.LoadError())
	}
}

func TestChangedCommandNeedsApprovalAgain(t *testing.T) {
	dir := t.TempDir()
	tr := OpenTrust(filepath.Join(dir, "trust.json"))
	a := fakeStdioConfig()
	if err := tr.Allow("x", Fingerprint(a)); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Args = []string{"--other"}
	if !tr.Trusted("x", Fingerprint(a)) || tr.Trusted("x", Fingerprint(b)) {
		t.Fatal("approval belongs to the exact command")
	}
	// another process (the CLI) writes the file: Reload sees it
	other := OpenTrust(filepath.Join(dir, "trust.json"))
	if !other.Trusted("x", Fingerprint(a)) {
		t.Fatal("approval must be stored in the file")
	}
}

func TestLegacyServerKeepsFullEnvironment(t *testing.T) {
	id, c, ok := LegacyServer("My Server!", "cmd", []string{"a"}, []string{"K=V", "bad"})
	if !ok || id != "My-Server" || !c.InheritEnv || c.Env["K"] != "V" || len(c.Env) != 1 {
		t.Fatalf("id=%q c=%+v ok=%v", id, c, ok)
	}
	if _, _, ok := LegacyServer("!!!", "cmd", nil, nil); ok {
		t.Fatal("a name with no usable characters is rejected")
	}
}

func TestManagedServerDisappearsWhenItsEntryPointIsRemoved(t *testing.T) {
	home := t.TempDir()
	id := "relay-built"
	serverDir := filepath.Join(home, "connectors", id)
	if err := os.MkdirAll(serverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(serverDir, "server.py")
	if err := os.WriteFile(entry, []byte("# server"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Normalize(ServerConfig{Command: "python3", Args: []string{entry}})
	if managedEntryPointMissing(home, id, cfg) {
		t.Fatal("existing managed entry point was reported missing")
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if !managedEntryPointMissing(home, id, cfg) {
		t.Fatal("deleted managed entry point must be treated as uninstalled")
	}
}

func TestExternalCommandIsNotTreatedAsManagedServer(t *testing.T) {
	home := t.TempDir()
	cfg := Normalize(ServerConfig{Command: "npx", Args: []string{"-y", "some-mcp-package"}})
	if managedEntryPointMissing(home, "external", cfg) {
		t.Fatal("external commands must not require a Relay connectors folder")
	}
}

func TestManagerOmitsRelayServerWithMissingSource(t *testing.T) {
	home := t.TempDir()
	id := "gone"
	entry := filepath.Join(home, "connectors", id, "server.py")
	path := filepath.Join(home, "mcp.json")
	cfg := Normalize(ServerConfig{Command: "python3", Args: []string{entry}})
	if err := Save(path, &File{Servers: map[string]ServerConfig{id: cfg}}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path, filepath.Join(home, "trust.json"), nil)
	defer m.Close()
	m.Reload()
	if got := m.Status(); len(got) != 0 {
		t.Fatalf("missing Relay-created source must not appear as installed: %+v", got)
	}
}

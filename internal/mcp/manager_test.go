package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
			result = `{"content":[{"type":"text","text":"pong"}]}`
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

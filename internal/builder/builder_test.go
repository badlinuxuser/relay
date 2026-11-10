package builder

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"relay/internal/mcp"
	"relay/internal/skills"
	"relay/internal/tools"
)

type env struct {
	b    *Builder
	home string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	m := mcp.NewManager(filepath.Join(home, "mcp.json"), filepath.Join(home, "mcp-trust.json"), nil)
	sk := skills.NewManager(filepath.Join(home, "skills"), filepath.Join(home, "skills.json"))
	sk.Reload()
	t.Cleanup(m.Close)
	return &env{b: &Builder{Home: home, MCP: m, Skills: sk, Callback: func() string { return "http://127.0.0.1:8787/oauth/callback" }}, home: home}
}

func (e *env) tool(name string) tools.Tool {
	for _, t := range e.b.Tools() {
		if t.Spec().Name == name {
			return t
		}
	}
	panic("no tool " + name)
}

func (e *env) run(t *testing.T, name string, args any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return e.tool(name).Run(context.Background(), raw)
}

func needPython(t *testing.T) {
	t.Helper()
	for _, n := range []string{"python3", "python"} {
		if _, err := exec.LookPath(n); err == nil {
			return
		}
	}
	t.Skip("Python 3 is not installed")
}

const goodServer = `from relay_mcp import Server, ToolError

srv = Server("demo")

@srv.tool("greet", "Greet someone by name.", {"name": "string"})
def greet(name):
    return "hello " + name

@srv.tool("fail", "Always fails.", {})
def fail():
    raise ToolError("nope")

srv.run()
`

func TestTheFiveToolsAreDescribedAndGuarded(t *testing.T) {
	e := newEnv(t)
	want := map[string]bool{ToolCreateServer: true, ToolCreateSkill: true, ToolRemove: true, ToolList: false, ToolAuthorize: false}
	got := e.b.Tools()
	if len(got) != len(want) {
		t.Fatalf("%d tools", len(got))
	}
	for _, tl := range got {
		sp := tl.Spec()
		approval, ok := want[sp.Name]
		if !ok {
			t.Errorf("unexpected tool %s", sp.Name)
			continue
		}
		if tl.NeedsApproval() != approval {
			t.Errorf("%s: approval must be %v", sp.Name, approval)
		}
		if oo, isOnce := tl.(tools.OnceOnly); approval && (!isOnce || !oo.OnceOnly()) {
			t.Errorf("%s: \"always\" must not be remembered for it", sp.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(sp.Schema, &schema); err != nil || schema["type"] != "object" || len(sp.Description) < 40 {
			t.Errorf("%s: schema %s / description %q", sp.Name, sp.Schema, sp.Description)
		}
	}
}

func TestCreateServerEndToEnd(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	out, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "name": "Demo", "code": goodServer})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "2 tools") || !strings.Contains(out, "mcp__demo__") {
		t.Fatalf("answer: %s", out)
	}
	for _, f := range []string{"server.py", "relay_mcp.py"} {
		if _, err := os.Stat(filepath.Join(e.home, "connectors", "demo", f)); err != nil {
			t.Errorf("%s was not written: %v", f, err)
		}
	}
	st := e.b.MCP.Status()
	if len(st) != 1 || st[0].State != mcp.StateConnected || st[0].Kind != mcp.KindTool || !st[0].Trusted {
		t.Fatalf("status: %+v", st)
	}
	if text, err := e.b.MCP.CallTool(context.Background(), "demo", "greet", json.RawMessage(`{"name":"Ann"}`)); err != nil || text != "hello Ann" {
		t.Fatalf("text=%q err=%v", text, err)
	}

	// the same id again needs replace=true
	if _, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "code": goodServer}); err == nil || !strings.Contains(err.Error(), "replace=true") {
		t.Fatalf("err=%v", err)
	}
	// replacing runs the new code
	newCode := strings.Replace(goodServer, `"hello " + name`, `"bonjour " + name`, 1)
	if out, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "code": newCode, "replace": true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if text, _ := e.b.MCP.CallTool(context.Background(), "demo", "greet", json.RawMessage(`{"name":"Ann"}`)); text != "bonjour Ann" {
		t.Fatalf("the new code must run, got %q", text)
	}

	// list_extensions shows it, the callback address and the built-in skill
	list, err := e.run(t, ToolList, map[string]any{})
	if err != nil || !strings.Contains(list, "demo: tool, stdio, connected") || !strings.Contains(list, "/oauth/callback") || !strings.Contains(list, "relay-builder") {
		t.Fatalf("list: %v\n%s", err, list)
	}

	// removing deletes the entry, the approval and the code
	if _, err := e.run(t, ToolRemove, map[string]any{"kind": "mcp_server", "name": "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.home, "connectors", "demo")); err == nil {
		t.Error("the code folder must be deleted")
	}
	if len(e.b.MCP.Status()) != 0 {
		t.Errorf("status: %+v", e.b.MCP.Status())
	}
}

func TestCreateServerRefusesBadInput(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	cases := map[string]map[string]any{
		"bad id":          {"id": "../x", "code": goodServer},
		"nothing to run":  {"id": "a"},
		"code and url":    {"id": "a", "code": goodServer, "url": "https://x.example/mcp"},
		"no helper":       {"id": "a", "code": "print('hi')\nsrv.run()"},
		"never runs":      {"id": "a", "code": "from relay_mcp import Server\nsrv = Server('a')"},
		"syntax error":    {"id": "a", "code": "from relay_mcp import Server\nsrv = Server('a'\nsrv.run()"},
		"file escapes":    {"id": "a", "code": goodServer, "files": map[string]string{"../evil.py": "x"}},
		"absolute file":   {"id": "a", "code": goodServer, "files": map[string]string{"/etc/x": "x"}},
		"reserved file":   {"id": "a", "code": goodServer, "files": map[string]string{"relay_mcp.py": "x"}},
		"bad oauth":       {"id": "a", "code": goodServer, "oauth": map[string]any{"authorize_url": "http://plain.example/a", "token_url": "https://x/t", "client_id": "c"}},
		"bad kind":        {"id": "a", "code": goodServer, "kind": "robot"},
		"http url scheme": {"id": "a", "url": "ftp://x"},
	}
	for name, args := range cases {
		if out, err := e.run(t, ToolCreateServer, args); err == nil {
			t.Errorf("%s: want an error, got %q", name, out)
		}
	}
	if st := e.b.MCP.Status(); len(st) != 0 {
		t.Errorf("nothing may be registered after errors: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(e.home, "evil.py")); err == nil {
		t.Error("a file must not escape the folder")
	}
}

func TestACrashingServerIsReportedWithItsMessage(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	code := "from relay_mcp import Server\nraise RuntimeError('missing GITHUB_TOKEN')\nSrv = Server('a')\nSrv.run()\n"
	out, err := e.run(t, ToolCreateServer, map[string]any{"id": "crash", "code": code})
	if err == nil || !strings.Contains(err.Error(), "missing GITHUB_TOKEN") || !strings.Contains(err.Error(), "replace=true") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestConnectorWithOAuthAsksForTheSignIn(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	out, err := e.run(t, ToolCreateServer, map[string]any{
		"id": "svc", "kind": "connector", "code": goodServer,
		"oauth": map[string]any{"authorize_url": "https://a.example/auth", "token_url": "https://a.example/token", "client_id": "cid", "client_secret": "${SVC_SECRET}"},
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "authorize_connector") || !strings.Contains(out, "http://127.0.0.1:8787/oauth/callback") {
		t.Fatalf("the answer must explain the sign-in and the callback address:\n%s", out)
	}
	if st := e.b.MCP.Status()[0]; st.Kind != mcp.KindConnector || !st.OAuth {
		t.Fatalf("%+v", st)
	}
}

func TestAuthorizeConnectorAsksTheAgentToSignIn(t *testing.T) {
	e := newEnv(t)
	if _, err := e.run(t, ToolAuthorize, map[string]any{"id": "nope"}); err == nil || !strings.Contains(err.Error(), "no server") {
		t.Fatalf("err=%v", err)
	}
}

func TestCreateSkillAndRemove(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, ToolCreateSkill, map[string]any{
		"name": "release-notes", "description": "Write release notes. Use when the user asks for a changelog.",
		"instructions": "# Release notes\n\n1. Group the changes.\n2. See references/style.md.\n",
		"files":        map[string]string{"references/style.md": "Short lines."},
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s, ok := e.b.Skills.Get("release-notes")
	if !ok || s.Builtin || !strings.Contains(s.Description, "changelog") {
		t.Fatalf("%+v", s)
	}
	if text, err := e.b.Skills.ReadFile("release-notes", "references/style.md"); err != nil || text != "Short lines." {
		t.Fatalf("%q %v", text, err)
	}
	if body, _ := e.b.Skills.Body("release-notes"); !strings.Contains(body, "Group the changes") {
		t.Fatalf("body=%q", body)
	}
	if _, err := e.run(t, ToolCreateSkill, map[string]any{"name": "release-notes", "description": "Write release notes again, please.", "instructions": "x"}); err == nil {
		t.Error("an existing skill needs replace=true")
	}
	for name, args := range map[string]map[string]any{
		"bad name":      {"name": "Release Notes", "description": "A long enough description.", "instructions": "x"},
		"short desc":    {"name": "a", "description": "x", "instructions": "x"},
		"no text":       {"name": "a", "description": "A long enough description.", "instructions": " "},
		"builtin name":  {"name": "relay-builder", "description": "A long enough description.", "instructions": "x"},
		"file escapes":  {"name": "a", "description": "A long enough description.", "instructions": "x", "files": map[string]string{"../x": "y"}},
		"reserved file": {"name": "a", "description": "A long enough description.", "instructions": "x", "files": map[string]string{"SKILL.md": "y"}},
	} {
		if _, err := e.run(t, ToolCreateSkill, args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := e.run(t, ToolRemove, map[string]any{"kind": "skill", "name": "relay-builder"}); err == nil {
		t.Error("a built-in skill cannot be removed")
	}
	if _, err := e.run(t, ToolRemove, map[string]any{"kind": "skill", "name": "release-notes"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.b.Skills.Get("release-notes"); ok {
		t.Error("the skill must be gone")
	}
}

func TestDescribeSummarisesWhatTheCodeCanDo(t *testing.T) {
	e := newEnv(t)
	code := "import subprocess\nfrom relay_mcp import http\nhttp('GET','https://api.github.com/x')\nopen('f','w')\nsrv.run()"
	raw, _ := json.Marshal(map[string]any{"id": "gh", "code": code, "env": map[string]string{"TOKEN": "${TOKEN}"}})
	d := e.tool(ToolCreateServer).Describe(raw)
	for _, want := range []string{"api.github.com", "RUNS PROGRAMS", "writes or deletes files", "env: TOKEN", filepath.Join("connectors", "gh")} {
		if !strings.Contains(d, want) {
			t.Errorf("the approval text must say %q:\n%s", want, d)
		}
	}
	quiet, _ := json.Marshal(map[string]any{"id": "q", "code": "from relay_mcp import Server\nsrv.run()"})
	if d := e.tool(ToolCreateServer).Describe(quiet); !strings.Contains(d, "contacts no web address") || strings.Contains(d, "RUNS PROGRAMS") {
		t.Errorf("describe: %s", d)
	}
}

func TestSDKIsEmbedded(t *testing.T) {
	if !strings.Contains(string(SDK()), "class Server") || !strings.Contains(string(SDK()), "def oauth_token") {
		t.Fatal("relay_mcp.py must be built into Relay")
	}
}

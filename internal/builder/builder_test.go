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

// goodSkill is the skill that every server needs.
var goodSkill = map[string]any{
	"description":  "Greeting tools of the demo server. Use them when the user wants to be greeted.",
	"instructions": "Use mcp__demo__greet with the name of the person. Use mcp__demo__fail only to test errors.",
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

func TestBuilderToolsAreDescribedAndGuarded(t *testing.T) {
	e := newEnv(t)
	want := map[string]bool{ToolCreateServer: true, ToolValidateServer: true, ToolCreateSkill: true, ToolRemove: true, ToolList: false, ToolReadServer: false, ToolAuthorize: false}
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
		oo, isOnce := tl.(tools.OnceOnly)
		once := isOnce && oo.OnceOnly()
		if once {
			t.Errorf("%s: \"always allow\" must be remembered for the session", sp.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(sp.Schema, &schema); err != nil || schema["type"] != "object" || len(sp.Description) < 40 {
			t.Errorf("%s: schema %s / description %q", sp.Name, sp.Schema, sp.Description)
		}
	}
}

func TestBuildingNeedsTheOfferTheGuideAndRefusalCancels(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{ToolCreateServer, ToolValidateServer, ToolCreateSkill} {
		tl := e.tool(name)
		if no, ok := tl.(tools.NeedsOffer); !ok || !no.NeedsOffer() {
			t.Errorf("%s: the user must be asked first (propose_extension)", name)
		}
		if c, ok := tl.(tools.CancelOnDeny); !ok || !c.CancelsRequest() {
			t.Errorf("%s: a refusal cancels the whole request", name)
		}
		nr, ok := tl.(tools.NeedsRead)
		if !ok || len(nr.NeedsRead([]byte(`{}`))) == 0 {
			t.Errorf("%s: the guide must be read first", name)
		}
	}
	nr := e.tool(ToolCreateServer).(tools.NeedsRead)
	if got := strings.Join(nr.NeedsRead([]byte(`{"code":"x"}`)), ","); got != "guide,sdk" {
		t.Errorf("a program needs the guide and the SDK: %s", got)
	}
	if got := strings.Join(nr.NeedsRead([]byte(`{"url":"https://x.example/mcp"}`)), ","); got != "guide" {
		t.Errorf("a remote server has no SDK to read: %s", got)
	}
}
func TestCreateServerEndToEnd(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	out, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "name": "Demo", "code": goodServer, "skill": goodSkill})
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
	if _, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "code": goodServer, "skill": goodSkill}); err == nil || !strings.Contains(err.Error(), "replace=true") {
		t.Fatalf("err=%v", err)
	}
	// replacing runs the new code
	newCode := strings.Replace(goodServer, `"hello " + name`, `"bonjour " + name`, 1)
	if out, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "code": newCode, "skill": goodSkill, "replace": true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if text, _ := e.b.MCP.CallTool(context.Background(), "demo", "greet", json.RawMessage(`{"name":"Ann"}`)); text != "bonjour Ann" {
		t.Fatalf("the new code must run, got %q", text)
	}

	// the skill was written together with the server, and lists its tools exactly
	sk, ok := e.b.Skills.Get("demo")
	if !ok || sk.Metadata["server"] != "demo" {
		t.Fatalf("the companion skill is missing: %+v", sk)
	}
	body, _ := e.b.Skills.Body("demo")
	if !strings.Contains(body, "Use mcp__demo__greet") || !strings.Contains(body, "`mcp__demo__greet`: Greet someone by name.") {
		t.Fatalf("the skill must have the instructions and the list of tools:\n%s", body)
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
	if _, ok := e.b.Skills.Get("demo"); ok {
		t.Error("the skill that was written with the server must go with it")
	}
}

func TestCreateServerRefusesBadInput(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	cases := map[string]map[string]any{
		"bad id":          {"id": "../x", "code": goodServer, "skill": goodSkill},
		"no skill":        {"id": "a", "code": goodServer},
		"empty skill":     {"id": "a", "code": goodServer, "skill": map[string]any{"description": "Short.", "instructions": "x"}},
		"skill name":      {"id": "a", "code": goodServer, "skill": map[string]any{"name": "Bad Name", "description": goodSkill["description"], "instructions": goodSkill["instructions"]}},
		"nothing to run":  {"id": "a", "skill": goodSkill},
		"code and url":    {"id": "a", "code": goodServer, "url": "https://x.example/mcp", "skill": goodSkill},
		"no helper":       {"id": "a", "code": "print('hi')\nsrv.run()", "skill": goodSkill},
		"never runs":      {"id": "a", "code": "from relay_mcp import Server\nsrv = Server('a')", "skill": goodSkill},
		"syntax error":    {"id": "a", "code": "from relay_mcp import Server\nsrv = Server('a'\nsrv.run()", "skill": goodSkill},
		"file escapes":    {"id": "a", "code": goodServer, "skill": goodSkill, "files": map[string]string{"../evil.py": "x"}},
		"absolute file":   {"id": "a", "code": goodServer, "skill": goodSkill, "files": map[string]string{"/etc/x": "x"}},
		"reserved file":   {"id": "a", "code": goodServer, "skill": goodSkill, "files": map[string]string{"relay_mcp.py": "x"}},
		"bad oauth":       {"id": "a", "code": goodServer, "skill": goodSkill, "oauth": map[string]any{"authorize_url": "http://plain.example/a", "token_url": "https://x/t", "client_id": "c"}},
		"bad kind":        {"id": "a", "code": goodServer, "skill": goodSkill, "kind": "robot"},
		"oauth unused":    {"id": "a", "code": goodServer, "skill": goodSkill, "oauth": map[string]any{"authorize_url": "https://a.example/auth", "token_url": "https://a.example/token", "client_id": "c"}},
		"oauth missing":   {"id": "a", "code": strings.Replace(goodServer, "return \"hello \" + name", "from relay_mcp import http\n    return http('GET', 'https://x.example', oauth=True)", 1), "skill": goodSkill},
		"http url scheme": {"id": "a", "url": "ftp://x", "skill": goodSkill},
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
	out, err := e.run(t, ToolCreateServer, map[string]any{"id": "crash", "code": code, "skill": goodSkill})
	if err == nil || !strings.Contains(err.Error(), "missing GITHUB_TOKEN") || !strings.Contains(err.Error(), "replace=true") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestConnectorWithOAuthAsksForTheSignIn(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	out, err := e.run(t, ToolCreateServer, map[string]any{
		"id": "svc", "kind": "connector", "code": goodServer, "skill": goodSkill,
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
	for _, want := range []string{"api.github.com", "RUNS PROGRAMS", "writes or deletes files", "env: TOKEN", "add the tool capability"} {
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

func TestAFailedServerWritesNoSkillYet(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	code := "from relay_mcp import Server\nraise RuntimeError('boom')\nSrv = Server('a')\nSrv.run()\n"
	if _, err := e.run(t, ToolCreateServer, map[string]any{"id": "bad", "code": code, "skill": goodSkill}); err == nil {
		t.Fatal("want an error")
	}
	if _, ok := e.b.Skills.Get("bad"); ok {
		t.Error("the skill is written only when the server starts")
	}
}

func TestOfferToBuild(t *testing.T) {
	e := newEnv(t)
	p := e.b.Propose()
	if !p.NeedsApproval() {
		t.Error("the offer is a question to the user")
	}
	if oo, ok := p.(tools.OnceOnly); ok && oo.OnceOnly() {
		t.Error("\"always allow\" is remembered for the session")
	}
	if bo, ok := p.(tools.BuildOffer); !ok || !bo.IsBuildOffer() {
		t.Error("the agent must know that this tool is the offer")
	}
	if c, ok := p.(tools.CancelOnDeny); !ok || !c.CancelsRequest() {
		t.Error("a refusal cancels the whole request")
	}
	d, ok := p.(tools.Denier)
	if !ok || !strings.Contains(d.DeniedMessage(), "cancelled") || !strings.Contains(d.DeniedMessage(), "another way") {
		t.Errorf("the agent must be told to stop")
	}
	raw, _ := json.Marshal(map[string]any{"what": "read your Google Calendar", "kind": "connector"})
	if desc := p.Describe(raw); !strings.Contains(desc, "no connector") || !strings.Contains(desc, "Google Calendar") || !strings.Contains(desc, "cancelled") {
		t.Errorf("describe: %s", desc)
	}
	fix, _ := json.Marshal(map[string]any{"what": "the browser tool cannot open a window", "kind": "fix"})
	if desc := p.Describe(fix); !strings.Contains(desc, "repair") || !strings.Contains(desc, "browser tool") {
		t.Errorf("describe a fix: %s", desc)
	}
	out, err := p.Run(context.Background(), raw)
	if err != nil || !strings.Contains(out, "relay-builder") || !strings.Contains(out, "references/sdk.md") || !strings.Contains(out, "read_server") {
		t.Errorf("after the user agreed the agent must be sent to read first: %v %q", err, out)
	}
	for _, tl := range e.b.Tools() {
		if tl.Spec().Name == ToolPropose {
			t.Error("the offer is not among the builder tools: the user can switch it off")
		}
	}
}
func TestProgressIsReported(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	var labels []string
	ctx := tools.WithProgress(context.Background(), func(label string, stage, stages int) {
		labels = append(labels, label)
		if stage < 1 || stage > stages {
			t.Errorf("stage %d of %d", stage, stages)
		}
	})
	raw, _ := json.Marshal(map[string]any{"id": "demo", "code": goodServer, "skill": goodSkill})
	if _, err := e.tool(ToolCreateServer).Run(ctx, raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"Writing the files", "Checking the code", "Starting the server", "Writing the skill"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("stages: %v", labels)
	}
}

func TestAnOAuthBlockWithoutTheTokenIsExplained(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	_, err := e.run(t, ToolCreateServer, map[string]any{"id": "tg", "kind": "connector", "code": goodServer, "skill": goodSkill,
		"env":   map[string]string{"TELEGRAM_BOT_TOKEN": "${TELEGRAM_BOT_TOKEN}"},
		"oauth": map[string]any{"authorize_url": "https://a.example/auth", "token_url": "https://a.example/token", "client_id": "c"}})
	if err == nil || !strings.Contains(err.Error(), "bot token") || !strings.Contains(err.Error(), "secret(") {
		t.Fatalf("a bot token is not OAuth, and the answer must say what to do: %v", err)
	}
}

func TestMissingVariablesAreReportedWithWhatToDo(t *testing.T) {
	needPython(t)
	t.Setenv("RELAY_TEST_PRESENT", "x")
	e := newEnv(t)
	out, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "code": goodServer, "skill": goodSkill,
		"env": map[string]string{"A": "${RELAY_TEST_MISSING}", "B": "${RELAY_TEST_PRESENT}"}})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "RELAY_TEST_MISSING") || strings.Contains(out, "RELAY_TEST_PRESENT") || !strings.Contains(out, "relay stop") {
		t.Fatalf("the answer must name the missing variable (only it) and say to restart Relay:\n%s", out)
	}
}

func TestReadServerShowsTheCodeForARepair(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	if _, err := e.run(t, ToolCreateServer, map[string]any{"id": "demo", "code": goodServer, "skill": goodSkill,
		"files": map[string]string{"helpers.py": "VALUE = 42\n"}}); err != nil {
		t.Fatal(err)
	}
	out, err := e.run(t, ToolReadServer, map[string]any{"id": "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "### server.py") || !strings.Contains(out, "def greet(name)") || !strings.Contains(out, "### helpers.py") || !strings.Contains(out, "VALUE = 42") {
		t.Fatalf("the code must be shown, server.py first:\n%s", out)
	}
	if strings.Contains(out, "class Server") {
		t.Error("the helper (relay_mcp.py) is not shown: it is Relay's, not the model's")
	}
	for _, bad := range []string{"nope", "../x", ""} {
		if _, err := e.run(t, ToolReadServer, map[string]any{"id": bad}); err == nil {
			t.Errorf("%q must not be readable", bad)
		}
	}
	if tl := e.tool(ToolReadServer); tl.NeedsApproval() {
		t.Error("reading needs no question")
	}
}

func TestListExtensionsTellsWhereRelayRuns(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, ToolList, map[string]any{})
	if err != nil || !strings.Contains(out, "graphical desktop:") || !strings.Contains(out, "Python:") || !strings.Contains(out, "this computer") {
		t.Fatalf("the model must be told that this is the user's computer, and what it has: %v\n%s", err, out)
	}
}

func TestValidateMCPProjectBeforeInstall(t *testing.T) {
	needPython(t)
	e := newEnv(t)
	out, err := e.run(t, ToolValidateServer, map[string]any{"id": "draft-demo", "code": goodServer})
	if err != nil {
		t.Fatalf("validation failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Validation passed") || !strings.Contains(out, "greet") {
		t.Fatalf("unexpected result: %s", out)
	}
	if _, err := os.Stat(filepath.Join(e.home, "connectors")); !os.IsNotExist(err) {
		t.Fatalf("validation must not create the connectors folder: %v", err)
	}
	if _, err := os.Stat(e.b.MCP.Path()); !os.IsNotExist(err) {
		t.Fatalf("validation must not register the draft: %v", err)
	}
}

func TestDependencySpecificationsAreRestricted(t *testing.T) {
	for _, spec := range []string{"requests>=2.31,<3", "beautifulsoup4==4.12.3", "pydantic[email]>=2"} {
		if err := validateDependencies([]string{spec}); err != nil {
			t.Errorf("valid dependency %q rejected: %v", spec, err)
		}
	}
	for _, spec := range []string{"https://example.com/pkg.whl", "-r requirements.txt", "../local-package", "git+https://example.com/repo.git"} {
		if err := validateDependencies([]string{spec}); err == nil {
			t.Errorf("unsafe dependency %q accepted", spec)
		}
	}
	if err := validateDependencies([]string{"Requests>=2", "requests==3"}); err == nil {
		t.Fatal("duplicate package names with different version constraints were accepted")
	}
}

func TestReadServerSkipsPrivateVirtualEnvironment(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.home, "connectors", "demo")
	for name, body := range map[string]string{
		"server.py":        "srv.run()\n",
		"api.py":           "def call(): return True\n",
		"requirements.txt": "requests>=2\n",
		filepath.Join(".venv", "lib", "site-packages", "huge_dependency.py"): "not project source\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.run(t, ToolReadServer, map[string]any{"id": "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "requirements.txt") || !strings.Contains(out, "api.py") || strings.Contains(out, "huge_dependency.py") {
		t.Fatalf("unexpected server source listing: %s", out)
	}
}

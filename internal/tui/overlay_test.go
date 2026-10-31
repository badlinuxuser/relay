package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/proto"
)

func sessionList() []proto.SessionInfo {
	now := time.Now()
	return []proto.SessionInfo{
		{ID: "s1", Title: "current chat", Mode: "small", Updated: now},
		{ID: "s2", Title: "New session", Mode: "small", Updated: now},
		{ID: "s3", Title: "older chat", Mode: "hard", Updated: now.Add(-time.Hour)},
	}
}

func TestSessionsOpenAsListWithoutEmptyOnes(t *testing.T) {
	m := testModel()
	next, _ := m.Update(sessionsMsg{list: sessionList()})
	m = next.(model)
	if m.ov.kind != ovSessions || len(m.ov.items) != 2 {
		t.Fatalf("overlay: %+v", m.ov)
	}
	out := m.View()
	for _, want := range []string{"sessions 1/2", "current chat", "older chat", "current"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if strings.Contains(out, "New session") {
		t.Error("empty sessions are hidden")
	}
	if n := len(strings.Split(out, "\n")); n != m.height {
		t.Errorf("height %d, want %d", n, m.height)
	}
}

func TestOverlayKeys(t *testing.T) {
	m := testModel()
	m.openSessions(sessionList())
	if h, _ := m.overlayKey("down"); !h || m.ov.sel != 1 {
		t.Fatalf("down: %+v", m.ov)
	}
	m.overlayKey("down")
	if m.ov.sel != 0 {
		t.Error("wraps around")
	}
	if h, _ := m.overlayKey("x"); !h {
		t.Error("letters do not leak into the input while a list is open")
	}
	m.overlayKey("esc")
	if m.ov.kind != ovNone {
		t.Error("esc closes the list")
	}
	if h, _ := m.overlayKey("a"); h {
		t.Error("with no list the key goes on")
	}
}

func TestStopListShowsOnlyRunning(t *testing.T) {
	m := testModel()
	m.openStopList([]proto.ProcInfo{
		{Name: "ollama", State: "ready", External: true},
		{Name: "llamacpp", State: "stopped"},
	})
	if m.ov.kind != ovStop || len(m.ov.items) != 1 || m.ov.items[0].id != "ollama" {
		t.Fatalf("overlay: %+v", m.ov)
	}
	if !strings.Contains(m.View(), "running outside Relay") {
		t.Error("external servers are marked")
	}
	m = testModel()
	m.openStopList([]proto.ProcInfo{{Name: "ollama", State: "stopped"}})
	if m.ov.kind != ovNone || !strings.Contains(m.entries[len(m.entries)-1].text, "Nothing is running") {
		t.Errorf("empty: %+v", m.entries)
	}
}

// listRow finds the screen row of a text.
func listRow(m model, text string) int {
	for y, l := range strings.Split(m.View(), "\n") {
		if strings.Contains(l, text) {
			return y
		}
	}
	return -1
}

func TestClickOnSessionRowChoosesIt(t *testing.T) {
	m := testModel()
	m.openSessions(sessionList())
	y := listRow(m, "older chat")
	if y < 0 {
		t.Fatal("row not drawn")
	}
	ok, cmd := m.click(5, y)
	if !ok || cmd == nil || m.ov.kind != ovNone {
		t.Errorf("click: ok=%v cmd=%v ov=%+v", ok, cmd != nil, m.ov)
	}
	// a click outside the rows does nothing
	m = testModel()
	m.openSessions(sessionList())
	if ok, _ := m.click(5, 1); ok || m.ov.kind == ovNone {
		t.Error("a click on the feed must not close or choose")
	}
}

func TestMouseLeftClickGoesThroughUpdate(t *testing.T) {
	m := testModel()
	m.openStopList([]proto.ProcInfo{{Name: "ollama", State: "ready"}})
	y := listRow(m, "ollama")
	_, cmd := m.Update(tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Error("clicking a process must send the stop request")
	}
}

func TestApprovalRowsAreClickable(t *testing.T) {
	m := approvalModel()
	y := listRow(m, "Deny")
	if y < 0 {
		t.Fatal("Deny row not drawn")
	}
	ok, cmd := m.click(3, y)
	if !ok || m.approval != nil || cmd == nil {
		t.Errorf("click on Deny: ok=%v approval=%v", ok, m.approval)
	}
	m = approvalModel()
	if ok, cmd := m.click(3, 1); !ok || cmd != nil || m.approval == nil {
		t.Error("a click elsewhere keeps the question open")
	}
}

func TestApprovalKeysGoThroughHandleKey(t *testing.T) {
	m := approvalModel()
	next, cmd := m.handleKey(tea.KeyMsg{S: "x"})
	if cmd != nil || next.(model).approval == nil {
		t.Error("other keys do nothing")
	}
	next, cmd = m.handleKey(tea.KeyMsg{S: "y"})
	if cmd == nil || next.(model).approval != nil {
		t.Error("y allows")
	}
}

func TestProcessCommands(t *testing.T) {
	m := testModel()
	m.status = &proto.StatusResp{Processes: []proto.ProcInfo{{Name: "ollama"}, {Name: "llamacpp"}}}
	for _, line := range []string{"/ollama", "/llamacpp ~/models/q.gguf", `/llamacpp "C:\my models\q.gguf"`} {
		if _, cmd := m.command(line); cmd == nil {
			t.Errorf("%s must start the process", line)
		}
	}
	if _, cmd := m.command("/stop"); cmd == nil {
		t.Error("/stop opens the list")
	}
	next, cmd := m.command("/start ollama")
	e := next.(model).entries
	if cmd != nil || !strings.Contains(e[len(e)-1].text, "/ollama") {
		t.Errorf("/start is gone: %+v", e[len(e)-1])
	}
	next, _ = m.command("/nosuch")
	e = next.(model).entries
	if e[len(e)-1].kind != eError {
		t.Error("unknown commands are an error")
	}
	if strings.Contains(helpText, "/start") {
		t.Error("help text still mentions /start")
	}
}

func TestProcessArgs(t *testing.T) {
	for _, c := range []struct {
		name, rest string
		want       string
	}{
		{"ollama", "", ""},
		{"llamacpp", "", ""},
		{"llamacpp", "/m/q.gguf", "-m|/m/q.gguf"},
		{"llamacpp", `"C:\my models\q.gguf"`, `-m|C:\my models\q.gguf`},
		{"llamacpp", "-hf org/repo", "-hf|org/repo"},
		{"ollama", "serve --x", "serve|--x"},
	} {
		if got := strings.Join(processArgs(c.name, c.rest), "|"); got != c.want {
			t.Errorf("%s %q -> %q, want %q", c.name, c.rest, got, c.want)
		}
	}
}

func TestProcMsgNoticesAndFailures(t *testing.T) {
	m := testModel()
	next, _ := m.Update(procMsg{name: "ollama", action: "start", already: true})
	if !strings.Contains(next.(model).entries[len(next.(model).entries)-1].text, "already running") {
		t.Error("already running notice")
	}
	next, _ = m.Update(procMsg{name: "ollama", action: "start"})
	if mm := next.(model); mm.statusBurst == 0 {
		t.Error("a start speeds up status polling")
	}
	st := &proto.StatusResp{Processes: []proto.ProcInfo{{Name: "llamacpp", State: "failed", Log: []string{"a", "error: bad model"}}}}
	next, _ = m.Update(statusMsg{st: st})
	next, _ = next.(model).Update(statusMsg{st: st})
	n := 0
	for _, e := range next.(model).entries {
		if e.kind == eError && strings.Contains(e.text, "bad model") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("a failure is reported once, got %d", n)
	}
}

// The mascot must stay on a normal terminal (about 38 rows) even with the processes block.
func TestMascotFitsWithProcessesBlock(t *testing.T) {
	m := testModel()
	m.width, m.height = 160, 38
	m.status = &proto.StatusResp{Servers: []proto.ServerInfo{{Name: "ollama", Models: 6}}}
	out := m.View()
	if !strings.Contains(out, "processes") || !strings.Contains(out, "ollama 6 models") {
		t.Error("processes block cut off")
	}
	if !strings.Contains(out, "▀") {
		t.Error("the mascot disappeared")
	}
}

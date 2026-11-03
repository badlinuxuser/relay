package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/proto"
)

func TestInputWrapsLongTextAndGrowsTheBox(t *testing.T) {
	m := testModel()
	m.width, m.height = 60, 24
	h1 := m.bodyHeight()
	m.input.SetValue(strings.Repeat("слово ", 30)) // 180 characters
	if got := m.inputHeight(); got < 3 || got > inputMaxLines {
		t.Fatalf("input height %d", got)
	}
	if m.bodyHeight() >= h1 {
		t.Error("the feed gets shorter when the input grows")
	}
	out := m.View()
	if n := len(strings.Split(out, "\n")); n != m.height {
		t.Errorf("screen height %d, want %d", n, m.height)
	}
	for _, l := range strings.Split(out, "\n") {
		if runeLen(stripANSI(l)) > m.width {
			t.Errorf("line wider than the screen: %q", stripANSI(l))
		}
	}
	// words are not cut in two
	r := []rune(m.input.Value())
	for _, l := range wrapInput(r, 20) {
		if l.e < len(r) && r[l.e-1] != ' ' && r[l.e] != ' ' {
			t.Errorf("a word was cut at %d", l.e)
		}
	}
	// one very long word is cut by force
	if ls := wrapInput([]rune(strings.Repeat("x", 50)), 20); len(ls) != 3 {
		t.Errorf("long word: %v", ls)
	}
}

func TestCommandHints(t *testing.T) {
	m := testModel()
	if g := m.commandGhost("/mod"); !strings.HasPrefix(g, "els") || !strings.Contains(g, "choose a model") {
		t.Errorf("ghost for /mod: %q", g)
	}
	if g := m.commandGhost("/mode "); !strings.Contains(g, "small|medium|hard|maximum") {
		t.Errorf("args hint: %q", g)
	}
	if m.commandGhost("hello") != "" || m.commandGhost("/zzz") != "" || m.commandGhost("/mode small") != "" {
		t.Error("no hint for plain text, unknown commands or finished arguments")
	}
	m.input.SetValue("/mod")
	if !strings.Contains(m.View(), "choose a model") {
		t.Error("the hint is drawn after the text")
	}
	if done, ok := m.completeCommand("/llama"); !ok || done != "/llamacpp " {
		t.Errorf("complete: %q %v", done, ok)
	}
	if done, ok := m.completeCommand("/new"); ok || done != "/new" {
		t.Errorf("a finished command stays: %q %v", done, ok)
	}
	m.input.SetValue("/ollam")
	next, _ := m.handleKey(tea.KeyMsg{S: "tab"})
	if got := next.(model).input.Value(); got != "/ollama" {
		t.Errorf("tab: %q", got)
	}
	m.status = &proto.StatusResp{Processes: []proto.ProcInfo{{Name: "lmstudio"}}}
	if g := m.commandGhost("/lmst"); !strings.Contains(g, "start lmstudio") {
		t.Errorf("config processes get a command: %q", g)
	}
}

func TestHistoryWithArrows(t *testing.T) {
	m := testModel()
	m.remember("first")
	m.remember("/new")
	m.remember("/new") // not repeated
	if len(m.hist) != 2 {
		t.Fatalf("history: %v", m.hist)
	}
	m.historyKey("up")
	if m.input.Value() != "/new" {
		t.Errorf("up: %q", m.input.Value())
	}
	m.historyKey("up")
	m.historyKey("up") // stays on the oldest
	if m.input.Value() != "first" {
		t.Errorf("oldest: %q", m.input.Value())
	}
	m.historyKey("down")
	m.historyKey("down")
	if m.input.Value() != "" || m.histPos != -1 {
		t.Errorf("down past the newest empties the line: %q", m.input.Value())
	}
	m.input.SetValue("typing")
	if m.historyKey("up") {
		t.Error("with text typed the arrows are not history")
	}
	// pgup / pgdn only scroll
	m.input.SetValue("")
	m.scrollKey("pgup")
	if m.input.Value() != "" {
		t.Error("pgup must not touch the input")
	}
}

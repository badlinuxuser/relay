package tui

import (
	"strings"
	"testing"

	"relay/internal/proto"
	"relay/internal/settings"
)

func withSettings(m model, f func(*settings.Settings)) model {
	s := settings.Default()
	f(&s)
	m.status = &proto.StatusResp{Settings: s}
	return m
}

func lastKinds(m model) []entryKind {
	var out []entryKind
	for _, e := range m.entries {
		out = append(out, e.kind)
	}
	return out
}

func TestThoughtsAreSeparatedFromTheAnswer(t *testing.T) {
	m := withSettings(testModel(), func(s *settings.Settings) { s.StreamSpeed = "instant" })
	m.handleEvent(proto.Event{Type: "model", Model: "gemini/gemma"})
	m.handleEvent(proto.Event{Type: "thinking", Text: "The user greets me. "})
	m.handleEvent(proto.Event{Type: "thinking", Text: "I should answer in Russian."})
	m.handleEvent(proto.Event{Type: "text", Text: "Привет! "})
	m.handleEvent(proto.Event{Type: "text", Text: "Чем помочь?"})
	e := m.entries
	n := len(e)
	if e[n-2].kind != eThought || e[n-1].kind != eAssistant {
		t.Fatalf("kinds: %v", lastKinds(m))
	}
	if e[n-2].text != "The user greets me. I should answer in Russian." || e[n-1].text != "Привет! Чем помочь?" {
		t.Errorf("texts: %q / %q", e[n-2].text, e[n-1].text)
	}
	m.width, m.height = 120, 30
	out := m.View()
	if !strings.Contains(out, "thinking") || !strings.Contains(out, "┊ The user greets me.") || !strings.Contains(out, "Привет! Чем помочь?") {
		t.Errorf("view:\n%s", out)
	}
}

func TestThoughtsOffAreDropped(t *testing.T) {
	m := withSettings(testModel(), func(s *settings.Settings) { s.StreamSpeed = "instant"; s.ShowThoughts = false })
	m.handleEvent(proto.Event{Type: "thinking", Text: "hidden reasoning"})
	m.handleEvent(proto.Event{Type: "text", Text: "answer"})
	for _, e := range m.entries {
		if e.kind == eThought || strings.Contains(e.text, "hidden") {
			t.Errorf("thoughts must not show: %+v", e)
		}
	}
}

func TestTypewriterDrainsGraduallyAndKeepsOrder(t *testing.T) {
	m := withSettings(testModel(), func(s *settings.Settings) { s.StreamSpeed = "slow" })
	text := strings.Repeat("слово ", 60)
	m.handleEvent(proto.Event{Type: "text", Text: text})
	if m.entries[len(m.entries)-1].kind == eAssistant {
		t.Fatal("nothing is typed before the first tick")
	}
	m.drain()
	got := m.entries[len(m.entries)-1].text
	if got == "" || len([]rune(got)) >= len([]rune(text)) {
		t.Fatalf("one tick types a part, got %d of %d", len([]rune(got)), len([]rune(text)))
	}
	for i := 0; i < 500 && len(m.pend) > 0; i++ {
		m.drain()
	}
	if m.entries[len(m.entries)-1].text != text {
		t.Error("everything must arrive in the end, unchanged")
	}
	// a tool call waits for the queue: the text stays above it
	m.handleEvent(proto.Event{Type: "text", Text: "before the tool"})
	m.handleEvent(proto.Event{Type: "tool_call", Tool: "read_file", Desc: "x"})
	k := lastKinds(m)
	if k[len(k)-1] != eTool || m.entries[len(m.entries)-2].kind != eAssistant ||
		!strings.HasSuffix(m.entries[len(m.entries)-2].text, "before the tool") {
		t.Errorf("order: %v", k)
	}
}

func TestSettingsAffectInterface(t *testing.T) {
	m := withSettings(testModel(), func(s *settings.Settings) { s.Sidebar = "hidden" })
	if m.showSidebar() || strings.Contains(m.View(), "processes") {
		t.Error("the side panel is hidden")
	}
	m = withSettings(testModel(), func(s *settings.Settings) { s.Mascot = false })
	m.width, m.height = 160, 40
	if strings.Contains(m.View(), "▀") {
		t.Error("no mascot when it is switched off")
	}
	m = withSettings(testModel(), func(s *settings.Settings) { s.TextWidth = 40 })
	m.entries = append(m.entries, entry{kind: eAssistant, text: strings.Repeat("abcd ", 40)})
	for _, l := range m.transcript(100, newStyles(themeFor("small", 0))) {
		if runeLen(l) > 44 {
			t.Errorf("line wider than the text width: %d", runeLen(l))
		}
	}
}

func TestCopyAndSettingsCommands(t *testing.T) {
	m := testModel()
	m.entries = append(m.entries, entry{kind: eAssistant, text: "old"}, entry{kind: eThought, text: "hmm"}, entry{kind: eAssistant, text: "the answer"})
	if m.lastAnswer() != "the answer" {
		t.Errorf("lastAnswer = %q", m.lastAnswer())
	}
	if _, cmd := m.command("/copy"); cmd == nil {
		t.Error("/copy must produce a command")
	}
	if _, cmd := m.command("/thoughts off"); cmd == nil {
		t.Error("/thoughts saves the setting")
	}
	next, cmd := m.command("/mouse off")
	if cmd == nil || next.(model).mouseOn {
		t.Error("/mouse off switches the mouse off")
	}
	if onOff(nil, true) || !onOff([]string{"on"}, false) || onOff([]string{"off"}, true) {
		t.Error("onOff")
	}
}

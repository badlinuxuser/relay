package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func TestScrollKeysAndIndicator(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 20
	for i := 0; i < 60; i++ {
		m.addNotice("line of the conversation")
	}
	for _, key := range []string{"pgup", "shift+up", "alt+up"} {
		m.scroll = 0
		if !m.scrollKey(key) || m.scroll == 0 {
			t.Errorf("%s must scroll up", key)
		}
	}
	m.scroll = 0
	m.scrollKey("home")
	if m.scroll != m.maxScroll() || m.scroll == 0 {
		t.Errorf("home goes to the top: %d of %d", m.scroll, m.maxScroll())
	}
	if !strings.Contains(m.View(), "scrolled up") {
		t.Error("the hint line says the chat is scrolled")
	}
	m.scrollKey("end")
	if m.scroll != 0 {
		t.Error("end returns to the bottom")
	}
	// with text in the input these keys belong to the input
	m.input.SetValue("abc")
	if m.scrollKey("home") || m.scrollKey("ctrl+u") {
		t.Error("home / ctrl+u must edit the input when it has text")
	}
	if !m.scrollKey("pgup") {
		t.Error("pgup scrolls even with text in the input")
	}
}

func TestSessionTotalsFollowTheOpenSession(t *testing.T) {
	m := testModel()
	m.totIn, m.totOut, m.totCost = 5, 6, 0.5
	next, _ := m.Update(sessionMsg{sess: &proto.SessionInfo{ID: "a", Mode: "small", TokensIn: 100, TokensOut: 40, Cost: 0.02}, reset: true})
	mm := next.(model)
	if mm.totIn != 100 || mm.totOut != 40 || mm.totCost != 0.02 {
		t.Errorf("totals of the loaded session: %d/%d $%v", mm.totIn, mm.totOut, mm.totCost)
	}
	next, _ = mm.Update(sessionMsg{sess: &proto.SessionInfo{ID: "b", Mode: "small"}})
	if mm = next.(model); mm.totIn != 0 || mm.totCost != 0 {
		t.Errorf("a new session starts at zero: %d $%v", mm.totIn, mm.totCost)
	}
	m.openSessions([]proto.SessionInfo{{ID: "x", Title: "chat", Mode: "small", TokensIn: 900, TokensOut: 100, Cost: 0.0123}})
	if !strings.Contains(m.ov.items[0].desc, "1000 tok") || !strings.Contains(m.ov.items[0].desc, "$0.0123") {
		t.Errorf("desc: %q", m.ov.items[0].desc)
	}
}

func TestSelectModeFreezesTheScreen(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 30
	c := m.enterSelect()
	if !m.selecting || m.mouseOn || c == nil {
		t.Fatalf("select mode: selecting=%v mouse=%v cmd=%v", m.selecting, m.mouseOn, c != nil)
	}
	before := m.View()
	for i := 0; i < 50; i++ { // ticks must change nothing
		next, _ := m.Update(tickMsg{})
		m = next.(model)
	}
	if m.View() != before {
		t.Error("the screen must not change in select mode")
	}
	if !strings.Contains(before, "select mode") {
		t.Error("the hint line explains select mode")
	}
	next, c2 := m.handleKey(tea.KeyMsg{S: "x"})
	m = next.(model)
	if m.selecting || !m.mouseOn || c2 == nil || m.input.Value() != "" {
		t.Errorf("any key leaves select mode and is not typed: selecting=%v mouse=%v input=%q", m.selecting, m.mouseOn, m.input.Value())
	}
	// with the mouse switched off in the settings it stays off after select mode
	m = withSettings(testModel(), func(s *settings.Settings) { s.Mouse = false })
	m.mouseOn = false
	m.enterSelect()
	m.leaveSelect()
	if m.mouseOn {
		t.Error("the mouse setting is respected on leaving")
	}
	if _, cmd := testModel().command("/select"); cmd == nil {
		t.Error("/select")
	}
}

func TestTokenNumbersAreShortAndContextIsShown(t *testing.T) {
	for n, want := range map[int]string{950: "950", 12345: "12.3k", 7656641: "7.66M"} {
		if got := humanTokens(n); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
	m := testModel()
	m.width, m.height = 120, 40
	m.handleEvent(proto.Event{Type: "usage", In: 60000, Out: 500})
	m.handleEvent(proto.Event{Type: "usage", In: 62000, Out: 700})
	if m.totIn != 122000 || m.ctxTokens != 62000 {
		t.Errorf("total %d context %d", m.totIn, m.ctxTokens)
	}
	out := m.View()
	if !strings.Contains(out, "122.0k / 1200") || !strings.Contains(out, "context") {
		t.Errorf("sidebar:\n%s", out)
	}
}

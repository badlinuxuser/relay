package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"relay/internal/proto"
	"relay/internal/provider"
	"relay/internal/settings"
)

func TestCleanTextRemovesTerminalControlCodes(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[31mred\x1b[0m":                    "red",
		"a\x1b[2Jb":                             "ab",
		"\x1b[?1049hscreen":                     "screen",
		"\x1b]0;title\x07text":                  "text",
		"progress 10%\rprogress 50%\rdone 100%": "done 100%",
		"line\r\nnext":                          "line\nnext",
		"bell\x07 and null\x00":                 "bell and null",
		"tab\tkept\nand a new line":             "tab\tkept\nand a new line",
		"Привет, мир ✓":                         "Привет, мир ✓",
		"first\r\nsecond\rSECOND\nthird":        "first\nSECOND\nthird",
	} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
	// a result that is drawn must not carry a control code to the terminal
	out := strings.Join(renderEntry(entry{kind: eTool, tool: "run_shell", text: "go test", result: "\x1b[2J\x1b[Hok\r\x1b[31mFAIL\x1b[0m"}, 60, newStyles(themeFor("medium", 0))), "\n")
	if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[H") || strings.Contains(out, "\r") {
		t.Errorf("control codes got through: %q", out)
	}
}

func TestClippingKeepsTextValid(t *testing.T) {
	big := strings.Repeat("ж", maxStoredResult) // two bytes per rune
	c := clipResult(big)
	if !utf8.ValidString(c) || len(c) > maxStoredResult+8 {
		t.Errorf("clipResult: valid=%v len=%d", utf8.ValidString(c), len(c))
	}
	th := clipThought(strings.Repeat("я", maxThoughtText))
	if !utf8.ValidString(th) || len(th) > maxThoughtText {
		t.Errorf("clipThought: valid=%v len=%d", utf8.ValidString(th), len(th))
	}
	if clipResult("short") != "short" || clipThought("short") != "short" {
		t.Error("short texts stay as they are")
	}
}

func TestTakeRunes(t *testing.T) {
	head, rest := takeRunes("привет мир", 3)
	if head != "при" || rest != "вет мир" {
		t.Fatalf("%q %q", head, rest)
	}
	if h, r := takeRunes("ab", 5); h != "ab" || r != "" {
		t.Fatalf("%q %q", h, r)
	}
}

func TestWindowOfPicksTheRightLines(t *testing.T) {
	parts := [][]string{{"a", "b"}, {}, {"c"}, {"d", "e", "f"}}
	all := []string{"a", "b", "c", "d", "e", "f"}
	for start := 0; start <= len(all); start++ {
		for end := start; end <= len(all); end++ {
			if got := strings.Join(windowOf(parts, start, end), ","); got != strings.Join(all[start:end], ",") {
				t.Errorf("[%d,%d): %q", start, end, got)
			}
		}
	}
	if countLines(parts) != 6 {
		t.Error("countLines")
	}
}

func direct(m model, w int) []string {
	st := newStyles(themeFor(m.mode, 0))
	var all []string
	for i := range m.entries {
		all = append(all, m.renderOne(i, w, st)...)
	}
	return all
}

func TestFeedCacheGivesWhatDrawingDirectlyGives(t *testing.T) {
	m := New(false, false).(model)
	m.width, m.height = 120, 40
	m.entries = []entry{
		{kind: eUser, text: "hello there"},
		{kind: eAssistant, text: strings.Repeat("a long answer with words ", 20)},
		{kind: eTool, tool: "run_shell", text: "ls", result: "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight"},
		{kind: eNotice, text: "a notice"},
	}
	st := newStyles(themeFor(m.mode, 0))
	check := func(label string, w int) {
		t.Helper()
		got := strings.Join(m.transcript(w, st), "\n")
		if want := strings.Join(direct(m, w), "\n"); got != want {
			t.Errorf("%s: the cached feed differs from the direct one", label)
		}
	}
	check("first", 60)
	check("again (from the cache)", 60)
	// the last entry grows while the answer is typed
	m.entries[1].text += " and then it continues for a while"
	check("a changed entry is drawn again", 60)
	m.entries[2].result += "\nnine"
	check("a tool result that arrived", 60)
	check("another width", 44)
	// an entry that is replaced by another one of the same length
	m.entries[0].text = "HELLO THERE"
	check("replaced text of the same length", 60)
	// fewer entries (the feed was cleared) and then new ones
	m.entries = m.entries[:1]
	check("shorter feed", 60)
	m.entries = append(m.entries, entry{kind: eAssistant, text: "new"})
	check("grown again", 60)
}

func TestTheFeedWindowIsTheEndOfTheConversation(t *testing.T) {
	m := New(false, false).(model)
	m.width, m.height = 100, 30
	for i := 0; i < 400; i++ {
		m.entries = append(m.entries, entry{kind: eAssistant, text: "answer number " + strings.Repeat("x", i%30)})
	}
	st := newStyles(themeFor(m.mode, 0))
	rows := m.feedRows(m.mainWidth(), m.bodyHeight(), st)
	if len(rows) != m.bodyHeight() {
		t.Fatalf("%d rows for a body of %d", len(rows), m.bodyHeight())
	}
	if !strings.Contains(strings.Join(rows, "\n"), "answer number") {
		t.Error("the end of the conversation must be on the screen")
	}
	if m.maxScroll() <= 0 {
		t.Error("a long conversation scrolls")
	}
}

func TestEntriesFromSessionHideWhatRelayAddedForTheModel(t *testing.T) {
	s := &proto.SessionInfo{Title: "t", Messages: []provider.Message{
		{Role: "user", Content: "write a long text"},
		{Role: "assistant", Content: "part one"},
		{Role: "user", Content: provider.InternalPrefix + "Continue exactly where you stopped."},
		{Role: "assistant", Content: "part two"},
	}}
	var users int
	for _, e := range entriesFromSession(s) {
		if e.kind == eUser {
			users++
			if strings.Contains(e.text, "Continue exactly") {
				t.Error("the line for the model must not be shown as the user's")
			}
		}
	}
	if users != 1 {
		t.Errorf("users=%d", users)
	}
}

func buildModel(view string) model {
	m := New(false, false).(model)
	st := settings.Default()
	st.BuilderView = view
	m.status = &proto.StatusResp{Settings: st}
	m.streaming = true
	m.width, m.height = 100, 30
	return m
}

func TestBuildProgressFollowsTheEvents(t *testing.T) {
	m := buildModel("progress")
	st := newStyles(themeFor(m.mode, 0))
	if m.buildPanel(80, st) != nil {
		t.Fatal("no panel before anything is built")
	}
	feed := func(evs ...proto.Event) {
		for _, ev := range evs {
			m.trackBuild(ev)
		}
	}
	feed(proto.Event{Type: "tool_call", Tool: "load_skill", Args: `{"name":"relay-builder"}`})
	if !m.build.active || m.build.phase != 1 {
		t.Fatalf("loading the guide starts it: %+v", m.build)
	}
	feed(proto.Event{Type: "tool_result", Tool: "load_skill"})
	if m.build.phase != 2 || m.build.label != "" {
		t.Fatalf("the model designs now: %+v", m.build)
	}
	feed(proto.Event{Type: "tool_call", Tool: "create_mcp_server"})
	feed(proto.Event{Type: "progress", Tool: "create_mcp_server", Text: "Checking the code", Stage: 2, Stages: 4})
	if m.build.phase != 3 || m.build.label != "Checking the code" || m.build.frac <= 0 {
		t.Fatalf("%+v", m.build)
	}
	feed(proto.Event{Type: "progress", Tool: "create_mcp_server", Text: "Writing the skill", Stage: 4, Stages: 4})
	if m.build.phase != 4 {
		t.Fatalf("the skill is part of the build: %+v", m.build)
	}
	panel := m.buildPanel(80, st)
	if len(panel) != 2 || !strings.Contains(panel[1], "Writing the skill") || strings.Contains(panel[1], "4/5") || strings.Contains(panel[1], "Development") {
		t.Fatalf("panel: %q", panel)
	}
	if strings.ContainsAny(strings.Join(panel, ""), "█░") {
		t.Fatalf("no bar: %q", panel)
	}
	feed(proto.Event{Type: "tool_result", Tool: "create_mcp_server"})
	if m.build.phase != 5 {
		t.Fatalf("then it is tried: %+v", m.build)
	}
	// a failed build goes back to designing, and the bar never goes back for other reasons
	feed(proto.Event{Type: "tool_result", Tool: "create_mcp_server", IsError: true})
	if m.build.phase != 2 || m.build.label != "Fixing the problem" {
		t.Fatalf("%+v", m.build)
	}
	// a request that ends clears it (streamEndMsg), and a panel is only shown while streaming
	m.streaming = false
	if m.buildPanel(80, st) != nil {
		t.Error("no panel when nothing is running")
	}
}

func TestProgressReplacesTheThoughtsOnlyWhenSoSet(t *testing.T) {
	m := buildModel("progress")
	m.build = buildState{active: true, phase: 2}
	m.queue(true, "I will first think about the design…")
	if len(m.pend) != 0 || len(m.entries) > 1 {
		t.Fatalf("with the progress bar the reasoning is not shown: pend=%d entries=%d", len(m.pend), len(m.entries))
	}
	m.queue(false, "A short answer.")
	if len(m.pend) != 1 {
		t.Error("the answer itself is shown")
	}

	m = buildModel("thoughts")
	m.build = buildState{active: true, phase: 2}
	m.queue(true, "I will first think about the design…")
	if len(m.pend) != 1 {
		t.Error("with the setting \"thoughts\" the reasoning is shown as before")
	}
	if m.buildPanel(80, newStyles(themeFor(m.mode, 0))) != nil {
		t.Error("and there is no progress bar")
	}

	// outside of a build the reasoning is shown as before
	m = buildModel("progress")
	m.queue(true, "thinking about a normal question")
	if len(m.pend) != 1 {
		t.Error("only a build replaces the reasoning")
	}
}

func TestTheProgressLineStandsRightUnderTheQuestion(t *testing.T) {
	m := buildModel("progress")
	m.entries = []entry{{kind: eUser, text: "connect my telegram"}}
	m.build = buildState{active: true, phase: 2, startAt: time.Now()}
	st := newStyles(themeFor(m.mode, 0))
	lines := m.transcript(60, st)
	last := lines[len(lines)-1]
	if !strings.Contains(last, "Designing") && !strings.Contains(last, "Writing the code") && !strings.Contains(last, "tools") {
		t.Fatalf("the progress line must be the last line of the feed: %q", lines)
	}
	var q int
	for i, l := range lines {
		if strings.Contains(l, "connect my telegram") {
			q = i
		}
	}
	if len(lines)-q > 4 {
		t.Fatalf("the line must stand right under the question, not far away at the bottom: question at %d of %d", q, len(lines))
	}
	m.streaming = false
	for _, l := range m.transcript(60, st) {
		if strings.Contains(l, "2/5") {
			t.Fatal("no progress line when nothing runs")
		}
	}
}

func TestTheWorkOfTheModelIsHiddenWhileBuilding(t *testing.T) {
	m := buildModel("progress")
	m.entries = []entry{
		{kind: eUser, text: "connect my telegram"},
		{kind: eTool, tool: "load_skill", text: "load skill relay-builder", result: "SECRET GUIDE TEXT"},
		{kind: eTool, tool: "read_skill_file", text: "read relay-builder/references/sdk.md", result: "SDK TEXT"},
		{kind: eTool, tool: "create_mcp_server", text: "create the MCP tool", result: "created"},
		{kind: eTool, tool: "read_file", text: "main.go", result: "VISIBLE RESULT"},
		{kind: eTool, tool: "mcp__tg__send", text: "tg/send", result: "SENT", hidden: true},
	}
	all := strings.Join(m.transcript(80, newStyles(themeFor(m.mode, 0))), "\n")
	for _, bad := range []string{"SECRET GUIDE TEXT", "SDK TEXT", "create the MCP tool", "SENT"} {
		if strings.Contains(all, bad) {
			t.Errorf("%q must not be shown", bad)
		}
	}
	if !strings.Contains(all, "VISIBLE RESULT") || !strings.Contains(all, "connect my telegram") {
		t.Error("the question and the other tools stay")
	}
	// with the setting "thoughts" everything is shown as before
	m2 := buildModel("thoughts")
	m2.entries = m.entries
	all = strings.Join(m2.transcript(80, newStyles(themeFor(m2.mode, 0))), "\n")
	if !strings.Contains(all, "SECRET GUIDE TEXT") || !strings.Contains(all, "SENT") {
		t.Error("the setting \"thoughts\" shows the work of the model")
	}
}

func TestWhatTheModelSaysOnTheWayIsDroppedAndTheAnswerIsKept(t *testing.T) {
	m := buildModel("progress")
	m.entries = []entry{{kind: eUser, text: "connect my telegram"}, {kind: eAssistant, text: "But wait, let me load the guide first."}}
	m.pend = []pendPiece{{false, "still typing"}}
	// the build starts: what was said before is not an answer
	m.trackBuild(proto.Event{Type: "tool_call", Tool: "load_skill", Args: `{"name":"relay-builder"}`})
	if len(m.entries) != 1 || len(m.pend) != 0 {
		t.Fatalf("the talk before the build must go: %d entries, %d pending", len(m.entries), len(m.pend))
	}
	// text during the build is held, and cleared by the next tool call
	m.queue(false, "Now I will write the server.")
	if m.hold == "" || len(m.pend) != 0 {
		t.Fatal("text during a build is held")
	}
	m.handleEvent(proto.Event{Type: "tool_call", Tool: "create_mcp_server"})
	if m.hold != "" {
		t.Fatal("what came before a tool call was not an answer")
	}
	if len(m.entries) < 2 || !m.entries[len(m.entries)-1].hidden {
		t.Fatalf("the call of a tool during a build is hidden: %+v", m.entries)
	}
	// the text after the last call is the answer: shown when the request ends
	m.queue(false, "Done: the Telegram connector works.")
	res, _ := m.Update(streamEndMsg{})
	m2 := res.(model)
	if last := m2.entries[len(m2.entries)-1]; last.kind != eAssistant || !strings.Contains(last.text, "Telegram connector works") {
		t.Fatalf("the answer must be shown at the end: %+v", last)
	}
	if m2.hold != "" || m2.build.active {
		t.Error("the build is over")
	}
}

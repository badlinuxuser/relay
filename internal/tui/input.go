package tui

import (
	"strings"
)

// The input line is drawn by Relay itself, not by the text-input widget: a long message
// wraps onto several lines (up to inputMaxLines) instead of scrolling sideways, the
// cursor stays put (blinking follows the clock),
// and a dimmed hint for the command being typed follows the text.

const inputMaxLines = 6

// inLine is one wrapped line of the input: the runes s..e of the text.
type inLine struct{ s, e int }

// wrapInput breaks the text into lines of at most w cells, at spaces where it can.
func wrapInput(r []rune, w int) []inLine {
	w = max(w, 4)
	var out []inLine
	start := 0
	for {
		if len(r)-start <= w {
			return append(out, inLine{start, len(r)})
		}
		end := start + w
		brk := end
		if r[end] != ' ' { // the line would cut a word: go back to the last space
			for i := end - 1; i > start; i-- {
				if r[i] == ' ' {
					brk = i + 1
					break
				}
			}
		} else {
			brk = end + 1
		}
		out = append(out, inLine{start, brk})
		start = brk
	}
}

func (m model) inputWidth() int { return max(10, m.width-8) } // minus border, padding and the prompt

// inputLayout is the wrapped text, the line the cursor is on, and the first visible line.
func (m model) inputLayout() (lines []inLine, cur, first int) {
	r := []rune(m.input.Value())
	lines = wrapInput(r, m.inputWidth()-1) // one cell stays free for the cursor
	pos := min(max(m.input.Position(), 0), len(r))
	for i, l := range lines {
		if pos >= l.s && pos < l.e || (i == len(lines)-1 && pos >= l.s) {
			cur = i
			break
		}
	}
	first = 0
	if len(lines) > inputMaxLines {
		first = min(max(0, cur-inputMaxLines/2), len(lines)-inputMaxLines)
	}
	return lines, cur, first
}

// inputHeight is the number of text lines the input box shows now.
func (m model) inputHeight() int {
	if m.width == 0 || m.approval != nil {
		return 1
	}
	lines, _, _ := m.inputLayout()
	return min(len(lines), inputMaxLines)
}

func reverse(s string) string { return "\x1b[7m" + s + "\x1b[27m" }

// cursor draws the cursor cell; it blinks with the clock (the clock stands still in select
// mode, so a selection is never disturbed).
func (m model) cursor(st styles, s string) string {
	if m.now().UnixMilli()/600%2 == 0 {
		return reverse(st.accent.Render(s)) // a block in the accent colour
	}
	return s
}

// inputRows draws the text of the input box, one string per line.
func (m model) inputRows(st styles) []string {
	if m.approval != nil {
		return []string{st.accentBold.Render("❯ ") + m.cursor(st, " ") + st.dim.Render("Choose an action in the list above")}
	}
	r := []rune(m.input.Value())
	if len(r) == 0 {
		return []string{st.accentBold.Render("❯ ") + m.cursor(st, " ") + st.dim.Render("Message or /help")}
	}
	lines, cur, first := m.inputLayout()
	pos := min(max(m.input.Position(), 0), len(r))
	var out []string
	for i := first; i < len(lines) && i < first+inputMaxLines; i++ {
		l := lines[i]
		prefix := "  "
		if i == 0 {
			prefix = st.accentBold.Render("❯ ")
		}
		text := string(r[l.s:l.e])
		if i == cur {
			c := pos - l.s
			rs := r[l.s:l.e]
			c = min(max(c, 0), len(rs))
			before, after := string(rs[:c]), ""
			cell := " "
			if c < len(rs) {
				cell, after = string(rs[c]), string(rs[c+1:])
			}
			line := st.text.Render(before) + m.cursor(st, cell) + st.text.Render(after)
			if pos == len(r) { // the hint for the command after the text
				line += st.dim.Render(m.commandGhost(string(r)))
			}
			out = append(out, prefix+line)
			continue
		}
		out = append(out, prefix+st.text.Render(text))
	}
	return out
}

// ---- command hints -----------------------------------------------------------

type cmdHelp struct{ name, usage, desc string }

var commandTable = []cmdHelp{
	{"/help", "", "open the help page"},
	{"/models", "[filter]", "choose a model"},
	{"/mode", "small|medium|hard|maximum", "switch the mode"},
	{"/small", "", "switch to small mode"},
	{"/medium", "", "switch to medium mode"},
	{"/hard", "", "switch to hard mode"},
	{"/max", "", "switch to maximum mode"},
	{"/agent", "on|off", "agent mode: tools and files"},
	{"/preset", "<name>|none", "system prompt preset"},
	{"/compare", "<model> <model> <text>", "ask several models at once"},
	{"/new", "", "start a new session"},
	{"/sessions", "", "open a saved session"},
	{"/load", "<id>", "load a session by id"},
	{"/ollama", "", "start Ollama"},
	{"/llamacpp", "<model.gguf>", "start llama.cpp with a model"},
	{"/stop", "[name]", "stop a running server"},
	{"/keys", "", "open the API keys page"},
	{"/settings", "", "open the settings page"},
	{"/thoughts", "on|off", "show the model's thoughts"},
	{"/mouse", "on|off", "mouse in the interface"},
	{"/select", "", "freeze the screen to select text"},
	{"/copy", "", "copy the last answer"},
	{"/clear", "", "clear the feed"},
	{"/quit", "", "quit Relay"},
}

// commands is the table plus a /name for every other process of config.toml.
func (m model) commands() []cmdHelp {
	out := append([]cmdHelp(nil), commandTable...)
	if m.status != nil {
		for _, p := range m.status.Processes {
			known := false
			for _, c := range out {
				if c.name == "/"+p.Name {
					known = true
				}
			}
			if !known {
				out = append(out, cmdHelp{"/" + p.Name, "", "start " + p.Name})
			}
		}
	}
	return out
}

// commandGhost is the dimmed hint shown after the text: the rest of the command name and what
// it does, or the arguments it takes. Empty when the text is not a command being typed.
func (m model) commandGhost(v string) string {
	if !strings.HasPrefix(v, "/") {
		return ""
	}
	cmds := m.commands()
	if i := strings.IndexByte(v, ' '); i >= 0 {
		name, rest := v[:i], strings.TrimLeft(v[i:], " ")
		for _, c := range cmds {
			if c.name == name && rest == "" && c.usage != "" {
				return c.usage + "  · " + c.desc
			}
		}
		return ""
	}
	var match []cmdHelp
	for _, c := range cmds {
		if strings.HasPrefix(c.name, v) {
			match = append(match, c)
		}
	}
	if len(match) == 0 {
		return ""
	}
	c := match[0]
	ghost := strings.TrimPrefix(c.name, v)
	if c.usage != "" {
		ghost += " " + c.usage
	}
	ghost += "  · " + c.desc
	if len(match) > 1 {
		ghost += " (tab; " + itoa(len(match)-1) + " more)"
	} else if c.name != v {
		ghost += " (tab)"
	}
	return ghost
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// completeCommand finishes the command name at the end of the text (tab).
func (m model) completeCommand(v string) (string, bool) {
	if !strings.HasPrefix(v, "/") || strings.ContainsAny(v, " ") {
		return v, false
	}
	for _, c := range m.commands() {
		if strings.HasPrefix(c.name, v) {
			done := c.name
			if c.usage != "" {
				done += " "
			}
			return done, done != v
		}
	}
	return v, false
}

// ---- history (up / down, like a shell) ---------------------------------------

func (m *model) remember(text string) {
	if n := len(m.hist); n == 0 || m.hist[n-1] != text {
		m.hist = append(m.hist, text)
		if len(m.hist) > 200 {
			m.hist = m.hist[len(m.hist)-200:]
		}
	}
	m.histPos = -1
}

// historyKey handles up and down: older and newer messages. It returns false when the key
// is not for the history (text is being edited, not browsed).
func (m *model) historyKey(key string) bool {
	if key != "up" && key != "down" {
		return false
	}
	if m.histPos < 0 && m.input.Value() != "" {
		return false // the arrows belong to the text being written
	}
	switch key {
	case "up":
		if len(m.hist) == 0 {
			return true
		}
		if m.histPos < 0 {
			m.histPos = len(m.hist)
		}
		if m.histPos > 0 {
			m.histPos--
		}
		m.input.SetValue(m.hist[m.histPos])
	case "down":
		if m.histPos < 0 {
			return true
		}
		m.histPos++
		if m.histPos >= len(m.hist) {
			m.histPos = -1
			m.input.SetValue("")
		} else {
			m.input.SetValue(m.hist[m.histPos])
		}
	}
	m.input.CursorEnd()
	return true
}

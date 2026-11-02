package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/client"
	"relay/internal/proto"
	"relay/internal/settings"
)

// The answer and the model's thoughts arrive in pieces whenever the network gives them
// (often in big lumps). They go into a queue and are typed out at the speed chosen on the
// /settings page, so the text flows instead of jumping. Any other event (a tool call, a
// notice, the end) first lets the queue empty out, so the order on screen stays right.

type pendPiece struct {
	think bool
	text  string
}

type typeMsg struct{}

const typeEvery = 40 * time.Millisecond

func typeTick() tea.Cmd {
	return tea.Tick(typeEvery, func(time.Time) tea.Msg { return typeMsg{} })
}

// appSettings are the user's preferences as last reported by the backend.
func (m model) appSettings() settings.Settings {
	if m.status != nil && m.status.Settings.StreamSpeed != "" {
		return m.status.Settings
	}
	return settings.Default()
}

// queue adds a piece of the answer (think=false) or of the thoughts (think=true).
func (m *model) queue(think bool, text string) {
	if text == "" {
		return
	}
	if think && !m.appSettings().ShowThoughts {
		return
	}
	if settings.SpeedCPS(m.appSettings().StreamSpeed) == 0 {
		m.apply(pendPiece{think, text})
		return
	}
	if n := len(m.pend); n > 0 && m.pend[n-1].think == think {
		m.pend[n-1].text += text
		return
	}
	m.pend = append(m.pend, pendPiece{think, text})
}

// apply puts text into the feed: thoughts into their own block, the answer into the answer.
func (m *model) apply(p pendPiece) {
	if p.think {
		if m.openThink < 0 || m.openThink >= len(m.entries) || m.entries[m.openThink].kind != eThought {
			m.entries = append(m.entries, entry{kind: eThought, model: m.curModel})
			m.openThink = len(m.entries) - 1
			m.openAsst = -1 // the answer that follows starts below the thoughts
		}
		m.entries[m.openThink].text += p.text
		return
	}
	m.openThink = -1
	if m.openAsst < 0 || m.openAsst >= len(m.entries) {
		m.entries = append(m.entries, entry{kind: eAssistant, model: m.curModel})
		m.openAsst = len(m.entries) - 1
	}
	m.entries[m.openAsst].text += p.text
}

// flushPending types out everything that is waiting, at once.
func (m *model) flushPending() {
	for _, p := range m.pend {
		m.apply(p)
	}
	m.pend = nil
}

func (m model) pendingRunes() int {
	n := 0
	for _, p := range m.pend {
		n += runeLen(p.text)
	}
	return n
}

// drain types out a few characters: the speed from the settings, faster when a lot is waiting.
func (m *model) drain() {
	if len(m.pend) == 0 {
		return
	}
	cps := settings.SpeedCPS(m.appSettings().StreamSpeed)
	if cps == 0 {
		m.flushPending()
		return
	}
	n := max(1, cps*int(typeEvery/time.Millisecond)/1000)
	n = max(n, m.pendingRunes()/12) // never fall far behind the network
	for n > 0 && len(m.pend) > 0 {
		p := &m.pend[0]
		r := []rune(p.text)
		take := min(n, len(r))
		m.apply(pendPiece{p.think, string(r[:take])})
		n -= take
		if take == len(r) {
			m.pend = m.pend[1:]
		} else {
			p.text = string(r[take:])
		}
	}
}

// renderThought draws the model's reasoning: dimmed, with a bar at the left, last lines only.
func renderThought(e entry, w int, st styles, done bool) []string {
	const maxLines = 6
	body := max(8, w-4)
	lines := wrapText(strings.TrimSpace(e.text), body)
	head := st.soft.Render("✻ ") + st.dim.Render("thinking")
	out := []string{head}
	if len(lines) > maxLines {
		out = append(out, st.soft.Render("┊ ")+st.dim.Render(fmt.Sprintf("… %d earlier lines", len(lines)-maxLines)))
		lines = lines[len(lines)-maxLines:]
	}
	for _, l := range lines {
		out = append(out, st.soft.Render("┊ ")+st.dim.Render(l))
	}
	return out
}

// ---- commands: /settings /thoughts /mouse /copy ------------------------------

func saveSettingsCmd(cl *client.Client, change func(*settings.Settings)) tea.Cmd {
	return func() tea.Msg {
		cur, err := cl.AppSettings()
		if err != nil {
			return errMsg{err}
		}
		change(&cur)
		if _, err := cl.SaveAppSettings(cur); err != nil {
			return errMsg{err}
		}
		st, err := cl.Status()
		if err != nil {
			return nil
		}
		return statusMsg{st: st}
	}
}

// onOff reads "on|off" (or nothing = toggle) from the command arguments.
func onOff(args []string, cur bool) bool {
	if len(args) == 1 {
		switch strings.ToLower(args[0]) {
		case "on", "yes", "1", "вкл":
			return true
		case "off", "no", "0", "выкл":
			return false
		}
	}
	return !cur
}

// lastAnswer is the text of the newest answer (for /copy).
func (m model) lastAnswer() string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].kind == eAssistant && strings.TrimSpace(m.entries[i].text) != "" {
			return m.entries[i].text
		}
	}
	return ""
}

// copyCmd puts text on the clipboard with the OSC 52 escape sequence, which most modern
// terminals (Windows Terminal, iTerm2, kitty, WezTerm, alacritty, tmux with set-clipboard) accept.
func copyCmd(text string) tea.Cmd {
	return func() tea.Msg {
		fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
		return nil
	}
}

// applyMouse switches mouse reporting to match the setting.
func (m *model) applyMouse(on bool) tea.Cmd {
	if on == m.mouseOn {
		return nil
	}
	m.mouseOn = on
	if on {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

func (m *model) noteSettingsChange(st *proto.StatusResp) tea.Cmd {
	if st == nil || st.Settings.StreamSpeed == "" {
		return nil
	}
	return m.applyMouse(st.Settings.Mouse)
}

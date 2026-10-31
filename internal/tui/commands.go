package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/browser"
	"relay/internal/client"
	"relay/internal/proto"
)

// helpText is shown only when the help page cannot be opened in a browser.
const helpText = `Relay help is a web page: run /help to open it, or open <backend address>/ui/help.
Quick reference: /models pick a model · /mode small|medium|hard|maximum · /agent on|off · /keys API keys
/preset <name>|none · /compare <model> <model> <text> · /new · /sessions · /load <id> · /clear · /quit
Servers: /ollama · /llamacpp <model.gguf> · /stop (pick what to stop)
Keys: ctrl+t mode · ctrl+a agent · ctrl+n new session · esc stop · pgup/pgdn scroll · ctrl+c quit`

func statusCmd(cl *client.Client) tea.Cmd {
	return func() tea.Msg {
		st, err := cl.Status()
		if err != nil {
			return statusMsg{}
		}
		return statusMsg{st: st}
	}
}

func settingsCmd(cl *client.Client, id string, req proto.SettingsReq) tea.Cmd {
	return func() tea.Msg {
		si, err := cl.Settings(id, req)
		if err != nil {
			return errMsg{err}
		}
		return sessionMsg{sess: si}
	}
}

func newSessionCmd(cl *client.Client, mode, cwd string) tea.Cmd {
	return func() tea.Msg {
		si, err := cl.CreateSession(mode, cwd)
		if err != nil {
			return errMsg{err}
		}
		return sessionMsg{sess: si}
	}
}

func approveCmd(cl *client.Client, id, decision string) tea.Cmd {
	return func() tea.Msg {
		if err := cl.Approve(id, decision); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func sessionsCmd(cl *client.Client) tea.Cmd {
	return func() tea.Msg {
		list, err := cl.Sessions()
		if err != nil {
			return errMsg{err}
		}
		return sessionsMsg{list: list}
	}
}

func loadCmd(cl *client.Client, query string) tea.Cmd {
	return func() tea.Msg {
		list, err := cl.Sessions()
		if err != nil {
			return errMsg{err}
		}
		for _, s := range list {
			if strings.Contains(s.ID, query) {
				si, err := cl.GetSession(s.ID)
				if err != nil {
					return errMsg{err}
				}
				return sessionMsg{sess: si, reset: true}
			}
		}
		return errMsg{fmt.Errorf("session %q not found", query)}
	}
}

// procMsg is the result of starting or stopping a process.
type procMsg struct {
	name, action string
	already      bool
	st           *proto.StatusResp
}

func procCmd(cl *client.Client, name, action string, args ...string) tea.Cmd {
	return func() tea.Msg {
		already, err := cl.Process(name, action, args...)
		if err != nil {
			return errMsg{err}
		}
		st, _ := cl.Status()
		return procMsg{name: name, action: action, already: already, st: st}
	}
}

// processNames are the names of the processes from the config (every one gets a /name command).
func (m model) isProcess(name string) bool {
	if m.status == nil {
		return name == "ollama" || name == "llamacpp"
	}
	for _, p := range m.status.Processes {
		if p.Name == name {
			return true
		}
	}
	return false
}

// unquote removes quotes around a path pasted with spaces and expands ~.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	if s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			s = home + s[1:]
		}
	}
	return s
}

// processArgs turns the text after /<name> into extra arguments. llama.cpp takes a
// model file (the path may contain spaces); other servers take plain arguments.
func processArgs(name, rest string) []string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil
	}
	if name == "llamacpp" && !strings.HasPrefix(rest, "-") {
		return []string{"-m", unquote(rest)}
	}
	return strings.Fields(rest)
}

func (m model) isModelID(id string) bool {
	for _, md := range m.modelList() {
		if md.ID == id {
			return true
		}
	}
	// "<provider>/<model>" works for any provider, even if the model is not in the list
	if i := strings.Index(id, "/"); i > 0 && i < len(id)-1 && m.status != nil {
		for _, p := range m.status.Providers {
			if p.Name == id[:i] {
				return true
			}
		}
	}
	return false
}

// command разбирает слэш-команду.
func (m model) command(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	cmd, args := fields[0], fields[1:]
	needConn := func() bool {
		if m.cl == nil || m.sess == nil {
			m.addNotice("Not connected to the backend yet")
			return false
		}
		return true
	}

	switch cmd {
	case "/help", "/?":
		if m.cl == nil {
			for _, l := range strings.Split(helpText, "\n") {
				m.addNotice(l)
			}
			return m, nil
		}
		url := m.cl.Base + "/ui/help"
		m.mc.react("noticed", 2*time.Second, "")
		if err := browser.Open(url); err != nil {
			m.addNotice("Could not open the browser. Open the help page manually: " + url)
		} else {
			m.addNotice("Opened the help page in your browser: " + url)
		}
		return m, nil

	case "/quit", "/exit", "/q":
		return m.quit()

	case "/clear":
		m.entries = nil
		m.scroll = 0
		return m, nil

	case "/small", "/medium", "/hard", "/maximum", "/max":
		mode := strings.TrimPrefix(cmd, "/")
		if mode == "max" {
			mode = "maximum"
		}
		for _, n := range m.modeNames() {
			if n == mode {
				cm := m.setMode(n)
				return m, cm
			}
		}
		m.addError("Mode " + mode + " is not configured")
		return m, nil

	case "/keys":
		if !needConn() {
			return m, nil
		}
		url := m.cl.Base + "/ui/keys#token=" + m.cl.Token
		m.mc.react("noticed", 2*time.Second, "")
		if err := browser.Open(url); err != nil {
			m.addNotice("Could not open the browser. Open it manually: " + url)
		} else {
			m.addNotice("Opened the keys page in your browser. Add your keys and come back: they apply immediately.")
		}
		m.catAt = time.Time{} // keys may change which models are available: ask again
		return m, nil

	case "/mode":
		if len(args) != 1 {
			m.addNotice("Current mode: " + m.mode + ". Usage: /mode " + strings.Join(m.modeNames(), "|"))
			return m, nil
		}
		for _, n := range m.modeNames() {
			if n == args[0] {
				c := m.setMode(n)
				return m, c
			}
		}
		m.addError("Unknown mode " + args[0])
		return m, nil

	case "/models":
		if !needConn() {
			return m, nil
		}
		if len(args) == 0 {
			m.listModels()
			if m.catStale() {
				m.catBusy = true
				return m, catalogCmd(m.cl, false)
			}
			return m, nil
		}
		c := m.useModel(args[0])
		return m, c

	case "/agent":
		if !needConn() {
			return m, nil
		}
		on := !m.sess.Agent
		if len(args) == 1 {
			on = args[0] == "on" || args[0] == "вкл"
		}
		m.sess.Agent = on
		return m, settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Agent: &on})

	case "/preset":
		if !needConn() {
			return m, nil
		}
		if len(args) != 1 {
			if m.status != nil {
				m.addNotice("Presets: " + strings.Join(m.status.Presets, ", "))
			}
			return m, nil
		}
		v := args[0]
		if v == "none" {
			v = ""
		}
		return m, settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Preset: &v})

	case "/new":
		c := m.newSession()
		return m, c

	case "/sessions":
		if !needConn() {
			return m, nil
		}
		m.ov = overlay{}
		return m, sessionsCmd(m.cl)

	case "/load":
		if !needConn() || len(args) != 1 {
			return m, nil
		}
		return m, loadCmd(m.cl, args[0])

	case "/stop":
		if !needConn() {
			return m, nil
		}
		if len(args) == 0 {
			return m, stopListCmd(m)
		}
		return m, procCmd(m.cl, args[0], "stop")

	case "/start":
		m.addNotice("/start is gone: use /ollama or /llamacpp <model.gguf>")
		return m, nil

	case "/compare":
		if !needConn() {
			return m, nil
		}
		if m.streaming {
			m.addNotice("Wait, another request is running")
			return m, nil
		}
		var models []string
		i := 0
		for i < len(args) && m.isModelID(args[i]) {
			models = append(models, args[i])
			i++
		}
		prompt := strings.Join(args[i:], " ")
		if len(models) == 0 || prompt == "" {
			m.addNotice("Usage: /compare <model> <model> ... <text>")
			return m, nil
		}
		m.entries = append(m.entries, entry{kind: eUser, text: prompt})
		cl := m.cl
		preset := m.sess.Preset
		runCmd := m.beginStream(func(ctx context.Context, emit func(proto.Event)) error {
			return cl.Compare(ctx, proto.CompareReq{Text: prompt, Models: models, Preset: preset}, emit)
		})
		m.mc.react("noticed", 2*time.Second, "")
		return m, runCmd
	}

	if name := strings.TrimPrefix(cmd, "/"); m.isProcess(name) {
		if !needConn() {
			return m, nil
		}
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), cmd))
		m.mc.react("noticed", 1500*time.Millisecond, "")
		return m, procCmd(m.cl, name, "start", processArgs(name, rest)...)
	}

	m.addError("Unknown command " + cmd + " (/help)")
	return m, nil
}

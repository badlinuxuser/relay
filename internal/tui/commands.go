package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/browser"
	"relay/internal/client"
	"relay/internal/proto"
)

const helpText = `Commands:
  /mode small|medium|hard|maximum   mode (UI color and model chain); shortcuts: /small /medium /hard /max
  /keys                             page for adding and storing API keys (opens in your browser)
  /models [text]                    pick a model: local Ollama, Ollama cloud, Gemini, ... (↑↓ enter);
                                    "auto" goes back to the mode's own choice
  /agent on|off                     agent mode (tools: files, shell, web, MCP)
  /preset <name>|none               system prompt
  /compare <id> <id> ... <text>     send one prompt to several models
  /new                              new session
  /sessions   /load <id>            list and load sessions
  /start <name>   /stop <name>      local processes (launcher)
  /clear   /quit
Keys: ctrl+t mode · ctrl+a agent · ctrl+n new · esc stop · pgup/pgdn scroll`

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

func procCmd(cl *client.Client, name, action string) tea.Cmd {
	return func() tea.Msg {
		if err := cl.Process(name, action); err != nil {
			return errMsg{err}
		}
		st, err := cl.Status()
		if err != nil {
			return infoMsg{fmt.Sprintf("%s: %s", name, action)}
		}
		return statusMsg{st: st}
	}
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
		for _, l := range strings.Split(helpText, "\n") {
			m.addNotice(l)
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
		return m, sessionsCmd(m.cl)

	case "/load":
		if !needConn() || len(args) != 1 {
			return m, nil
		}
		return m, loadCmd(m.cl, args[0])

	case "/start", "/stop":
		if !needConn() || len(args) != 1 {
			m.addNotice("Usage: " + cmd + " <process name>")
			return m, nil
		}
		return m, procCmd(m.cl, args[0], strings.TrimPrefix(cmd, "/"))

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

	m.addError("Unknown command " + cmd + " (/help)")
	return m, nil
}

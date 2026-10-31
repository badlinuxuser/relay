// Package tui это терминальный интерфейс Relay (Bubble Tea). Он ничего не знает
// о провайдерах: всё делает бэкенд, интерфейс общается с ним по HTTP.
package tui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/client"
	"relay/internal/proto"
)

type entryKind int

const (
	eUser entryKind = iota
	eAssistant
	eTool
	eNotice
	eError
)

type entry struct {
	kind   entryKind
	text   string
	model  string
	tool   string
	result string
	isErr  bool
}

type model struct {
	width, height int

	cl     *client.Client
	spawn  bool
	keep   bool
	status *proto.StatusResp
	sess   *proto.SessionInfo
	mode   string
	cwd    string

	entries []entry
	input   textinput.Model
	scroll  int

	streaming bool
	cancel    context.CancelFunc
	events    chan proto.Event
	openAsst  int
	slots     map[int]int
	approval  *proto.Event
	runErr    bool

	frame      int
	mc         mascot
	sprites    map[string]*spriteSet
	statusBusy bool

	curModel      string
	totIn, totOut int
	totCost       float64

	// model picker (/models): the full list from the backend and the picker's state
	models       *proto.ModelsResp
	catAt        time.Time
	catBusy      bool
	pickSel      int
	pickClosedAt string // input text at which esc closed the picker

	appSel int // selected line of the approval list (see approval.go)

	ov          overlay         // list opened by /sessions or /stop (overlay.go)
	statusBurst int             // ticks left of fast status polling after a start
	failSeen    map[string]bool // failed processes that were already reported

	transFrom  string // предыдущий режим во время анимации смены ("" = нет)
	transStart int    // кадр, на котором началась анимация
}

// New создаёт интерфейс. spawn: запускать бэкенд, если он не запущен.
// keep: не останавливать бэкенд при выходе.
func New(spawn, keep bool) tea.Model {
	ti := textinput.New()
	ti.Placeholder = "Message or /help"
	ti.CharLimit = 8000
	ti.Focus()
	cwd, _ := os.Getwd()
	return model{
		spawn:    spawn,
		keep:     keep,
		mode:     "small",
		cwd:      cwd,
		input:    ti,
		openAsst: -1,
		slots:    map[int]int{},
		mc:       newMascot(),
		sprites:  loadSprites(),
		entries:  []entry{{kind: eNotice, text: "Connecting to the backend…"}},
	}
}

// ---- сообщения ---------------------------------------------------------------

type connectedMsg struct {
	cl     *client.Client
	status *proto.StatusResp
	sess   *proto.SessionInfo
}
type errMsg struct{ err error }
type statusMsg struct{ st *proto.StatusResp }
type sessionMsg struct {
	sess  *proto.SessionInfo
	reset bool
}
type sessionsMsg struct{ list []proto.SessionInfo }
type evMsg proto.Event
type streamEndMsg struct{}
type tickMsg struct{}
type infoMsg struct{ text string }

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func waitEvent(ch <-chan proto.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamEndMsg{}
		}
		return evMsg(ev)
	}
}

func connectCmd(spawn bool, cwd string) tea.Cmd {
	return func() tea.Msg {
		cl, err := client.Connect(spawn)
		if err != nil {
			return errMsg{err}
		}
		st, err := cl.Status()
		if err != nil {
			return errMsg{err}
		}
		sess, err := cl.CreateSession(st.DefaultMode, cwd)
		if err != nil {
			return errMsg{err}
		}
		return connectedMsg{cl: cl, status: st, sess: sess}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tick(), connectCmd(m.spawn, m.cwd))
}

// ---- Update ------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.frame++
		every := 40
		if m.statusBurst > 0 { // a server was just started: watch it come up
			m.statusBurst--
			every = 8
		}
		if m.cl != nil && !m.statusBusy && m.frame%every == 0 {
			m.statusBusy = true
			return m, tea.Batch(tick(), statusCmd(m.cl))
		}
		return m, tick()

	case connectedMsg:
		m.cl, m.status, m.sess = msg.cl, msg.status, msg.sess
		m.mode = msg.sess.Mode
		m.entries = []entry{{kind: eNotice, text: fmt.Sprintf("Relay %s · folder: %s", msg.status.Version, shortPath(msg.sess.Workspace))}}
		if miss := missingKeys(msg.status); len(miss) > 0 {
			m.addNotice("No API keys for: " + strings.Join(miss, ", ") + ". Run /keys to open a page where you can add them (keys are saved).")
		}
		m.mc.react("joy", 2500*time.Millisecond, "Relay is here.")
		m.catBusy = true // look for models (Ollama, Gemini, ...) in the background
		return m, catalogCmd(msg.cl, false)

	case catalogMsg:
		m.catBusy = false
		if msg.err == nil && msg.resp != nil {
			m.models, m.catAt = msg.resp, time.Now()
		}
		return m, nil

	case errMsg:
		m.addError(msg.err.Error())
		m.statusBusy = false
		m.mc.react("surprise", 4*time.Second, "Something went wrong")
		return m, nil

	case statusMsg:
		if msg.st != nil {
			m.status = msg.st
			m.reportFailed(msg.st)
		}
		m.statusBusy = false
		return m, nil

	case procMsg:
		switch {
		case msg.action == "stop":
			m.addNotice("Stopped " + msg.name)
		case msg.already:
			m.addNotice(msg.name + " is already running")
		default:
			m.addNotice("Starting " + msg.name + "…")
			m.statusBurst = 125 // about 15 s of quick polling
		}
		if msg.st != nil {
			m.status = msg.st
		}
		return m, nil

	case stopListMsg:
		m.openStopList(msg.procs)
		return m, nil

	case sessionMsg:
		m.sess = msg.sess
		m.mode = msg.sess.Mode
		if msg.reset {
			m.entries = entriesFromSession(msg.sess)
			m.scroll = 0
			m.totIn, m.totOut, m.totCost = 0, 0, 0
		}
		return m, nil

	case sessionsMsg:
		m.openSessions(msg.list)
		return m, nil

	case infoMsg:
		m.addNotice(msg.text)
		return m, nil

	case evMsg:
		m.handleEvent(proto.Event(msg))
		return m, waitEvent(m.events)

	case streamEndMsg:
		m.streaming = false
		m.cancel = nil
		m.approval = nil
		m.openAsst = -1
		if !m.runErr {
			m.mc.base = "calm"
			m.mc.react("joy", 3*time.Second, "")
		}
		return m, nil

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scroll = min(m.scroll+3, m.maxScroll())
		case tea.MouseButtonWheelDown:
			m.scroll -= 3
			if m.scroll < 0 {
				m.scroll = 0
			}
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress {
				_, c := m.click(msg.X, msg.Y)
				return m, c
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) quit() (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	if m.cl != nil && m.cl.Spawned && !m.keep {
		m.cl.Shutdown()
	}
	return m, tea.Quit
}

// mouseJunk matches terminal mouse reports ("\x1b[<64;101;29M") that were split while the
// screen was busy and ended up in the input line as plain text, e.g. "[<64;101;29M".
var mouseJunk = regexp.MustCompile(`(?:\x1b)?\[?<\d{1,3};\d{1,4};\d{1,4}[Mm]`)

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mc.touch(time.Now())
	key := msg.String()

	if key == "ctrl+c" {
		return m.quit()
	}

	// the agent's question or a list (sessions, running processes) takes the keys first
	if handled, c := m.overlayKey(key); handled {
		return m, c
	}

	// the model list is open above the input line: it takes ↑ ↓ tab enter esc
	if m.pickerOpen() {
		handled, c := m.pickerKey(key)
		if handled {
			return m, c
		}
	}

	switch key {
	case "ctrl+t":
		c := m.cycleMode()
		return m, c
	case "ctrl+a":
		c := m.toggleAgent()
		return m, c
	case "ctrl+n":
		c := m.newSession()
		return m, c
	case "esc":
		if m.streaming && m.cancel != nil {
			m.cancel()
			m.addNotice("Stopped")
		}
		return m, nil
	case "pgup":
		m.scroll = min(m.scroll+max(3, m.bodyHeight()/2), m.maxScroll())
		return m, nil
	case "pgdown":
		m.scroll = max(0, m.scroll-max(3, m.bodyHeight()/2))
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.SetValue("")
		if strings.HasPrefix(text, "/") {
			return m.command(text)
		}
		return m.send(text)
	}

	prev := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if v := m.input.Value(); mouseJunk.MatchString(v) {
		// Mouse wheel events that reached the input as text (see mouseJunk).
		m.input.SetValue(mouseJunk.ReplaceAllString(v, ""))
		m.input.CursorEnd()
	}
	pc := m.syncPicker(prev)
	return m, tea.Batch(cmd, pc)
}

// ---- запуск запросов ---------------------------------------------------------

func (m *model) beginStream(run func(ctx context.Context, emit func(proto.Event)) error) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan proto.Event, 256)
	m.cancel, m.events, m.streaming = cancel, ch, true
	m.openAsst, m.slots, m.runErr = -1, map[int]int{}, false
	m.scroll = 0
	go func() {
		err := run(ctx, func(ev proto.Event) { ch <- ev })
		if err != nil && ctx.Err() == nil {
			ch <- proto.Event{Type: "error", Kind: "internal", Text: err.Error()}
		}
		close(ch)
	}()
	return waitEvent(ch)
}

func (m model) send(text string) (tea.Model, tea.Cmd) {
	if m.cl == nil || m.sess == nil {
		m.addNotice("Not connected to the backend yet")
		return m, nil
	}
	if m.streaming {
		m.addNotice("Wait, the previous request is still running (esc to stop)")
		return m, nil
	}
	m.entries = append(m.entries, entry{kind: eUser, text: text})
	cl, id := m.cl, m.sess.ID
	cmd := m.beginStream(func(ctx context.Context, emit func(proto.Event)) error {
		return cl.Send(ctx, id, text, emit)
	})
	return m, cmd
}

func (m *model) handleEvent(ev proto.Event) {
	switch ev.Type {
	case "model":
		m.curModel = ev.Model
		if ev.Slot > 0 {
			m.entries = append(m.entries, entry{kind: eAssistant, model: ev.Model})
			m.slots[ev.Slot] = len(m.entries) - 1
		}

	case "text":
		if ev.Slot > 0 {
			if i, ok := m.slots[ev.Slot]; ok {
				m.entries[i].text += ev.Text
			}
			return
		}
		if m.openAsst < 0 || m.openAsst >= len(m.entries) {
			m.entries = append(m.entries, entry{kind: eAssistant, model: m.curModel})
			m.openAsst = len(m.entries) - 1
		}
		m.entries[m.openAsst].text += ev.Text

	case "notice":
		m.addNotice(ev.Text)
		switch ev.Kind {
		case "rate_limit":
			m.mc.react("tired", 8*time.Second, "Limit reached, switching")
		case "key_switch":
			m.mc.react("noticed", 4*time.Second, "Switching key")
		case "fallback":
			m.mc.react("surprise", 3*time.Second, "Fallback model")
		case "no_key":
			m.mc.react("surprise", 5*time.Second, "No key: /keys")
		}

	case "tool_call":
		m.openAsst = -1
		desc := ev.Desc
		if desc == "" {
			desc = shorten(ev.Args, 100)
		}
		m.entries = append(m.entries, entry{kind: eTool, tool: ev.Tool, text: desc})
		m.mc.react("noticed", 2*time.Second, "")

	case "approval":
		e := ev
		m.approval = &e
		m.appSel = 0
		m.ov = overlay{}
		m.mc.react("surprise", 3*time.Second, "Need your approval")

	case "tool_result":
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].kind == eTool && m.entries[i].tool == ev.Tool && m.entries[i].result == "" {
				m.entries[i].result = ev.Result
				m.entries[i].isErr = ev.IsError
				break
			}
		}
		if ev.IsError {
			m.mc.react("surprise", 2*time.Second, "")
		}

	case "usage":
		m.totIn += ev.In
		m.totOut += ev.Out
		m.totCost += ev.Cost

	case "error":
		m.runErr = true
		m.addError(ev.Text)
		if ev.Kind == "rate_limit" {
			m.mc.base = "tired"
			m.mc.react("tired", 10*time.Second, "Key limit reached")
		} else if ev.Kind == "no_key" {
			m.addNotice("Add a key with /keys (opens a page in your browser).")
			m.mc.react("surprise", 6*time.Second, "No key: /keys")
		} else {
			m.mc.react("surprise", 4*time.Second, "Something went wrong")
		}
	}
}

// ---- вспомогательные ---------------------------------------------------------

func (m *model) addNotice(text string) {
	m.entries = append(m.entries, entry{kind: eNotice, text: text})
}

func (m *model) addError(text string) {
	m.entries = append(m.entries, entry{kind: eError, text: text})
}

func (m model) modeNames() []string {
	if m.status != nil && len(m.status.Modes) > 0 {
		var out []string
		for _, md := range m.status.Modes {
			out = append(out, md.Name)
		}
		return out
	}
	return []string{"small", "medium", "hard", "maximum"}
}

func (m *model) setMode(mode string) tea.Cmd {
	if mode != m.mode {
		m.transFrom, m.transStart = m.mode, m.frame
	}
	m.mode = mode
	m.mc.react("joy", 1500*time.Millisecond, "")
	if m.cl == nil || m.sess == nil {
		return nil
	}
	return settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Mode: &mode})
}

func (m *model) cycleMode() tea.Cmd {
	names := m.modeNames()
	next := names[0]
	for i, n := range names {
		if n == m.mode {
			next = names[(i+1)%len(names)]
			break
		}
	}
	return m.setMode(next)
}

func (m *model) toggleAgent() tea.Cmd {
	if m.cl == nil || m.sess == nil {
		return nil
	}
	on := !m.sess.Agent
	m.sess.Agent = on
	if on {
		m.mc.react("noticed", 2*time.Second, "")
	} else {
		m.mc.react("calm", time.Second, "")
	}
	return settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Agent: &on})
}

func (m *model) newSession() tea.Cmd {
	if m.cl == nil {
		return nil
	}
	m.entries = []entry{{kind: eNotice, text: "New session"}}
	m.scroll = 0
	return newSessionCmd(m.cl, m.mode, m.cwd)
}

func (m model) bodyHeight() int { return max(3, m.height-5) }

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// entriesFromSession восстанавливает ленту из сохранённых сообщений сессии.
func entriesFromSession(s *proto.SessionInfo) []entry {
	out := []entry{{kind: eNotice, text: "Session: " + s.Title}}
	for _, pm := range s.Messages {
		switch pm.Role {
		case "user":
			out = append(out, entry{kind: eUser, text: pm.Content})
		case "assistant":
			if pm.Content != "" {
				out = append(out, entry{kind: eAssistant, text: pm.Content})
			}
			for _, tc := range pm.ToolCalls {
				out = append(out, entry{kind: eTool, tool: tc.Name, text: shorten(tc.Args, 100)})
			}
		case "tool":
			for i := len(out) - 1; i >= 0; i-- {
				if out[i].kind == eTool && out[i].tool == pm.Name && out[i].result == "" {
					out[i].result = pm.Content
					break
				}
			}
		}
	}
	return out
}

// missingKeys возвращает провайдеров, которым нужен ключ (и которые используются
// моделями), но ключей нет.
func missingKeys(st *proto.StatusResp) []string {
	used := map[string]bool{}
	for _, md := range st.Models {
		used[md.Provider] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range st.Providers {
		if used[p.Name] && p.NeedsKey && p.Keys == 0 {
			name := p.Name
			if p.KeyFrom != "" {
				name = p.KeyFrom // the keys are added under the provider that owns them
			}
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// reportFailed tells once when a process Relay started has died, with the last line of its log.
func (m *model) reportFailed(st *proto.StatusResp) {
	if m.failSeen == nil {
		m.failSeen = map[string]bool{}
	}
	for _, p := range st.Processes {
		if p.State != "failed" {
			m.failSeen[p.Name] = false
			continue
		}
		if m.failSeen[p.Name] {
			continue
		}
		m.failSeen[p.Name] = true
		why := p.Error
		if n := len(p.Log); n > 0 {
			why = p.Log[n-1]
		}
		m.addError(p.Name + " did not start: " + shorten(why, 200))
	}
}

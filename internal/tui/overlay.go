package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/proto"
)

// Lists above the input line, used for everything you choose from: the model list
// (/models), saved sessions (/sessions), running servers (/stop) and the agent's
// questions (allow / always / deny). Each row is a "button": move with ↑ ↓ and press
// enter, or click it with the mouse.

type ovKind int

const (
	ovNone ovKind = iota
	ovSessions
	ovStop
)

// overlay is a list opened by a command (the model list is driven by the input line instead).
type overlay struct {
	kind  ovKind
	items []pickItem
	sel   int
}

// activeList is the list that is on screen now.
type activeList struct {
	kind  string // "overlay" | "models"
	head  string
	note  []string // lines between the head and the rows
	items []pickItem
	sel   int
	empty string
}

// listDraw is a drawn list and where its rows are (for mouse clicks).
type listDraw struct {
	lines []string
	first int // index in lines of the first visible row
	start int // index in items of the first visible row
	rows  int // number of visible rows
}

func listStart(n, sel, rows int) int {
	if sel >= rows {
		return sel - rows + 1
	}
	return 0
}

func drawList(a activeList, w, maxH int, st styles) listDraw {
	n := len(a.items)
	lines := []string{st.dim.Render(a.head)}
	lines = append(lines, a.note...)
	rows := min(n, pickMaxRows, max(1, maxH-len(lines)-1))
	if n == 0 && a.empty != "" {
		lines = append(lines, st.dim.Render("  "+a.empty))
	}
	d := listDraw{first: len(lines), rows: rows}
	d.start = listStart(n, a.sel, rows)
	idW := 0
	for _, it := range a.items {
		idW = max(idW, runeLen(it.label()))
	}
	idW = min(idW, max(12, w/2))
	for i := d.start; i < d.start+rows && i < n; i++ {
		it := a.items[i]
		id := padRight(shorten(it.label(), idW), idW)
		if i == a.sel {
			lines = append(lines, st.accentBold.Render("❯ "+id)+"  "+st.dim.Render(it.desc))
		} else {
			lines = append(lines, "  "+st.text.Render(id)+"  "+st.dim.Render(it.desc))
		}
	}
	d.lines = lines
	return d
}

// active returns the list to show (the agent question has its own drawing, see approval.go).
func (m model) active(w int, st styles) (activeList, bool) {
	switch {
	case m.ov.kind != ovNone:
		a := activeList{kind: "overlay", items: m.ov.items, sel: min(m.ov.sel, max(0, len(m.ov.items)-1))}
		switch m.ov.kind {
		case ovSessions:
			a.head = fmt.Sprintf("sessions %d/%d · ↑↓ select · enter open · click a row · esc close", a.sel+1, len(a.items))
		case ovStop:
			a.head = fmt.Sprintf("running %d/%d · ↑↓ select · enter stop · click a row · esc close", a.sel+1, len(a.items))
		}
		return a, true
	case m.pickerOpen():
		items := m.pickerItems()
		sel := m.pickSelected(items)
		pos := 0
		if len(items) > 0 {
			pos = sel + 1
		}
		a := activeList{
			kind: "models", sel: sel, items: items,
			head: fmt.Sprintf("models %d/%d · ↑↓ select · tab fill · enter use · esc close", pos, len(items)),
		}
		if m.catBusy && m.models == nil {
			a.empty = "loading models…"
		} else {
			a.empty = "no models match"
		}
		return a, true
	}
	return activeList{}, false
}

// overlayLines draws the active list for the view; nil if there is none.
func (m model) overlayLines(w, maxH int, st styles) []string {
	a, ok := m.active(w, st)
	if !ok {
		return nil
	}
	return drawList(a, w, maxH, st).lines
}

// ---- keys --------------------------------------------------------------------

// overlayKey handles a key while an agent question or a list is open.
// handled=false lets the key go on (typing into the input line).
func (m *model) overlayKey(key string) (handled bool, cmd tea.Cmd) {
	if m.approval != nil {
		if decision := m.approvalKey(key); decision != "" {
			return true, m.decide(decision)
		}
		return true, nil // nothing else works while the agent waits
	}
	if m.ov.kind == ovNone {
		return false, nil
	}
	n := len(m.ov.items)
	switch key {
	case "up":
		if n > 0 {
			m.ov.sel = (m.ov.sel + n - 1) % n
		}
	case "down":
		if n > 0 {
			m.ov.sel = (m.ov.sel + 1) % n
		}
	case "enter":
		return true, m.chooseOverlay()
	case "esc":
		m.ov = overlay{}
	}
	return true, nil
}

// decide answers the agent's question.
func (m *model) decide(decision string) tea.Cmd {
	if m.approval == nil || m.cl == nil {
		return nil
	}
	id := m.approval.CallID
	m.approval, m.appSel = nil, 0
	m.mc.react("noticed", 1500*time.Millisecond, "")
	return approveCmd(m.cl, id, decision)
}

// chooseOverlay acts on the selected row of a list opened by a command.
func (m *model) chooseOverlay() tea.Cmd {
	o := m.ov
	m.ov = overlay{}
	if o.sel < 0 || o.sel >= len(o.items) || m.cl == nil {
		return nil
	}
	it := o.items[o.sel]
	switch o.kind {
	case ovSessions:
		return loadCmd(m.cl, it.id)
	case ovStop:
		return procCmd(m.cl, it.id, "stop")
	}
	return nil
}

// ---- mouse -------------------------------------------------------------------

// click handles a left click at screen cell (x, y). It returns false if the click
// was not on a row of the open list.
func (m *model) click(x, y int) (bool, tea.Cmd) {
	w := m.mainWidth() - 1
	st := newStyles(themeFor(m.mode, 0))
	if x >= m.mainWidth() {
		return false, nil
	}
	bodyH := m.bodyHeight()
	if m.approval != nil {
		lines := m.approvalLines(w, bodyH, st)
		n := len(approvalOptions)
		i := y - 1 - (bodyH - len(lines)) - (len(lines) - n) // the options are the last n lines
		if i < 0 || i >= n {
			return true, nil // clicks elsewhere do nothing while the agent waits
		}
		return true, m.decide(approvalOptions[i].decision)
	}
	a, ok := m.active(w, st)
	if !ok {
		return false, nil
	}
	d := drawList(a, w, bodyH, st)
	k := y - 1 - (bodyH - len(d.lines)) // line of the list under the click (row 0 of the screen is the header)
	i := k - d.first
	if i < 0 || i >= d.rows || d.start+i >= len(a.items) {
		return false, nil
	}
	idx := d.start + i
	if a.kind == "overlay" {
		m.ov.sel = idx
		return true, m.chooseOverlay()
	}
	m.pickSel = idx // models
	m.input.SetValue("")
	return true, m.useModel(a.items[idx].id)
}

// ---- opening lists -----------------------------------------------------------

type stopListMsg struct{ procs []proto.ProcInfo }

func stopListCmd(m model) tea.Cmd {
	cl := m.cl
	return func() tea.Msg {
		st, err := cl.Status()
		if err != nil {
			return errMsg{err}
		}
		return stopListMsg{procs: st.Processes}
	}
}

func isRunning(p proto.ProcInfo) bool { return p.State == "ready" || p.State == "starting" }

func (m *model) openStopList(procs []proto.ProcInfo) {
	var items []pickItem
	for _, p := range procs {
		if !isRunning(p) {
			continue
		}
		desc := p.State
		if p.External {
			desc = "running outside Relay"
		}
		items = append(items, pickItem{id: p.Name, title: p.Name, desc: desc})
	}
	if len(items) == 0 {
		m.addNotice("Nothing is running :)")
		return
	}
	m.ov = overlay{kind: ovStop, items: items}
}

func (m *model) openSessions(list []proto.SessionInfo) {
	var items []pickItem
	for _, s := range list {
		current := m.sess != nil && s.ID == m.sess.ID
		if (s.Title == "" || s.Title == "New session") && !current {
			continue // empty sessions are only noise
		}
		if len(items) >= 30 {
			break
		}
		desc := s.Updated.Local().Format("Jan 2 15:04") + " · " + s.Mode
		if current {
			desc += " · current"
		}
		title := s.Title
		if title == "" || title == "New session" {
			title = "(empty)"
		}
		items = append(items, pickItem{id: s.ID, title: title, desc: desc})
	}
	if len(items) == 0 {
		m.addNotice("No saved sessions")
		return
	}
	m.ov = overlay{kind: ovSessions, items: items}
}

// procsRunning is the text for the side panel: names of the running servers.
func procsRunning(st *proto.StatusResp) []proto.ProcInfo {
	var out []proto.ProcInfo
	if st == nil {
		return nil
	}
	for _, p := range st.Processes {
		if isRunning(p) {
			out = append(out, p)
		}
	}
	return out
}

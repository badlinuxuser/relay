package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"relay/internal/client"
	"relay/internal/proto"
)

// The model picker: typing /models (or /model on the way to it) opens a list above
// the input line. The text after the command filters the list.

const (
	pickMaxRows  = 8
	catalogFresh = 20 * time.Second // how long the client trusts the list it already has
	autoDesc     = "the mode's own model chain"
)

type pickItem struct {
	id   string // "" for auto
	desc string
}

type catalogMsg struct {
	resp *proto.ModelsResp
	err  error
}

func catalogCmd(cl *client.Client, refresh bool) tea.Cmd {
	return func() tea.Msg {
		r, err := cl.Models(refresh)
		return catalogMsg{resp: r, err: err}
	}
}

// pickerQuery tells whether the input line is a /models command and returns the
// filter text after it.
func pickerQuery(v string) (filter string, ok bool) {
	switch {
	case v == "/model" || v == "/models":
		return "", true
	case strings.HasPrefix(v, "/models "):
		return strings.TrimSpace(strings.TrimPrefix(v, "/models ")), true
	case strings.HasPrefix(v, "/model "):
		return strings.TrimSpace(strings.TrimPrefix(v, "/model ")), true
	}
	return "", false
}

func (m model) pickerOpen() bool {
	if m.cl == nil || m.approval != nil {
		return false
	}
	v := m.input.Value()
	if v == m.pickClosedAt {
		return false
	}
	_, ok := pickerQuery(v)
	return ok
}

// modelList is every model to offer: the full list from the backend, or (until it
// arrives) the models from config.toml that came with the status.
func (m model) modelList() []proto.ModelInfo {
	if m.models != nil {
		return m.models.Models
	}
	if m.status != nil {
		return m.status.Models
	}
	return nil
}

func providerLabel(name string) string { return strings.ReplaceAll(name, "-", " ") }

func describeModel(md proto.ModelInfo) string {
	return providerLabel(md.Provider) + " · " + md.Name
}

// pickerItems is the list after filtering: "auto" first, then the models.
func (m model) pickerItems() []pickItem {
	filter, _ := pickerQuery(m.input.Value())
	words := strings.Fields(strings.ToLower(filter))
	match := func(text string) bool {
		text = strings.ToLower(text)
		for _, w := range words {
			if !strings.Contains(text, w) {
				return false
			}
		}
		return true
	}
	var items []pickItem
	if match("auto " + autoDesc) {
		items = append(items, pickItem{id: "", desc: autoDesc})
	}
	for _, md := range m.modelList() {
		desc := describeModel(md)
		if match(md.ID + " " + desc + " " + md.Kind) {
			items = append(items, pickItem{id: md.ID, desc: desc})
		}
	}
	return items
}

func (it pickItem) label() string {
	if it.id == "" {
		return "auto"
	}
	return it.id
}

func (m model) pickSelected(items []pickItem) int {
	if len(items) == 0 {
		return 0
	}
	return min(max(m.pickSel, 0), len(items)-1)
}

// catStale: the list of models is missing or old, and nobody is fetching it now.
func (m model) catStale() bool {
	return m.cl != nil && !m.catBusy && (m.models == nil || time.Since(m.catAt) > catalogFresh)
}

// syncPicker is called after the input line changed: it resets the selection,
// starts at the model that is in use, and asks the backend for a fresh list.
func (m *model) syncPicker(prev string) tea.Cmd {
	cur := m.input.Value()
	if cur == prev {
		return nil
	}
	m.pickSel = 0
	m.pickClosedAt = ""
	filter, ok := pickerQuery(cur)
	if !ok {
		return nil
	}
	// with no filter (also on /model -> /models) start at the model that is in use
	if filter == "" && m.sess != nil && m.sess.Model != "" {
		for i, it := range m.pickerItems() {
			if it.id == m.sess.Model {
				m.pickSel = i
				break
			}
		}
	}
	if m.catStale() {
		m.catBusy = true
		return catalogCmd(m.cl, false)
	}
	return nil
}

// pickerKey handles a key while the picker is open. handled=false lets the key go
// on to the normal handling (typing, enter on a typed id, ...).
func (m *model) pickerKey(key string) (handled bool, cmd tea.Cmd) {
	items := m.pickerItems()
	sel := m.pickSelected(items)
	switch key {
	case "up":
		if n := len(items); n > 0 {
			m.pickSel = (sel + n - 1) % n
		}
		return true, nil
	case "down":
		if n := len(items); n > 0 {
			m.pickSel = (sel + 1) % n
		}
		return true, nil
	case "tab":
		if len(items) == 0 {
			return true, nil
		}
		prev := m.input.Value()
		m.input.SetValue("/models " + items[sel].label())
		m.input.CursorEnd()
		cmd = m.syncPicker(prev)
		m.pickSel = 0
		return true, cmd
	case "esc":
		m.pickClosedAt = m.input.Value()
		return true, nil
	case "enter":
		if len(items) == 0 {
			return false, nil // an id that is not in the list: try it as typed
		}
		m.input.SetValue("")
		return true, m.useModel(items[sel].id)
	}
	return false, nil
}

// useModel switches the session to the model ("" = auto).
func (m *model) useModel(id string) tea.Cmd {
	if m.cl == nil || m.sess == nil {
		m.addNotice("Not connected to the backend yet")
		return nil
	}
	if id == "" || id == "auto" {
		m.addNotice("Model: auto (" + autoDesc + ")")
		id = ""
	} else {
		m.addNotice("Model: " + id)
	}
	m.mc.react("noticed", 1500*time.Millisecond, "")
	return settingsCmd(m.cl, m.sess.ID, proto.SettingsReq{Model: &id})
}

// listModels prints the models in the feed (/models without a picker).
func (m *model) listModels() {
	list := m.modelList()
	if len(list) == 0 {
		m.addNotice("No models found yet. Start Ollama or add keys with /keys, then try /models again.")
		return
	}
	var order []string
	byProv := map[string][]string{}
	for _, md := range list {
		if _, ok := byProv[md.Provider]; !ok {
			order = append(order, md.Provider)
		}
		byProv[md.Provider] = append(byProv[md.Provider], md.Name)
	}
	m.addNotice("Models (/models <text> to pick, or type /models and use ↑↓):")
	for _, p := range order {
		m.addNotice(fmt.Sprintf("%s (%d): %s", providerLabel(p), len(byProv[p]), strings.Join(byProv[p], ", ")))
	}
}

// providerProblems is a short line about providers whose model list could not be
// fetched (wrong key, server down, ...).
func (m model) providerProblems() string {
	if m.models == nil {
		return ""
	}
	var parts []string
	for _, p := range m.models.Providers {
		if p.Error != "" {
			parts = append(parts, p.Name+": "+shorten(p.Error, 70))
		}
	}
	return strings.Join(parts, " · ")
}

// pickerLines draws the picker; the lines replace the bottom of the feed.
func (m model) pickerLines(w, maxH int, st styles) []string {
	items := m.pickerItems()
	n := len(items)
	sel := m.pickSelected(items)

	pos := 0
	if n > 0 {
		pos = sel + 1
	}
	lines := []string{st.dim.Render(fmt.Sprintf("models %d/%d · ↑↓ select · tab fill · enter use · esc close", pos, n))}

	prob := m.providerProblems()
	rows := min(n, pickMaxRows, max(1, maxH-2))
	if n == 0 {
		switch {
		case m.catBusy && m.models == nil:
			lines = append(lines, st.dim.Render("  loading models…"))
		default:
			lines = append(lines, st.dim.Render("  no models match"))
		}
	}
	start := 0
	if sel >= rows {
		start = sel - rows + 1
	}
	idW := 0
	for _, it := range items {
		idW = max(idW, runeLen(it.label()))
	}
	idW = min(idW, max(12, w/2))
	for i := start; i < start+rows && i < n; i++ {
		it := items[i]
		id := padRight(shorten(it.label(), idW), idW)
		if i == sel {
			lines = append(lines, st.accentBold.Render("❯ "+id)+"  "+st.dim.Render(it.desc))
		} else {
			lines = append(lines, "  "+st.text.Render(id)+"  "+st.dim.Render(it.desc))
		}
	}
	if prob != "" && len(lines) < maxH {
		lines = append(lines, st.red.Render("  ✗ "+shorten(prob, max(10, w-6))))
	}
	return lines
}

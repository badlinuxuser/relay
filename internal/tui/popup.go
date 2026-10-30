package tui

import (
	"fmt"
	"strings"
)

// The /model popup: typing "/model " opens a list of all models above the input.
// Up/down select, tab fills the input, enter applies, esc closes.

const popupRows = 8

type suggestion struct{ id, info string }

// modelSuggestions lists models for the popup, filtered by what is typed after "/model ".
func (m model) modelSuggestions() []suggestion {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/model ") || m.status == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(v, "/model ")))
	var out []suggestion
	add := func(id, info string) {
		if q == "" || strings.Contains(strings.ToLower(id), q) {
			out = append(out, suggestion{id, info})
		}
	}
	add("auto", "the mode's own model chain")
	for _, md := range m.status.Models {
		info := md.Provider
		if md.Name != "" && md.Name != md.ID {
			info += " · " + md.Name
		}
		add(md.ID, info)
	}
	// an exact match goes first, so that enter applies what was typed
	for i, it := range out {
		if q != "" && strings.ToLower(it.id) == q && i > 0 {
			out[0], out[i] = out[i], out[0]
			break
		}
	}
	return out
}

// selIndex keeps the highlighted row inside the list.
func (m model) selIndex(n int) int {
	if m.sel < 0 {
		return 0
	}
	if m.sel >= n {
		return n - 1
	}
	return m.sel
}

// modelPopup renders the popup lines (header first); nil when it is closed.
func (m model) modelPopup(w int, st styles) []string {
	items := m.modelSuggestions()
	if len(items) == 0 {
		return nil
	}
	sel := m.selIndex(len(items))
	start := 0
	if sel >= popupRows {
		start = sel - popupRows + 1
	}
	end := min(len(items), start+popupRows)
	out := []string{st.dim.Render(fmt.Sprintf(" models %d/%d · ↑↓ select · tab fill · enter use · esc close", sel+1, len(items)))}
	idw := 0
	for _, it := range items[start:end] {
		idw = max(idw, runeLen(it.id))
	}
	idw = min(idw, max(10, w/2))
	infoW := max(8, w-idw-8)
	for i := start; i < end; i++ {
		it := items[i]
		id := padRight(shorten(it.id, idw), idw)
		if i == sel {
			out = append(out, " "+st.accentBold.Render("❯ "+id)+"  "+st.gray.Render(shorten(it.info, infoW)))
		} else {
			out = append(out, "   "+st.text.Render(id)+"  "+st.dim.Render(shorten(it.info, infoW)))
		}
	}
	return out
}

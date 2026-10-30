package tui

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

const (
	sidebarW   = 48
	transTicks = 14 // длительность анимации смены режима, тиков по 120 мс
)

var spin = []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"}

func (m model) showSidebar() bool { return m.width >= 104 }

// transition возвращает прогресс анимации смены режима (0..1) и признак «идёт».
func (m model) transition() (float64, bool) {
	if m.transFrom == "" {
		return 1, false
	}
	d := m.frame - m.transStart
	if d >= transTicks {
		return 1, false
	}
	x := float64(d) / transTicks
	return 1 - math.Pow(1-x, 3), true
}

// currentTheme это тема режима; во время смены режима цвета плавно перетекают.
func (m model) currentTheme() theme {
	to := themeFor(m.mode, m.frame)
	if p, on := m.transition(); on {
		return blendTheme(themeFor(m.transFrom, m.frame), to, p)
	}
	return to
}

func (m model) mainWidth() int {
	if m.showSidebar() {
		return m.width - sidebarW - 3
	}
	return m.width
}

// maxScroll это максимальная прокрутка вверх (в строках) для текущей ленты.
func (m model) maxScroll() int {
	st := newStyles(themeFor(m.mode, 0))
	return max(0, len(m.transcript(m.mainWidth()-1, st))-m.bodyHeight())
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// wrapText переносит текст по словам; короткие строки сохраняются как есть
// (чтобы не ломать отступы в коде).
func wrapText(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	var out []string
	for _, para := range strings.Split(s, "\n") {
		para = strings.TrimRight(para, " \r")
		if runeLen(para) <= w {
			out = append(out, para)
			continue
		}
		cur, curLen := "", 0
		for _, word := range strings.Fields(para) {
			wl := runeLen(word)
			for wl > w {
				if curLen > 0 {
					out = append(out, cur)
					cur, curLen = "", 0
				}
				r := []rune(word)
				out = append(out, string(r[:w]))
				word = string(r[w:])
				wl = len(r) - w
			}
			switch {
			case curLen == 0:
				cur, curLen = word, wl
			case curLen+1+wl <= w:
				cur += " " + word
				curLen += 1 + wl
			default:
				out = append(out, cur)
				cur, curLen = word, wl
			}
		}
		if curLen > 0 {
			out = append(out, cur)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func padRight(s string, w int) string {
	n := lipgloss.Width(s)
	if n > w {
		return lipgloss.NewStyle().MaxWidth(w).Render(s)
	}
	return s + strings.Repeat(" ", w-n)
}

// paintLine обрезает строку по ширине и заливает фоном темы: после каждого сброса
// стиля фон включается заново, а хвост добивается пробелами.
func paintLine(line string, width int, bgSeq string) string {
	line = lipgloss.NewStyle().MaxWidth(width).Render(line)
	if bgSeq == "" {
		return line
	}
	pad := max(0, width-lipgloss.Width(line))
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bgSeq)
	return bgSeq + line + strings.Repeat(" ", pad) + "\x1b[0m"
}

// ---- лента -------------------------------------------------------------------

func renderEntry(e entry, w int, st styles) []string {
	body := max(8, w-2)
	var out []string
	switch e.kind {
	case eUser:
		for i, l := range wrapText(e.text, body) {
			p := "  "
			if i == 0 {
				p = st.accentBold.Render("❯ ")
			}
			out = append(out, p+st.bold.Render(l))
		}
		out = append(out, "")

	case eAssistant:
		for i, l := range wrapText(e.text, body) {
			p := "  "
			if i == 0 {
				p = st.accent.Render("● ")
			}
			out = append(out, p+st.text.Render(l))
		}
		if e.model != "" {
			out = append(out, "  "+st.dim.Render("↳ "+e.model))
		}
		out = append(out, "")

	case eTool:
		head := st.soft.Render("⚙ ") + st.gray.Render(e.tool) + " " + st.dim.Render(shorten(e.text, max(8, body-runeLen(e.tool)-2)))
		out = append(out, head)
		if e.result != "" {
			rs := st.dim
			if e.isErr {
				rs = st.red
			}
			rl := strings.Split(strings.TrimRight(strings.ReplaceAll(e.result, "\t", "    "), "\n"), "\n")
			for i, l := range rl {
				if i >= 6 {
					out = append(out, "    "+st.dim.Render(fmt.Sprintf("… %d more lines", len(rl)-i)))
					break
				}
				out = append(out, "    "+rs.Render(shorten(l, max(8, w-6))))
			}
		}

	case eNotice:
		for _, l := range wrapText(e.text, body) {
			out = append(out, st.dim.Render("· "+l))
		}

	case eError:
		for i, l := range wrapText(e.text, body) {
			p := "  "
			if i == 0 {
				p = "✗ "
			}
			out = append(out, st.red.Render(p+l))
		}
	}
	return out
}

func (m model) transcript(w int, st styles) []string {
	var lines []string
	for _, e := range m.entries {
		lines = append(lines, renderEntry(e, w, st)...)
	}
	return lines
}

// ---- боковая панель ----------------------------------------------------------

func (m model) modelLabel() string {
	if m.streaming && m.curModel != "" {
		return m.curModel
	}
	if m.sess != nil && m.sess.Model != "" {
		return m.sess.Model
	}
	if m.status != nil {
		for _, md := range m.status.Modes {
			if md.Name == m.mode && len(md.Models) > 0 {
				return md.Models[0]
			}
		}
	}
	return "—"
}

func shortLeft(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

func (m model) sidebar(w, h int, th theme, st styles) []string {
	var lines []string
	now := time.Now()

	set := m.sprites[m.mode]
	if set == nil {
		set = m.sprites["small"]
	}
	mood := m.mc.frame(now)
	if cols, rows := set.size(mood); rows > 0 && h >= rows+13 {
		var art string
		if p, on := m.transition(); on {
			scan := mixHex(th.accent, "#FFFFFF", 0.6)
			art = renderSprite(set, m.sprites[m.transFrom], mood, m.frame, th.card, int(p*float64(cols)), scan)
		} else {
			art = renderSprite(set, nil, mood, m.frame, th.card, -1, "")
		}
		lines = append(lines, "")
		for _, l := range strings.Split(art, "\n") {
			lines = append(lines, " "+l)
		}
		lines = append(lines, "")
	}

	label, say := m.mc.caption(now)
	lines = append(lines, " "+st.accentBold.Render("◆ "+label))
	if say != "" {
		for i, l := range wrapText(say, w-2) {
			if i >= 2 {
				break
			}
			lines = append(lines, " "+st.dim.Render(l))
		}
	}

	sep := st.dim.Render(strings.Repeat("─", w))
	kv := func(k, v string) string {
		return " " + st.dim.Render(fmt.Sprintf("%-9s", k)) + v
	}
	agent := "off"
	if m.sess != nil && m.sess.Agent {
		agent = "on"
	}
	lines = append(lines, sep,
		kv("mode", st.accentBold.Render(m.mode)),
		kv("model", st.text.Render(shorten(m.modelLabel(), w-11))),
		kv("agent", st.text.Render(agent)),
		kv("folder", st.text.Render(shortLeft(shortPath(m.cwd), w-11))),
		kv("tokens", st.text.Render(fmt.Sprintf("%d / %d", m.totIn, m.totOut))),
		kv("cost", st.text.Render(fmt.Sprintf("$%.4f", m.totCost))),
	)

	if m.status != nil && len(m.status.Processes) > 0 {
		lines = append(lines, sep, " "+st.gray.Render("processes"))
		for _, p := range m.status.Processes {
			dot, style := "○", st.dim
			switch p.State {
			case "ready":
				dot, style = "●", st.accent
			case "starting":
				dot, style = "◐", st.gray
			case "failed":
				dot, style = "✗", st.red
			}
			label := p.State
			if p.External {
				label = "running (external)"
			}
			lines = append(lines, " "+style.Render(dot)+" "+st.text.Render(p.Name)+" "+st.dim.Render(label))
		}
	}

	if m.status != nil {
		var keyLines []string
		for _, p := range m.status.Providers {
			if p.Keys == 0 && !p.NeedsKey {
				continue
			}
			switch {
			case p.Keys == 0:
				keyLines = append(keyLines, " "+st.red.Render(fmt.Sprintf("%s  no key", p.Name)))
			case p.KeysAvailable == 0:
				keyLines = append(keyLines, " "+st.red.Render(fmt.Sprintf("%s  %d/%d", p.Name, p.KeysAvailable, p.Keys)))
			default:
				keyLines = append(keyLines, " "+st.text.Render(fmt.Sprintf("%s  %d/%d", p.Name, p.KeysAvailable, p.Keys)))
			}
		}
		if len(keyLines) > 0 {
			lines = append(lines, sep, " "+st.gray.Render("keys")+"  "+st.dim.Render("/keys: add"))
			lines = append(lines, keyLines...)
		}
	}

	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// ---- шапка, ввод, подсказки --------------------------------------------------

func (m model) header(st styles) string {
	agent := "off"
	if m.sess != nil && m.sess.Agent {
		agent = "on"
	}
	modeLabel := st.accent.Render(m.mode)
	if _, on := m.transition(); on {
		modeLabel = st.dim.Render(m.transFrom) + st.dim.Render(" → ") + st.accentBold.Render(m.mode)
	}
	s := " " + st.accentBold.Render("◆ Relay") + "  " +
		st.dim.Render("mode ") + modeLabel + "  " +
		st.dim.Render("agent ") + st.text.Render(agent)
	if m.cl == nil {
		s += st.dim.Render("  connecting…")
	}
	if !m.showSidebar() {
		label, _ := m.mc.caption(time.Now())
		s += "  " + st.dim.Render("· "+label)
	}
	return s
}

func (m model) footer(th theme, st styles) []string {
	w := m.width - 2
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(w)
	if m.approval != nil {
		text := fmt.Sprintf("⚠ %s: %s   [y] once · [a] always · [n] no", m.approval.Tool, shorten(m.approval.Desc, max(10, w-50)))
		box = box.BorderForeground(lipgloss.Color(colRed))
		return strings.Split(box.Render(st.text.Render(shorten(text, max(10, w-4)))), "\n")
	}
	in := m.input
	in.Prompt = "❯ "
	in.PromptStyle = st.accentBold
	in.TextStyle = st.text
	in.PlaceholderStyle = st.dim
	in.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(th.accent))
	in.Width = max(5, w-7)
	border := th.accent
	if m.streaming {
		border = th.soft
	}
	box = box.BorderForeground(lipgloss.Color(border))
	return strings.Split(box.Render(in.View()), "\n")
}

func (m model) hint(st styles) string {
	if m.streaming {
		return " " + st.accent.Render(spin[m.frame%len(spin)]) + " " + st.gray.Render("working…") + st.dim.Render("  esc to stop")
	}
	return " " + st.dim.Render("enter send · ctrl+t mode · ctrl+a agent · ctrl+n new · pgup/pgdn scroll · /help")
}

// ---- сборка экрана -----------------------------------------------------------

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	th := m.currentTheme()
	st := newStyles(th)
	bgSeq := bgSequence(th.bg)

	bodyH := m.bodyHeight()
	mainW := m.mainWidth()

	all := m.transcript(mainW-1, st)
	maxScroll := max(0, len(all)-bodyH)
	scroll := min(m.scroll, maxScroll)
	start := max(0, len(all)-bodyH-scroll)
	end := min(len(all), start+bodyH)
	visible := all[start:end]

	popup := m.modelPopup(mainW-1, st)
	if len(popup) > bodyH-1 {
		popup = popup[len(popup)-(bodyH-1):]
	}

	var side []string
	if m.showSidebar() {
		side = m.sidebar(sidebarW-1, bodyH, th, st)
	}

	out := make([]string, 0, m.height)
	out = append(out, paintLine(m.header(st), m.width, bgSeq))
	for i := 0; i < bodyH; i++ {
		left := ""
		if i < len(visible) {
			left = visible[i]
		}
		if k := i - (bodyH - len(popup)); len(popup) > 0 && k >= 0 {
			left = popup[k]
		}
		row := padRight(left, mainW)
		if m.showSidebar() {
			right := ""
			if i < len(side) {
				right = side[i]
			}
			row += " " + st.dim.Render("│") + " " + padRight(right, sidebarW)
		}
		out = append(out, paintLine(row, m.width, bgSeq))
	}
	for _, l := range m.footer(th, st) {
		out = append(out, paintLine(l, m.width, bgSeq))
	}
	out = append(out, paintLine(m.hint(st), m.width, bgSeq))
	return strings.Join(out, "\n")
}

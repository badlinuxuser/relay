package tui

// When the agent wants to do something that needs permission (write a file, run a
// command, use an MCP tool), a list opens above the input line, like the model list:
// ↑ ↓ and enter, or one key.

type approvalOption struct {
	decision string   // allow | always | deny (what the backend expects)
	keys     []string // keys that choose it (Latin and the same physical keys on a Russian layout)
	hint     string   // shown at the right
}

var approvalOptions = []approvalOption{
	{"allow", []string{"y", "Y", "н", "Н"}, "y"},
	{"always", []string{"a", "A", "ф", "Ф"}, "a"},
	{"deny", []string{"n", "N", "т", "Т", "esc"}, "n"},
}

// approvalKey handles a key while the list is open and returns the decision
// ("allow", "always", "deny") once there is one, otherwise "".
func (m *model) approvalKey(key string) string {
	n := len(approvalOptions)
	switch key {
	case "up":
		m.appSel = (m.appSel + n - 1) % n
	case "down":
		m.appSel = (m.appSel + 1) % n
	case "enter":
		return approvalOptions[m.appSel].decision
	case "pgup":
		m.scroll = min(m.scroll+max(3, m.bodyHeight()/2), m.maxScroll())
	case "pgdown":
		m.scroll = max(0, m.scroll-max(3, m.bodyHeight()/2))
	default:
		for _, o := range approvalOptions {
			for _, k := range o.keys {
				if k == key {
					return o.decision
				}
			}
		}
	}
	return ""
}

func approvalLabel(i int, tool string) string {
	switch i {
	case 0:
		return "Allow once"
	case 1:
		return "Always allow " + tool + " in this session"
	}
	return "Deny"
}

// approvalLines draws the list; the lines replace the bottom of the feed.
func (m model) approvalLines(w, maxH int, st styles) []string {
	ev := m.approval
	if ev == nil {
		return nil
	}
	head := st.red.Render("⚠ approval needed") + st.dim.Render(" · "+ev.Tool+" · ↑↓ select · enter confirm · y once · a always · n deny")

	what := ev.Desc
	if what == "" {
		what = ev.Args
	}
	lines := []string{head}
	descMax := min(4, max(0, maxH-1-len(approvalOptions)))
	shown := wrapText(what, max(10, w-4))
	truncated := len(shown) > descMax
	if truncated {
		shown = shown[:descMax]
	}
	for i, l := range shown {
		if truncated && i == len(shown)-1 {
			l = shorten(l, max(8, w-8)) + " …"
		}
		lines = append(lines, "  "+st.text.Render(l))
	}

	labelW := 0
	for i := range approvalOptions {
		labelW = max(labelW, runeLen(approvalLabel(i, ev.Tool)))
	}
	labelW = min(labelW, max(14, w-8))
	for i, o := range approvalOptions {
		label := padRight(shorten(approvalLabel(i, ev.Tool), labelW), labelW)
		if i == m.appSel {
			lines = append(lines, st.accentBold.Render("❯ "+label)+"  "+st.dim.Render(o.hint))
		} else {
			lines = append(lines, "  "+st.text.Render(label)+"  "+st.dim.Render(o.hint))
		}
	}
	return lines
}

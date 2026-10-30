package tui

import (
	"math"

	"github.com/charmbracelet/lipgloss"
)

const (
	colText = "#E6E8EA"
	colGray = "#9AA0A6"
	colDim  = "#5C6168"
	colRed  = "#F26D6D"
)

// theme это цвета интерфейса для режима. Режим задаёт и спрайты маскота:
// small = зелёный, medium = фиолетовый, hard = красный, maximum = радуга.
type theme struct {
	name   string
	accent string
	soft   string
	bg     string
	card   string // фон карточки маскота: чуть светлее bg, чтобы чёрный капюшон читался
}

// themeFor вычисляет тему; для maximum цвета плывут по радуге вместе с кадром t.
func themeFor(mode string, t int) theme {
	switch mode {
	case "medium":
		return withCard(theme{name: "medium", accent: "#A07CFF", soft: "#5B3FB0", bg: "#110E1F"})
	case "hard":
		return withCard(theme{name: "hard", accent: "#FF4D6A", soft: "#A02A40", bg: "#180C11"})
	case "maximum":
		h := math.Mod(float64(t)*0.012, 1)
		return withCard(theme{name: "maximum", accent: hsvToHex(h, 0.62, 1), soft: hsvToHex(h+0.5, 0.45, 0.62), bg: "#0F1014"})
	default:
		return withCard(theme{name: "small", accent: "#67DD5D", soft: "#2E8B3E", bg: "#0C1510"})
	}
}

type styles struct {
	accent, accentBold, soft, text, bold, gray, dim, red lipgloss.Style
}

func newStyles(th theme) styles {
	fg := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	return styles{
		accent:     fg(th.accent),
		accentBold: fg(th.accent).Bold(true),
		soft:       fg(th.soft),
		text:       fg(colText),
		bold:       fg(colText).Bold(true),
		gray:       fg(colGray),
		dim:        fg(colDim),
		red:        fg(colRed),
	}
}

// bgSequence получает у lipgloss escape-код фона под цветовой профиль терминала.
func bgSequence(bg string) string {
	s := lipgloss.NewStyle().Background(lipgloss.Color(bg)).Render("X")
	for i := 0; i < len(s); i++ {
		if s[i] == 'X' {
			return s[:i]
		}
	}
	return ""
}

func withCard(t theme) theme {
	t.card = mixHex(t.bg, "#FFFFFF", 0.09)
	return t
}

// mixHex смешивает два цвета: p = 0 даёт a, p = 1 даёт b.
func mixHex(a, b string, p float64) string {
	ca, cb := parseHex(a), parseHex(b)
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*p) }
	return rgb{m(ca.r, cb.r), m(ca.g, cb.g), m(ca.b, cb.b)}.hex()
}

// blendTheme плавно переводит тему from в тему to (p от 0 до 1).
func blendTheme(from, to theme, p float64) theme {
	return withCard(theme{
		name:   to.name,
		accent: mixHex(from.accent, to.accent, p),
		soft:   mixHex(from.soft, to.soft, p),
		bg:     mixHex(from.bg, to.bg, p),
	})
}

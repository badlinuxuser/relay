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
}

// themeFor вычисляет тему; для maximum цвета плывут по радуге вместе с кадром t.
func themeFor(mode string, t int) theme {
	switch mode {
	case "medium":
		return theme{"medium", "#A07CFF", "#5B3FB0", "#110E1F"}
	case "hard":
		return theme{"hard", "#FF4D6A", "#A02A40", "#180C11"}
	case "maximum":
		h := math.Mod(float64(t)*0.012, 1)
		return theme{"maximum", hsvToHex(h, 0.62, 1), hsvToHex(h+0.5, 0.45, 0.62), "#0F1014"}
	default:
		return theme{"small", "#67DD5D", "#2E8B3E", "#0C1510"}
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

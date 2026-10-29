package tui

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Спрайты собираются tools/sprites/build.py из листов в assets/sheets.
// Каждая ячейка терминала это символ Брайля (блок 2x4 пикселя) с цветом точек (fg)
// и цветом фона (bg). Тема maximum: «акцентные» цвета палитры перекрашиваются
// в радугу, оттенок зависит от позиции ячейки и времени.

//go:embed sprites/*.json
var spriteFS embed.FS

const spriteChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

type rgb struct{ r, g, b uint8 }

func (c rgb) hex() string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }

type frame struct {
	w, h int
	ch   [][]rune
	fg   []string
	bg   []string
}

type spriteSet struct {
	pal     []rgb
	accent  map[int]bool
	rainbow bool
	frames  map[string]*frame
}

type spriteFile struct {
	Palette []string `json:"palette"`
	Accent  []int    `json:"accent"`
	Rainbow bool     `json:"rainbow"`
	Frames  map[string]struct {
		W  int      `json:"w"`
		H  int      `json:"h"`
		Ch []string `json:"ch"`
		Fg []string `json:"fg"`
		Bg []string `json:"bg"`
	} `json:"frames"`
}

func parseHex(s string) rgb {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return rgb{}
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgb{}
	}
	return rgb{uint8(v >> 16), uint8(v >> 8), uint8(v)}
}

func loadSprites() map[string]*spriteSet {
	out := map[string]*spriteSet{}
	for _, name := range []string{"small", "medium", "hard", "maximum"} {
		b, err := spriteFS.ReadFile("sprites/" + name + ".json")
		if err != nil {
			panic("relay: missing sprite set " + name + " (run tools/sprites/build.py)")
		}
		var f spriteFile
		if err := json.Unmarshal(b, &f); err != nil {
			panic("relay: bad sprite set " + name + ": " + err.Error())
		}
		s := &spriteSet{accent: map[int]bool{}, rainbow: f.Rainbow, frames: map[string]*frame{}}
		for _, p := range f.Palette {
			s.pal = append(s.pal, parseHex(p))
		}
		for _, a := range f.Accent {
			s.accent[a] = true
		}
		for fname, fr := range f.Frames {
			nf := &frame{w: fr.W, h: fr.H, fg: fr.Fg, bg: fr.Bg}
			for _, row := range fr.Ch {
				nf.ch = append(nf.ch, []rune(row))
			}
			s.frames[fname] = nf
		}
		out[name] = s
	}
	return out
}

func rgbToHSV(c rgb) (h, s, v float64) {
	r, g, b := float64(c.r)/255, float64(c.g)/255, float64(c.b)/255
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	v = mx
	d := mx - mn
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, v
	}
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h /= 6
	if h < 0 {
		h++
	}
	return h, s, v
}

func hsvToHex(h, s, v float64) string {
	h = math.Mod(h, 1)
	if h < 0 {
		h++
	}
	i := int(h * 6)
	f := h*6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)
	var r, g, b float64
	switch i % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return fmt.Sprintf("#%02x%02x%02x", uint8(r*255), uint8(g*255), uint8(b*255))
}

func (s *spriteSet) color(ci byte, x, y, t int) string {
	if ci == '.' {
		return ""
	}
	idx := strings.IndexByte(spriteChars, ci)
	if idx < 0 || idx >= len(s.pal) {
		return ""
	}
	c := s.pal[idx]
	if s.rainbow && s.accent[idx] {
		_, sat, val := rgbToHSV(c)
		h := float64(x)*0.028 + float64(y)*0.045 + float64(t)*0.012
		return hsvToHex(h, math.Max(sat, 0.75), val)
	}
	return c.hex()
}

// render возвращает кадр как многострочную строку с ANSI-цветами.
// panelBg это цвет фона интерфейса для прозрачных ячеек.
func (s *spriteSet) render(name string, t int, panelBg string) string {
	f := s.frames[name]
	if f == nil {
		f = s.frames["calm"]
	}
	if f == nil {
		return ""
	}
	var sb strings.Builder
	for y := 0; y < f.h && y < len(f.ch); y++ {
		var run strings.Builder
		curFg, curBg := "\x00", "\x00"
		flush := func() {
			if run.Len() == 0 {
				return
			}
			st := lipgloss.NewStyle().Background(lipgloss.Color(curBg))
			if curFg != "" {
				st = st.Foreground(lipgloss.Color(curFg))
			}
			sb.WriteString(st.Render(run.String()))
			run.Reset()
		}
		for x := 0; x < f.w && x < len(f.ch[y]); x++ {
			ch := f.ch[y][x]
			fg := s.color(f.fg[y][x], x, y, t)
			bg := s.color(f.bg[y][x], x, y, t)
			if bg == "" {
				bg = panelBg
			}
			if ch == ' ' {
				fg = ""
			}
			if fg != curFg || bg != curBg {
				flush()
				curFg, curBg = fg, bg
			}
			run.WriteRune(ch)
		}
		flush()
		if y < f.h-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func (s *spriteSet) height(name string) int {
	if f := s.frames[name]; f != nil {
		return f.h
	}
	return 0
}

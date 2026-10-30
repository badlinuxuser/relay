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
// Кадр это сетка пикселей; два пикселя по вертикали рисуются одной ячейкой
// терминала символом "▀" (верх = цвет текста, низ = цвет фона), поэтому пиксели
// квадратные и чёткие. Прозрачные пиксели показывают цвет карточки.
// Тема maximum: «акцентные» цвета палитры перекрашиваются в радугу, оттенок
// зависит от позиции пикселя и времени.

//go:embed sprites/*.json
var spriteFS embed.FS

const spriteChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

type rgb struct{ r, g, b uint8 }

func (c rgb) hex() string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }

type frame struct {
	w, h int
	px   []string
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
		Px []string `json:"px"`
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
			s.frames[fname] = &frame{w: fr.W, h: fr.H, px: fr.Px}
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

// pixel возвращает цвет пикселя (x, y) кадра или "" для прозрачного.
func (s *spriteSet) pixel(f *frame, x, y, t int) string {
	if y >= len(f.px) || x >= len(f.px[y]) {
		return ""
	}
	ci := f.px[y][x]
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

func (s *spriteSet) frame(name string) *frame {
	if f := s.frames[name]; f != nil {
		return f
	}
	return s.frames["calm"]
}

// size возвращает ширину в ячейках и высоту в строках терминала.
func (s *spriteSet) size(name string) (cols, rows int) {
	f := s.frame(name)
	if f == nil {
		return 0, 0
	}
	return f.w, f.h / 2
}

// renderSprite рисует кадр name набором cur. Если old != nil, идёт переход между
// режимами: колонки левее boundary берутся из cur, правее из old, а на границе
// рисуется яркая линия цвета scan. card это цвет фона карточки.
func renderSprite(cur, old *spriteSet, name string, t int, card string, boundary int, scan string) string {
	f := cur.frame(name)
	if f == nil {
		return ""
	}
	var of *frame
	if old != nil {
		of = old.frame(name)
	}
	var sb strings.Builder
	for y := 0; y+1 < f.h; y += 2 {
		var run strings.Builder
		curKey := ""
		var curFg, curBg string
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
		for x := 0; x < f.w; x++ {
			var top, bot string
			switch {
			case old != nil && of != nil && x == boundary:
				top, bot = scan, scan
			case old != nil && of != nil && x > boundary:
				top, bot = old.pixel(of, x, y, t), old.pixel(of, x, y+1, t)
			default:
				top, bot = cur.pixel(f, x, y, t), cur.pixel(f, x, y+1, t)
			}
			ch, fg, bg := " ", "", card
			switch {
			case top == "" && bot == "":
			case bot == "":
				ch, fg = "▀", top
			case top == "":
				ch, fg = "▄", bot
			default:
				ch, fg, bg = "▀", top, bot
			}
			key := ch + fg + bg
			if key != curKey {
				flush()
				curKey, curFg, curBg = key, fg, bg
			}
			run.WriteString(ch)
		}
		flush()
		if y+2 < f.h {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

package tui

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"relay/internal/config"
)

// ---- text from outside is cleaned before it is drawn -------------------------

// The output of a command or of a model may hold terminal control codes: colours, cursor
// movement, a carriage return that rewinds a progress bar, "clear screen". Drawn as they are,
// they write over the interface and leave garbage on the screen.
var reEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

func isControl(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// cleanText removes terminal control codes and control characters (a new line and a tab stay);
// of a line that was rewound with a carriage return only the last version is kept.
func cleanText(s string) string {
	if strings.IndexFunc(s, isControl) < 0 {
		return s
	}
	s = reEscape.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.Contains(s, "\r") {
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			if strings.Contains(l, "\r") {
				parts := strings.Split(l, "\r")
				last := ""
				for _, p := range parts {
					if strings.TrimSpace(p) != "" {
						last = p
					}
				}
				lines[i] = last
			}
		}
		s = strings.Join(lines, "\n")
	}
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

// maxStoredResult is how much of a tool result the interface keeps (it shows a few lines).
const maxStoredResult = 8000

// clipResult shortens a tool result that the interface only shows in part.
func clipResult(s string) string {
	if len(s) <= maxStoredResult {
		return s
	}
	return strings.ToValidUTF8(s[:maxStoredResult], "") + "\n…"
}

// maxThoughtText is how much of the model's reasoning stays in a block: the reasoning of some
// models never ends, and only its last lines are shown anyway.
const maxThoughtText = 40000

func clipThought(s string) string {
	if len(s) <= maxThoughtText {
		return s
	}
	return strings.ToValidUTF8(s[len(s)-maxThoughtText/2:], "")
}

// takeRunes splits off the first n runes without converting the whole text.
func takeRunes(s string, n int) (head, rest string) {
	i := 0
	for ; n > 0 && i < len(s); n-- {
		_, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
	}
	return s[:i], s[i:]
}

// ---- the picture of the feed is drawn once, not at every frame -----------------

// feedCache keeps the drawn lines of every entry of the feed. An entry is drawn again only when
// it changed (it grows while the answer is typed) or when the width or the theme changed. Before
// this the whole conversation was drawn again for every frame, and a long one made the interface
// slow, and then unusable.
type feedCache struct {
	w     int
	theme string
	items []cachedEntry
}

type cachedEntry struct {
	key   entryKey
	lines []string
}

// entryKey is what the picture of an entry depends on. The texts are compared by length and by
// a sample of their ends, which is enough: an entry only grows, or is replaced as a whole.
type entryKey struct {
	kind        entryKind
	tl, rl      int
	ts, rs      uint32
	isErr, open bool
	hide        bool
	tool, model string
}

func sample(s string) uint32 {
	h := fnv.New32a()
	if len(s) <= 64 {
		h.Write([]byte(s))
	} else {
		h.Write([]byte(s[:32]))
		h.Write([]byte(s[len(s)-32:]))
	}
	return h.Sum32()
}

func (m model) keyOf(i int) entryKey {
	e := m.entries[i]
	return entryKey{
		kind: e.kind, tl: len(e.text), rl: len(e.result), ts: sample(e.text), rs: sample(e.result),
		isErr: e.isErr, open: e.kind == eThought && i == m.openThink, hide: m.hideEntry(e), tool: e.tool, model: e.model,
	}
}

func (m model) renderOne(i, w int, st styles) []string {
	e := m.entries[i]
	if m.hideEntry(e) {
		return []string{}
	}
	if e.kind == eThought {
		return renderThought(e, w, st, i != m.openThink)
	}
	return renderEntry(e, w, st)
}

// transcriptParts returns the lines of every entry. During a change of the mode (the colours
// flow into each other) and without a cache, the entries are drawn directly.
func (m model) transcriptParts(w int, st styles) [][]string {
	if tw := m.appSettings().TextWidth; tw > 0 && tw < w {
		w = tw // "text width" on the settings page
	}
	parts := make([][]string, len(m.entries))
	c := m.cache
	if _, flowing := m.transition(); c == nil || flowing {
		for i := range m.entries {
			parts[i] = m.renderOne(i, w, st)
		}
		return m.withBuildLine(parts, w, st)
	}
	still := newStyles(themeFor(m.mode, 0)) // the cache is drawn in the colours of the mode, not of a frame
	th := themeFor(m.mode, 0).name
	if c.w != w || c.theme != th {
		c.items, c.w, c.theme = c.items[:0], w, th
	}
	if len(c.items) > len(m.entries) {
		c.items = c.items[:len(m.entries)]
	}
	for i := range m.entries {
		key := m.keyOf(i)
		if i < len(c.items) && c.items[i].lines != nil && c.items[i].key == key {
			parts[i] = c.items[i].lines
			continue
		}
		lines := m.renderOne(i, w, still)
		if lines == nil {
			lines = []string{}
		}
		for len(c.items) <= i {
			c.items = append(c.items, cachedEntry{})
		}
		c.items[i] = cachedEntry{key: key, lines: lines}
		parts[i] = lines
	}
	return m.withBuildLine(parts, w, st)
}

func countLines(parts [][]string) int {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	return n
}

// windowOf returns the lines [start, end) of the whole feed without joining all of it.
func windowOf(parts [][]string, start, end int) []string {
	out := make([]string, 0, max(0, end-start))
	pos := 0
	for _, p := range parts {
		if pos+len(p) <= start {
			pos += len(p)
			continue
		}
		for j, l := range p {
			if k := pos + j; k >= start && k < end {
				out = append(out, l)
			}
		}
		pos += len(p)
		if pos >= end {
			break
		}
	}
	return out
}

// ---- a crash must not take the whole interface down ----------------------------

// logCrash writes a caught panic with its stack to crash.log in the Relay folder.
func logCrash(where string, r any) {
	home, err := config.Home()
	if err != nil {
		return
	}
	p := filepath.Join(home, "crash.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		_ = os.Truncate(p, 0)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s: %v\n%s\n\n", time.Now().Format(time.RFC3339), where, r, debug.Stack())
}

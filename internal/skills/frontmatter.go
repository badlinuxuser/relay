package skills

import (
	"fmt"
	"strconv"
	"strings"
)

// A SKILL.md starts with a YAML block between two "---" lines. Relay reads the small part of
// YAML that Agent Skills use: "key: value" lines with plain or quoted text, folded (>) and
// literal (|) blocks, one level of "key:" followed by indented "name: value" lines or
// "- item" lines, and [a, b] / {a: b} on one line. Values are kept as text.

// splitFrontmatter separates the YAML block from the Markdown body.
func splitFrontmatter(src string) (yaml, body string, ok bool) {
	src = strings.TrimPrefix(src, "\ufeff")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return "", src, false
	}
	lines := strings.Split(src[4:], "\n")
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == "---" {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", src, false
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		if r == ' ' {
			n++
		} else if r == '\t' {
			n += 2
		} else {
			break
		}
	}
	return n
}

func isBlankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// splitKey splits "key: value". The key is the text before the first ": " (or a final ":").
func splitKey(line string) (key, val string, ok bool) {
	i := strings.Index(line, ":")
	for i >= 0 {
		if i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t' {
			break
		}
		next := strings.Index(line[i+1:], ":")
		if next < 0 {
			i = -1
			break
		}
		i += 1 + next
	}
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	if key == "" || strings.ContainsAny(key, " \t") && !strings.HasPrefix(key, `"`) && !strings.HasPrefix(key, "'") {
		return "", "", false
	}
	return strings.Trim(key, `"'`), strings.TrimSpace(line[i+1:]), true
}

// parseYAML returns string, []string or map[string]string values.
func parseYAML(src string) (map[string]any, error) {
	lines := strings.Split(src, "\n")
	out := map[string]any{}
	for i := 0; i < len(lines); {
		line := lines[i]
		if isBlankOrComment(line) {
			i++
			continue
		}
		if indentOf(line) > 0 {
			return nil, fmt.Errorf("line %d: unexpected indentation", i+1)
		}
		key, val, ok := splitKey(line)
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\"", i+1)
		}
		start := i + 1
		i++
		var cont []string
		for i < len(lines) && (strings.TrimSpace(lines[i]) == "" || indentOf(lines[i]) > 0) {
			cont = append(cont, lines[i])
			i++
		}
		v, err := parseValue(val, cont)
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", start, key, err)
		}
		out[key] = v
	}
	return out, nil
}

func parseValue(val string, cont []string) (any, error) {
	for len(cont) > 0 && strings.TrimSpace(cont[len(cont)-1]) == "" {
		cont = cont[:len(cont)-1]
	}
	if val != "" && (val[0] == '>' || val[0] == '|') {
		return blockScalar(val, cont), nil
	}
	if val == "" {
		if len(cont) == 0 {
			return "", nil
		}
		return parseNested(cont)
	}
	if val[0] == '[' && strings.HasSuffix(val, "]") {
		return flowList(val[1 : len(val)-1]), nil
	}
	if val[0] == '{' && strings.HasSuffix(val, "}") {
		return flowMap(val[1 : len(val)-1]), nil
	}
	s, err := unquote(val)
	if err != nil {
		return nil, err
	}
	if len(cont) > 0 && val[0] != '"' && val[0] != '\'' { // a plain text that continues on the next lines
		parts := []string{s}
		for _, c := range cont {
			if t := strings.TrimSpace(c); t != "" {
				parts = append(parts, t)
			}
		}
		s = strings.Join(parts, " ")
	}
	return s, nil
}

func blockScalar(header string, cont []string) string {
	literal := header[0] == '|'
	keep := strings.Contains(header, "+")
	base := -1
	for _, c := range cont {
		if strings.TrimSpace(c) != "" {
			base = indentOf(c)
			break
		}
	}
	if base < 0 {
		return ""
	}
	var rows []string
	for _, c := range cont {
		if strings.TrimSpace(c) == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, strings.TrimRight(dedent(c, base), " \t"))
	}
	var s string
	if literal {
		s = strings.Join(rows, "\n")
	} else {
		var b strings.Builder
		for i, r := range rows {
			switch {
			case r == "":
				b.WriteString("\n")
			case i > 0 && rows[i-1] != "":
				b.WriteString(" " + r)
			default:
				b.WriteString(r)
			}
		}
		s = b.String()
	}
	if keep {
		return s + "\n"
	}
	return strings.TrimRight(s, "\n")
}

func dedent(line string, n int) string {
	cut := 0
	width := 0
	for cut < len(line) && width < n {
		if line[cut] == ' ' {
			width++
		} else if line[cut] == '\t' {
			width += 2
		} else {
			break
		}
		cut++
	}
	return line[cut:]
}

// parseNested reads the indented lines under "key:": a list ("- a") or a map ("a: b").
func parseNested(cont []string) (any, error) {
	var first string
	for _, c := range cont {
		if !isBlankOrComment(c) {
			first = strings.TrimSpace(c)
			break
		}
	}
	if first == "" {
		return "", nil
	}
	if first == "-" || strings.HasPrefix(first, "- ") {
		var list []string
		for _, c := range cont {
			if isBlankOrComment(c) {
				continue
			}
			t := strings.TrimSpace(c)
			if t != "-" && !strings.HasPrefix(t, "- ") {
				return nil, fmt.Errorf("expected \"- item\", found %q", t)
			}
			s, err := unquote(strings.TrimSpace(strings.TrimPrefix(t, "-")))
			if err != nil {
				return nil, err
			}
			list = append(list, s)
		}
		return list, nil
	}
	m := map[string]string{}
	base := -1
	for _, c := range cont {
		if isBlankOrComment(c) {
			continue
		}
		n := indentOf(c)
		if base < 0 {
			base = n
		}
		if n != base {
			return nil, fmt.Errorf("only one level of nesting is supported (line %q)", strings.TrimSpace(c))
		}
		k, v, ok := splitKey(strings.TrimSpace(c))
		if !ok {
			return nil, fmt.Errorf("expected \"name: value\", found %q", strings.TrimSpace(c))
		}
		s, err := unquote(v)
		if err != nil {
			return nil, err
		}
		m[k] = s
	}
	return m, nil
}

func flowList(inner string) []string {
	var out []string
	for _, p := range splitFlow(inner) {
		if s, err := unquote(strings.TrimSpace(p)); err == nil && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func flowMap(inner string) map[string]string {
	out := map[string]string{}
	for _, p := range splitFlow(inner) {
		if k, v, ok := splitKey(strings.TrimSpace(p)); ok {
			if s, err := unquote(v); err == nil {
				out[k] = s
			}
		}
	}
	return out
}

// splitFlow splits on commas that are not inside quotes.
func splitFlow(s string) []string {
	var parts []string
	var cur strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			cur.WriteRune(r)
		case r == ',':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		parts = append(parts, cur.String())
	}
	return parts
}

// unquote turns a YAML scalar into text: removes quotes, handles escapes, drops a trailing comment.
func unquote(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	switch v[0] {
	case '"':
		end := -1
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' {
				i++
				continue
			}
			if v[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return "", fmt.Errorf("a quote is not closed")
		}
		s, err := strconv.Unquote(v[:end+1])
		if err != nil {
			return v[1:end], nil
		}
		return s, nil
	case '\'':
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			if v[i] == '\'' {
				if i+1 < len(v) && v[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				return b.String(), nil
			}
			b.WriteByte(v[i])
		}
		return "", fmt.Errorf("a quote is not closed")
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v), nil
}

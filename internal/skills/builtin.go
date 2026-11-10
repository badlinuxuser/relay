package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// The skills that come with Relay are inside the program, so they exist right after it is built:
// no files to install. A skill with the same name in the skills folder replaces a built-in one.
//
//go:embed builtin
var builtinFS embed.FS

// builtinSkills returns the skills inside Relay.
func builtinSkills() []*Skill {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil
	}
	var out []*Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(builtinFS, "builtin/"+e.Name())
		if err != nil {
			continue
		}
		s, _, err := parseFS(sub, e.Name())
		if err != nil {
			continue // a test makes sure that this does not happen
		}
		s.Builtin, s.fsys = true, sub
		out = append(out, s)
	}
	return out
}

// readInsideFS reads a text file of a built-in skill. io/fs paths cannot leave their root.
func readInsideFS(fsys fs.FS, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("path is empty")
	}
	p := path.Clean(strings.ReplaceAll(rel, `\`, "/"))
	if p == "." || !fs.ValidPath(p) {
		return "", fmt.Errorf("the path must be relative and stay inside the skill folder")
	}
	fi, err := fs.Stat(fsys, p)
	if err != nil {
		return "", fmt.Errorf("%s does not exist in the skill", rel)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", rel)
	}
	data, err := fs.ReadFile(fsys, p)
	if err != nil {
		return "", err
	}
	if len(data) > maxSkillFile || !utf8.Valid(data) {
		return "", fmt.Errorf("%s is too large or not a text file", rel)
	}
	return string(data), nil
}

func listFilesFS(fsys fs.FS, max int) []string {
	var out []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == File {
			return nil
		}
		out = append(out, p)
		if len(out) >= max {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

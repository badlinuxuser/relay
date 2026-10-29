package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"relay/internal/provider"
)

const maxFileBytes = 5 << 20

// ---- read_file ---------------------------------------------------------------

type readFile struct{ ws string }

func (t *readFile) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "read_file",
		Description: "Read a text file from the workspace. Returns numbered lines. Use offset (1-based line) and limit for large files.",
		Schema: schema(map[string]any{
			"path":   prop("string", "File path (relative to the workspace)"),
			"offset": prop("integer", "First line to read, 1-based (default 1)"),
			"limit":  prop("integer", "Max number of lines (default 400)"),
		}, "path"),
	}
}
func (t *readFile) NeedsApproval() bool { return false }
func (t *readFile) Describe(a json.RawMessage) string {
	var v struct{ Path string `json:"path"` }
	_ = parse(a, &v)
	return "read " + v.Path
}
func (t *readFile) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	p, err := resolve(t.ws, v.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, use list_dir", v.Path)
	}
	if info.Size() > maxFileBytes {
		return "", fmt.Errorf("file is too large (%d bytes)", info.Size())
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(b), "\n")
	start := v.Offset
	if start < 1 {
		start = 1
	}
	limit := v.Limit
	if limit <= 0 {
		limit = 400
	}
	if start > len(lines) {
		return "", fmt.Errorf("offset %d is beyond the end of the file (%d lines)", start, len(lines))
	}
	end := start - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}
	var sb strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&sb, "%6d\t%s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&sb, "… [%d more lines, continue with offset=%d]\n", len(lines)-end, end+1)
	}
	return truncate(sb.String(), 40000), nil
}

// ---- list_dir ----------------------------------------------------------------

type listDir struct{ ws string }

func (t *listDir) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "list_dir",
		Description: "List files and folders in a workspace directory (folders end with /).",
		Schema:      schema(map[string]any{"path": prop("string", "Directory (default: workspace root)")}),
	}
}
func (t *listDir) NeedsApproval() bool { return false }
func (t *listDir) Describe(a json.RawMessage) string {
	var v struct{ Path string `json:"path"` }
	_ = parse(a, &v)
	if v.Path == "" {
		v.Path = "."
	}
	return "list " + v.Path
}
func (t *listDir) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct{ Path string `json:"path"` }
	if err := parse(a, &v); err != nil {
		return "", err
	}
	p, err := resolve(t.ws, v.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, e := range entries {
		if i >= 300 {
			fmt.Fprintf(&sb, "… and %d more\n", len(entries)-i)
			break
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
	}
	if sb.Len() == 0 {
		return "(empty directory)", nil
	}
	return sb.String(), nil
}

// ---- grep --------------------------------------------------------------------

type grep struct{ ws string }

func (t *grep) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "grep",
		Description: "Search files in the workspace with a regular expression. Returns path:line: text (max 200 matches).",
		Schema: schema(map[string]any{
			"pattern": prop("string", "Regular expression (Go RE2 syntax)"),
			"path":    prop("string", "File or directory to search (default: workspace root)"),
		}, "pattern"),
	}
}
func (t *grep) NeedsApproval() bool { return false }
func (t *grep) Describe(a json.RawMessage) string {
	var v struct{ Pattern string `json:"pattern"` }
	_ = parse(a, &v)
	return "grep " + v.Pattern
}
func (t *grep) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	re, err := regexp.Compile(v.Pattern)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %w", err)
	}
	root, err := resolve(t.ws, v.Path)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	matches := 0
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			n := d.Name()
			if p != root && (strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 1<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		head := b
		if len(head) > 8000 {
			head = head[:8000]
		}
		if strings.IndexByte(string(head), 0) >= 0 {
			return nil // бинарный файл
		}
		rel, _ := filepath.Rel(t.ws, p)
		for i, line := range strings.Split(string(b), "\n") {
			if re.MatchString(line) {
				fmt.Fprintf(&sb, "%s:%d: %s\n", rel, i+1, truncate(line, 300))
				matches++
				if matches >= 200 {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != fs.SkipAll {
		return "", walkErr
	}
	if matches == 0 {
		return "no matches", nil
	}
	return sb.String(), nil
}

// ---- write_file --------------------------------------------------------------

type writeFile struct{ ws string }

func (t *writeFile) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "write_file",
		Description: "Create or overwrite a file in the workspace with the given content.",
		Schema: schema(map[string]any{
			"path":    prop("string", "File path (relative to the workspace)"),
			"content": prop("string", "Full file content"),
		}, "path", "content"),
	}
}
func (t *writeFile) NeedsApproval() bool { return true }
func (t *writeFile) Describe(a json.RawMessage) string {
	var v struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	_ = parse(a, &v)
	return fmt.Sprintf("write %s (%d bytes)", v.Path, len(v.Content))
}
func (t *writeFile) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	p, err := resolve(t.ws, v.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(v.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(v.Content), v.Path), nil
}

// ---- edit_file ---------------------------------------------------------------

type editFile struct{ ws string }

func (t *editFile) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "edit_file",
		Description: "Replace an exact, unique piece of text in a file. old_text must occur exactly once.",
		Schema: schema(map[string]any{
			"path":     prop("string", "File path (relative to the workspace)"),
			"old_text": prop("string", "Exact text to replace (must be unique in the file)"),
			"new_text": prop("string", "Replacement text"),
		}, "path", "old_text", "new_text"),
	}
}
func (t *editFile) NeedsApproval() bool { return true }
func (t *editFile) Describe(a json.RawMessage) string {
	var v struct{ Path string `json:"path"` }
	_ = parse(a, &v)
	return "edit " + v.Path
}
func (t *editFile) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	if v.OldText == "" {
		return "", fmt.Errorf("old_text must not be empty")
	}
	p, err := resolve(t.ws, v.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	src := string(b)
	switch n := strings.Count(src, v.OldText); {
	case n == 0:
		return "", fmt.Errorf("old_text was not found in %s", v.Path)
	case n > 1:
		return "", fmt.Errorf("old_text occurs %d times in %s; make it unique", n, v.Path)
	}
	out := strings.Replace(src, v.OldText, v.NewText, 1)
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return "", err
	}
	return "edited " + v.Path, nil
}

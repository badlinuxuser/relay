// Package tools содержит инструменты агента: файлы, поиск, shell, веб и MCP.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"relay/internal/provider"
)

// Tool это один инструмент, доступный модели.
type Tool interface {
	Spec() provider.ToolSpec
	// NeedsApproval: требуется ли подтверждение пользователя перед запуском.
	NeedsApproval() bool
	// Describe это короткое человекочитаемое описание конкретного вызова.
	Describe(args json.RawMessage) string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	byName map[string]Tool
	order  []string
}

// NewRegistry создаёт набор встроенных инструментов, ограниченных папкой workspace.
func NewRegistry(workspace string) *Registry {
	r := &Registry{byName: map[string]Tool{}}
	for _, t := range []Tool{
		&readFile{ws: workspace},
		&listDir{ws: workspace},
		&grep{ws: workspace},
		&writeFile{ws: workspace},
		&editFile{ws: workspace},
		&runShell{ws: workspace},
		&webFetch{},
	} {
		r.Add(t)
	}
	return r
}

func (r *Registry) Add(t Tool) {
	name := t.Spec().Name
	if _, ok := r.byName[name]; !ok {
		r.order = append(r.order, name)
	}
	r.byName[name] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

func (r *Registry) Specs() []provider.ToolSpec {
	out := make([]provider.ToolSpec, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n].Spec())
	}
	return out
}

// --- вспомогательные функции -------------------------------------------------

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	b, _ := json.Marshal(s)
	return b
}

// resolve превращает путь в абсолютный и не даёт выйти за пределы workspace.
func resolve(ws, p string) (string, error) {
	if p == "" {
		p = "."
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(ws, p)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(ws, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace %q", p, ws)
	}
	return abs, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n… [truncated, %d more bytes]", len(s)-n)
}

func parse(args json.RawMessage, v any) error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

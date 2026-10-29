// Package usage считает токены и стоимость по моделям и сохраняет итоги на диск.
package usage

import (
	"encoding/json"
	"os"
	"sync"
)

type Entry struct {
	In       int     `json:"in"`
	Out      int     `json:"out"`
	Requests int     `json:"requests"`
	Cost     float64 `json:"cost"`
}

type Tracker struct {
	mu   sync.Mutex
	path string
	data map[string]*Entry
}

func Load(path string) *Tracker {
	t := &Tracker{path: path, data: map[string]*Entry{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &t.data)
	}
	if t.data == nil {
		t.data = map[string]*Entry{}
	}
	return t
}

func (t *Tracker) Add(model string, in, out int, cost float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.data[model]
	if e == nil {
		e = &Entry{}
		t.data[model] = e
	}
	e.In += in
	e.Out += out
	e.Requests++
	e.Cost += cost
	if b, err := json.MarshalIndent(t.data, "", "  "); err == nil {
		_ = os.WriteFile(t.path, b, 0o600)
	}
}

func (t *Tracker) Snapshot() map[string]Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]Entry, len(t.data))
	for k, v := range t.data {
		out[k] = *v
	}
	return out
}

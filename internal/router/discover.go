package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"relay/internal/config"
)

// Model discovery: Relay asks every provider which models it serves (GET /models,
// or /v1/models for Anthropic), using the keys the user added. Local servers such as
// Ollama are asked too, so installed models show up without any config editing.

const (
	refreshOK        = 5 * time.Minute  // re-list a provider that works
	refreshLocalFail = 15 * time.Second // a local server was down: look again soon
	refreshFail      = 2 * time.Minute  // a remote provider failed (no key, no network)
)

type listing struct {
	names []string
	ok    bool
	last  time.Time
	busy  bool
}

type catalog struct {
	mu sync.Mutex
	by map[string]*listing
}

func newCatalog() *catalog { return &catalog{by: map[string]*listing{}} }

// Words that mark models which are not chat models (embeddings, speech, images...).
var nonChat = []string{"embed", "whisper", "tts", "dall-e", "image", "moderation", "audio",
	"realtime", "transcribe", "davinci", "babbage", "imagen", "veo", "aqa", "rerank",
	"guard", "speech", "video"}

func chatLike(id string) bool {
	low := strings.ToLower(id)
	for _, bad := range nonChat {
		if strings.Contains(low, bad) {
			return false
		}
	}
	return true
}

func inList(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// fetchModels asks one provider which models it serves.
func (r *Router) fetchModels(ctx context.Context, p config.Provider) ([]string, error) {
	key := ""
	if k := r.keys[p.Name]; k != nil {
		key = k.Any()
		if key == "" && k.NeedsKey() {
			return nil, fmt.Errorf("no API key")
		}
	}
	base := strings.TrimRight(p.BaseURL, "/")
	url := base + "/models"
	if p.Kind == "anthropic" {
		url = base + "/v1/models?limit=1000"
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if p.Kind == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for hk, hv := range p.Headers {
		req.Header.Set(hk, hv)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range body.Data {
		id := strings.TrimPrefix(d.ID, "models/") // Gemini prefixes ids with "models/"
		if id == "" || seen[id] || !chatLike(id) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func (r *Router) refreshOne(ctx context.Context, p config.Provider) {
	r.cat.mu.Lock()
	l := r.cat.by[p.Name]
	if l == nil {
		l = &listing{}
		r.cat.by[p.Name] = l
	}
	if l.busy {
		r.cat.mu.Unlock()
		return
	}
	l.busy = true
	r.cat.mu.Unlock()

	names, err := r.fetchModels(ctx, p)

	r.cat.mu.Lock()
	l.busy = false
	l.last = time.Now()
	if err == nil {
		l.names, l.ok = names, true
	} else {
		l.names, l.ok = nil, false
	}
	r.cat.mu.Unlock()
}

func (r *Router) refresh(ctx context.Context, only []string) {
	var wg sync.WaitGroup
	for _, p := range r.cfg.Providers {
		if len(only) > 0 && !inList(only, p.Name) {
			continue
		}
		wg.Add(1)
		go func(p config.Provider) {
			defer wg.Done()
			r.refreshOne(ctx, p)
		}(p)
	}
	wg.Wait()
}

// RefreshAsync re-lists the models of all providers in the background
// (after a key was added or removed, and at startup).
func (r *Router) RefreshAsync() { go r.refresh(context.Background(), nil) }

// RefreshStale re-lists, in the background, the providers whose list is missing or old.
func (r *Router) RefreshStale() {
	now := time.Now()
	var due []string
	r.cat.mu.Lock()
	for _, p := range r.cfg.Providers {
		l := r.cat.by[p.Name]
		switch {
		case l == nil:
			due = append(due, p.Name)
		case l.busy:
		case l.ok && now.Sub(l.last) > refreshOK:
			due = append(due, p.Name)
		case !l.ok && now.Sub(l.last) > retryAfter(p):
			due = append(due, p.Name)
		}
	}
	r.cat.mu.Unlock()
	if len(due) > 0 {
		go r.refresh(context.Background(), due)
	}
}

func retryAfter(p config.Provider) time.Duration {
	if !p.NeedsKey() {
		return refreshLocalFail
	}
	return refreshFail
}

// names returns the models a provider reported, and whether the listing succeeded.
func (r *Router) names(provider string) ([]string, bool) {
	r.cat.mu.Lock()
	defer r.cat.mu.Unlock()
	if l := r.cat.by[provider]; l != nil && l.ok {
		return l.names, true
	}
	return nil, false
}

// available reports whether a configured model is actually served by its provider.
// If the provider could not be listed, the model is assumed to be available.
func (r *Router) available(m config.Model) bool {
	names, ok := r.names(m.Provider)
	if !ok {
		return true
	}
	for _, n := range names {
		if n == m.Name || n == m.Name+":latest" ||
			strings.HasPrefix(n, m.Name+"-") || strings.HasPrefix(m.Name, n+"-") {
			return true
		}
	}
	return false
}

func discoveredModel(provider, name string) config.Model {
	return config.Model{ID: provider + "/" + name, Provider: provider, Name: name, Tools: true}
}

// Models returns every selectable model: configured ones that their provider serves,
// then the models discovered from the providers' own lists (id is "provider/name").
func (r *Router) Models() []config.Model {
	var out []config.Model
	have := map[string]bool{}
	for _, m := range r.cfg.Models {
		have[m.Provider+"/"+m.Name] = true
		if r.available(m) && !r.keyless(m) {
			out = append(out, m)
		}
	}
	for _, p := range r.cfg.Providers {
		names, ok := r.names(p.Name)
		if !ok {
			continue
		}
		for _, n := range names {
			if !have[p.Name+"/"+n] {
				out = append(out, discoveredModel(p.Name, n))
			}
		}
	}
	return out
}

// ModelByID finds a configured model by id, or a discovered one ("provider/name").
func (r *Router) ModelByID(id string) (config.Model, bool) {
	if m, ok := r.cfg.ModelByID(id); ok {
		return m, true
	}
	i := strings.Index(id, "/")
	if i <= 0 {
		return config.Model{}, false
	}
	prov, name := id[:i], id[i+1:]
	names, ok := r.names(prov)
	if !ok || !inList(names, name) {
		return config.Model{}, false
	}
	return discoveredModel(prov, name), true
}

var lightWords = []string{"flash", "mini", "haiku", "lite", "nano", "small", "tiny", "1b", "3b", "7b", "8b"}

func isLight(id string) bool {
	low := strings.ToLower(id)
	for _, w := range lightWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// pickName chooses a model from a provider's list for a mode: a light model for
// "small", a bigger one otherwise; among equals the highest name (usually the newest).
func pickName(mode string, names []string) string {
	wantLight := mode == "small"
	var want, rest []string
	for _, n := range names {
		low := strings.ToLower(n)
		if strings.Contains(low, "preview") || strings.Contains(low, "-exp") {
			continue
		}
		if isLight(n) == wantLight {
			want = append(want, n)
		} else {
			rest = append(rest, n)
		}
	}
	pool := want
	if len(pool) == 0 {
		pool = rest
	}
	if len(pool) == 0 {
		return ""
	}
	sort.Strings(pool)
	return pool[len(pool)-1]
}

// autoPool picks one model from every provider that is usable (has a key, or is a
// local server) but is not in the chain yet. These are appended as last-resort
// fallbacks, so every key the user adds can serve requests without editing the config.
func (r *Router) autoPool(mode string, chain []string) []string {
	used := map[string]bool{}
	for _, id := range chain {
		if m, ok := r.ModelByID(id); ok {
			used[m.Provider] = true
		}
	}
	var out []string
	for _, p := range r.cfg.Providers {
		if used[p.Name] {
			continue
		}
		if k := r.keys[p.Name]; k != nil && k.NeedsKey() && k.Count() == 0 {
			continue
		}
		names, ok := r.names(p.Name)
		if !ok || len(names) == 0 {
			continue
		}
		if name := pickName(mode, names); name != "" {
			out = append(out, p.Name+"/"+name)
		}
	}
	return out
}

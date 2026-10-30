// Package catalog finds out which models providers really have: what is installed
// in the local Ollama, what Ollama cloud offers for the saved key, what the Gemini
// API lists, and so on. The result is cached; it is refreshed when keys change.
//
// Models found this way have ids "<provider>/<model>" (ollama/granite4.1:3b,
// gemini/gemini-2.5-flash); config.Config.ModelByID understands such ids.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"relay/internal/config"
	"relay/internal/provider"
)

const (
	defaultTTL     = 60 * time.Second
	defaultTimeout = 6 * time.Second
)

// Model is one model offered by a provider.
type Model struct {
	ID       string // provider/name
	Provider string
	Name     string
	Kind     string // local | cloud
}

// ProviderResult tells how listing one provider went.
type ProviderResult struct {
	Name  string
	Style string // ollama | gemini | anthropic | openai
	Local bool
	Count int
	// Listed: the list was fetched. Authoritative: a model missing from such a list
	// really does not exist (true for Ollama and Gemini, whose lists are complete).
	Listed        bool
	Authoritative bool
	Skipped       string // "no key" | "disabled"
	Error         string
}

// Snapshot is the result of one pass over all providers.
type Snapshot struct {
	Models    []Model
	Providers []ProviderResult
	At        time.Time
}

func norm(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ":latest")
}

// Result returns how listing the named provider went.
func (s *Snapshot) Result(name string) (ProviderResult, bool) {
	if s == nil {
		return ProviderResult{}, false
	}
	for _, r := range s.Providers {
		if r.Name == name {
			return r, true
		}
	}
	return ProviderResult{}, false
}

// Has reports whether the provider's list contains the model.
func (s *Snapshot) Has(providerName, model string) bool {
	if s == nil {
		return false
	}
	want := norm(model)
	for _, m := range s.Models {
		if m.Provider == providerName && norm(m.Name) == want {
			return true
		}
	}
	return false
}

// Missing reports that the provider's list was fetched, is complete, and does not
// contain the model: the model is not installed (Ollama) or does not exist (Gemini).
func (s *Snapshot) Missing(providerName, model string) bool {
	r, ok := s.Result(providerName)
	return ok && r.Listed && r.Authoritative && !s.Has(providerName, model)
}

// Pick returns up to n models of one provider (in the provider's own order).
func (s *Snapshot) Pick(providerName string, n int) []Model {
	var out []Model
	if s == nil {
		return out
	}
	for _, m := range s.Models {
		if m.Provider == providerName && len(out) < n {
			out = append(out, m)
		}
	}
	return out
}

// Fallback returns up to n models to use when nothing from the mode's own list is
// usable: models of complete lists only, those on this computer first.
func (s *Snapshot) Fallback(n int) []Model {
	var local, remote []Model
	if s == nil {
		return nil
	}
	for _, r := range s.Providers {
		if !r.Listed || !r.Authoritative {
			continue
		}
		var list []Model
		for _, m := range s.Models {
			if m.Provider == r.Name {
				list = append(list, m)
			}
		}
		sort.SliceStable(list, func(i, j int) bool {
			return strings.Contains(strings.ToLower(list[i].Name), "flash") &&
				!strings.Contains(strings.ToLower(list[j].Name), "flash")
		})
		if r.Local {
			local = append(local, list...)
		} else {
			remote = append(remote, list...)
		}
	}
	all := append(local, remote...)
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// Catalog lists models of every configured provider and caches the answer.
type Catalog struct {
	cfg  *config.Config
	keys func(name string) *provider.Keys
	http *http.Client

	TTL     time.Duration // how long a snapshot is fresh
	Timeout time.Duration // per provider

	mu      sync.Mutex
	snap    *Snapshot
	gen     int // number of finished refreshes
	inv     int // number of Invalidate calls
	stale   bool
	running atomic.Bool

	refreshMu sync.Mutex
}

// New creates a catalog. keys returns the key holder of a provider (may return nil).
func New(cfg *config.Config, keys func(name string) *provider.Keys) *Catalog {
	return &Catalog{
		cfg: cfg, keys: keys, http: &http.Client{},
		TTL: defaultTTL, Timeout: defaultTimeout,
	}
}

// Peek returns the last snapshot without any network access (nil if there is none yet).
func (c *Catalog) Peek() *Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap
}

// Invalidate marks the snapshot outdated (keys were added or removed).
func (c *Catalog) Invalidate() {
	c.mu.Lock()
	c.inv++
	c.stale = true
	c.mu.Unlock()
}

func (c *Catalog) fresh() *Snapshot {
	if c.snap != nil && !c.stale && time.Since(c.snap.At) < c.TTL {
		return c.snap
	}
	return nil
}

// Warm starts a background refresh if the snapshot is missing or outdated.
func (c *Catalog) Warm() {
	c.mu.Lock()
	ok := c.fresh() != nil
	c.mu.Unlock()
	if ok || !c.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.running.Store(false)
		c.Get(context.Background(), false)
	}()
}

// Get returns a fresh snapshot, fetching it when needed (force: always fetch).
// Concurrent callers share one fetch.
func (c *Catalog) Get(ctx context.Context, force bool) *Snapshot {
	c.mu.Lock()
	gen := c.gen
	if !force {
		if s := c.fresh(); s != nil {
			c.mu.Unlock()
			return s
		}
	}
	c.mu.Unlock()

	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	if c.gen != gen && c.snap != nil { // somebody refreshed while we waited
		s := c.snap
		c.mu.Unlock()
		return s
	}
	inv := c.inv
	c.mu.Unlock()

	s := c.fetch(ctx)

	c.mu.Lock()
	c.snap = s
	c.gen++
	c.stale = c.inv != inv // keys changed during the fetch: fetch again next time
	c.mu.Unlock()
	return s
}

func (c *Catalog) fetch(ctx context.Context) *Snapshot {
	provs := c.cfg.Providers
	results := make([]ProviderResult, len(provs))
	lists := make([][]Model, len(provs))
	var wg sync.WaitGroup
	for i, p := range provs {
		wg.Add(1)
		go func(i int, p config.Provider) {
			defer wg.Done()
			results[i], lists[i] = c.one(ctx, p)
		}(i, p)
	}
	wg.Wait()
	s := &Snapshot{Providers: results, At: time.Now()}
	for _, l := range lists {
		s.Models = append(s.Models, l...)
	}
	return s
}

func (c *Catalog) one(ctx context.Context, p config.Provider) (ProviderResult, []Model) {
	res := ProviderResult{Name: p.Name, Style: p.DiscoverStyle(), Local: p.IsLocal()}
	if res.Style == "none" {
		res.Skipped = "disabled"
		return res, nil
	}
	var keys []string
	if c.keys != nil {
		if k := c.keys(p.Name); k != nil {
			keys = k.All()
		}
	}
	if len(keys) == 0 {
		if p.NeedsKey() {
			res.Skipped = "no key"
			return res, nil
		}
		keys = []string{""} // a local server without a key
	}

	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	// Try the keys in turn: one of several may be wrong or expired.
	var names []string
	var err, firstErr error
	for _, key := range keys {
		switch res.Style {
		case "ollama":
			names, err = c.ollama(ctx, p, key)
		case "gemini":
			names, err = c.gemini(ctx, p, key)
		case "anthropic":
			names, err = c.anthropic(ctx, p, key)
		default:
			names, err = c.openai(ctx, p, key)
		}
		if err == nil {
			break
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if err != nil {
		res.Error = describe(ctx, firstErr)
		return res, nil
	}

	res.Listed = true
	res.Authoritative = res.Style == "ollama" || res.Style == "gemini"
	seen := map[string]bool{}
	var out []Model
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		kind := "cloud"
		if p.IsLocal() && !(res.Style == "ollama" && isOllamaCloudTag(n)) {
			kind = "local"
		}
		out = append(out, Model{ID: p.Name + "/" + n, Provider: p.Name, Name: n, Kind: kind})
	}
	res.Count = len(out)
	return res, out
}

// isOllamaCloudTag: a local Ollama lists cloud models it forwards to ollama.com
// (after `ollama signin`) with a -cloud or :cloud tag.
func isOllamaCloudTag(name string) bool {
	return strings.HasSuffix(name, "-cloud") || strings.HasSuffix(name, ":cloud")
}

// describe turns a listing error into a short message for the user.
func describe(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed out"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return "not reachable: " + ue.Err.Error()
	}
	return err.Error()
}

// ---- requests ----------------------------------------------------------------

func (c *Catalog) getJSON(ctx context.Context, p config.Provider, rawURL string, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiMessage(b))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

// apiMessage pulls the human-readable text out of a JSON error body
// ({"error":{"message":..}} or {"error":".."}); other bodies are returned trimmed.
func apiMessage(b []byte) string {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && len(e.Error) > 0 {
		var s string
		if json.Unmarshal(e.Error, &s) == nil && s != "" {
			return s
		}
		var o struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Error, &o) == nil && o.Message != "" {
			return o.Message
		}
	}
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func bearer(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + key}
}

func trimBase(p config.Provider, suffixes ...string) string {
	base := strings.TrimRight(p.BaseURL, "/")
	for _, s := range suffixes {
		base = strings.TrimSuffix(base, s)
	}
	return base
}

// ollama: GET <host>/api/tags. The same for the local server (no key) and for
// ollama.com (key as a Bearer token): the list is what the account can run.
func (c *Catalog) ollama(ctx context.Context, p config.Provider, key string) ([]string, error) {
	var r struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, p, trimBase(p, "/v1")+"/api/tags", bearer(key), &r); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range r.Models {
		n := m.Name
		if n == "" {
			n = m.Model
		}
		if isEmbeddingName(n) {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

func isEmbeddingName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "embed") || strings.Contains(n, "minilm") || strings.HasPrefix(n, "bge-")
}

// gemini: GET <v1beta>/models, only models that can generate text.
func (c *Catalog) gemini(ctx context.Context, p config.Provider, key string) ([]string, error) {
	base := trimBase(p, "/openai")
	var out []string
	token := ""
	for page := 0; page < 10; page++ {
		u := base + "/models?pageSize=1000"
		if token != "" {
			u += "&pageToken=" + url.QueryEscape(token)
		}
		var r struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			Next string `json:"nextPageToken"`
		}
		if err := c.getJSON(ctx, p, u, map[string]string{"x-goog-api-key": key}, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Models {
			if !contains(m.Methods, "generateContent") {
				continue
			}
			name := strings.TrimPrefix(m.Name, "models/")
			if isGeminiNonChat(name) {
				continue
			}
			out = append(out, name)
		}
		if r.Next == "" {
			break
		}
		token = r.Next
	}
	return out, nil
}

func isGeminiNonChat(name string) bool {
	n := strings.ToLower(name)
	for _, bad := range []string{"embedding", "imagen", "veo", "aqa", "tts", "native-audio", "-live", "robotics", "image-generation"} {
		if strings.Contains(n, bad) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// anthropic: GET <base>/v1/models.
func (c *Catalog) anthropic(ctx context.Context, p config.Provider, key string) ([]string, error) {
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	hdr := map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}
	if err := c.getJSON(ctx, p, trimBase(p, "/v1")+"/v1/models?limit=1000", hdr, &r); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range r.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// openai: GET <base>/models (OpenAI, OpenRouter, LM Studio, llama.cpp, vLLM, ...).
func (c *Catalog) openai(ctx context.Context, p config.Provider, key string) ([]string, error) {
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, p, strings.TrimRight(p.BaseURL, "/")+"/models", bearer(key), &r); err != nil {
		return nil, err
	}
	official := strings.Contains(p.BaseURL, "api.openai.com")
	var out []string
	for _, m := range r.Data {
		if official && !isOpenAIChat(m.ID) {
			continue
		}
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out, nil
}

// isOpenAIChat drops the models of api.openai.com that are not for chat
// (embeddings, speech, images, moderation, ...).
func isOpenAIChat(id string) bool {
	n := strings.ToLower(id)
	if !(strings.HasPrefix(n, "gpt-") || strings.HasPrefix(n, "chatgpt-") ||
		strings.HasPrefix(n, "o1") || strings.HasPrefix(n, "o3") || strings.HasPrefix(n, "o4") || strings.HasPrefix(n, "o5")) {
		return false
	}
	for _, bad := range []string{"embedding", "whisper", "tts", "dall-e", "moderation", "transcribe", "realtime", "audio", "image", "sora", "davinci", "babbage"} {
		if strings.Contains(n, bad) {
			return false
		}
	}
	return true
}

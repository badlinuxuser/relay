package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"relay/internal/config"
	"relay/internal/provider"
)

// keysrc is a fake key store: provider name -> keys.
type keysrc map[string][]string

func (k keysrc) Values(name string) []string { return k[name] }

type fakes struct {
	local, cloud, gemini *httptest.Server
	hits                 atomic.Int32
}

func newFakes(t *testing.T) *fakes {
	f := &fakes{}
	// local Ollama: no key, lists an embedding model and a forwarded cloud model too
	f.local = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"models":[{"name":"dolphin-phi:latest"},{"name":"granite4.1:3b"},
			{"name":"nomic-embed-text:latest"},{"name":"gpt-oss:120b-cloud"}]}`))
	}))
	// Ollama cloud: needs the key as a Bearer token
	f.cloud = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer ollama-key" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Write([]byte(`{"models":[{"name":"gpt-oss:120b"},{"name":"qwen3-coder:480b"}]}`))
	}))
	// Gemini: key in x-goog-api-key, two pages, an embedding model in the list
	f.gemini = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-goog-api-key") != "gemini-key" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`))
			return
		}
		if r.URL.Query().Get("pageToken") == "" {
			w.Write([]byte(`{"models":[
				{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent","countTokens"]},
				{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}],
				"nextPageToken":"p2"}`))
			return
		}
		w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro","supportedGenerationMethods":["generateContent"]}]}`))
	}))
	t.Cleanup(func() { f.local.Close(); f.cloud.Close(); f.gemini.Close() })
	return f
}

func (f *fakes) config() *config.Config {
	return &config.Config{Providers: []config.Provider{
		{Name: "ollama", Kind: "openai", BaseURL: f.local.URL + "/v1"},
		{Name: "ollama-cloud", Kind: "openai", BaseURL: f.cloud.URL + "/v1", KeyFrom: "ollama", Discover: "ollama"},
		{Name: "gemini", Kind: "openai", BaseURL: f.gemini.URL + "/v1beta/openai", Discover: "gemini", APIKeyEnv: []string{"RELAY_TEST_NOT_SET"}},
	}}
}

func newCatalog(cfg *config.Config, src keysrc) *Catalog {
	byName := map[string]*provider.Keys{}
	for _, p := range cfg.Providers {
		byName[p.Name] = provider.NewKeys(p.KeyName(), p.APIKeyEnv, src, p.NeedsKey())
	}
	return New(cfg, func(name string) *provider.Keys { return byName[name] })
}

func ids(s *Snapshot) string {
	var out []string
	for _, m := range s.Models {
		out = append(out, m.ID)
	}
	return strings.Join(out, " ")
}

func TestListsOllamaCloudAndGemini(t *testing.T) {
	f := newFakes(t)
	c := newCatalog(f.config(), keysrc{"ollama": {"ollama-key"}, "gemini": {"gemini-key"}})
	s := c.Get(context.Background(), false)

	want := "ollama/dolphin-phi:latest ollama/granite4.1:3b ollama/gpt-oss:120b-cloud " +
		"ollama-cloud/gpt-oss:120b ollama-cloud/qwen3-coder:480b gemini/gemini-2.5-flash gemini/gemini-2.5-pro"
	if got := ids(s); got != want {
		t.Fatalf("models:\n got %s\nwant %s", got, want)
	}
	kinds := map[string]string{}
	for _, m := range s.Models {
		kinds[m.ID] = m.Kind
	}
	if kinds["ollama/granite4.1:3b"] != "local" || kinds["ollama/gpt-oss:120b-cloud"] != "cloud" {
		t.Errorf("kinds: %v", kinds)
	}
}

func TestMissingUsesCompleteListsOnly(t *testing.T) {
	f := newFakes(t)
	c := newCatalog(f.config(), keysrc{"ollama": {"ollama-key"}, "gemini": {"gemini-key"}})
	s := c.Get(context.Background(), false)

	if !s.Missing("ollama", "qwen2.5-coder:7b") {
		t.Error("a model that is not installed must be reported missing")
	}
	if s.Missing("ollama", "granite4.1:3b") || s.Missing("ollama", "dolphin-phi") || s.Missing("ollama", "GRANITE4.1:3b") {
		t.Error("installed models (with :latest or other case) must not be missing")
	}
	if !s.Missing("gemini", "gemini-3.5-flash") || s.Missing("gemini", "gemini-2.5-pro") {
		t.Error("gemini list is complete")
	}
	if s.Missing("anthropic", "whatever") {
		t.Error("an unknown provider is never reported missing")
	}
}

func TestBadKeyIsReportedAndNothingIsHidden(t *testing.T) {
	f := newFakes(t)
	c := newCatalog(f.config(), keysrc{"ollama": {"wrong"}, "gemini": {"nope"}})
	s := c.Get(context.Background(), false)

	g, _ := s.Result("gemini")
	if g.Listed || !strings.Contains(g.Error, "API key not valid") {
		t.Errorf("gemini result: %+v", g)
	}
	oc, _ := s.Result("ollama-cloud")
	if oc.Listed || !strings.Contains(oc.Error, "unauthorized") {
		t.Errorf("ollama-cloud result: %+v", oc)
	}
	// local Ollama still works with the same (cloud) key stored for "ollama"
	if r, _ := s.Result("ollama"); !r.Listed || r.Count != 3 {
		t.Errorf("local ollama: %+v", r)
	}
	// a failed list proves nothing, so the model must not count as missing
	if s.Missing("gemini", "gemini-3.5-flash") {
		t.Error("failed listing must not hide models")
	}
}

func TestNoKeyIsSkippedSilently(t *testing.T) {
	f := newFakes(t)
	c := newCatalog(f.config(), keysrc{})
	s := c.Get(context.Background(), false)
	for _, name := range []string{"ollama-cloud", "gemini"} {
		r, _ := s.Result(name)
		if r.Skipped != "no key" || r.Error != "" || r.Listed {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r, _ := s.Result("ollama"); !r.Listed {
		t.Error("local Ollama needs no key")
	}
}

func TestUnreachableServer(t *testing.T) {
	f := newFakes(t)
	f.local.Close()
	c := newCatalog(f.config(), keysrc{})
	r, _ := c.Get(context.Background(), false).Result("ollama")
	if r.Listed || !strings.HasPrefix(r.Error, "not reachable") {
		t.Errorf("result: %+v", r)
	}
}

func TestCacheAndInvalidate(t *testing.T) {
	f := newFakes(t)
	c := newCatalog(f.config(), keysrc{})
	c.Get(context.Background(), false)
	c.Get(context.Background(), false)
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("second Get must use the cache, server hit %d times", n)
	}
	c.Invalidate() // a key was added
	c.Get(context.Background(), false)
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("after Invalidate the list must be fetched again, hits %d", n)
	}
	c.Get(context.Background(), true) // refresh=1
	if n := f.hits.Load(); n != 3 {
		t.Fatalf("force must fetch, hits %d", n)
	}
}

func TestFallbackPrefersLocalAndSkipsIncompleteLists(t *testing.T) {
	f := newFakes(t)
	c := newCatalog(f.config(), keysrc{"ollama": {"ollama-key"}, "gemini": {"gemini-key"}})
	s := c.Get(context.Background(), false)
	var got []string
	for _, m := range s.Fallback(3) {
		got = append(got, m.ID)
	}
	// local Ollama first (its own order); remote complete lists follow
	if strings.Join(got, " ") != "ollama/dolphin-phi:latest ollama/granite4.1:3b ollama/gpt-oss:120b-cloud" {
		t.Errorf("fallback: %v", got)
	}
}

func TestWarmFillsSnapshotInBackground(t *testing.T) {
	f := newFakes(t)
	c := newCatalog(f.config(), keysrc{})
	if c.Peek() != nil {
		t.Fatal("no snapshot before the first fetch")
	}
	c.Warm()
	deadline := time.Now().Add(3 * time.Second)
	for c.Peek() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Peek() == nil {
		t.Fatal("Warm did not produce a snapshot")
	}
}

func TestOpenAIChatFilter(t *testing.T) {
	for id, want := range map[string]bool{
		"gpt-5.5": true, "o3-mini": true, "chatgpt-4o-latest": true,
		"text-embedding-3-small": false, "whisper-1": false, "gpt-4o-mini-tts": false,
		"dall-e-3": false, "gpt-image-1": false, "davinci-002": false,
	} {
		if got := isOpenAIChat(id); got != want {
			t.Errorf("isOpenAIChat(%q) = %v, want %v", id, got, want)
		}
	}
}

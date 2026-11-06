package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"relay/internal/catalog"
	"relay/internal/config"
	"relay/internal/provider"
	"relay/internal/usage"
)

type keysrc map[string][]string

func (k keysrc) Values(name string) []string { return k[name] }

// newRouter builds a router whose "ollama" provider is a fake server with the given models.
func newRouter(t *testing.T, tags string, src keysrc) (*Router, *catalog.Catalog) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tags))
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{
		Providers: []config.Provider{
			{Name: "ollama", Kind: "openai", BaseURL: srv.URL + "/v1"},
			{Name: "anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com"},
		},
		Models: []config.Model{
			{ID: "local-small", Provider: "ollama", Name: "qwen2.5-coder:7b", Tools: true},
			{ID: "haiku", Provider: "anthropic", Name: "claude-haiku-5-5", Tools: true},
		},
		Modes: map[string]config.Mode{
			"small": {Models: []string{"local-small", "haiku"}},
		},
	}
	rt := New(cfg, usage.Load(filepath.Join(t.TempDir(), "usage.json")), src)
	cat := catalog.New(cfg, rt.Keys)
	rt.SetCatalog(cat)
	return rt, cat
}

func resolve(t *testing.T, rt *Router, model string) string {
	t.Helper()
	chain, _, err := rt.Resolve("small", model, false, Hint{})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(chain, " ")
}

func TestUninstalledLocalModelIsReplacedByInstalledOnes(t *testing.T) {
	rt, cat := newRouter(t, `{"models":[{"name":"dolphin-phi:latest"},{"name":"granite4.1:3b"},{"name":"phi4:14b"}]}`,
		keysrc{"anthropic": {"k"}})

	// before the first look at the providers nothing is filtered
	if got := resolve(t, rt, ""); got != "local-small haiku" {
		t.Fatalf("without a snapshot: %q", got)
	}
	cat.Get(context.Background(), true)

	// qwen2.5-coder:7b is not installed: two installed models take its place
	if got := resolve(t, rt, ""); got != "ollama/dolphin-phi:latest ollama/granite4.1:3b haiku" {
		t.Fatalf("with a snapshot: %q", got)
	}
	// an explicitly chosen model goes first
	if got := resolve(t, rt, "ollama/phi4:14b"); got != "ollama/phi4:14b ollama/dolphin-phi:latest ollama/granite4.1:3b haiku" {
		t.Fatalf("explicit model: %q", got)
	}
}

func TestInstalledModelKeepsItsConfigName(t *testing.T) {
	rt, cat := newRouter(t, `{"models":[{"name":"qwen2.5-coder:7b"},{"name":"granite4.1:3b"}]}`, keysrc{})
	cat.Get(context.Background(), true)
	// no anthropic key: haiku is skipped; local-small is installed and stays
	if got := resolve(t, rt, ""); got != "local-small" {
		t.Fatalf("got %q", got)
	}
}

func TestNothingUsableFallsBackToInstalled(t *testing.T) {
	rt, cat := newRouter(t, `{"models":[{"name":"granite4.1:3b"}]}`, keysrc{})
	cat.Get(context.Background(), true)
	// local-small is not installed (replaced by granite), haiku has no key
	if got := resolve(t, rt, ""); got != "ollama/granite4.1:3b" {
		t.Fatalf("got %q", got)
	}
}

func TestOllamaDownKeepsConfiguredChain(t *testing.T) {
	rt, cat := newRouter(t, `{"models":[]}`, keysrc{"anthropic": {"k"}})
	rt.cfg.Providers[0].BaseURL = "http://127.0.0.1:1/v1" // nothing listens there
	cat.Get(context.Background(), true)
	// the list could not be fetched, so nothing is known to be missing
	if got := resolve(t, rt, ""); got != "local-small haiku" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamFindsModelByProviderSlashName(t *testing.T) {
	rt, _ := newRouter(t, `{"models":[]}`, keysrc{})
	if _, ok := rt.cfg.ModelByID("ollama/granite4.1:3b"); !ok {
		t.Fatal("provider/model must resolve")
	}
	if _, _, err := rt.Resolve("small", "nope/x", false, Hint{}); err == nil {
		t.Fatal("unknown provider must be an error")
	}
}

func TestStreamAskBeforeSwitchingModels(t *testing.T) {
	good := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"model":"bad"`) {
			http.Error(w, `{"error":{"message":"boom"}}`, 500)
			return
		}
		good++
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{
		Providers: []config.Provider{{Name: "p", Kind: "openai", BaseURL: srv.URL + "/v1"}},
		Models: []config.Model{
			{ID: "bad-model", Provider: "p", Name: "bad", Tools: true},
			{ID: "good-model", Provider: "p", Name: "good", Tools: true},
		},
		Modes: map[string]config.Mode{"small": {Models: []string{"bad-model", "good-model"}}},
	}
	r := New(cfg, usage.Load(filepath.Join(t.TempDir(), "usage.json")), keysrc{})
	chain := []string{"bad-model", "good-model"}
	emit := func(provider.Event) error { return nil }

	var asked []string
	id, err := r.StreamAsk(context.Background(), chain, provider.Request{}, emit,
		func(failed, next, why string) string { asked = append(asked, failed+">"+next); return "deny" })
	if err == nil || id != "bad-model" || len(asked) != 1 || asked[0] != "bad-model>good-model" || good != 0 {
		t.Fatalf("refusal: id=%q err=%v asked=%v good=%d", id, err, asked, good)
	}
	id, err = r.StreamAsk(context.Background(), chain, provider.Request{}, emit,
		func(string, string, string) string { return "allow" })
	if err != nil || id != "good-model" || good != 1 {
		t.Fatalf("consent: id=%q err=%v good=%d", id, err, good)
	}
}

// providerServer answers "good" and "other" and fails (500) for models whose name starts with "bad".
func providerServer(t *testing.T) (*httptest.Server, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		name := ""
		for _, n := range []string{"bad1", "bad2", "good", "other"} {
			if strings.Contains(string(body), `"model":"`+n+`"`) {
				name = n
			}
		}
		mu.Lock()
		hits[name]++
		mu.Unlock()
		if strings.HasPrefix(name, "bad") {
			http.Error(w, `{"error":{"message":"boom"}}`, 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, func(n string) int { mu.Lock(); defer mu.Unlock(); return hits[n] }
}

func twoProviders(t *testing.T, srv *httptest.Server) *Router {
	cfg := &config.Config{
		Providers: []config.Provider{
			{Name: "p", Kind: "openai", BaseURL: srv.URL + "/v1"},
			{Name: "q", Kind: "openai", BaseURL: srv.URL + "/v1"},
		},
		Models: []config.Model{
			{ID: "bad1-m", Provider: "p", Name: "bad1", Tools: true},
			{ID: "other-m", Provider: "q", Name: "other", Tools: true},
			{ID: "bad2-m", Provider: "p", Name: "bad2", Tools: true},
			{ID: "good-m", Provider: "p", Name: "good", Tools: true},
		},
		Modes: map[string]config.Mode{"small": {Models: []string{"bad1-m", "other-m"}}},
	}
	return New(cfg, usage.Load(filepath.Join(t.TempDir(), "usage.json")), keysrc{})
}

func TestStreamAskOffersModelsOfTheSameProviderOnly(t *testing.T) {
	srv, hits := providerServer(t)
	r := twoProviders(t, srv)
	emit := func(provider.Event) error { return nil }
	var asked []string
	// the chain puts another provider's model second: it must not be offered
	id, err := r.StreamAsk(context.Background(), []string{"bad1-m", "other-m"}, provider.Request{}, emit,
		func(failed, next, why string) string { asked = append(asked, failed+">"+next); return "allow" })
	if err != nil || id != "good-m" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if len(asked) != 2 || asked[0] != "bad1-m>bad2-m" || asked[1] != "bad2-m>good-m" {
		t.Fatalf("asked=%v", asked)
	}
	if hits("other") != 0 {
		t.Fatal("a model of another provider was used")
	}
}

func TestStreamAskDenyShowsTheModelsOwnError(t *testing.T) {
	srv, hits := providerServer(t)
	r := twoProviders(t, srv)
	emit := func(provider.Event) error { return nil }
	id, err := r.StreamAsk(context.Background(), []string{"bad1-m", "other-m"}, provider.Request{}, emit,
		func(string, string, string) string { return "deny" })
	if id != "bad1-m" || err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if hits("bad2") != 0 || hits("good") != 0 {
		t.Fatal("no other model must be tried after a refusal")
	}
}

func TestStreamAskAlwaysAsksOnlyOnce(t *testing.T) {
	srv, _ := providerServer(t)
	r := twoProviders(t, srv)
	emit := func(provider.Event) error { return nil }
	calls := 0
	id, err := r.StreamAsk(context.Background(), []string{"bad1-m"}, provider.Request{}, emit,
		func(string, string, string) string { calls++; return "always" })
	if err != nil || id != "good-m" || calls != 1 {
		t.Fatalf("id=%q err=%v asked=%d", id, err, calls)
	}
}

func TestStreamAskWholeProviderFailing(t *testing.T) {
	srv, hits := providerServer(t)
	r := twoProviders(t, srv)
	r.cfg.Models = []config.Model{ // p has only failing models; q has a good one
		{ID: "bad1-m", Provider: "p", Name: "bad1", Tools: true},
		{ID: "bad2-m", Provider: "p", Name: "bad2", Tools: true},
		{ID: "other-m", Provider: "q", Name: "other", Tools: true},
	}
	emit := func(provider.Event) error { return nil }
	id, err := r.StreamAsk(context.Background(), []string{"bad1-m", "other-m"}, provider.Request{}, emit, nil)
	if id != "" || err == nil || !strings.Contains(err.Error(), "none of the models of p") || !strings.Contains(err.Error(), "pick another model") {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if hits("bad1") != 1 || hits("bad2") != 1 || hits("other") != 0 {
		t.Fatalf("hits: bad1=%d bad2=%d other=%d", hits("bad1"), hits("bad2"), hits("other"))
	}
}

func TestAdviceMentionsKeysForRemoteProviders(t *testing.T) {
	if !strings.Contains(advice(false), "/keys") {
		t.Fatal("a remote provider: point to /keys")
	}
	if strings.Contains(advice(true), "/keys") {
		t.Fatal("a local server has no keys")
	}
}

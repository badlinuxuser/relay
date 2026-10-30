package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"relay/internal/catalog"
	"relay/internal/config"
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

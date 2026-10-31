package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := Load(write(t, ExampleTOML))
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, p := range c.Providers {
		count[p.Name]++
		if p.Name == "ollama-cloud" && (p.KeyFrom != "ollama" || !p.NeedsKey()) {
			t.Errorf("ollama-cloud: %+v", p)
		}
	}
	for _, name := range []string{"ollama-cloud", "grok", "deepseek", "lmstudio"} {
		if count[name] != 1 {
			t.Errorf("%s must appear once in the example, found %d", name, count[name])
		}
	}
}

// config.toml is created once and never rewritten, so a file from an older version
// has no ollama-cloud: it is added next to the local Ollama.
func TestOldConfigGetsOllamaCloud(t *testing.T) {
	c, err := Load(write(t, `
[[providers]]
name = "ollama"
kind = "openai"
base_url = "http://127.0.0.1:11434/v1"

[modes.small]
models = []
`))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := c.ProviderByName("ollama-cloud")
	if !ok || p.KeyFrom != "ollama" || p.BaseURL != "https://ollama.com/v1" {
		t.Fatalf("implicit provider: %+v ok=%v", p, ok)
	}
	if p.KeyName() != "ollama" {
		t.Errorf("KeyName = %q", p.KeyName())
	}
}

func TestNoOllamaNoImplicitCloud(t *testing.T) {
	c, err := Load(write(t, `
[[providers]]
name = "gemini"
kind = "openai"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai"

[modes.small]
models = []
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.ProviderByName("ollama-cloud"); ok {
		t.Error("ollama-cloud must not be added without a local Ollama provider")
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"key_from unknown": `[[providers]]
name = "a"
kind = "openai"
base_url = "http://x"
key_from = "nope"
[modes.small]
models = []`,
		"slash in name": `[[providers]]
name = "a/b"
kind = "openai"
base_url = "http://x"
[modes.small]
models = []`,
		"bad discover": `[[providers]]
name = "a"
kind = "openai"
base_url = "http://x"
discover = "magic"
[modes.small]
models = []`,
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestModelByIDAcceptsProviderSlashModel(t *testing.T) {
	c := &Config{
		Providers: []Provider{{Name: "ollama"}, {Name: "gemini"}, {Name: "openrouter"}},
		Models:    []Model{{ID: "haiku", Provider: "gemini", Name: "x"}},
	}
	m, ok := c.ModelByID("ollama/granite4.1:3b")
	if !ok || m.Provider != "ollama" || m.Name != "granite4.1:3b" || !m.Tools || m.PriceIn != 0 {
		t.Errorf("ollama/granite4.1:3b -> %+v ok=%v", m, ok)
	}
	// the model name may itself contain a slash (OpenRouter)
	m, ok = c.ModelByID("openrouter/openai/gpt-4o")
	if !ok || m.Provider != "openrouter" || m.Name != "openai/gpt-4o" {
		t.Errorf("openrouter/openai/gpt-4o -> %+v ok=%v", m, ok)
	}
	if m, ok = c.ModelByID("haiku"); !ok || m.Name != "x" {
		t.Errorf("configured id must win: %+v", m)
	}
	for _, bad := range []string{"nope/x", "ollama/", "/x", "ollama", ""} {
		if _, ok := c.ModelByID(bad); ok {
			t.Errorf("%q must not resolve", bad)
		}
	}
}

func TestDiscoverStyle(t *testing.T) {
	for _, tc := range []struct {
		p    Provider
		want string
	}{
		{Provider{Name: "ollama", BaseURL: "http://127.0.0.1:11434/v1"}, "ollama"},
		{Provider{Name: "x", BaseURL: "http://10.0.0.5:11434/v1"}, "ollama"},
		{Provider{Name: "ollama-cloud", BaseURL: "https://ollama.com/v1"}, "ollama"},
		{Provider{Name: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai"}, "gemini"},
		{Provider{Name: "anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com"}, "anthropic"},
		{Provider{Name: "openai", BaseURL: "https://api.openai.com/v1"}, "openai"},
		{Provider{Name: "llamacpp", BaseURL: "http://127.0.0.1:8080/v1"}, "openai"},
		{Provider{Name: "ollama", BaseURL: "http://127.0.0.1:11434/v1", Discover: "none"}, "none"},
	} {
		if got := tc.p.DiscoverStyle(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.p.Name, got, tc.want)
		}
	}
	if strings.Contains(ExampleTOML, "discover = \"") && !strings.Contains(ExampleTOML, "# discover") {
		t.Log("example uses discover outside a comment")
	}
}

// Old config files have no Grok and DeepSeek: they are added, as official providers.
func TestOldConfigGetsGrokAndDeepSeek(t *testing.T) {
	c, err := Load(write(t, `
[[providers]]
name = "gemini"
kind = "openai"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai"

[modes.small]
models = []
`))
	if err != nil {
		t.Fatal(err)
	}
	g, ok := c.ProviderByName("grok")
	if !ok || g.BaseURL != "https://api.x.ai/v1" || !g.IsOfficial() || !g.NeedsKey() {
		t.Errorf("grok: %+v ok=%v", g, ok)
	}
	d, ok := c.ProviderByName("deepseek")
	if !ok || d.BaseURL != "https://api.deepseek.com" || !d.IsOfficial() {
		t.Errorf("deepseek: %+v ok=%v", d, ok)
	}
	lm, ok := c.ProviderByName("lmstudio")
	if !ok || lm.BaseURL != "http://127.0.0.1:1234/v1" || !lm.IsLocal() || lm.NeedsKey() || lm.IsOfficial() {
		t.Errorf("lmstudio: %+v ok=%v", lm, ok)
	}
	// a provider the user already has is not duplicated or changed
	c2, _ := Load(write(t, `
[[providers]]
name = "grok"
kind = "openai"
base_url = "http://my-proxy:9000/v1"

[modes.small]
models = []
`))
	n := 0
	for _, p := range c2.Providers {
		if p.Name == "grok" {
			n++
			if p.BaseURL != "http://my-proxy:9000/v1" {
				t.Errorf("user's grok was changed: %+v", p)
			}
		}
	}
	if n != 1 {
		t.Errorf("grok appears %d times", n)
	}
}

func TestOfficialProvidersComeFirst(t *testing.T) {
	c := &Config{Providers: []Provider{
		{Name: "ollama", BaseURL: "http://127.0.0.1:11434/v1"},
		{Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1"},
		{Name: "anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com"},
		{Name: "llamacpp", BaseURL: "http://127.0.0.1:8080/v1"},
		{Name: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai"},
		{Name: "ollama-cloud", BaseURL: "https://ollama.com/v1"},
		{Name: "grok", BaseURL: "https://api.x.ai/v1"},
		{Name: "openai", BaseURL: "https://api.openai.com/v1"},
		{Name: "deepseek", BaseURL: "https://api.deepseek.com"},
	}}
	var got []string
	for _, p := range c.OrderedProviders() {
		got = append(got, p.Name)
	}
	want := "anthropic gemini grok openai deepseek ollama openrouter llamacpp ollama-cloud"
	if strings.Join(got, " ") != want {
		t.Errorf("order:\n got  %s\n want %s", strings.Join(got, " "), want)
	}
}

// Every config gets /ollama and /llamacpp commands, also an old one without [[processes]].
func TestOldConfigGetsOllamaAndLlamacppProcesses(t *testing.T) {
	c, err := Load(write(t, `
[[providers]]
name = "ollama"
kind = "openai"
base_url = "http://127.0.0.1:11434/v1"

[modes.small]
models = []
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Process{}
	for _, p := range c.Processes {
		got[p.Name] = p
	}
	if got["ollama"].Command != "ollama" || got["llamacpp"].Command != "llama-server" {
		t.Fatalf("processes: %+v", c.Processes)
	}
	if got["llamacpp"].ReadyURL == "" {
		t.Error("llamacpp needs a ready_url so Relay sees it is running")
	}
	// the example config defines both once
	c, err = Load(write(t, ExampleTOML))
	if err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, p := range c.Processes {
		n[p.Name]++
	}
	if n["ollama"] != 1 || n["llamacpp"] != 1 {
		t.Errorf("example processes: %v", n)
	}
}

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
	n := 0
	for _, p := range c.Providers {
		if p.Name == "ollama-cloud" {
			n++
			if p.KeyFrom != "ollama" || !p.NeedsKey() {
				t.Errorf("ollama-cloud: %+v", p)
			}
		}
	}
	if n != 1 {
		t.Errorf("ollama-cloud must appear once, found %d", n)
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

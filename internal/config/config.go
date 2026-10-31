// Package config загружает конфигурацию Relay (TOML) и находит рабочие каталоги.
package config

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed example.toml
var ExampleTOML string

// ValidModes это режимы, известные интерфейсу (от них зависит цветовая тема).
var ValidModes = []string{"small", "medium", "hard", "maximum"}

type Provider struct {
	Name      string            `toml:"name"`
	Kind      string            `toml:"kind"` // openai | anthropic
	BaseURL   string            `toml:"base_url"`
	APIKeyEnv []string          `toml:"api_key_env"`
	Headers   map[string]string `toml:"headers"`
	// KeyFrom: take keys from another provider (for example ollama-cloud uses the
	// keys saved for "ollama"). Such a provider has no card on the keys page.
	KeyFrom string `toml:"key_from"`
	// Discover: how to list the provider's models: ollama | gemini | anthropic |
	// openai | none. Empty means "guess from the address".
	Discover string `toml:"discover"`
}

// KeyName is the name under which this provider's keys are stored.
func (p Provider) KeyName() string {
	if p.KeyFrom != "" {
		return p.KeyFrom
	}
	return p.Name
}

// DiscoverStyle returns how the provider's model list is fetched (see Discover).
func (p Provider) DiscoverStyle() string {
	if p.Discover != "" {
		return p.Discover
	}
	u, err := url.Parse(p.BaseURL)
	host, port := "", ""
	if err == nil {
		host, port = strings.ToLower(u.Hostname()), u.Port()
	}
	switch {
	case host == "generativelanguage.googleapis.com":
		return "gemini"
	case p.Kind == "anthropic":
		return "anthropic"
	case p.Name == "ollama" || strings.HasPrefix(p.Name, "ollama-") || port == "11434" || host == "ollama.com":
		return "ollama"
	}
	return "openai"
}

// officialHosts are the APIs of the companies that make the models themselves.
var officialHosts = map[string]bool{
	"api.anthropic.com":                 true,
	"api.openai.com":                    true,
	"generativelanguage.googleapis.com": true,
	"api.x.ai":                          true,
	"api.deepseek.com":                  true,
}

// IsOfficial: the provider is the model maker's own API (Anthropic, OpenAI, Gemini,
// Grok, DeepSeek), as opposed to local servers, Ollama and resellers like OpenRouter.
func (p Provider) IsOfficial() bool {
	u, err := url.Parse(p.BaseURL)
	return err == nil && officialHosts[strings.ToLower(u.Hostname())]
}

// IsLocal: the provider is a server on this computer (loopback address).
func (p Provider) IsLocal() bool {
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}

// NeedsKey: нужен ли провайдеру API-ключ. Локальным серверам (loopback) он не нужен.
func (p Provider) NeedsKey() bool {
	if p.Kind == "anthropic" || len(p.APIKeyEnv) > 0 || p.KeyFrom != "" {
		return true
	}
	return !p.IsLocal()
}

type Model struct {
	ID       string  `toml:"id"`
	Provider string  `toml:"provider"`
	Name     string  `toml:"model"`
	Tier     string  `toml:"tier"`
	PriceIn  float64 `toml:"price_in"`
	PriceOut float64 `toml:"price_out"`
	Context  int     `toml:"context"`
	Tools    bool    `toml:"tools"`
}

type Mode struct {
	Models   []string `toml:"models"`
	MaxSteps int      `toml:"max_steps"`
}

type Process struct {
	Name      string   `toml:"name"`
	Command   string   `toml:"command"`
	Args      []string `toml:"args"`
	Dir       string   `toml:"dir"`
	Env       []string `toml:"env"`
	ReadyURL  string   `toml:"ready_url"`
	AutoStart bool     `toml:"autostart"`
}

type MCPServer struct {
	Name    string   `toml:"name"`
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
	Env     []string `toml:"env"`
}

type Preset struct {
	Name   string `toml:"name"`
	Prompt string `toml:"prompt"`
}

type Config struct {
	Listen      string          `toml:"listen"`
	Workspace   string          `toml:"workspace"`
	DefaultMode string          `toml:"default_mode"`
	Providers   []Provider      `toml:"providers"`
	Models      []Model         `toml:"models"`
	Modes       map[string]Mode `toml:"modes"`
	Processes   []Process       `toml:"processes"`
	MCP         []MCPServer     `toml:"mcp"`
	Presets     []Preset        `toml:"presets"`
}

// Home возвращает каталог данных Relay: $RELAY_HOME или <UserConfigDir>/relay.
func Home() (string, error) {
	h := os.Getenv("RELAY_HOME")
	if h == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		h = filepath.Join(base, "relay")
	}
	if err := os.MkdirAll(h, 0o700); err != nil {
		return "", err
	}
	return h, nil
}

// Ensure создаёт config.toml из примера, если его ещё нет, и возвращает путь.
func Ensure(home string) (string, error) {
	path := filepath.Join(home, "config.toml")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(ExampleTOML), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.DefaultMode == "" {
		c.DefaultMode = "small"
	}
	if c.Modes == nil {
		c.Modes = map[string]Mode{}
	}
	provs := map[string]bool{}
	for _, p := range c.Providers {
		if p.Name == "" || provs[p.Name] {
			return fmt.Errorf("provider name is empty or duplicated: %q", p.Name)
		}
		if p.Kind != "openai" && p.Kind != "anthropic" {
			return fmt.Errorf("provider %q: kind must be openai or anthropic", p.Name)
		}
		if p.BaseURL == "" {
			return fmt.Errorf("provider %q: base_url is required", p.Name)
		}
		if strings.Contains(p.Name, "/") {
			return fmt.Errorf("provider name %q must not contain \"/\" (model ids are provider/model)", p.Name)
		}
		switch p.Discover {
		case "", "ollama", "gemini", "anthropic", "openai", "none":
		default:
			return fmt.Errorf("provider %q: discover must be ollama, gemini, anthropic, openai or none", p.Name)
		}
		provs[p.Name] = true
	}
	for _, p := range c.Providers {
		if p.KeyFrom != "" && (!provs[p.KeyFrom] || p.KeyFrom == p.Name) {
			return fmt.Errorf("provider %q: key_from %q is not another configured provider", p.Name, p.KeyFrom)
		}
	}
	c.addImplicitProviders(provs)
	models := map[string]bool{}
	for _, m := range c.Models {
		if m.ID == "" || models[m.ID] {
			return fmt.Errorf("model id is empty or duplicated: %q", m.ID)
		}
		if !provs[m.Provider] {
			return fmt.Errorf("model %q: unknown provider %q", m.ID, m.Provider)
		}
		models[m.ID] = true
	}
	for name, md := range c.Modes {
		if !validMode(name) {
			return fmt.Errorf("unknown mode %q (allowed: small, medium, hard, maximum)", name)
		}
		for _, id := range md.Models {
			if !models[id] {
				return fmt.Errorf("mode %q: unknown model %q", name, id)
			}
		}
		if md.MaxSteps <= 0 {
			md.MaxSteps = 12
			c.Modes[name] = md
		}
	}
	if _, ok := c.Modes[c.DefaultMode]; !ok {
		return fmt.Errorf("default_mode %q is not configured", c.DefaultMode)
	}
	return nil
}

// addImplicitProviders keeps old config files working: config.toml is created once
// and never rewritten, so a file from an earlier version lacks the providers added
// later. They are added here: Ollama cloud (next to a local Ollama, with the same
// keys), Grok and DeepSeek.
func (c *Config) addImplicitProviders(provs map[string]bool) {
	add := func(p Provider) {
		if !provs[p.Name] {
			c.Providers = append(c.Providers, p)
			provs[p.Name] = true
		}
	}
	if provs["ollama"] {
		add(Provider{Name: "ollama-cloud", Kind: "openai", BaseURL: "https://ollama.com/v1", KeyFrom: "ollama", Discover: "ollama"})
	}
	add(Provider{Name: "grok", Kind: "openai", BaseURL: "https://api.x.ai/v1"})
	add(Provider{Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com"})
}

// OrderedProviders lists the providers the way the user sees them: the model
// makers' own APIs first, then the rest (Ollama, llama.cpp, OpenRouter, ...);
// the configured order is kept inside each group.
func (c *Config) OrderedProviders() []Provider {
	out := make([]Provider, 0, len(c.Providers))
	for _, p := range c.Providers {
		if p.IsOfficial() {
			out = append(out, p)
		}
	}
	for _, p := range c.Providers {
		if !p.IsOfficial() {
			out = append(out, p)
		}
	}
	return out
}

func validMode(name string) bool {
	for _, m := range ValidModes {
		if m == name {
			return true
		}
	}
	return false
}

// ModelByID finds a model by id. Besides the models from config.toml, any
// "<provider>/<model>" is accepted for a configured provider (that is how models
// found by the provider's own model list are addressed, for example
// "ollama/granite4.1:3b" or "gemini/gemini-2.5-flash"). Such models have no price.
func (c *Config) ModelByID(id string) (Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	if i := strings.Index(id, "/"); i > 0 && i < len(id)-1 {
		if _, ok := c.ProviderByName(id[:i]); ok {
			return Model{ID: id, Provider: id[:i], Name: id[i+1:], Tools: true}, true
		}
	}
	return Model{}, false
}

func (c *Config) ProviderByName(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

func (c *Config) PresetPrompt(name string) string {
	for _, p := range c.Presets {
		if p.Name == name {
			return p.Prompt
		}
	}
	return ""
}

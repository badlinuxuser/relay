// Package config загружает конфигурацию Relay (TOML) и находит рабочие каталоги.
package config

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

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
}

// NeedsKey: нужен ли провайдеру API-ключ. Локальным серверам (loopback) он не нужен.
func (p Provider) NeedsKey() bool {
	if p.Kind == "anthropic" || len(p.APIKeyEnv) > 0 {
		return true
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return true
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return false
	}
	return true
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
		provs[p.Name] = true
	}
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

func validMode(name string) bool {
	for _, m := range ValidModes {
		if m == name {
			return true
		}
	}
	return false
}

func (c *Config) ModelByID(id string) (Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

func (c *Config) PresetPrompt(name string) string {
	for _, p := range c.Presets {
		if p.Name == name {
			return p.Prompt
		}
	}
	return ""
}

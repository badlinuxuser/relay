// Package settings keeps the user's preferences (settings.json next to config.toml).
// config.toml describes providers, models and processes and is edited by hand;
// settings.json is changed from the /settings page and applies at once.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Settings is what the /settings page edits. The zero value is not the default: use Default().
type Settings struct {
	// Chat
	ShowThoughts bool   `json:"show_thoughts"` // show the model's reasoning, set apart from the answer
	StreamSpeed  string `json:"stream_speed"`  // instant | fast | normal | slow: how quickly the answer is typed out

	// Interface
	Sidebar   string `json:"sidebar"`    // auto | hidden
	Mascot    bool   `json:"mascot"`     // draw the mascot in the side panel
	TextWidth int    `json:"text_width"` // 0 = the whole width, otherwise wrap the chat at this many columns
	Mouse     bool   `json:"mouse"`      // the interface takes the mouse (clicks, wheel). Off: you can select and copy text freely

	// Launcher
	DefaultMode string            `json:"default_mode"` // "" = the one from config.toml
	Autostart   map[string]bool   `json:"autostart"`    // process name -> start it when Relay starts (overrides config.toml)
	ProcessArgs map[string]string `json:"process_args"` // process name -> extra arguments used when started without any (llamacpp: -m /path/model.gguf)
	KeepBackend bool              `json:"keep_backend"` // leave the background service running when the interface quits
}

func Default() Settings {
	return Settings{
		ShowThoughts: true,
		StreamSpeed:  "normal",
		Sidebar:      "auto",
		Mascot:       true,
		Mouse:        true,
		Autostart:    map[string]bool{},
		ProcessArgs:  map[string]string{},
	}
}

// SpeedCPS is the typing speed in characters per second (0 = no limit).
func SpeedCPS(speed string) int {
	switch speed {
	case "instant":
		return 0
	case "fast":
		return 900
	case "slow":
		return 80
	}
	return 280
}

// Clean fixes values that are out of range, so a hand-edited file cannot break the interface.
func (s *Settings) Clean() {
	switch s.StreamSpeed {
	case "instant", "fast", "normal", "slow":
	default:
		s.StreamSpeed = "normal"
	}
	switch s.Sidebar {
	case "auto", "hidden":
	default:
		s.Sidebar = "auto"
	}
	if s.TextWidth != 0 {
		s.TextWidth = min(max(s.TextWidth, 40), 240)
	}
	if s.Autostart == nil {
		s.Autostart = map[string]bool{}
	}
	if s.ProcessArgs == nil {
		s.ProcessArgs = map[string]string{}
	}
	for k, v := range s.ProcessArgs {
		if strings.TrimSpace(v) == "" {
			delete(s.ProcessArgs, k)
		}
	}
}

// Store reads and writes settings.json.
type Store struct {
	path string
	mu   sync.Mutex
	cur  Settings
}

// Open loads settings.json; a missing or broken file gives the defaults.
func Open(home string) *Store {
	st := &Store{path: filepath.Join(home, "settings.json"), cur: Default()}
	if b, err := os.ReadFile(st.path); err == nil {
		s := Default()
		if json.Unmarshal(b, &s) == nil { // fields missing from the file keep their defaults
			st.cur = s
		}
	}
	st.cur.Clean()
	return st
}

func (st *Store) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return copyOf(st.cur)
}

// Set replaces the settings and saves them.
func (st *Store) Set(s Settings) (Settings, error) {
	s.Clean()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.cur = copyOf(s)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return copyOf(s), err
	}
	return copyOf(s), os.WriteFile(st.path, b, 0o600)
}

func copyOf(s Settings) Settings {
	out := s
	out.Autostart = map[string]bool{}
	for k, v := range s.Autostart {
		out.Autostart[k] = v
	}
	out.ProcessArgs = map[string]string{}
	for k, v := range s.ProcessArgs {
		out.ProcessArgs[k] = v
	}
	return out
}

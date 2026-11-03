package server

import (
	_ "embed"
	"net/http"

	"relay/internal/proto"
	"relay/internal/settings"
)

// The settings page: static HTML like the keys page. The token comes from the URL fragment.
//
//go:embed ui/settings.html
var settingsHTML []byte

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; connect-src 'self'")
	_, _ = w.Write(settingsHTML)
}

// settingsView is what the page needs besides the settings: choices and the processes of config.toml.
type settingsView struct {
	Settings  settings.Settings `json:"settings"`
	Modes     []string          `json:"modes"`
	Processes []procView        `json:"processes"`
}

type procView struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Autostart bool   `json:"autostart"` // effective value
	Args      string `json:"args"`      // saved extra arguments
}

func (s *Server) settingsView() settingsView {
	st := s.set.Get()
	v := settingsView{Settings: st, Modes: []string{}, Processes: []procView{}}
	for _, m := range s.cfgModes() {
		v.Modes = append(v.Modes, m)
	}
	on := map[string]bool{}
	for _, n := range s.Autostart() {
		on[n] = true
	}
	for _, p := range s.cfg.Processes {
		v.Processes = append(v.Processes, procView{Name: p.Name, Command: p.Command, Autostart: on[p.Name], Args: st.ProcessArgs[p.Name]})
	}
	return v
}

func (s *Server) cfgModes() []string {
	var out []string
	for _, n := range []string{"small", "medium", "hard", "maximum"} {
		if _, ok := s.cfg.Modes[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.settingsView())
}

func (s *Server) handleSetSettings(w http.ResponseWriter, r *http.Request) {
	cur := s.set.Get()
	next := cur // fields missing from the request keep their value
	if err := readJSON(r, &next); err != nil {
		httpErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if next.DefaultMode != "" {
		if _, ok := s.cfg.Modes[next.DefaultMode]; !ok {
			httpErr(w, 400, "unknown mode "+next.DefaultMode)
			return
		}
	}
	if _, err := s.set.Set(next); err != nil {
		httpErr(w, 500, "could not save settings: "+err.Error())
		return
	}
	writeJSON(w, 200, s.settingsView())
}

func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	out := []proto.SessionRow{}
	for _, sm := range s.store.List() {
		out = append(out, proto.SessionRow{ID: sm.ID, Title: sm.Title, Mode: sm.Mode, Updated: sm.Updated, Messages: sm.Messages,
			TokensIn: sm.TokensIn, TokensOut: sm.TokensOut, Cost: sm.Cost})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handlePruneSessions deletes many sessions at once: {"which":"empty"} (no messages) or {"which":"all"}.
func (s *Server) handlePruneSessions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Which string `json:"which"`
	}
	if err := readJSON(r, &req); err != nil || (req.Which != "empty" && req.Which != "all") {
		httpErr(w, 400, `which must be "empty" or "all"`)
		return
	}
	deleted, busy := 0, 0
	for _, sm := range s.store.List() {
		if req.Which == "empty" && sm.Messages > 0 {
			continue
		}
		if err := s.store.Delete(sm.ID); err != nil {
			busy++
			continue
		}
		deleted++
	}
	writeJSON(w, 200, map[string]any{"deleted": deleted, "skipped": busy})
}

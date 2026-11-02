package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/config"
	"relay/internal/proto"
	"relay/internal/provider"
)

func newTestServer(t *testing.T) (*Server, func(method, path, body string) *httptest.ResponseRecorder) {
	t.Helper()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", DefaultMode: "small",
		Providers: []config.Provider{
			{Name: "ollama", Kind: "openai", BaseURL: "http://127.0.0.1:1/v1", Discover: "none"},
			{Name: "ollama-cloud", Kind: "openai", BaseURL: "https://ollama.com/v1", KeyFrom: "ollama", Discover: "none"},
			{Name: "llamacpp", Kind: "openai", BaseURL: "http://127.0.0.1:1/v1", Discover: "none"},
		},
		Modes:     map[string]config.Mode{"small": {}, "medium": {}},
		Processes: []config.Process{{Name: "llamacpp", Command: "llama-server"}, {Name: "ollama", Command: "ollama", AutoStart: true}},
	}
	s := New(cfg, t.TempDir(), "tok")
	h := s.Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:8787"
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	return s, call
}

func TestSettingsAPI(t *testing.T) {
	s, call := newTestServer(t)

	rec := call("GET", "/api/settings", "")
	var v settingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if !v.Settings.ShowThoughts || len(v.Processes) != 2 || len(v.Modes) != 2 {
		t.Errorf("defaults: %+v", v)
	}
	if got := strings.Join(s.Autostart(), ","); got != "ollama" {
		t.Errorf("autostart from config: %q", got)
	}

	rec = call("POST", "/api/settings", `{"show_thoughts":false,"stream_speed":"fast","autostart":{"ollama":false,"llamacpp":true},"process_args":{"llamacpp":"-m /m/q.gguf"},"default_mode":"medium"}`)
	if rec.Code != 200 {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	if got := strings.Join(s.Autostart(), ","); got != "llamacpp" {
		t.Errorf("autostart overridden by settings: %q", got)
	}
	if got := strings.Join(s.StartArgs("llamacpp"), " "); got != "-m /m/q.gguf" {
		t.Errorf("saved args: %q", got)
	}
	if s.defaultMode() != "medium" {
		t.Error("default mode from settings")
	}
	if rec = call("POST", "/api/settings", `{"default_mode":"nope"}`); rec.Code != 400 {
		t.Errorf("unknown mode must be refused: %d", rec.Code)
	}

	// the status carries the settings to the interface
	rec = call("GET", "/api/status", "")
	var st proto.StatusResp
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Settings.ShowThoughts || st.Settings.StreamSpeed != "fast" || st.DefaultMode != "medium" {
		t.Errorf("status settings: %+v mode=%s", st.Settings, st.DefaultMode)
	}

	// the page is served without a token
	req := httptest.NewRequest("GET", "/ui/settings", nil)
	req.Host = "127.0.0.1:8787"
	prec := httptest.NewRecorder()
	s.Handler().ServeHTTP(prec, req)
	if prec.Code != 200 || !strings.Contains(prec.Body.String(), "Settings") {
		t.Errorf("settings page: %d", prec.Code)
	}
}

func TestSessionManagement(t *testing.T) {
	s, call := newTestServer(t)
	var ids []string
	for i := 0; i < 3; i++ {
		rec := call("POST", "/api/sessions", `{"mode":"small"}`)
		var si proto.SessionInfo
		_ = json.Unmarshal(rec.Body.Bytes(), &si)
		ids = append(ids, si.ID)
	}
	rec := call("GET", "/api/session-list", "")
	var rows []proto.SessionRow
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 3 {
		t.Fatalf("list: %s", rec.Body)
	}
	if rec = call("DELETE", "/api/sessions/"+ids[0], ""); rec.Code != 200 {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = call("DELETE", "/api/sessions/"+ids[0], ""); rec.Code != 400 {
		t.Errorf("deleting twice: %d", rec.Code)
	}
	if rec = call("DELETE", "/api/sessions/..%2Fkeys", ""); rec.Code == 200 {
		t.Error("a path must not be accepted as an id")
	}

	// a session with messages survives "delete empty"
	sess, _ := s.store.Get(ids[1])
	sess.Messages = append(sess.Messages, providerMessage("hi"))
	_ = s.store.Save(sess)
	rec = call("POST", "/api/sessions/prune", `{"which":"empty"}`)
	var r struct{ Deleted int }
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	if r.Deleted != 1 {
		t.Errorf("prune empty: %s", rec.Body)
	}
	if rec = call("POST", "/api/sessions/prune", `{"which":"all"}`); rec.Code != 200 {
		t.Errorf("prune all: %d", rec.Code)
	}
	if left := len(s.store.List()); left != 0 {
		t.Errorf("%d sessions left", left)
	}
	if rec = call("POST", "/api/sessions/prune", `{"which":"x"}`); rec.Code != 400 {
		t.Errorf("bad request: %d", rec.Code)
	}
}

func TestKeysPageMarksOptionalKeys(t *testing.T) {
	_, call := newTestServer(t)
	rec := call("GET", "/api/keys", "")
	var kr proto.KeysResp
	_ = json.Unmarshal(rec.Body.Bytes(), &kr)
	got := map[string]proto.ProviderKeys{}
	for _, p := range kr.Providers {
		got[p.Name] = p
	}
	if !got["ollama"].KeyOptional || got["ollama"].NeedsKey {
		t.Errorf("ollama: a key is optional (cloud models): %+v", got["ollama"])
	}
	if got["llamacpp"].KeyOptional || got["llamacpp"].NeedsKey {
		t.Errorf("llamacpp needs no key and offers none: %+v", got["llamacpp"])
	}
}

func providerMessage(text string) provider.Message {
	return provider.Message{Role: "user", Content: text}
}

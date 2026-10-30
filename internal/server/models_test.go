package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"relay/internal/config"
	"relay/internal/proto"
)

// A whole backend against fake Ollama and Gemini servers: the keys saved through the
// API decide which models /api/models returns.
func TestModelsEndpointFollowsKeys(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"name":"granite4.1:3b"}]}`))
	}))
	defer ollama.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer okkey" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Write([]byte(`{"models":[{"name":"gpt-oss:120b"}]}`))
	}))
	defer cloud.Close()
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "" {
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]}]}`))
	}))
	defer gem.Close()

	cfg := &config.Config{
		Listen: "127.0.0.1:0", DefaultMode: "small",
		Providers: []config.Provider{
			{Name: "ollama", Kind: "openai", BaseURL: ollama.URL + "/v1"},
			{Name: "ollama-cloud", Kind: "openai", BaseURL: cloud.URL + "/v1", KeyFrom: "ollama", Discover: "ollama"},
			{Name: "gemini", Kind: "openai", BaseURL: gem.URL + "/v1beta/openai", Discover: "gemini", APIKeyEnv: []string{"RELAY_TEST_NOT_SET"}},
		},
		Models: []config.Model{{ID: "gemini-flash", Provider: "gemini", Name: "gemini-3.5-flash", Tools: true}},
		Modes:  map[string]config.Mode{"small": {Models: []string{"gemini-flash"}}},
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
	models := func(refresh bool) proto.ModelsResp {
		path := "/api/models"
		if refresh {
			path += "?refresh=1"
		}
		rec := call("GET", path, "")
		if rec.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		var r proto.ModelsResp
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	idsOf := func(r proto.ModelsResp) string {
		var out []string
		for _, m := range r.Models {
			out = append(out, m.ID)
		}
		return strings.Join(out, " ")
	}

	// no keys: only the local Ollama; the keyless providers are skipped, not errors
	r := models(true)
	if got := idsOf(r); got != "ollama/granite4.1:3b" {
		t.Fatalf("no keys: %q", got)
	}
	for _, p := range r.Providers {
		if p.Error != "" {
			t.Errorf("provider %s must not report an error without a key: %+v", p.Name, p)
		}
	}

	// a wrong Ollama key: the cloud error is reported, local models stay
	call("POST", "/api/keys", `{"provider":"ollama","key":"badkey"}`)
	r = models(false) // no refresh=1: adding a key must invalidate the cache by itself
	if got := idsOf(r); got != "ollama/granite4.1:3b" {
		t.Fatalf("bad key: %q", got)
	}
	var cloudErr string
	for _, p := range r.Providers {
		if p.Name == "ollama-cloud" {
			cloudErr = p.Error
		}
	}
	if !strings.Contains(cloudErr, "unauthorized") {
		t.Errorf("ollama-cloud error = %q", cloudErr)
	}

	// the right keys unlock Ollama cloud and Gemini
	call("POST", "/api/keys", `{"provider":"ollama","key":"okkey"}`)
	call("POST", "/api/keys", `{"provider":"gemini","key":"gkey"}`)
	if got := idsOf(models(false)); got != "ollama/granite4.1:3b ollama-cloud/gpt-oss:120b gemini/gemini-2.5-flash" {
		t.Fatalf("with keys: %q", got)
	}

	// keys cannot be saved under a provider that uses another one's keys
	if rec := call("POST", "/api/keys", `{"provider":"ollama-cloud","key":"x"}`); rec.Code != 400 {
		t.Errorf("saving a key for ollama-cloud: %d", rec.Code)
	}
	// ... and such a provider has no card on the keys page
	rec := call("GET", "/api/keys", "")
	if strings.Contains(rec.Body.String(), `"ollama-cloud"`) {
		t.Errorf("keys API must not list ollama-cloud: %s", rec.Body)
	}

	// a model found by the list can be selected for a session
	rec = call("POST", "/api/sessions", `{"mode":"small"}`)
	var si proto.SessionInfo
	json.Unmarshal(rec.Body.Bytes(), &si)
	for _, id := range []string{"ollama/granite4.1:3b", "gemini/gemini-2.5-flash", "ollama-cloud/gpt-oss:120b", "auto"} {
		rec = call("POST", "/api/sessions/"+si.ID+"/settings", `{"model":"`+id+`"}`)
		if rec.Code != 200 {
			t.Errorf("selecting %s: %d %s", id, rec.Code, rec.Body)
		}
	}
	if rec = call("POST", "/api/sessions/"+si.ID+"/settings", `{"model":"nope/x"}`); rec.Code != 400 {
		t.Errorf("unknown provider must be rejected: %d", rec.Code)
	}

	// the OpenAI-compatible gateway lists them too
	rec = call("GET", "/v1/models", "")
	for _, id := range []string{"gemini/gemini-2.5-flash", "ollama/granite4.1:3b", "gemini-flash", "relay-small"} {
		if !strings.Contains(rec.Body.String(), `"`+id+`"`) {
			t.Errorf("/v1/models lacks %s: %s", id, rec.Body)
		}
	}

	// status shows the chain that is really used
	rec = call("GET", "/api/status", "")
	var st proto.StatusResp
	json.Unmarshal(rec.Body.Bytes(), &st)
	if len(st.Modes) != 1 || len(st.Modes[0].Effective) == 0 {
		t.Errorf("status modes: %+v", st.Modes)
	}
	_ = filepath.Join
}

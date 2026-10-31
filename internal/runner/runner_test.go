package runner

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/config"
)

func TestLacksModel(t *testing.T) {
	c := config.Process{Name: "llamacpp", Command: "llama-server"}
	if !lacksModel(c, []string{"--port", "8080"}) {
		t.Error("llama-server without a model must be refused")
	}
	if lacksModel(c, []string{"--port", "8080", "-m", "a.gguf"}) {
		t.Error("-m is a model")
	}
	if lacksModel(c, []string{"-hf", "org/repo"}) {
		t.Error("-hf is a model")
	}
	if lacksModel(config.Process{Name: "ollama", Command: "ollama"}, nil) {
		t.Error("only llama-server needs a model")
	}
	if !lacksModel(config.Process{Command: `C:\bin\llama-server.exe`}, nil) {
		t.Error(".exe is recognised")
	}
}

func TestStopHintHasNoSystemctlOnWindowsText(t *testing.T) {
	h := stopHint("ollama")
	if h == "" {
		t.Fatal("empty hint")
	}
	if stopHint("other") == h || !strings.Contains(stopHint("other"), "other") {
		t.Error("other servers get a generic hint")
	}
}

func TestStartRefusesWithoutModelAndAttachesToRunning(t *testing.T) {
	m := NewManager([]config.Process{{Name: "llamacpp", Command: "llama-server"}})
	err := m.Start("llamacpp", nil)
	if err == nil || !strings.Contains(err.Error(), "/llamacpp") {
		t.Errorf("want a hint about /llamacpp, got %v", err)
	}

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	m = NewManager([]config.Process{{Name: "ollama", Command: "no-such-binary-xyz", ReadyURL: up.URL}})
	if err := m.Start("ollama", nil); !errors.Is(err, ErrAlready) {
		t.Errorf("a server that answers is already running: %v", err)
	}
	if err := m.Stop("ollama"); err == nil {
		t.Error("an external server is not stopped by Relay: expect the hint")
	}
	st := m.Status()
	if len(st) != 1 || st[0].State != "ready" || !st[0].External {
		t.Errorf("status: %+v", st)
	}

	m = NewManager([]config.Process{{Name: "x", Command: "no-such-binary-xyz"}})
	if err := m.Start("x", nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("missing binary: %v", err)
	}
}

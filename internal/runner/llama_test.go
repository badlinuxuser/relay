package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModelArgsPicksTheFlag(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "my model")
	_ = os.WriteFile(file, nil, 0o600)
	cases := map[string][]string{
		"/models/q4.gguf":              {"-m", "/models/q4.gguf"},
		`"/my models/q4.GGUF"`:         {"-m", "/my models/q4.GGUF"},
		"ggml-org/gemma-3-1b-it-GGUF":  {"-hf", "ggml-org/gemma-3-1b-it-GGUF"},
		"unsloth/Qwen3-8B-GGUF:Q4_K_M": {"-hf", "unsloth/Qwen3-8B-GGUF:Q4_K_M"},
		"https://x.org/m.bin":          {"-mu", "https://x.org/m.bin"},
		"-m /a.gguf -c 4096":           {"-m", "/a.gguf", "-c", "4096"},
		file:                           {"-m", file},
		"":                             nil,
		"hf.co/org/repo:Q4_K_M":        {"-hf", "org/repo:Q4_K_M"},
		"hf://org/repo":                {"-hf", "org/repo"},
		"https://huggingface.co/org/repo":           {"-hf", "org/repo"},
		"https://huggingface.co/org/repo/tree/main": {"-hf", "org/repo"},
		"https://huggingface.co/org/repo/blob/main/m.gguf": {"-mu", "https://huggingface.co/org/repo/resolve/main/m.gguf"},
		"/models/q4.gguf -c 8192 --port 8081": {"-m", "/models/q4.gguf", "-c", "8192", "--port", "8081"},
		`"/my models/q4.gguf" -c 4096`:       {"-m", "/my models/q4.gguf", "-c", "4096"},
		"org/repo -ngl 99":                   {"-hf", "org/repo", "-ngl", "99"},
	}
	for in, want := range cases {
		if got := ModelArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ModelArgs(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestModelArgsPicksTheMainFileOfAFolder(t *testing.T) {
	dir := t.TempDir()
	for name, size := range map[string]int{"small.gguf": 10, "big.gguf": 100, "mmproj-big.gguf": 500} {
		_ = os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600)
	}
	want := []string{"-m", filepath.Join(dir, "big.gguf")}
	if got := ModelArgs(dir); !reflect.DeepEqual(got, want) {
		t.Errorf("ModelArgs(folder) = %v, want %v", got, want)
	}
}

func TestLlamaArgsFitTheProgram(t *testing.T) {
	if got := serveArgs([]string{"--port", "8080"}); !reflect.DeepEqual(got, []string{"serve", "--port", "8080"}) {
		t.Errorf("serveArgs = %v", got)
	}
	if got := serveArgs([]string{"serve", "-m", "a.gguf"}); !reflect.DeepEqual(got, []string{"serve", "-m", "a.gguf"}) {
		t.Errorf("serveArgs kept = %v", got)
	}
	if got := stripServe([]string{"serve", "--port", "8080"}); !reflect.DeepEqual(got, []string{"--port", "8080"}) {
		t.Errorf("stripServe = %v", got)
	}
}

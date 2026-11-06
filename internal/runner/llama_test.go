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
	}
	for in, want := range cases {
		if got := ModelArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ModelArgs(%q) = %v, want %v", in, got, want)
		}
	}
}

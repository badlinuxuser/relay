package runner

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// hfRepo matches Hugging Face names: "org/repo" or "org/repo:Q4_K_M".
var hfRepo = regexp.MustCompile(`^[A-Za-z0-9][\w.-]*/[\w.-]+(:[\w.-]+)?$`)

// ModelArgs turns what the user typed for llama.cpp into arguments:
//   - a path to a file (existing, or ending in .gguf)  -> -m <path>
//   - org/repo or org/repo:quant                       -> -hf <name>
//   - http(s)://...                                    -> -mu <url>
//   - anything that starts with "-"                    -> used as typed (flags)
//
// The path may contain spaces and quotes, and may start with ~.
func ModelArgs(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "-") {
		return strings.Fields(s)
	}
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	if s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			s = home + s[1:]
		}
	}
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
		return []string{"-mu", s}
	case strings.HasSuffix(low, ".gguf"):
		return []string{"-m", s}
	}
	if _, err := os.Stat(s); err == nil {
		return []string{"-m", s}
	}
	if hfRepo.MatchString(s) && !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "../") {
		return []string{"-hf", s}
	}
	return []string{"-m", s}
}

// llamaNames are the names the llama.cpp server goes by in different packages.
var llamaNames = []string{"llama-server", "llama-cpp-server", "llama.cpp-server", "llama_server", "llamacpp-server", "llama-server.exe"}

// resolveCommand finds the executable to run. The configured name is tried first; for
// llama.cpp the other known names are tried too.
func resolveCommand(name, command string) (string, bool) {
	if _, err := exec.LookPath(command); err == nil {
		return command, true
	}
	if name == "llamacpp" {
		for _, n := range llamaNames {
			if _, err := exec.LookPath(n); err == nil {
				return n, true
			}
		}
	}
	return command, false
}

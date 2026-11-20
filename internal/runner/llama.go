package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// hfRepo matches Hugging Face names: "org/repo" or "org/repo:Q4_K_M".
var hfRepo = regexp.MustCompile(`^[A-Za-z0-9][\w.-]*/[\w.-]+(:[\w.-]+)?$`)

// trailingFlags finds where the flags begin after a model: " -c 4096", " --port 8081".
var trailingFlags = regexp.MustCompile(`\s--?[A-Za-z]`)

// hfPage matches a Hugging Face model page: https://huggingface.co/org/repo[/tree/main].
var hfPage = regexp.MustCompile(`^https?://(?:www\.)?(?:huggingface\.co|hf\.co)/([A-Za-z0-9][\w.-]*/[\w.-]+)(?::([\w.-]+))?(?:/(?:tree|blob|resolve)/[^/]+/?)?$`)

// ModelArgs turns what the user typed for llama.cpp into arguments:
//   - a path to a file (existing, or ending in .gguf)  -> -m <path>
//   - a folder with .gguf files                        -> -m <the main file of it>
//   - org/repo or org/repo:quant                       -> -hf <name>
//     (also hf.co/org/repo, hf://org/repo and a huggingface.co page address)
//   - http(s)://...                                    -> -mu <url> (a huggingface "blob" link is turned into "resolve")
//   - anything that starts with "-"                    -> used as typed (flags)
//   - a model followed by flags ("model.gguf -c 8192") -> the model's flag, then the flags
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
	model, flags := splitModelAndFlags(s)
	out := modelFlag(model)
	return append(out, flags...)
}

// splitModelAndFlags separates the model from the flags typed after it.
func splitModelAndFlags(s string) (string, []string) {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			return s[1 : end+1], strings.Fields(s[end+2:])
		}
	}
	if loc := trailingFlags.FindStringIndex(s); loc != nil && loc[0] > 0 {
		return strings.TrimSpace(s[:loc[0]]), strings.Fields(s[loc[0]:])
	}
	return s, nil
}

// modelFlag chooses -m, -hf or -mu for one model reference.
func modelFlag(s string) []string {
	s = strings.TrimSpace(s)
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
	case strings.HasPrefix(low, "hf://"):
		return []string{"-hf", s[len("hf://"):]}
	case strings.HasPrefix(low, "hf.co/"):
		return []string{"-hf", s[len("hf.co/"):]}
	case strings.HasPrefix(low, "huggingface.co/"):
		return []string{"-hf", s[len("huggingface.co/"):]}
	case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
		if strings.HasSuffix(low, ".gguf") {
			return []string{"-mu", strings.Replace(s, "/blob/", "/resolve/", 1)}
		}
		if m := hfPage.FindStringSubmatch(s); m != nil {
			repo := m[1]
			if m[2] != "" {
				repo += ":" + m[2]
			}
			return []string{"-hf", repo}
		}
		return []string{"-mu", s}
	case strings.HasSuffix(low, ".gguf"):
		return []string{"-m", s}
	}
	if st, err := os.Stat(s); err == nil {
		if st.IsDir() {
			if f := mainGGUF(s); f != "" {
				return []string{"-m", f}
			}
		}
		return []string{"-m", s}
	}
	if hfRepo.MatchString(s) && !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "../") {
		return []string{"-hf", s}
	}
	return []string{"-m", s}
}

// mainGGUF picks the model file of a folder: the first part of a split model, else the
// largest .gguf file; projector files (mmproj) are never the main model.
func mainGGUF(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type cand struct {
		path string
		size int64
	}
	var all []cand
	for _, e := range entries {
		name := e.Name()
		low := strings.ToLower(name)
		if e.IsDir() || !strings.HasSuffix(low, ".gguf") || strings.Contains(low, "mmproj") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if strings.Contains(low, "-00001-of-") { // llama.cpp finds the other parts itself
			return filepath.Join(dir, name)
		}
		if strings.Contains(low, "-of-") && !strings.Contains(low, "-00001-of-") {
			continue
		}
		all = append(all, cand{filepath.Join(dir, name), info.Size()})
	}
	if len(all) == 0 {
		return ""
	}
	sort.Slice(all, func(i, j int) bool { return all[i].size > all[j].size })
	return all[0].path
}

// llamaNames are the names of the older separate llama.cpp server binary in different packages.
var llamaNames = []string{"llama-server", "llama-cpp-server", "llama.cpp-server", "llama_server", "llamacpp-server", "llama-server.exe"}

func baseName(command string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(command, "\\", "/"))), ".exe")
}

// serveArgs makes the arguments of the `llama` binary start with the "serve" command.
func serveArgs(args []string) []string {
	if len(args) > 0 && args[0] == "serve" {
		return args
	}
	return append([]string{"serve"}, args...)
}

// stripServe removes the "serve" command: the older llama-server binary does not take it.
func stripServe(args []string) []string {
	if len(args) > 0 && args[0] == "serve" {
		return args[1:]
	}
	return args
}

// llamaSupportsServe reports whether `llama serve` exists on this computer.
func llamaSupportsServe(command string) bool {
	if _, err := exec.LookPath(command); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, command, "serve", "--help").Run() == nil
}

// llamaLaunch decides what to run for llama.cpp. `llama serve` is the way to start it now;
// a configured older llama-server binary is used when it is there, and `llama serve` is the
// fallback when it is not. The returned arguments fit the chosen program.
func llamaLaunch(command string, args []string) (string, []string, bool) {
	if baseName(command) == "llama" {
		if _, err := exec.LookPath(command); err == nil {
			return command, serveArgs(args), true
		}
	} else if _, err := exec.LookPath(command); err == nil {
		return command, stripServe(args), true
	}
	for _, n := range llamaNames {
		if _, err := exec.LookPath(n); err == nil {
			return n, stripServe(args), true
		}
	}
	if llamaSupportsServe("llama") {
		return "llama", serveArgs(args), true
	}
	return command, args, false
}

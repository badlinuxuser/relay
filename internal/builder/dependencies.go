package builder

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Package specifications are intentionally restricted to PyPI names and version constraints.
// Direct URLs, VCS references, local paths, pip options and editable installs are not accepted.
var dependencySpec = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9_,.-]+\])?([<>=!~]{1,2}[A-Za-z0-9.*+!_-]+(,[<>=!~]{1,2}[A-Za-z0-9.*+!_-]+)*)?$`)

func validateDependencies(deps []string) error {
	seen := map[string]bool{}
	for _, raw := range deps {
		d := strings.TrimSpace(raw)
		if d == "" || len(d) > 160 || strings.HasPrefix(d, "-") || !dependencySpec.MatchString(d) {
			return fmt.Errorf("invalid dependency %q: use a PyPI package name with an optional version constraint, not a URL, path or pip option", raw)
		}
		base := d
		if i := strings.IndexAny(d, "<>=!~["); i >= 0 {
			base = d[:i]
		}
		key := strings.ToLower(base)
		if seen[key] {
			return fmt.Errorf("dependency %q is listed more than once", key)
		}
		seen[key] = true
	}
	return nil
}

func normalizeDependencies(deps []string) []string {
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		out = append(out, strings.TrimSpace(d))
	}
	return out
}

func venvPythonPath(venv string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", "python.exe")
	}
	return filepath.Join(venv, "bin", "python")
}

// createPythonEnv installs dependencies into a server-private virtual environment. The caller
// has already obtained user approval for running the server project and its dependencies.
func createPythonEnv(ctx context.Context, basePython, venv string, deps []string) (string, error) {
	if err := validateDependencies(deps); err != nil {
		return "", err
	}
	createCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(createCtx, basePython, "-m", "venv", venv)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating virtual environment: %v: %s", err, lastLines(string(out), 12))
	}
	py := venvPythonPath(venv)
	args := []string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input"}
	args = append(args, normalizeDependencies(deps)...)
	installCtx, installCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer installCancel()
	cmd = exec.CommandContext(installCtx, py, args...)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("installing %s: %v: %s", strings.Join(deps, ", "), err, lastLines(string(out), 20))
	}
	return py, nil
}

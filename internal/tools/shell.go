package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"relay/internal/provider"
)

// ---- run_shell ---------------------------------------------------------------

type runShell struct{ ws string }

func (t *runShell) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "run_shell",
		Description: "Run a shell command in the workspace (sh -c on Linux/macOS, cmd /C on Windows). Returns combined stdout/stderr and the exit code.",
		Schema: schema(map[string]any{
			"command":     prop("string", "Command line to execute"),
			"timeout_sec": prop("integer", "Timeout in seconds (default 60, max 600)"),
		}, "command"),
	}
}
func (t *runShell) NeedsApproval() bool { return true }
func (t *runShell) Describe(a json.RawMessage) string {
	var v struct {
		Command string `json:"command"`
	}
	_ = parse(a, &v)
	return "$ " + v.Command
}
func (t *runShell) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	if strings.TrimSpace(v.Command) == "" {
		return "", fmt.Errorf("command is empty")
	}
	timeout := time.Duration(v.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 600*time.Second {
		timeout = 600 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/C", v.Command)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", v.Command)
	}
	cmd.Dir = t.ws
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := truncate(buf.String(), 30000)
	switch {
	case cctx.Err() == context.DeadlineExceeded:
		return out + fmt.Sprintf("\n[timed out after %s]", timeout), fmt.Errorf("timed out")
	case err != nil:
		if ee, ok := err.(*exec.ExitError); ok {
			return out + fmt.Sprintf("\n[exit code %d]", ee.ExitCode()), fmt.Errorf("exit code %d", ee.ExitCode())
		}
		return out, err
	}
	if out == "" {
		out = "(no output)"
	}
	return out + "\n[exit code 0]", nil
}

// ---- web_fetch ---------------------------------------------------------------

type webFetch struct{}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLines  = regexp.MustCompile(`\n{3,}`)
)

func (t *webFetch) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "web_fetch",
		Description: "Fetch a web page (http/https GET) and return its text content, HTML tags removed.",
		Schema:      schema(map[string]any{"url": prop("string", "Full URL")}, "url"),
	}
}
func (t *webFetch) NeedsApproval() bool { return false }
func (t *webFetch) Describe(a json.RawMessage) string {
	var v struct {
		URL string `json:"url"`
	}
	_ = parse(a, &v)
	return "fetch " + v.URL
}
func (t *webFetch) Run(ctx context.Context, a json.RawMessage) (string, error) {
	var v struct {
		URL string `json:"url"`
	}
	if err := parse(a, &v); err != nil {
		return "", err
	}
	if !strings.HasPrefix(v.URL, "http://") && !strings.HasPrefix(v.URL, "https://") {
		return "", fmt.Errorf("only http and https URLs are allowed")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, v.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "relay/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	text := string(body)
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		text = reScript.ReplaceAllString(text, " ")
		text = reTag.ReplaceAllString(text, " ")
		text = reSpace.ReplaceAllString(text, " ")
		text = reLines.ReplaceAllString(text, "\n\n")
	}
	text = strings.TrimSpace(text)
	if resp.StatusCode >= 400 {
		return truncate(text, 2000), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return truncate(text, 20000), nil
}

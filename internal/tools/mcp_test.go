package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"relay/internal/mcp"
)

func TestMCPToolNamesAreValidUniqueAndShort(t *testing.T) {
	used := map[string]bool{}
	a := mcpToolName("files", "read file", used) // a space is not allowed by providers
	if a != "mcp__files__read_file" {
		t.Fatalf("a=%q", a)
	}
	b := mcpToolName("files", "read_file", used) // the same name once cleaned
	if b == a || !strings.HasPrefix(b, "mcp__files__read_file") {
		t.Fatalf("b=%q must differ from %q", b, a)
	}
	long := mcpToolName("server", strings.Repeat("x", 100), used)
	long2 := mcpToolName("server", strings.Repeat("x", 99)+"y", used)
	if len(long) > 64 || len(long2) > 64 || long == long2 {
		t.Fatalf("long=%q long2=%q", long, long2)
	}
}

func TestMCPCallsRequireApprovalUnlessAllowedForSession(t *testing.T) {
	for _, name := range []string{"send_message", "read_messages", "sign_in"} {
		t.Run(name, func(t *testing.T) {
			tl := &mcpTool{ref: mcp.ToolRef{Server: "telegram", Name: name}}
			if !tl.NeedsApproval() {
				t.Fatal("every MCP call, including sign-in, must require approval")
			}
			if tl.OnceOnly() {
				t.Fatal("MCP approval may be remembered per tool for the session")
			}
			if !tl.HideRawArgs() {
				t.Fatal("raw MCP arguments must not be streamed to the UI")
			}
		})
	}
}

func TestMCPApprovalPreviewShowsActionAndRedactsCredentials(t *testing.T) {
	tl := &mcpTool{ref: mcp.ToolRef{Server: "telegram", Name: "send_message"}}
	got := tl.Describe(json.RawMessage(`{"chat_id":"123","message":"hello","access_token":"do-not-show","nested":{"api_key":"also-secret"}}`))
	for _, want := range []string{"telegram/send_message", "123", "hello", "[redacted]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("approval preview %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "do-not-show") || strings.Contains(got, "also-secret") {
		t.Fatalf("approval preview leaked credentials: %s", got)
	}
}

package tools

import (
	"strings"
	"testing"
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

package mcp

import (
	"strings"
	"testing"
)

func TestParseReadsBothKeysAndGuessesTheType(t *testing.T) {
	f, err := Parse([]byte(`{
		"servers":    {"files": {"command": "npx", "args": ["-y", "pkg"]}, "web": {"url": "https://example.com/mcp"}},
		"mcpServers": {"old": {"command": "x"}, "files": {"command": "ignored"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Servers["files"].Type != TypeStdio || f.Servers["files"].Command != "npx" {
		t.Fatalf("files: %+v", f.Servers["files"])
	}
	if f.Servers["web"].Type != TypeHTTP || f.Servers["old"].Type != TypeStdio {
		t.Fatalf("types: %+v", f.Servers)
	}
}

func TestParseRejectsTheWholeFileOnOneBadEntry(t *testing.T) {
	for name, src := range map[string]string{
		"not json":     `{"servers": `,
		"no target":    `{"servers": {"a": {"name": "A"}}}`,
		"bad id":       `{"servers": {"bad id!": {"command": "x"}}}`,
		"bad url":      `{"servers": {"a": {"url": "ftp://x"}}}`,
		"creds in url": `{"servers": {"a": {"url": "https://u:p@x/mcp"}}}`,
		"unknown type": `{"servers": {"a": {"type": "carrier-pigeon", "command": "x"}}}`,
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if f, err := Parse([]byte("  \n")); err != nil || len(f.Servers) != 0 {
		t.Errorf("an empty file is an empty configuration: %v %v", f, err)
	}
}

func TestFingerprintChangesWithTheCommandNotTheName(t *testing.T) {
	a := Normalize(ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}, Name: "A"})
	b := a
	b.Name, b.DisabledTools = "B", []string{"x"}
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("name and disabled_tools must not change the fingerprint")
	}
	b.Args = []string{"-y", "evil"}
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("other arguments must change the fingerprint")
	}
}

func TestBuildEnvKeepsSecretsOutButExpandsReferences(t *testing.T) {
	t.Setenv("RELAY_TEST_SECRET", "s3cret")
	t.Setenv("RELAY_TEST_TOKEN", "abc")
	env := strings.Join(buildEnv(ServerConfig{Env: map[string]string{"TOKEN": "${RELAY_TEST_TOKEN}"}}), "\n")
	if strings.Contains(env, "s3cret") {
		t.Fatal("the environment of Relay must not be passed on")
	}
	if !strings.Contains(env, "TOKEN=abc") {
		t.Fatalf("env=%q", env)
	}
	if !strings.Contains(env, "PATH=") {
		t.Fatal("PATH is needed to find programs")
	}
	all := strings.Join(buildEnv(ServerConfig{InheritEnv: true}), "\n")
	if !strings.Contains(all, "RELAY_TEST_SECRET=s3cret") {
		t.Fatal("old [[mcp]] servers keep the full environment")
	}
}

func TestParseResourceMetadata(t *testing.T) {
	h := `Bearer realm="mcp", resource_metadata="https://x.example/.well-known/oauth-protected-resource"`
	if got := parseResourceMetadata(h); got != "https://x.example/.well-known/oauth-protected-resource" {
		t.Fatalf("got %q", got)
	}
	if parseResourceMetadata(`Bearer realm="mcp"`) != "" {
		t.Fatal("no metadata: empty")
	}
}

func TestWarningForPlainHTTP(t *testing.T) {
	if Warning(Normalize(ServerConfig{URL: "http://192.168.1.5/mcp"})) == "" {
		t.Fatal("plain http to another machine deserves a warning")
	}
	if Warning(Normalize(ServerConfig{URL: "http://localhost:3000/mcp"})) != "" || Warning(Normalize(ServerConfig{URL: "https://x/mcp"})) != "" {
		t.Fatal("localhost and https are fine")
	}
}

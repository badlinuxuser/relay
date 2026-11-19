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

func TestAServerCanOpenWindowsOnTheUsersDesktop(t *testing.T) {
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	t.Setenv("RELAY_TEST_SECRET", "s3cret")
	env := strings.Join(buildEnv(ServerConfig{}), "\n")
	for _, want := range []string{"DISPLAY=:0", "WAYLAND_DISPLAY=wayland-0", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus"} {
		if !strings.Contains(env, want) {
			t.Errorf("a program that opens the browser or a window needs %s", want)
		}
	}
	if strings.Contains(env, "s3cret") {
		t.Error("but not the secrets")
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

func TestOAuthDynamicRegistrationAndResourceAreAccepted(t *testing.T) {
	f, err := Parse([]byte(`{"servers":{"remote":{"url":"https://mcp.example/mcp","oauth":{"authorize_url":"https://auth.example/authorize","token_url":"https://auth.example/token","registration_url":"https://auth.example/register","resource":"https://mcp.example/mcp","pkce_s256_supported":true}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	o := f.Servers["remote"].OAuth
	if o == nil || o.RegistrationURL != "https://auth.example/register" || o.Resource != "https://mcp.example/mcp" {
		t.Fatalf("OAuth config was not preserved: %+v", o)
	}
}

func TestOAuthProviderProfileFillsEndpoints(t *testing.T) {
	f, err := Parse([]byte(`{"servers":{"github":{"type":"http","url":"https://api.example/mcp","oauth":{"provider":"github","client_id":"client-id","client_secret":"${GITHUB_SECRET}","scopes":["read:user"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	c := staticOAuth(f.Servers["github"])
	if c == nil || c.AuthorizeURL != "https://github.com/login/oauth/authorize" || c.TokenURL != "https://github.com/login/oauth/access_token" {
		t.Fatalf("profile was not applied: %#v", c)
	}
}

func TestOAuthRejectsUnknownProviderProfile(t *testing.T) {
	_, err := Parse([]byte(`{"servers":{"bad":{"type":"http","url":"https://api.example/mcp","oauth":{"provider":"made-up","client_id":"id"}}}}`))
	if err == nil || !strings.Contains(err.Error(), "unknown OAuth provider") {
		t.Fatalf("err=%v", err)
	}
}

func TestStdioOAuthRequiresResourceWhenSendingResource(t *testing.T) {
	_, err := Parse([]byte(`{"servers":{"bad":{"type":"stdio","command":"python","oauth":{"authorize_url":"https://auth.example/a","token_url":"https://auth.example/t","client_id":"id","send_resource":true,"pkce_s256_supported":true}}}}`))
	if err == nil || !strings.Contains(err.Error(), "stdio OAuth requires oauth.resource") {
		t.Fatalf("err=%v", err)
	}
}

func TestCustomOAuthRequiresPKCES256Declaration(t *testing.T) {
	_, err := Parse([]byte(`{"servers":{"custom":{"type":"stdio","command":"python","oauth":{"authorize_url":"https://auth.example/a","token_url":"https://auth.example/t","client_id":"id","resource":"https://api.example/"}}}}`))
	if err == nil || !strings.Contains(err.Error(), "pkce_s256_supported=true") {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderEndpointOverrideRequiresExplicitPKCEAttestation(t *testing.T) {
	_, err := Parse([]byte(`{"servers":{"bad":{"type":"http","url":"https://api.example/mcp","oauth":{"provider":"github","authorize_url":"https://auth.example/authorize","client_id":"id","client_secret":"${SECRET}"}}}}`))
	if err == nil || !strings.Contains(err.Error(), "overriding a built-in provider endpoint requires explicit pkce_s256_supported=true") {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPResourceMustCoverConfiguredEndpoint(t *testing.T) {
	_, err := Parse([]byte(`{"servers":{"bad":{"type":"http","url":"https://api.example/mcp","oauth":{"authorize_url":"https://auth.example/a","token_url":"https://auth.example/t","client_id":"id","resource":"https://evil.example/mcp","pkce_s256_supported":true}}}}`))
	if err == nil || !strings.Contains(err.Error(), "oauth.resource must identify the configured MCP endpoint") {
		t.Fatalf("err=%v", err)
	}
}

func TestStdioOAuthRequiresResourceBinding(t *testing.T) {
	_, err := Parse([]byte(`{"servers":{"bad":{"type":"stdio","command":"python","oauth":{"authorize_url":"https://auth.example/a","token_url":"https://auth.example/t","client_id":"id","pkce_s256_supported":true}}}}`))
	if err == nil || !strings.Contains(err.Error(), "stdio OAuth requires oauth.resource") {
		t.Fatalf("err=%v", err)
	}
}

func TestDynamicRegistrationMayObtainConfidentialClientSecret(t *testing.T) {
	_, err := Parse([]byte(`{"servers":{"dynamic":{"type":"http","url":"https://mcp.example/mcp","oauth":{"authorize_url":"https://auth.example/a","token_url":"https://auth.example/t","registration_url":"https://auth.example/register","token_endpoint_auth_method":"client_secret_basic","pkce_s256_supported":true}}}}`))
	if err != nil {
		t.Fatalf("dynamic registration must be allowed to issue its client secret: %v", err)
	}
}

func TestOAuthClientCanBeConfiguredLaterInConnectionsUI(t *testing.T) {
	f, err := Parse([]byte(`{"servers":{"github":{"type":"http","url":"https://api.example/mcp","oauth":{"provider":"github","scopes":["read:user"]}}}}`))
	if err != nil {
		t.Fatalf("OAuth connector without a client ID should load for setup in Connections UI: %v", err)
	}
	if f.Servers["github"].OAuth == nil {
		t.Fatal("OAuth config was lost")
	}
}

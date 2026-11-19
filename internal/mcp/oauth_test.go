package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relay/internal/oauth"
)

// service is one test server that plays three roles: an OAuth service, an MCP server over
// HTTP that wants a bearer token, and the metadata that leads from one to the other.
type service struct {
	srv       *httptest.Server
	challenge string
}

func newService(t *testing.T) *service {
	sv := &service{}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+sv.srv.URL+`/.well-known/oauth-protected-resource/mcp"`)
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var msg rpcMsg
		_ = json.NewDecoder(r.Body).Decode(&msg)
		w.Header().Set("Content-Type", "application/json")
		switch msg.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"remote"}}}`, msg.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"d","inputSchema":{"type":"object"}}]}}`, msg.ID)
		case "tools/call":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"remote pong"}]}}`, msg.ID)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"resource":"%s/mcp","authorization_servers":["%s"]}`, sv.srv.URL, sv.srv.URL)
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"authorization_endpoint":"%[1]s/authorize","token_endpoint":"%[1]s/token","registration_endpoint":"%[1]s/register","code_challenge_methods_supported":["S256"],"resource_parameter_supported":true}`, sv.srv.URL)
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"client_id":"dyn"}`) })
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != sv.challenge {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"tok-1","token_type":"bearer","expires_in":3600}`)
	})
	sv.srv = httptest.NewServer(mux)
	t.Cleanup(sv.srv.Close)
	return sv
}

// finishSignIn plays the browser: it takes the address from Authorize and calls the callback.
func (sv *service) finishSignIn(t *testing.T, b *oauth.Broker, authURL string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	sv.challenge = u.Query().Get("code_challenge")
	rec := httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("GET", "/oauth/callback?code=good-code&state="+u.Query().Get("state"), nil))
	if rec.Code != 200 {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
}

func newOAuthManager(t *testing.T, servers map[string]ServerConfig) (*Manager, *oauth.Broker, string) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := Save(path, &File{Servers: servers}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path, filepath.Join(dir, "trust.json"), nil)
	b := oauth.NewBroker(oauth.NewStore(filepath.Join(dir, "auth")))
	m.SetOAuth(b, func() string { return "http://127.0.0.1:8787/oauth/callback" })
	t.Cleanup(m.Close)
	return m, b, dir
}

func TestRemoteServerSignInByDiscovery(t *testing.T) {
	sv := newService(t)
	m, b, _ := newOAuthManager(t, map[string]ServerConfig{"remote": Normalize(ServerConfig{URL: sv.srv.URL + "/mcp"})})
	m.Reload()

	st := waitState(t, m, "remote", StateAuthRequired)
	if st.Kind != KindConnector {
		t.Fatalf("a remote server is a connector: %+v", st)
	}
	// the model gets one tool that starts the sign-in
	refs := m.Tools()
	if len(refs) != 1 || refs[0].Name != SignInTool {
		t.Fatalf("tools: %+v", refs)
	}
	var need *AuthRequiredError
	if _, err := m.CallTool(context.Background(), "remote", SignInTool, nil); !errors.As(err, &need) || !need.SafeToRetry {
		t.Fatalf("sign_in before the sign-in must ask for a safe retry: %v", err)
	}

	if _, err := m.AuthorizationDescription(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	authURL, wait, err := m.Authorize(context.Background(), "remote")
	if err != nil {
		t.Fatal(err)
	}
	sv.finishSignIn(t, b, authURL)
	if err := wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "remote", StateConnected) // the manager reconnects by itself
	if got := m.Tools(); len(got) != 1 || got[0].Name != "echo" {
		t.Fatalf("tools after the sign-in: %+v", got)
	}
	out, err := m.CallTool(context.Background(), "remote", "echo", nil)
	if err != nil || out != "remote pong" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestConnectorWithOAuthBlockAsksBeforeTheCallAndGetsTheTokenFile(t *testing.T) {
	sv := newService(t)
	cfg := fakeStdioConfig()
	cfg.OAuth = &OAuthConfig{AuthorizeURL: sv.srv.URL + "/authorize", TokenURL: sv.srv.URL + "/token", ClientID: "cid", PKCES256Supported: true, Resource: "https://api.example/", Scopes: []string{"read"}}
	m, b, dir := newOAuthManager(t, map[string]ServerConfig{"conn": Normalize(cfg)})
	m.Reload()
	if err := m.Trust("conn"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "conn", StateConnected)
	if st := m.Status()[0]; st.Kind != KindConnector || !st.OAuth || st.SignedIn {
		t.Fatalf("status: %+v", st)
	}
	if !m.NeedsSignIn("conn") {
		t.Fatal("not signed in yet")
	}

	// the program is running, its tools are visible, but a call needs the sign-in first
	var need *AuthRequiredError
	if _, err := m.CallTool(context.Background(), "conn", "echo", nil); !errors.As(err, &need) || need.Server != "conn" || !need.SafeToRetry {
		t.Fatalf("err=%v", err)
	}

	if _, err := m.AuthorizationDescription(context.Background(), "conn"); err != nil {
		t.Fatal(err)
	}
	authURL, wait, err := m.Authorize(context.Background(), "conn")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authURL, "scope=read") || !strings.Contains(authURL, "client_id=cid") {
		t.Fatalf("authURL=%s", authURL)
	}
	sv.finishSignIn(t, b, authURL)
	if err := wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, err := m.CallTool(context.Background(), "conn", "echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "auth", "conn.token")
	if !strings.HasSuffix(out, tokenFile) {
		t.Fatalf("the program must be told where its token file is: %q (want …%s)", out, tokenFile)
	}
	if data, _ := os.ReadFile(tokenFile); !strings.Contains(string(data), "tok-1") || strings.Contains(string(data), "refresh") {
		t.Fatalf("token file: %s", data)
	}
	if m.NeedsSignIn("conn") {
		t.Fatal("signed in now")
	}
}

func TestConnectorSaysTheServiceRefusedTheToken(t *testing.T) {
	cfg := fakeStdioConfig()
	cfg.OAuth = &OAuthConfig{AuthorizeURL: "https://a.example/auth", TokenURL: "https://a.example/token", ClientID: "cid", PKCES256Supported: true, Resource: "https://api.example/"}
	m, b, _ := newOAuthManager(t, map[string]ServerConfig{"conn": Normalize(cfg)})
	if err := b.Store().Save("conn", &oauth.Record{Token: &oauth.Token{AccessToken: "revoked", Expiry: time.Now().Add(time.Hour)}, Resource: "https://api.example/", TargetURL: "stdio://conn", Config: staticOAuth(cfg)}); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	if err := m.Trust("conn"); err != nil {
		t.Fatal(err)
	}
	if m.NeedsSignIn("conn") {
		t.Fatal("a token is there")
	}
	// the program reports that the service refused the token: Relay drops it and asks again
	var need *AuthRequiredError
	if _, err := m.CallTool(context.Background(), "conn", "revoked", nil); !errors.As(err, &need) {
		t.Fatalf("err=%v", err)
	}
	if !m.NeedsSignIn("conn") {
		t.Fatal("the refused token must be gone")
	}
}

func TestKindDefaultsAndOAuthValidation(t *testing.T) {
	if k := Normalize(ServerConfig{Command: "x"}).Kind; k != KindTool {
		t.Errorf("a local program is a tool, got %q", k)
	}
	if k := Normalize(ServerConfig{URL: "https://x/mcp"}).Kind; k != KindConnector {
		t.Errorf("a remote server is a connector, got %q", k)
	}
	if k := Normalize(ServerConfig{Command: "x", OAuth: &OAuthConfig{}}).Kind; k != KindConnector {
		t.Errorf("a program with OAuth is a connector, got %q", k)
	}
	good := Normalize(ServerConfig{Command: "x", OAuth: &OAuthConfig{AuthorizeURL: "https://a/auth", TokenURL: "https://a/token", ClientID: "c", PKCES256Supported: true, Resource: "https://api.example/"}})
	if err := Validate("a", good); err != nil {
		t.Errorf("good oauth block: %v", err)
	}
	for name, o := range map[string]*OAuthConfig{
		"no client":  {AuthorizeURL: "https://a/auth", TokenURL: "https://a/token"},
		"http url":   {AuthorizeURL: "http://a.example/auth", TokenURL: "https://a/token", ClientID: "c"},
		"no token":   {AuthorizeURL: "https://a/auth", ClientID: "c"},
		"bad scheme": {AuthorizeURL: "javascript:x", TokenURL: "https://a/token", ClientID: "c"},
	} {
		if err := Validate("a", Normalize(ServerConfig{Command: "x", OAuth: o})); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if err := Validate("a", Normalize(ServerConfig{Command: "x", Kind: "robot"})); err == nil {
		t.Error("an unknown kind is an error")
	}
	if Warning(Normalize(ServerConfig{Command: "x", OAuth: &OAuthConfig{ClientSecret: "plain", ClientID: "c", AuthorizeURL: "https://a/a", TokenURL: "https://a/t", PKCES256Supported: true, Resource: "https://api.example/"}})) == "" {
		t.Error("a plain client_secret deserves a warning")
	}
}

func TestAuthorizationDescriptionPrecedesClientRegistration(t *testing.T) {
	sv := newService(t)
	cfg := fakeStdioConfig()
	cfg.OAuth = &OAuthConfig{AuthorizeURL: sv.srv.URL + "/authorize", TokenURL: sv.srv.URL + "/token", RegistrationURL: sv.srv.URL + "/register", PKCES256Supported: true, Resource: "https://api.example/", Scopes: []string{"read"}}
	m, _, _ := newOAuthManager(t, map[string]ServerConfig{"connector": Normalize(cfg)})
	m.Reload()
	if _, _, err := m.Authorize(context.Background(), "connector"); err == nil || !strings.Contains(err.Error(), "consent description") {
		t.Fatalf("authorization started without a prepared consent description: %v", err)
	}
	desc, err := m.AuthorizationDescription(context.Background(), "connector")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(desc, "read") || !strings.Contains(desc, "Resource:") {
		t.Fatalf("incomplete consent description: %s", desc)
	}
	// Client registration and authorization URL creation happen only after the caller
	// obtains this description and asks Authorize (the consent approval boundary).
	if sv.challenge != "" {
		t.Fatal("description must not start authorization or code exchange")
	}
	authURL, wait, err := m.Authorize(context.Background(), "connector")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if u.Query().Get("client_id") != "dyn" {
		t.Fatalf("dynamic client was not registered after consent: %s", authURL)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx); err == nil {
		t.Fatal("cancelled authorization wait unexpectedly succeeded")
	}
}

func TestNeedsSignInWhenStdioResourceBindingChanges(t *testing.T) {
	cfg := fakeStdioConfig()
	cfg.OAuth = &OAuthConfig{AuthorizeURL: "https://auth.example/a", TokenURL: "https://auth.example/t", ClientID: "id", PKCES256Supported: true, Resource: "https://api.example/new/"}
	m, b, _ := newOAuthManager(t, map[string]ServerConfig{"conn": Normalize(cfg)})
	old := &oauth.Config{AuthorizeURL: "https://auth.example/a", TokenURL: "https://auth.example/t", ClientID: "id", PKCES256Supported: true, Resource: "https://api.example/old/"}
	if err := b.Store().Save("conn", &oauth.Record{Token: &oauth.Token{AccessToken: "old", Expiry: time.Now().Add(time.Hour)}, Resource: old.Resource, TargetURL: "stdio://conn", Config: old}); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	if !m.NeedsSignIn("conn") {
		t.Fatal("resource change must require reauthorization")
	}
}

func TestHTTPMCPAlwaysSendsResourceIndicator(t *testing.T) {
	cfg := Normalize(ServerConfig{Type: TypeHTTP, URL: "https://mcp.example/mcp", OAuth: &OAuthConfig{AuthorizeURL: "https://auth.example/a", TokenURL: "https://auth.example/t", ClientID: "id", PKCES256Supported: true}})
	oc := staticOAuth(cfg)
	if !oc.SendResource || oc.Resource != cfg.URL {
		t.Fatalf("HTTP MCP must always send its canonical resource: %#v", oc)
	}
}

func TestInitialHTTP401ConnectBecomesStructuredAuthRequired(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+srv.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	m, _, _ := newOAuthManager(t, map[string]ServerConfig{"remote": {Type: TypeHTTP, URL: srv.URL + "/mcp"}})
	m.Reload()
	_, err := m.CallTool(context.Background(), "remote", "protected", nil)
	var need *AuthRequiredError
	if !errors.As(err, &need) || !need.SafeToRetry {
		t.Fatalf("expected a safe structured auth-required result, got %v", err)
	}
}

func TestSaveClientCredentialsStoresSecretInEncryptedVault(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "mcp.json"), "", nil)
	vault := oauth.NewStore(t.TempDir())
	m.broker = oauth.NewBroker(vault)
	m.servers["github"] = &server{id: "github", cfg: ServerConfig{
		Type: TypeHTTP, URL: "https://api.example/mcp",
		OAuth: &OAuthConfig{Provider: "github"},
	}}
	if err := m.SaveClientCredentials("github", "client-id", "client-secret", "client_secret_post"); err != nil {
		t.Fatal(err)
	}
	rec, err := vault.Load("github")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Config == nil || rec.Config.ClientID != "client-id" || rec.Config.ClientSecret != "client-secret" {
		t.Fatalf("saved client credentials missing from vault: %#v", rec.Config)
	}
	conns, err := vault.Connections()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(conns)
	if strings.Contains(string(raw), "client-secret") || strings.Contains(string(raw), "client-id") {
		t.Fatalf("connection metadata leaked client credentials: %s", raw)
	}
}

func TestOAuthStatusShowsNeedsSetupWithoutClientRegistration(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "mcp.json"), "", nil)
	m.broker = oauth.NewBroker(oauth.NewStore(t.TempDir()))
	m.servers["github"] = &server{id: "github", state: StateConnected, cfg: ServerConfig{
		Type: TypeHTTP, URL: "https://api.example/mcp",
		OAuth: &OAuthConfig{Provider: "github"},
	}}
	got := m.Status()
	if len(got) != 1 || got[0].State != StateNeedsSetup {
		t.Fatalf("expected needs_setup state, got %#v", got)
	}
}

func TestSaveClientScopesOverridesConfiguredScopesAtSignIn(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "mcp.json"), "", nil)
	vault := oauth.NewStore(t.TempDir())
	m.broker = oauth.NewBroker(vault)
	m.servers["github"] = &server{id: "github", cfg: ServerConfig{
		Type: TypeHTTP, URL: "https://api.example/mcp",
		OAuth: &OAuthConfig{Provider: "github", Scopes: []string{"read:user"}},
	}}
	if err := m.SaveClientScopes("github", []string{"repo", "read:org", "repo"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveClientScopes("github", []string{"bad scope"}); err == nil {
		t.Fatal("a scope containing a space must be rejected")
	}
	cfg := staticOAuth(m.servers["github"].cfg)
	m.applyStoredClient("github", cfg)
	if got := strings.Join(cfg.Scopes, " "); got != "repo read:org" {
		t.Fatalf("scopes = %q, want %q", got, "repo read:org")
	}
}

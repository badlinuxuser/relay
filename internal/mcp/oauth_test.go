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
		fmt.Fprintf(w, `{"authorization_endpoint":"%[1]s/authorize","token_endpoint":"%[1]s/token","registration_endpoint":"%[1]s/register","code_challenge_methods_supported":["S256"]}`, sv.srv.URL)
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
	if _, err := m.CallTool(context.Background(), "remote", SignInTool, nil); !errors.As(err, &need) {
		t.Fatalf("sign_in before the sign-in must ask for it: %v", err)
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
	cfg.OAuth = &OAuthConfig{AuthorizeURL: sv.srv.URL + "/authorize", TokenURL: sv.srv.URL + "/token", ClientID: "cid", Scopes: []string{"read"}}
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
	if _, err := m.CallTool(context.Background(), "conn", "echo", nil); !errors.As(err, &need) || need.Server != "conn" {
		t.Fatalf("err=%v", err)
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
	cfg.OAuth = &OAuthConfig{AuthorizeURL: "https://a.example/auth", TokenURL: "https://a.example/token", ClientID: "cid"}
	m, b, _ := newOAuthManager(t, map[string]ServerConfig{"conn": Normalize(cfg)})
	if err := b.Store().Save("conn", &oauth.Record{Token: &oauth.Token{AccessToken: "revoked", Expiry: time.Now().Add(time.Hour)}}); err != nil {
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
	good := Normalize(ServerConfig{Command: "x", OAuth: &OAuthConfig{AuthorizeURL: "https://a/auth", TokenURL: "https://a/token", ClientID: "c"}})
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
	if Warning(Normalize(ServerConfig{Command: "x", OAuth: &OAuthConfig{ClientSecret: "plain", ClientID: "c", AuthorizeURL: "https://a/a", TokenURL: "https://a/t"}})) == "" {
		t.Error("a plain client_secret deserves a warning")
	}
}

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// provider is a small OAuth service: it checks PKCE and hands out tokens.
type provider struct {
	srv       *httptest.Server
	challenge string
	grants    []string
}

func newProvider(t *testing.T) *provider {
	p := &provider{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.grants = append(p.grants, r.Form.Get("grant_type"))
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge || r.Form.Get("code") != "the-code" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"bad verifier or code"}`))
				return
			}
			w.Write([]byte(`{"access_token":"at-1","token_type":"bearer","refresh_token":"rt-1","expires_in":3600}`))
		case "refresh_token":
			if r.Form.Get("refresh_token") != "rt-1" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			w.Write([]byte(`{"access_token":"at-2","expires_in":"3600"}`))
		}
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.RedirectURIs) != 1 {
			w.WriteHeader(400)
			return
		}
		w.Write([]byte(`{"client_id":"dyn-client"}`))
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"resource":"` + p.srv.URL + `/mcp","authorization_servers":["` + p.srv.URL + `"],"scopes_supported":["read"]}`))
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"authorization_endpoint":"` + p.srv.URL + `/authorize","token_endpoint":"` + p.srv.URL +
			`/token","registration_endpoint":"` + p.srv.URL + `/register","code_challenge_methods_supported":["S256"],"resource_parameter_supported":false,"token_endpoint_auth_methods_supported":["client_secret_basic","none"]}`))
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provider) config() Config {
	return Config{PKCES256Supported: true, AuthorizeURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", ClientID: "cid", Scopes: []string{"repo", "user"}, Resource: "https://api.example/", TargetURL: "https://api.example/mcp"}
}

func TestPKCEChallengeMatchesVerifier(t *testing.T) {
	v, c := pkce()
	sum := sha256.Sum256([]byte(v))
	if len(v) < 43 || c != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatalf("verifier=%q challenge=%q", v, c)
	}
}

func TestAuthURLCarriesTheFlowParameters(t *testing.T) {
	c := Config{AuthorizeURL: "https://x.example/auth?a=1", ClientID: "cid", Scopes: []string{"s1", "s2"},
		Params: map[string]string{"access_type": "offline"}, Resource: "https://mcp.example/", SendResource: true}
	got, err := c.AuthURL("http://127.0.0.1:8787/oauth/callback", "st", "ch")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(got)
	v := q.Query()
	if v.Get("a") != "1" || v.Get("access_type") != "offline" || v.Get("client_id") != "cid" ||
		v.Get("scope") != "s1 s2" || v.Get("code_challenge_method") != "S256" || v.Get("resource") != "https://mcp.example/" {
		t.Fatalf("url=%s", got)
	}
	if v.Get("state") != "st" {
		t.Fatal("a parameter from the configuration must not replace the state")
	}
	withoutResource := c
	withoutResource.SendResource = false
	urlWithoutResource, err := withoutResource.AuthURL("http://127.0.0.1:8787/oauth/callback", "st", "ch")
	if err != nil {
		t.Fatal(err)
	}
	qWithout, _ := url.Parse(urlWithoutResource)
	if qWithout.Query().Get("resource") != "" {
		t.Fatal("resource indicator was sent despite the explicit provider opt-out")
	}
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{"https://a.example/x", "http://127.0.0.1:8787/cb", "http://localhost/x"} {
		if checkURL(ok, "x") != nil {
			t.Errorf("%s must be accepted", ok)
		}
	}
	for _, bad := range []string{"http://a.example/x", "ftp://a/x", "javascript:alert(1)", "", "https://",
		"https://user:pass@example.com/token", "https://example.com/token#fragment", "https://127.0.0.1/token", "https://10.0.0.1/token", "https://localhost/token", "https://[::ffff:127.0.0.1]/token", "https://100.64.0.1/token"} {
		if checkURL(bad, "x") == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestCallbackRejectsUnexpectedIssuerAndConsumesState(t *testing.T) {
	p := newProvider(t)
	store := NewStore(t.TempDir())
	b := NewBroker(store)
	cfg := p.config()
	cfg.Issuer = "https://expected.example"
	authURL, wait, err := b.Begin(context.Background(), "issuer-test", cfg, false, "http://127.0.0.1:8787/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	state := u.Query().Get("state")
	rec := httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("GET", "/oauth/callback?state="+state+"&code=the-code&iss=https%3A%2F%2Fevil.example", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected issuer callback status = %d", rec.Code)
	}
	if err := wait(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected issuer") {
		t.Fatalf("expected issuer validation error, got %v", err)
	}
	// The state is one-use, including failed callbacks.
	replay := httptest.NewRecorder()
	b.HandleCallback(replay, httptest.NewRequest("GET", "/oauth/callback?state="+state+"&code=the-code", nil))
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback status = %d", replay.Code)
	}
}

func TestCallbackRequiresExactMethodAndPath(t *testing.T) {
	b := NewBroker(NewStore(t.TempDir()))
	rec := httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("POST", "/oauth/callback", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("GET", "/other", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("path status = %d", rec.Code)
	}
}

func TestSignInEndToEnd(t *testing.T) {
	p := newProvider(t)
	store := NewStore(t.TempDir())
	b := NewBroker(store)
	signed := make(chan string, 1)
	b.OnSignedIn = func(id string) { signed <- id }

	redirect := "http://127.0.0.1:8787/oauth/callback"
	authURL, wait, err := b.Begin(context.Background(), "gh", p.config(), false, redirect)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	p.challenge = u.Query().Get("code_challenge")
	state := u.Query().Get("state")

	// the browser comes back with the code
	rec := httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("GET", "/oauth/callback?code=the-code&state="+state, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Signed in") {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	if err := wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-signed:
		if id != "gh" {
			t.Fatalf("id=%q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnSignedIn was not called")
	}

	tok, ok, err := b.TokenForResource(context.Background(), "gh", nil, "https://api.example/", "https://api.example/mcp")
	if err != nil || !ok || tok != "at-1" {
		t.Fatalf("tok=%q ok=%v err=%v", tok, ok, err)
	}
	// the connector gets only the access token, in a file nobody else can read
	b2, err := os.ReadFile(store.TokenFile("gh"))
	if err != nil || strings.Contains(string(b2), "rt-1") || !strings.Contains(string(b2), "at-1") {
		t.Fatalf("token file: %q %v", b2, err)
	}
	if fi, _ := os.Stat(store.TokenFile("gh")); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("the token file must be private: %v", fi.Mode())
	}
	// the same state cannot be used twice
	rec = httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("GET", "/oauth/callback?code=the-code&state="+state, nil))
	if rec.Code != 400 {
		t.Fatalf("a used state must be refused: %d", rec.Code)
	}
}

func TestCallbackRefusesUnknownStateAndReportsDenial(t *testing.T) {
	p := newProvider(t)
	b := NewBroker(NewStore(t.TempDir()))
	rec := httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("GET", "/oauth/callback?code=x&state=forged", nil))
	if rec.Code != 400 {
		t.Fatalf("code=%d", rec.Code)
	}
	authURL, wait, err := b.Begin(context.Background(), "gh", p.config(), false, "http://127.0.0.1:1/cb")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	rec = httptest.NewRecorder()
	b.HandleCallback(rec, httptest.NewRequest("GET", "/oauth/callback?error=access_denied&state="+u.Query().Get("state"), nil))
	if err := wait(context.Background()); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("err=%v", err)
	}
}

func TestWaitEndsWhenTheContextEnds(t *testing.T) {
	p := newProvider(t)
	b := NewBroker(NewStore(t.TempDir()))
	_, wait, err := b.Begin(context.Background(), "gh", p.config(), false, "http://127.0.0.1:1/cb")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := wait(ctx); err == nil {
		t.Fatal("cancelling must end the wait")
	}
}

func TestExpiredTokenIsRefreshed(t *testing.T) {
	p := newProvider(t)
	store := NewStore(t.TempDir())
	cfg := p.config()
	if err := store.Save("gh", &Record{Token: &Token{AccessToken: "old", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Hour)}, Resource: cfg.Resource, TargetURL: cfg.TargetURL, Config: &cfg}); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(store)
	tok, ok, err := b.TokenForResource(context.Background(), "gh", &cfg, cfg.Resource, cfg.TargetURL)
	if err != nil || !ok || tok != "at-2" {
		t.Fatalf("tok=%q ok=%v err=%v", tok, ok, err)
	}
	rec, _ := store.Load("gh")
	if rec.Token.RefreshToken != "rt-1" {
		t.Fatal("the refresh token must be kept when the service sends no new one")
	}
	if b3, _ := os.ReadFile(store.TokenFile("gh")); !strings.Contains(string(b3), "at-2") {
		t.Fatalf("the token file must follow the refresh: %s", b3)
	}
}

func TestRefusedRefreshTokenMeansSignInAgain(t *testing.T) {
	p := newProvider(t)
	store := NewStore(t.TempDir())
	cfg := p.config()
	_ = store.Save("gh", &Record{Token: &Token{AccessToken: "old", RefreshToken: "revoked", Expiry: time.Now().Add(-time.Hour)}, Resource: cfg.Resource, TargetURL: cfg.TargetURL, Config: &cfg})
	_, ok, err := NewBroker(store).TokenForResource(context.Background(), "gh", &cfg, cfg.Resource, cfg.TargetURL)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v: the user has to sign in again", ok, err)
	}
	if store.SignedIn("gh") {
		t.Fatal("the dead token must be dropped")
	}
	if _, err := os.Stat(store.TokenFile("gh")); err == nil {
		t.Fatal("the token file must be removed")
	}
}

func TestDiscoverAndRegister(t *testing.T) {
	p := newProvider(t)
	cfg, err := Discover(context.Background(), nil, p.srv.URL+"/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthorizeURL != p.srv.URL+"/authorize" || cfg.TokenURL != p.srv.URL+"/token" ||
		!cfg.PKCES256Supported || cfg.Resource != p.srv.URL+"/mcp" || len(cfg.Scopes) != 0 || len(cfg.SupportedScopes) != 1 || cfg.Registration == "" || !cfg.SendResource || cfg.TokenAuthMethod != "none" {
		t.Fatalf("%+v", cfg)
	}
	authURL, err := cfg.AuthURL("http://127.0.0.1:8787/oauth/callback", "state", "challenge")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authURL)
	if parsed.Query().Get("scope") != "" {
		t.Fatalf("supported scopes must not be requested automatically: %s", authURL)
	}
	if parsed.Query().Get("resource") != cfg.Resource {
		t.Fatalf("authorization server advertised resource support but request omitted it: %s", authURL)
	}
	id, _, err := Register(context.Background(), nil, cfg.Registration, "http://127.0.0.1:8787/oauth/callback", "Relay")
	if err != nil || id != "dyn-client" {
		t.Fatalf("id=%q err=%v", id, err)
	}

	// Begin registers a client by itself when the configuration has none, and remembers it
	store := NewStore(t.TempDir())
	b := NewBroker(store)
	if _, _, err := b.Begin(context.Background(), "remote", *cfg, true, "http://127.0.0.1:8787/oauth/callback"); err != nil {
		t.Fatal(err)
	}
	rec, _ := store.Load("remote")
	if rec.Config == nil || rec.Config.ClientID != "dyn-client" {
		t.Fatalf("record: %+v", rec)
	}
}

func TestStoreRefusesOddIDs(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"../x", "a/b", "", ".hidden", "a b"} {
		if _, err := s.Load(id); err == nil {
			t.Errorf("%q must be refused", id)
		}
	}
}

func TestVaultEncryptsRecordsAndKeepsConnectorTokenNarrow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	rec := &Record{Token: &Token{AccessToken: "access-secret", RefreshToken: "refresh-secret"}, Config: &Config{ClientID: "client-secret", ClientSecret: "client-password", Resource: "https://api.example/"}}
	if err := store.Save("vault-test", rec); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "vault-test.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"access-secret", "refresh-secret", "client-secret", "client-password"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("encrypted vault contains plaintext %q", secret)
		}
	}
	key, err := os.ReadFile(filepath.Join(dir, "master.key"))
	if err == nil {
		if len(key) != 32 {
			t.Fatalf("master key len=%d", len(key))
		}
	} else if _, markerErr := os.Stat(filepath.Join(dir, "master.keyring")); markerErr != nil {
		t.Fatalf("neither local master key nor OS-keyring marker exists: key=%v marker=%v", err, markerErr)
	}
	loaded, err := NewStore(dir).Load("vault-test")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token.AccessToken != "access-secret" || loaded.Config.ClientSecret != "client-password" {
		t.Fatal("vault did not round-trip")
	}
	tokenFile, err := os.ReadFile(store.TokenFile("vault-test"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tokenFile), "refresh-secret") || strings.Contains(string(tokenFile), "client-password") {
		t.Fatal("connector token file leaked refresh token or client secret")
	}
	if !strings.Contains(string(tokenFile), `"resource":"https://api.example/"`) {
		t.Fatalf("connector token file omitted its resource binding: %s", tokenFile)
	}
}

func TestVaultRejectsTampering(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save("tamper", &Record{Token: &Token{AccessToken: "secret"}}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "tamper.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-3] ^= 0x01
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir).Load("tamper"); err == nil {
		t.Fatal("tampered vault record was accepted")
	}
}

func TestVaultMigratesLegacyPlaintextOnSave(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"token":{"access_token":"old-access","refresh_token":"old-refresh"},"config":{"client_id":"old-client"}}`
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	r, err := store.Load("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if r.Token.AccessToken != "old-access" {
		t.Fatal("legacy token did not load")
	}
	if err := store.Save("legacy", r); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "legacy.json"))
	if strings.Contains(string(b), "old-refresh") {
		t.Fatal("legacy record was not encrypted on save")
	}
}

func TestConnectionsAreRedactedAndSorted(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save("zeta", &Record{Token: &Token{AccessToken: "secret", Expiry: time.Now().Add(time.Hour)}, Config: &Config{Issuer: "https://issuer.example", Resource: "https://resource.example", Scopes: []string{"read"}}, LastUsed: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("alpha", &Record{Config: &Config{ClientSecret: "do-not-return"}}); err != nil {
		t.Fatal(err)
	}
	cs, err := s.Connections()
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].ID != "alpha" || cs[1].ID != "zeta" {
		t.Fatalf("connections: %#v", cs)
	}
	if cs[1].Status != "reauthorization_required" {
		t.Fatalf("legacy remote binding status=%q", cs[1].Status)
	}
	b, _ := json.Marshal(cs)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "do-not-return") {
		t.Fatalf("connection listing leaked secrets: %s", b)
	}
}

func TestTokenIsBoundToItsResource(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save("bound", &Record{Token: &Token{AccessToken: "bound-token", Expiry: time.Now().Add(time.Hour)}, Resource: "https://api.example/resource", TargetURL: "https://api.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(store)
	if _, ok, err := b.TokenForResource(context.Background(), "bound", nil, "https://evil.example/resource", "https://evil.example/mcp"); err != nil || ok {
		t.Fatalf("wrong audience accepted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := b.TokenForResource(context.Background(), "bound", nil, "https://api.example/resource", "https://other.example/mcp"); err != nil || ok {
		t.Fatalf("wrong endpoint accepted: ok=%v err=%v", ok, err)
	}
	got, ok, err := b.TokenForResource(context.Background(), "bound", nil, "https://api.example/resource", "https://api.example/mcp")
	if err != nil || !ok || got != "bound-token" {
		t.Fatalf("correct audience rejected: token=%q ok=%v err=%v", got, ok, err)
	}
}

func TestResolveAccountUsesAuthenticatedUserInfoWithoutExposingToken(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"relay-user","id":123}`))
	}))
	defer srv.Close()
	c := Config{UserInfoURL: srv.URL}
	name, err := c.ResolveAccount(context.Background(), srv.Client(), "private-token")
	if err != nil || name != "relay-user" || seen != "Bearer private-token" {
		t.Fatalf("name=%q seen=%q err=%v", name, seen, err)
	}
}

func TestConcurrentRefreshIsSerializedAndRotatesToken(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old-refresh" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	store := NewStore(t.TempDir())
	cfg := Config{TokenURL: srv.URL + "/token", ClientID: "client", Resource: "https://api.example/", TargetURL: "https://api.example/mcp"}
	if err := store.Save("refresh", &Record{Token: &Token{RefreshToken: "old-refresh", Expiry: time.Now().Add(-time.Hour)}, Config: &cfg, Resource: cfg.Resource, TargetURL: cfg.TargetURL}); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(store)
	const workers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, ok, err := b.TokenForResource(context.Background(), "refresh", &cfg, cfg.Resource, cfg.TargetURL)
			if err != nil {
				errCh <- err
				return
			}
			if !ok || token != "new-access" {
				errCh <- errors.New("refresh did not return the rotated access token")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("token endpoint called %d times, want one", got)
	}
	rec, err := store.Load("refresh")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Token.RefreshToken != "rotated-refresh" {
		t.Fatalf("refresh token was not rotated: %q", rec.Token.RefreshToken)
	}
}

func TestCallbackRequiresIssuerWhenMetadataAdvertisesIt(t *testing.T) {
	p := newProvider(t)
	b := NewBroker(NewStore(t.TempDir()))
	cfg := Config{Issuer: p.srv.URL, PKCES256Supported: true, AuthorizeURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", ClientID: "client", RequireIssuer: true, Resource: "https://api.example/", TargetURL: "https://api.example/mcp"}
	authURL, wait, err := b.Begin(context.Background(), "issuer", cfg, false, "http://127.0.0.1:8787/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=good-code&state="+u.Query().Get("state"), nil)
	w := httptest.NewRecorder()
	b.HandleCallback(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := wait(context.Background()); err == nil {
		t.Fatal("missing required issuer was accepted")
	}
}

func TestTokenEndpointAuthenticationMethods(t *testing.T) {
	for _, method := range []string{"client_secret_basic", "client_secret_post", "none"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, pass, basic := r.BasicAuth()
				_ = r.ParseForm()
				if r.Form.Get("resource") != "https://api.example/" {
					t.Errorf("RFC 8707 resource indicator missing from token request: %v", r.Form)
				}
				switch method {
				case "client_secret_basic":
					if !basic || user != "client-id" || pass != "client-secret" || r.Form.Get("client_secret") != "" || r.Form.Get("client_id") != "" {
						t.Errorf("basic auth was not isolated: basic=%v user=%q pass=%q form=%v", basic, user, pass, r.Form)
					}
				case "client_secret_post":
					if basic || r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" {
						t.Errorf("post auth missing: basic=%v form=%v", basic, r.Form)
					}
				case "none":
					if basic || r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "" {
						t.Errorf("public auth missing: basic=%v form=%v", basic, r.Form)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"ok"}`))
			}))
			defer srv.Close()
			secret := "client-secret"
			if method == "none" {
				secret = ""
			}
			cfg := Config{TokenURL: srv.URL, ClientID: "client-id", ClientSecret: secret, TokenAuthMethod: method, Resource: "https://api.example/", SendResource: true}
			if _, err := cfg.Exchange(context.Background(), srv.Client(), "http://127.0.0.1:8787/oauth/callback", "code", "verifier"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegisterClientPreservesAuthMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["token_endpoint_auth_method"] != "client_secret_basic" {
			t.Errorf("registration request method=%q", body["token_endpoint_auth_method"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_id":"dynamic-id","client_secret":"dynamic-secret","token_endpoint_auth_method":"client_secret_basic"}`))
	}))
	defer srv.Close()
	id, secret, method, err := RegisterClient(context.Background(), srv.Client(), srv.URL, "http://127.0.0.1:8787/oauth/callback", "Relay", "client_secret_basic")
	if err != nil || id != "dynamic-id" || secret != "dynamic-secret" || method != "client_secret_basic" {
		t.Fatalf("id=%q secret=%q method=%q err=%v", id, secret, method, err)
	}
}

func TestBeginRejectsMissingConfidentialClientSecretBeforeBrowser(t *testing.T) {
	b := NewBroker(NewStore(t.TempDir()))
	_, _, err := b.Begin(context.Background(), "missing-secret", Config{PKCES256Supported: true, AuthorizeURL: "https://auth.example/authorize", TokenURL: "https://auth.example/token", ClientID: "client", TokenAuthMethod: "client_secret_basic", Resource: "https://api.example/", TargetURL: "https://api.example/mcp"}, false, "http://127.0.0.1:8787/oauth/callback")
	if err == nil || !strings.Contains(err.Error(), "requires oauth.client_secret") {
		t.Fatalf("err=%v", err)
	}
}

func TestConcurrentAuthorizationForSameServerIsRejected(t *testing.T) {
	p := newProvider(t)
	b := NewBroker(NewStore(t.TempDir()))
	cfg := Config{PKCES256Supported: true, AuthorizeURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", ClientID: "client", TokenAuthMethod: "none", Resource: "https://api.example/", TargetURL: "https://api.example/mcp"}
	_, wait, err := b.Begin(context.Background(), "same", cfg, false, "http://127.0.0.1:8787/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Begin(context.Background(), "same", cfg, false, "http://127.0.0.1:8787/oauth/callback"); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("second authorization err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx); err == nil {
		t.Fatal("cancelled wait unexpectedly succeeded")
	}
	if _, _, err := b.Begin(context.Background(), "same", cfg, false, "http://127.0.0.1:8787/oauth/callback"); err != nil {
		t.Fatalf("new flow after cancellation: %v", err)
	}
}

func TestMarkUsedRecordsSuccessfulUse(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save("used", &Record{Token: &Token{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(store)
	if err := b.MarkUsed("used"); err != nil {
		t.Fatal(err)
	}
	rec, err := store.Load("used")
	if err != nil {
		t.Fatal(err)
	}
	if rec.LastUsed.IsZero() {
		t.Fatal("successful use timestamp was not recorded")
	}
}

func TestDisconnectCancelsPendingAuthorization(t *testing.T) {
	p := newProvider(t)
	store := NewStore(t.TempDir())
	b := NewBroker(store)
	cfg := p.config()
	authURL, wait, err := b.Begin(context.Background(), "disconnect", cfg, false, "http://127.0.0.1:8787/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if err := b.Disconnect("disconnect"); err != nil {
		t.Fatal(err)
	}
	if err := wait(context.Background()); err == nil {
		t.Fatal("pending authorization did not cancel")
	}
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=good-code&state="+u.Query().Get("state"), nil)
	w := httptest.NewRecorder()
	b.HandleCallback(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("late callback accepted: %d", w.Code)
	}
	if store.SignedIn("disconnect") {
		t.Fatal("late callback persisted a token after disconnect")
	}
}

func TestRevocationFailureStillDisconnectsLocally(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	store := NewStore(t.TempDir())
	cfg := Config{ClientID: "client", TokenAuthMethod: "none", RevocationURL: srv.URL}
	if err := store.Save("revoke", &Record{Token: &Token{AccessToken: "access", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}, Config: &cfg}); err != nil {
		t.Fatal(err)
	}
	attempted, err := NewBroker(store).Revoke(context.Background(), "revoke")
	if err == nil || attempted {
		t.Fatalf("attempted=%v err=%v", attempted, err)
	}
	if store.SignedIn("revoke") {
		t.Fatal("local credential survived failed provider revocation")
	}
}

func TestVaultCiphertextCannotBeSwappedAcrossServerIDs(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save("server-a", &Record{Token: &Token{AccessToken: "account-a"}, Resource: "https://api.example"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("server-b", &Record{Token: &Token{AccessToken: "account-b"}, Resource: "https://api.example"}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "server-a.json"))
	b, _ := os.ReadFile(filepath.Join(dir, "server-b.json"))
	if err := os.WriteFile(filepath.Join(dir, "server-a.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server-b.json"), a, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("server-a"); err == nil {
		t.Fatal("ciphertext from another server ID was accepted")
	}
}

func TestBeginRejectsProviderWithoutPKCES256Declaration(t *testing.T) {
	b := NewBroker(NewStore(t.TempDir()))
	_, _, err := b.Begin(context.Background(), "no-pkce", Config{AuthorizeURL: "https://auth.example/a", TokenURL: "https://auth.example/t", ClientID: "id", TokenAuthMethod: "none"}, false, "http://127.0.0.1:8787/oauth/callback")
	if err == nil || !strings.Contains(err.Error(), "PKCE S256") {
		t.Fatalf("err=%v", err)
	}
}

func TestProtectedResourceMayBeABaseURIButNotAnotherOriginOrSibling(t *testing.T) {
	if !ResourceMatchesTarget("https://api.example/v1", "https://api.example/v1/mcp") {
		t.Fatal("base resource should cover its MCP endpoint")
	}
	if !ResourceMatchesTarget("https://api.example", "https://api.example/mcp") {
		t.Fatal("origin resource should cover its endpoint")
	}
	if ResourceMatchesTarget("https://api.example/v1", "https://api.example/v10/mcp") {
		t.Fatal("sibling path must not match")
	}
	if ResourceMatchesTarget("https://api.example/v1", "https://evil.example/v1/mcp") {
		t.Fatal("different origin must not match")
	}
	if ResourceMatchesTarget("https://api.example/v1", "https://api.example/v1/%2e%2e/admin") {
		t.Fatal("encoded path traversal must not match")
	}
	if ResourceMatchesTarget("https://api.example/v1", "https://api.example/v1%2f..%2fadmin") {
		t.Fatal("encoded slash must not match")
	}

}

func TestCallbackRejectsAmbiguousDuplicateParameters(t *testing.T) {
	b := NewBroker(NewStore(t.TempDir()))
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=a&state=b&code=c", nil)
	w := httptest.NewRecorder()
	b.HandleCallback(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAuthURLRejectsReservedParameterOverrides(t *testing.T) {
	for _, key := range []string{"state", "scope", "resource", "client_id", "redirect_uri", "code_challenge", "audience"} {
		c := Config{AuthorizeURL: "https://auth.example/a", ClientID: "id", Params: map[string]string{key: "attacker-value"}}
		if _, err := c.AuthURL("http://127.0.0.1:8787/oauth/callback", "state", "challenge"); err == nil || !strings.Contains(err.Error(), "reserved authorization parameter") {
			t.Fatalf("key %s err=%v", key, err)
		}
	}
}

func TestDiscoveryRejectsAuthorizationServerWithoutAdvertisedPKCES256(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			_, _ = w.Write([]byte(`{"resource":"` + srv.URL + `/mcp","authorization_servers":["` + srv.URL + `"]}`))
		case "/.well-known/oauth-authorization-server":
			_, _ = w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/authorize","token_endpoint":"` + srv.URL + `/token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_, err := Discover(context.Background(), srv.Client(), srv.URL+"/mcp", "")
	if err == nil || !strings.Contains(err.Error(), "does not advertise PKCE S256") {
		t.Fatalf("err=%v", err)
	}
}

func TestBeginRequiresResourceAndTargetBinding(t *testing.T) {
	base := Config{PKCES256Supported: true, AuthorizeURL: "https://auth.example/a", TokenURL: "https://auth.example/t", ClientID: "id", TokenAuthMethod: "none", Resource: "https://api.example/v1", TargetURL: "https://api.example/v1/mcp"}
	cases := []Config{base, base, base}
	cases[0].Resource = ""
	cases[1].TargetURL = ""
	cases[2].TargetURL = "https://api.example/v10/mcp"
	for i, cfg := range cases {
		b := NewBroker(NewStore(t.TempDir()))
		if _, _, err := b.Begin(context.Background(), fmt.Sprintf("binding-%d", i), cfg, false, "http://127.0.0.1:8787/oauth/callback"); err == nil || !strings.Contains(err.Error(), "resource") {
			t.Fatalf("case %d err=%v", i, err)
		}
	}
}

func TestCallbackRejectsIssuerWhenNoExpectedIssuerWasConfigured(t *testing.T) {
	p := newProvider(t)
	b := NewBroker(NewStore(t.TempDir()))
	authURL, wait, err := b.Begin(context.Background(), "unknown-issuer", p.config(), false, "http://127.0.0.1:8787/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=good&state="+u.Query().Get("state")+"&iss=https%3A%2F%2Fevil.example", nil)
	w := httptest.NewRecorder()
	b.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
	if err := wait(context.Background()); err == nil || !strings.Contains(err.Error(), "no expected issuer") {
		t.Fatalf("wait err=%v", err)
	}
}

func TestIsPublicIPRejectsReservedAndPrivateRanges(t *testing.T) {
	for _, raw := range []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "169.254.1.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "224.0.0.1", "::1", "fc00::1", "fe80::1", "2001:db8::1", "ff02::1"} {
		if isPublicIP(net.ParseIP(raw)) {
			t.Errorf("%s must not be treated as public", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !isPublicIP(net.ParseIP(raw)) {
			t.Errorf("%s should be public", raw)
		}
	}
}

func TestBeginReusesDCRClientOnlyForSameAuthorizationServer(t *testing.T) {
	p := newProvider(t)
	store := NewStore(t.TempDir())
	b := NewBroker(store)
	redirect := "http://127.0.0.1:8787/oauth/callback"
	old := Config{PKCES256Supported: true, Issuer: "https://issuer.example", AuthorizeURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", Registration: p.srv.URL + "/register", ClientID: "registered-client", TokenAuthMethod: "none", Resource: "https://api.example/old/", SendResource: true, TargetURL: "https://api.example/old/mcp"}
	if err := store.Save("remote", &Record{Config: &old, RedirectURI: redirect, Resource: old.Resource, TargetURL: old.TargetURL}); err != nil {
		t.Fatal(err)
	}
	next := old
	next.ClientID = ""
	next.Resource = "https://api.example/new/"
	next.TargetURL = "https://api.example/new/mcp"
	next.Scopes = []string{"read:items"}
	authURL, _, err := b.Begin(context.Background(), "remote", next, true, redirect)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("client_id") != "registered-client" || q.Get("resource") != next.Resource || q.Get("scope") != "read:items" {
		t.Fatalf("authorization parameters=%v", q)
	}
	b.Disconnect("remote")
	other := next
	other.Issuer = "https://other-issuer.example"
	other.Registration = ""
	if _, _, err := b.Begin(context.Background(), "remote", other, false, redirect); err == nil || !strings.Contains(err.Error(), "OAuth client setup is required") {
		t.Fatalf("client registration must not cross issuer boundaries: %v", err)
	}
}

func TestBeginUsesConfiguredCIMDClientIDBeforeDCR(t *testing.T) {
	b := NewBroker(NewStore(t.TempDir()))
	cfg := Config{
		PKCES256Supported: true, AuthorizeURL: "https://issuer.example/authorize",
		TokenURL: "https://issuer.example/token", Registration: "https://issuer.example/register",
		CIMDURL: "https://relay.example/oauth/client.json", CIMDSupported: true,
		TokenAuthMethod: "none", Resource: "https://api.example/mcp", TargetURL: "https://api.example/mcp",
	}
	_, wait, err := b.Begin(context.Background(), "cimd", cfg, true, "http://127.0.0.1:8787/oauth/callback")
	if err != nil {
		t.Fatalf("Begin with CIMD should succeed: %v", err)
	}
	rec, err := b.Store().Load("cimd")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Config == nil || rec.Config.ClientID != cfg.CIMDURL {
		t.Fatalf("expected CIMD URL as client_id, got %#v", rec.Config)
	}
	b.CancelAuthorization("cimd")
	_ = wait(context.Background())
}

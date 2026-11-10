package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
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
			`/token","registration_endpoint":"` + p.srv.URL + `/register","code_challenge_methods_supported":["S256"]}`))
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provider) config() Config {
	return Config{AuthorizeURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", ClientID: "cid", Scopes: []string{"repo", "user"}}
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
		Params: map[string]string{"access_type": "offline", "state": "evil"}, Resource: "https://mcp.example/"}
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
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{"https://a.example/x", "http://127.0.0.1:8787/cb", "http://localhost/x"} {
		if checkURL(ok, "x") != nil {
			t.Errorf("%s must be accepted", ok)
		}
	}
	for _, bad := range []string{"http://a.example/x", "ftp://a/x", "javascript:alert(1)", "", "https://"} {
		if checkURL(bad, "x") == nil {
			t.Errorf("%q must be refused", bad)
		}
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

	tok, ok, err := b.Token(context.Background(), "gh", nil)
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
	if err := store.Save("gh", &Record{Token: &Token{AccessToken: "old", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(store)
	tok, ok, err := b.Token(context.Background(), "gh", &cfg)
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
	_ = store.Save("gh", &Record{Token: &Token{AccessToken: "old", RefreshToken: "revoked", Expiry: time.Now().Add(-time.Hour)}})
	_, ok, err := NewBroker(store).Token(context.Background(), "gh", &cfg)
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
		cfg.Resource != p.srv.URL+"/mcp" || len(cfg.Scopes) != 1 || cfg.Registration == "" {
		t.Fatalf("%+v", cfg)
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

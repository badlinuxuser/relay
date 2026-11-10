// Package oauth signs the user in to external services for MCP connectors: the authorization
// code flow with PKCE, a loopback callback served by the Relay daemon, tokens kept in files
// that only the user can read, and refreshing of expired tokens.
//
// A connector program never sees the refresh token or the client secret: Relay keeps them
// and gives the program only a file with the current access token.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config describes how to sign in to one service.
type Config struct {
	AuthorizeURL string            `json:"authorize_url"`
	TokenURL     string            `json:"token_url"`
	Registration string            `json:"registration_url,omitempty"` // dynamic client registration (RFC 7591)
	ClientID     string            `json:"client_id,omitempty"`
	ClientSecret string            `json:"client_secret,omitempty"`
	Scopes       []string          `json:"scopes,omitempty"`
	Params       map[string]string `json:"params,omitempty"`   // extra parameters of the authorize address
	Resource     string            `json:"resource,omitempty"` // RFC 8707: the MCP server the token is for
}

// Token is what the service gave us.
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"` // zero: the service did not say
	Scope        string    `json:"scope,omitempty"`
}

// Valid reports whether the access token can be used now (with a minute to spare).
func (t *Token) Valid() bool {
	return t != nil && t.AccessToken != "" && (t.Expiry.IsZero() || time.Until(t.Expiry) > time.Minute)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkURL accepts https addresses, and http only for this computer (testing, the callback).
func checkURL(raw, what string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s is not a valid address: %q", what, raw)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopback(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("%s must be an https address (got %q)", what, raw)
}

func newHTTP() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		// credentials go only where the user's configuration says
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// pkce returns a code verifier and its S256 challenge.
func pkce() (verifier, challenge string) {
	verifier = randomString(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthURL is the address to open in the browser.
func (c *Config) AuthURL(redirect, state, challenge string) (string, error) {
	u, err := url.Parse(c.AuthorizeURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range c.Params {
		q.Set(k, v)
	}
	q.Set("response_type", "code")
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if len(c.Scopes) > 0 {
		q.Set("scope", strings.Join(c.Scopes, " "))
	}
	if c.Resource != "" {
		q.Set("resource", c.Resource)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Error is an answer of the token endpoint that says no (as opposed to a network problem).
type Error struct{ Code, Desc string }

func (e *Error) Error() string {
	if e.Desc != "" {
		return "the service refused: " + e.Code + ": " + e.Desc
	}
	return "the service refused: " + e.Code
}

func seconds(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func (c *Config) tokenRequest(ctx context.Context, hc *http.Client, form url.Values) (*Token, error) {
	form.Set("client_id", c.ClientID)
	if c.ClientSecret != "" {
		form.Set("client_secret", c.ClientSecret)
	}
	if c.Resource != "" {
		form.Set("resource", c.Resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json") // GitHub answers with a form without it
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the token address did not answer: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var f struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    any    `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(body, &f); err != nil {
			return nil, fmt.Errorf("unreadable answer of the token address: %w", err)
		}
	} else if vals, err := url.ParseQuery(trimmed); err == nil {
		f.AccessToken, f.TokenType, f.RefreshToken = vals.Get("access_token"), vals.Get("token_type"), vals.Get("refresh_token")
		f.ExpiresIn, f.Scope = vals.Get("expires_in"), vals.Get("scope")
		f.Error, f.Description = vals.Get("error"), vals.Get("error_description")
	}
	if f.Error != "" {
		return nil, &Error{Code: f.Error, Desc: f.Description}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("the token address answered HTTP %d", resp.StatusCode)
	}
	if f.AccessToken == "" {
		return nil, errors.New("the token address sent no access token")
	}
	t := &Token{AccessToken: f.AccessToken, TokenType: f.TokenType, RefreshToken: f.RefreshToken, Scope: f.Scope}
	if n := seconds(f.ExpiresIn); n > 0 {
		t.Expiry = time.Now().Add(time.Duration(n) * time.Second)
	}
	return t, nil
}

// Exchange trades the code from the callback for a token.
func (c *Config) Exchange(ctx context.Context, hc *http.Client, redirect, code, verifier string) (*Token, error) {
	return c.tokenRequest(ctx, hc, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier},
	})
}

// Refresh gets a new access token. The old refresh token stays if the service sends no new one.
func (c *Config) Refresh(ctx context.Context, hc *http.Client, refresh string) (*Token, error) {
	t, err := c.tokenRequest(ctx, hc, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	if err != nil {
		return nil, err
	}
	if t.RefreshToken == "" {
		t.RefreshToken = refresh
	}
	return t, nil
}

// ---- storage ---------------------------------------------------------------

// Record is everything kept for one server.
type Record struct {
	Token       *Token  `json:"token,omitempty"`
	Config      *Config `json:"config,omitempty"` // the discovered endpoints and the registered client
	RedirectURI string  `json:"redirect_uri,omitempty"`
}

// Store keeps records as files in the auth folder (owner only):
//
//	<id>.json   the whole record: only Relay reads it
//	<id>.token  just the access token: this is the file a connector program is given
type Store struct {
	dir string
	mu  sync.Mutex
}

var reID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)

func NewStore(dir string) *Store { return &Store{dir: dir} }

// TokenFile is the path of the access token file of a server.
func (s *Store) TokenFile(id string) string { return filepath.Join(s.dir, id+".token") }

func (s *Store) recordPath(id string) (string, error) {
	if !reID.MatchString(id) {
		return "", fmt.Errorf("bad server id %q", id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

func (s *Store) Load(id string) (*Record, error) {
	p, err := s.recordPath(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(p)
}

func (s *Store) load(p string) (*Record, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Record{}, nil
		}
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", p, err)
	}
	return &r, nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) Save(id string, r *Record) error {
	p, err := s.recordPath(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(id, p, r)
}

func (s *Store) save(id, p string, r *Record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(p, append(b, '\n')); err != nil {
		return err
	}
	if r.Token == nil || r.Token.AccessToken == "" {
		_ = os.Remove(s.TokenFile(id))
		return nil
	}
	view := map[string]any{"access_token": r.Token.AccessToken, "token_type": "Bearer"}
	if !r.Token.Expiry.IsZero() {
		view["expires_at"] = r.Token.Expiry.Unix()
	}
	tb, _ := json.Marshal(view)
	return writeFile(s.TokenFile(id), append(tb, '\n'))
}

// DropToken signs out: the token goes, the registered client stays.
func (s *Store) DropToken(id string) error {
	p, err := s.recordPath(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.load(p)
	if err != nil {
		return err
	}
	r.Token = nil
	return s.save(id, p, r)
}

// Delete forgets everything about a server.
func (s *Store) Delete(id string) error {
	p, err := s.recordPath(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.TokenFile(id))
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SignedIn reports whether there is a token (or a refresh token that can make one).
func (s *Store) SignedIn(id string) bool {
	r, err := s.Load(id)
	return err == nil && r.Token != nil && (r.Token.Valid() || r.Token.RefreshToken != "")
}

// Package oauth signs the user in to external services for MCP connectors: the authorization
// code flow with PKCE, a loopback callback served by the Relay daemon, tokens kept in files
// that only the user can read, and refreshing of expired tokens.
//
// A connector program never sees the refresh token or the client secret: Relay keeps them
// and gives the program only a file with the current access token.
package oauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config describes how to sign in to one service.
type Config struct {
	Provider          string            `json:"provider,omitempty"`
	PKCES256Supported bool              `json:"pkce_s256_supported,omitempty"`
	Issuer            string            `json:"issuer,omitempty"`
	RequireIssuer     bool              `json:"require_issuer,omitempty"`
	AuthorizeURL      string            `json:"authorize_url"`
	TokenURL          string            `json:"token_url"`
	Registration      string            `json:"registration_url,omitempty"` // dynamic client registration (RFC 7591)
	CIMDURL           string            `json:"cimd_url,omitempty"`         // MCP Client ID Metadata Document URL; also used as client_id
	CIMDSupported     bool              `json:"cimd_supported,omitempty"`
	RevocationURL     string            `json:"revocation_url,omitempty"`
	UserInfoURL       string            `json:"userinfo_url,omitempty"`
	Resource          string            `json:"resource,omitempty"`      // RFC 8707 resource indicator
	SendResource      bool              `json:"send_resource,omitempty"` // RFC 8707 resource indicator in authorization and token requests
	TargetURL         string            `json:"target_url,omitempty"`    // exact MCP endpoint this connection was authorized for
	ClientID          string            `json:"client_id,omitempty"`
	ClientSecret      string            `json:"client_secret,omitempty"`
	TokenAuthMethod   string            `json:"token_endpoint_auth_method,omitempty"`
	Scopes            []string          `json:"scopes,omitempty"`           // explicitly requested scopes
	UserScopes        []string          `json:"user_scopes,omitempty"`      // scopes chosen in Connections; they replace Scopes from mcp.json
	SupportedScopes   []string          `json:"supported_scopes,omitempty"` // advertised by protected-resource metadata; not automatically requested
	Params            map[string]string `json:"params,omitempty"`           // extra parameters of the authorize address
	DefaultScopes     []string          `json:"default_scopes,omitempty"`
	DefaultParams     map[string]string `json:"default_params,omitempty"`
}

type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"` // zero: the service did not say
	Scope        string    `json:"scope,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
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

// isPublicIP excludes address ranges that must never be reached through remote OAuth
// metadata, including private, loopback, link-local, documentation and carrier-grade NAT.
func isPublicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	blocked := []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "100::/64", "2001:10::/28", "2001:20::/28"}
	for _, cidr := range blocked {
		_, subnet, _ := net.ParseCIDR(cidr)
		if subnet.Contains(ip) {
			return false
		}
	}
	return true
}

// checkURL accepts https addresses, and http only for this computer (testing, the callback).
func checkURL(raw, what string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" ||
		u.Opaque != "" || strings.ContainsAny(raw, "\r\n\t\x00 ") {
		return fmt.Errorf("%s is not a safe absolute URL: %q", what, raw)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return fmt.Errorf("%s must use HTTPS (HTTP is allowed only for loopback addresses)", what)
	}
	if u.Scheme == "https" {
		if isLoopback(u.Hostname()) {
			return fmt.Errorf("%s must not point to a loopback address", what)
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil && !isPublicIP(ip) {
			return fmt.Errorf("%s must not point to a private or non-routable address", what)
		}
	}
	return nil
}

func newHTTP() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // OAuth endpoints are dialed directly so DNS validation cannot be bypassed by a proxy.
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("endpoint hostname resolved to no addresses")
		}
		for _, candidate := range ips {
			ip := candidate.IP
			loopbackHost := strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
			if loopbackHost {
				if !ip.IsLoopback() {
					return nil, fmt.Errorf("loopback OAuth endpoint %q resolved to a non-loopback address", host)
				}
				continue
			}
			if !isPublicIP(ip) {
				return nil, fmt.Errorf("endpoint hostname %q resolves to a non-public address", host)
			}
		}
		var last error
		for _, candidate := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport,
		// Never forward authorization material through redirects.
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

// ValidateParams prevents custom provider parameters from overriding protocol-critical
// values that Relay must derive from the validated authorization session.
func ValidateParams(params map[string]string) error {
	reserved := map[string]bool{"response_type": true, "client_id": true, "redirect_uri": true, "state": true, "code_challenge": true, "code_challenge_method": true, "scope": true, "resource": true, "audience": true}
	for key := range params {
		if reserved[strings.ToLower(key)] {
			return fmt.Errorf("oauth.params must not override reserved authorization parameter %q", key)
		}
	}
	return nil
}

// AuthURL is the address to open in the browser.
func (c *Config) AuthURL(redirect, state, challenge string) (string, error) {
	if err := ValidateParams(c.Params); err != nil {
		return "", err
	}
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
	if c.SendResource && c.Resource != "" {
		q.Set("resource", c.Resource)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Error is an answer of the token endpoint that says no (as opposed to a network problem).
type Error struct{ Code, Desc string }

func (e *Error) Error() string {
	// Provider-controlled descriptions may echo request credentials; keep them out of logs.
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

// ValidateClientAuth checks the configured token endpoint client-auth contract before consent.
func (c *Config) ValidateClientAuth() error { return c.validateClientAuth() }

func (c *Config) validateClientAuth() error {
	method := c.TokenAuthMethod
	if method == "" {
		if c.ClientSecret != "" {
			method = "client_secret_post"
		} else {
			method = "none"
		}
	}
	switch method {
	case "client_secret_basic", "client_secret_post":
		if c.ClientSecret == "" {
			return fmt.Errorf("oauth.token_endpoint_auth_method %s requires oauth.client_secret", method)
		}
	case "none":
		if c.ClientSecret != "" {
			return errors.New("oauth.client_secret must be omitted when token_endpoint_auth_method is none")
		}
	default:
		return fmt.Errorf("unsupported token endpoint authentication method %q", method)
	}
	return nil
}

func (c *Config) tokenRequest(ctx context.Context, hc *http.Client, form url.Values) (*Token, error) {
	if err := c.validateClientAuth(); err != nil {
		return nil, err
	}
	method := c.TokenAuthMethod
	if method == "" {
		if c.ClientSecret != "" {
			method = "client_secret_post"
		} else {
			method = "none"
		}
	}
	switch method {
	case "client_secret_basic":
		// Credentials are sent in the Authorization header, never duplicated in the body.
	case "client_secret_post":
		form.Set("client_id", c.ClientID)
		if c.ClientSecret != "" {
			form.Set("client_secret", c.ClientSecret)
		}
	case "none":
		form.Set("client_id", c.ClientID)
	default:
		return nil, fmt.Errorf("unsupported token endpoint authentication method %q", method)
	}
	if c.SendResource && c.Resource != "" {
		form.Set("resource", c.Resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json") // GitHub answers with a form without it
	if method == "client_secret_basic" {
		req.SetBasicAuth(c.ClientID, c.ClientSecret)
	}
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
		IDToken      string `json:"id_token"`
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
	t := &Token{AccessToken: f.AccessToken, TokenType: f.TokenType, RefreshToken: f.RefreshToken, Scope: f.Scope, IDToken: f.IDToken}
	if n := seconds(f.ExpiresIn); n > 0 {
		t.Expiry = time.Now().Add(time.Duration(n) * time.Second)
	}
	return t, nil
}

// ResolveAccount asks the provider's userinfo endpoint for a non-secret account label.
// It never decodes an unverified ID token and treats the endpoint as optional metadata.
func (c *Config) ResolveAccount(ctx context.Context, hc *http.Client, token string) (string, error) {
	if c.UserInfoURL == "" {
		return "", nil
	}
	if err := checkURL(c.UserInfoURL, "userinfo_endpoint"); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.UserInfoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Relay/0.3.1")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("userinfo endpoint answered HTTP %d", resp.StatusCode)
	}
	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&claims); err != nil {
		return "", err
	}
	for _, key := range []string{"email", "preferred_username", "login", "username", "name"} {
		if v, ok := claims[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
	return "", nil
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
	Token       *Token    `json:"token,omitempty"`
	Config      *Config   `json:"config,omitempty"` // the discovered endpoints and the registered client
	RedirectURI string    `json:"redirect_uri,omitempty"`
	Resource    string    `json:"resource,omitempty"`
	TargetURL   string    `json:"target_url,omitempty"`
	Account     string    `json:"account,omitempty"`
	LastUsed    time.Time `json:"last_used,omitempty"`
}

// Store keeps records as files in the auth folder (owner-only on Unix):
//
//	<id>.json   the whole record: only Relay reads it
//	<id>.token  just the access token: this is the file a connector program is given
type Store struct {
	dir string
	mu  sync.Mutex
	key []byte
}

var reID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)

func NewStore(dir string) *Store { return &Store{dir: dir} }

// encryptionKey creates or reads a per-installation 256-bit key. The key is kept in a
// separate owner-only file; encrypted records are authenticated with AES-256-GCM.
// On Unix the directory and key are restricted to the current user. Other OS permission semantics
// follow the user's profile ACLs. This protects copied
// token records and accidental disclosure of the record file; it is not a defence against
// malware running as the same OS user (use an OS keychain for that threat model).
func (s *Store) encryptionKey() ([]byte, error) {
	if s.key != nil {
		return s.key, nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(s.dir, "master.key")
	marker := filepath.Join(s.dir, "master.keyring")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		if key, ok := loadOSKeyringKey(filepath.Clean(s.dir)); ok {
			if _, markerErr := os.Stat(marker); os.IsNotExist(markerErr) {
				if markerErr = os.WriteFile(marker, []byte("secret-service-v1\n"), 0o600); markerErr != nil {
					return nil, markerErr
				}
			} else if markerErr != nil {
				return nil, markerErr
			}
			s.key = key
			return key, nil
		}
		if _, markerErr := os.Stat(marker); markerErr == nil {
			return nil, errors.New("Relay OAuth vault key is stored in the OS keyring, but the keyring is unavailable or locked; unlock Secret Service and retry")
		}
	}
	if err == nil {
		if len(b) != 32 {
			return nil, errors.New("OAuth vault master key has an invalid length")
		}
		if err := os.Chmod(p, 0o600); err != nil {
			return nil, err
		}
		s.key = b
		return b, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return s.encryptionKey()
		}
		return nil, err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(p)
		return nil, err
	}
	if storeOSKeyringKey(filepath.Clean(s.dir), b) {
		if markerErr := os.WriteFile(marker, []byte("secret-service-v1\n"), 0o600); markerErr == nil {
			_ = os.Chmod(marker, 0o600)
			_ = os.Remove(p) // key is held by the OS keyring, not beside ciphertext
			s.key = b
			return b, nil
		}
	}
	s.key = b
	return b, nil
}

func (s *Store) seal(id string, plain []byte) ([]byte, error) {
	k, err := s.encryptionKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plain, []byte("relay-oauth-vault-v1:"+id))
	envelope := struct {
		Version int    `json:"version"`
		Nonce   string `json:"nonce"`
		Data    string `json:"data"`
	}{1, base64.RawStdEncoding.EncodeToString(nonce), base64.RawStdEncoding.EncodeToString(ciphertext)}
	return json.Marshal(envelope)
}

func (s *Store) unseal(id string, raw []byte) ([]byte, bool, error) {
	var env struct {
		Version int    `json:"version"`
		Nonce   string `json:"nonce"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false, nil
	} // legacy plaintext record
	if env.Version == 0 {
		if env.Nonce != "" || env.Data != "" {
			return nil, true, errors.New("OAuth vault envelope is missing its version")
		}
		return nil, false, nil
	}
	if env.Version != 1 {
		return nil, true, fmt.Errorf("unsupported OAuth vault version %d", env.Version)
	}
	k, err := s.encryptionKey()
	if err != nil {
		return nil, true, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, true, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, true, err
	}
	nonce, err := base64.RawStdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, true, errors.New("OAuth vault nonce is invalid")
	}
	data, err := base64.RawStdEncoding.DecodeString(env.Data)
	if err != nil {
		return nil, true, errors.New("OAuth vault ciphertext is invalid")
	}
	plain, err := gcm.Open(nil, nonce, data, []byte("relay-oauth-vault-v1:"+id))
	if err != nil {
		return nil, true, errors.New("OAuth vault authentication failed: wrong key or damaged data")
	}
	return plain, true, nil
}

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
	id := strings.TrimSuffix(filepath.Base(p), ".json")
	plain, encrypted, decErr := s.unseal(id, b)
	if decErr != nil {
		return nil, fmt.Errorf("%s: %w", p, decErr)
	}
	if encrypted {
		b = plain
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", p, err)
	}
	if !encrypted { // transparently migrate pre-vault records on first read
		if reID.MatchString(id) {
			if err := s.save(id, p, &r); err != nil {
				return nil, fmt.Errorf("migrate legacy OAuth record: %w", err)
			}
		}
	}
	return &r, nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
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
	plain, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	b, err := s.seal(id, plain)
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
	resource := r.Resource
	if resource == "" && r.Config != nil {
		resource = r.Config.Resource
	}
	view := map[string]any{"access_token": r.Token.AccessToken, "token_type": "Bearer", "resource": resource}
	if !r.Token.Expiry.IsZero() {
		view["expires_at"] = r.Token.Expiry.Unix()
	}
	tb, _ := json.Marshal(view)
	return writeFile(s.TokenFile(id), append(tb, '\n'))
}

// Connection is a redacted, UI-safe summary of one stored authorization.
type Connection struct {
	ID        string    `json:"id"`
	Issuer    string    `json:"issuer,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Resource  string    `json:"resource,omitempty"`
	TargetURL string    `json:"target_url,omitempty"`
	Scopes    []string  `json:"scopes,omitempty"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	LastUsed  time.Time `json:"last_used,omitempty"`
	Account   string    `json:"account,omitempty"`
}

// Connections lists stored OAuth records without exposing credentials or tokens.
func (s *Store) Connections() ([]Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(s.dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]Connection, 0, len(files))
	for _, p := range files {
		id := strings.TrimSuffix(filepath.Base(p), ".json")
		if !reID.MatchString(id) {
			continue
		}
		r, err := s.load(p)
		if err != nil {
			return nil, err
		}
		if r.Token == nil && r.Config == nil {
			continue
		}
		c := Connection{ID: id, Account: r.Account, LastUsed: r.LastUsed, Status: "disconnected"}
		c.Resource = r.Resource
		c.TargetURL = r.TargetURL
		if r.Config != nil {
			c.Issuer = r.Config.Issuer
			c.Provider = r.Config.Provider
			if c.Resource == "" {
				c.Resource = r.Config.Resource
			}
			c.Scopes = append([]string(nil), r.Config.Scopes...)
		}
		if r.Token != nil && strings.TrimSpace(r.Token.Scope) != "" {
			c.Scopes = strings.Fields(r.Token.Scope)
		}
		if r.Token != nil {
			c.ExpiresAt = r.Token.Expiry
			if r.Token.Valid() || r.Token.RefreshToken != "" {
				if r.TargetURL == "" {
					c.Status = "reauthorization_required"
				} else {
					c.Status = "connected"
				}
			} else {
				c.Status = "reauthorization_required"
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
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

// Revoke attempts RFC 7009 revocation when the provider advertises an endpoint, then
// removes the local credential regardless. The boolean reports whether remote revocation
// was attempted successfully; local deletion errors are returned.
func (b *Broker) Revoke(ctx context.Context, id string) (bool, error) {
	b.cancelActive(id, "connection was revoked")
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	rec, err := b.store.Load(id)
	if err != nil {
		return false, err
	}
	attempted := false
	var remoteErr error
	if rec.Config != nil && rec.Config.RevocationURL != "" && rec.Token != nil {
		if err := checkURL(rec.Config.RevocationURL, "revocation_endpoint"); err != nil {
			remoteErr = err
		} else {
			token, hint := rec.Token.RefreshToken, "refresh_token"
			if token == "" {
				token, hint = rec.Token.AccessToken, "access_token"
			}
			form := url.Values{"token": {token}, "token_type_hint": {hint}}
			method := rec.Config.TokenAuthMethod
			if method == "" {
				if rec.Config.ClientSecret != "" {
					method = "client_secret_post"
				} else {
					method = "none"
				}
			}
			switch method {
			case "client_secret_basic":
				if rec.Config.ClientSecret == "" {
					remoteErr = errors.New("client_secret_basic requires oauth.client_secret")
				}
			case "client_secret_post":
				form.Set("client_id", rec.Config.ClientID)
				if rec.Config.ClientSecret != "" {
					form.Set("client_secret", rec.Config.ClientSecret)
				}
			case "none":
				form.Set("client_id", rec.Config.ClientID)
			default:
				remoteErr = fmt.Errorf("unsupported token endpoint authentication method %q", method)
			}
			if remoteErr == nil {
				req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, rec.Config.RevocationURL, strings.NewReader(form.Encode()))
				if reqErr != nil {
					remoteErr = reqErr
				} else {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					if method == "client_secret_basic" {
						req.SetBasicAuth(rec.Config.ClientID, rec.Config.ClientSecret)
					}
					resp, doErr := b.hc.Do(req)
					if doErr != nil {
						remoteErr = doErr
					} else {
						io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
						resp.Body.Close()
						if resp.StatusCode < 200 || resp.StatusCode >= 300 {
							remoteErr = fmt.Errorf("revocation endpoint answered HTTP %d", resp.StatusCode)
						} else {
							attempted = true
						}
					}
				}
			}
		}
	}
	// A failed provider revocation must never leave a usable local credential behind.
	if err := b.store.DropToken(id); err != nil {
		return attempted, err
	}
	return attempted, remoteErr
}

// Disconnect deletes local tokens while retaining the discovered client configuration.
func (b *Broker) Disconnect(id string) error {
	b.cancelActive(id, "connection was disconnected")
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	return b.store.DropToken(id)
}

// Forget deletes the complete local connection record and exported token file.
func (b *Broker) Forget(id string) error {
	b.cancelActive(id, "connection was forgotten")
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	return b.store.Delete(id)
}

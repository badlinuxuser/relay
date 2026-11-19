package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"relay/internal/oauth"
	"relay/internal/watch"
)

// State of one server.
type State string

const (
	StateConfigured   State = "configured"    // in mcp.json, not started (also: waiting for approval)
	StateConnecting   State = "connecting"    // starting or handshaking
	StateConnected    State = "connected"     // tools are available
	StateAuthRequired State = "auth_required" // the server wants the user to sign in
	StateNeedsSetup   State = "needs_setup"   // OAuth endpoints are known but no client registration is configured
	StateDisconnected State = "disconnected"  // stopped by the user, disabled, or the connection dropped
	StateError        State = "error"         // could not start or connect (see Error)
)

// Where an entry came from.
const (
	SourceFile   = "mcp.json"
	SourceLegacy = "config.toml" // the old [[mcp]] section; still works
)

// ToolRef is one tool of one connected server, as the agent sees it.
type ToolRef struct {
	Server      string
	Name        string
	Description string
	Schema      json.RawMessage
}

type ToolStatus struct {
	Name        string
	Description string
	Disabled    bool
}

// Status is what the CLI, the UI and the side panel show about a server.
type Status struct {
	ID            string
	Name          string
	Type          string
	Target        string
	Source        string
	State         State
	Error         string
	Warning       string
	Trusted       bool // for a stdio server: the user approved this exact command
	Disabled      bool
	Kind          string // tool | connector
	OAuth         bool   // the entry has an "oauth" block
	OAuthProvider string
	SignedIn      bool // there is a token (or a refresh token)
	Tools         []ToolStatus
	DisabledTools []string
}

// StatusOf describes an entry that is not (necessarily) running.
func StatusOf(id string, c ServerConfig, source string, trust *Trust) Status {
	name := c.Name
	if name == "" {
		name = id
	}
	st := Status{
		ID: id, Name: name, Type: c.Type, Target: Target(c), Source: source, Kind: c.Kind, OAuth: c.OAuth != nil,
		State: StateConfigured, Warning: Warning(c), Disabled: c.Disabled,
		DisabledTools: c.DisabledTools,
	}
	if c.OAuth != nil {
		st.OAuthProvider = c.OAuth.Provider
	}
	st.Trusted = c.Type != TypeStdio || source == SourceLegacy || trust.Trusted(id, Fingerprint(c))
	if c.Disabled {
		st.State = StateDisconnected
	}
	return st
}

type entry struct {
	cfg    ServerConfig
	source string
}

type server struct {
	id     string
	cfg    ServerConfig
	source string
	state  State
	err    string
	client *Client
	gen    int  // bumped by every start, stop and change: a late result of an older attempt is dropped
	paused bool // the user disconnected it; stays so until Connect

	authMeta string // resource_metadata address from the last 401 answer (for the sign-in discovery)
}

// Manager keeps the servers of mcp.json (and the old [[mcp]] entries) running. It watches
// mcp.json and applies changes while Relay runs: a new entry is started, a removed one
// stopped, a changed one restarted, an unchanged one left alone. A file with an error is
// ignored as a whole and the last good configuration stays in force.
type preparedAuthorization struct {
	cfg         oauth.Config
	fingerprint string
	created     time.Time
}

type Manager struct {
	path      string
	trustPath string
	trust     *Trust
	legacy    map[string]ServerConfig

	mu       sync.Mutex
	servers  map[string]*server
	loadErr  string
	loaded   bool
	onChange func()

	broker   *oauth.Broker
	redirect func() string // the callback address of the daemon
	prepared map[string]preparedAuthorization

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager makes a manager for mcp.json at path. legacy are the servers of the old
// [[mcp]] section of config.toml (may be nil).
func NewManager(path, trustPath string, legacy map[string]ServerConfig) *Manager {
	return &Manager{
		path: path, trustPath: trustPath, trust: OpenTrust(trustPath),
		legacy: legacy, servers: map[string]*server{}, prepared: map[string]preparedAuthorization{},
	}
}

// OnChange sets a function that is called (without locks held) whenever a state changes.
func (m *Manager) OnChange(f func()) {
	m.mu.Lock()
	m.onChange = f
	m.mu.Unlock()
}

func (m *Manager) changed() {
	m.mu.Lock()
	f := m.onChange
	m.mu.Unlock()
	if f != nil {
		f()
	}
}

func (m *Manager) Path() string { return m.path }

// LoadError is the problem with mcp.json that is currently being ignored ("" if none).
func (m *Manager) LoadError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadErr
}

// Start reads mcp.json, starts the servers in the background and begins to watch the file.
func (m *Manager) Start() {
	m.Reload()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		sig := func() string {
			connectors := filepath.Join(filepath.Dir(m.path), "connectors")
			return watch.FileSig(m.path) + "|" + watch.FileSig(m.trustPath) + "|" + watch.DirSig(connectors, "server.py")
		}
		watch.Poll(ctx, time.Second, sig, m.Reload)
	}()
}

// Close stops the watcher and every server.
func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	m.mu.Lock()
	var clients []*Client
	for _, s := range m.servers {
		s.gen++
		if s.client != nil {
			clients = append(clients, s.client)
			s.client = nil
		}
	}
	m.mu.Unlock()
	closeAll(clients)
}

func closeAll(clients []*Client) {
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *Client) { defer wg.Done(); c.Close() }(c)
	}
	wg.Wait()
}

func (m *Manager) desired() (map[string]entry, error) {
	out := map[string]entry{}
	for id, c := range m.legacy {
		out[id] = entry{c, SourceLegacy}
	}
	f, err := LoadFile(m.path)
	if err != nil {
		return out, err
	}
	for id, c := range f.Servers {
		// Servers created by Relay have their entry point inside connectors/<id>.
		// If that file is manually removed, do not advertise a stale server as installed.
		if managedEntryPointMissing(filepath.Dir(m.path), id, c) {
			continue
		}
		out[id] = entry{c, SourceFile}
	}
	return out, nil
}

// managedEntryPointMissing recognizes only the exact layout written by create_mcp_server.
// External stdio commands and remote servers remain valid without a Relay-owned source folder.
func managedEntryPointMissing(home, id string, cfg ServerConfig) bool {
	if cfg.Type != TypeStdio || len(cfg.Args) == 0 || !ValidID(id) {
		return false
	}
	want := filepath.Join(home, "connectors", id, "server.py")
	if filepath.Clean(cfg.Args[0]) != filepath.Clean(want) {
		return false
	}
	fi, err := os.Stat(want)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil { // temporary I/O or permission errors are not proof of deletion
		return false
	}
	return fi.IsDir()
}

// Reload rereads mcp.json and the approvals and brings the running servers in line.
func (m *Manager) Reload() {
	m.trust.Reload()
	want, err := m.desired()

	m.mu.Lock()
	if err != nil {
		m.loadErr = err.Error()
		if m.loaded {
			m.mu.Unlock()
			m.changed()
			return // keep what works
		}
	} else {
		m.loadErr = ""
	}
	m.loaded = true

	var stale []*Client
	for id, s := range m.servers {
		if _, ok := want[id]; ok {
			continue
		}
		s.gen++
		if s.client != nil {
			stale = append(stale, s.client)
			s.client = nil
		}
		delete(m.servers, id)
	}
	for id, e := range want {
		s := m.servers[id]
		if s == nil {
			m.servers[id] = &server{id: id, cfg: e.cfg, source: e.source, state: StateConfigured}
			continue
		}
		sameConn := Fingerprint(s.cfg) == Fingerprint(e.cfg) && s.cfg.Disabled == e.cfg.Disabled && s.source == e.source
		if sameConn && s.cfg.InheritEnv == e.cfg.InheritEnv {
			s.cfg = e.cfg // the name or the list of disabled tools may have changed
			continue
		}
		s.gen++
		if s.client != nil {
			stale = append(stale, s.client)
			s.client = nil
		}
		s.cfg, s.source = e.cfg, e.source
		s.state, s.err, s.paused = StateConfigured, "", false
	}
	var start []string
	for id, s := range m.servers {
		if s.state != StateConfigured || s.paused {
			continue
		}
		// Do not launch an unapproved stdio process even briefly. In particular, a
		// concurrent Trust() must not race an auto-connect that was queued by Reload().
		if s.cfg.Type == TypeStdio && s.source != SourceLegacy && !m.trust.Trusted(id, Fingerprint(s.cfg)) {
			continue
		}
		start = append(start, id)
	}
	m.mu.Unlock()

	if len(stale) > 0 {
		go closeAll(stale)
	}
	for _, id := range start {
		go func(id string) { _ = m.connect(id, false) }(id)
	}
	m.changed()
}

// connect starts one server. manual is true when the user asked for it: then a disabled or
// unapproved server is an error instead of being skipped quietly.
func (m *Manager) connect(id string, manual bool) error {
	m.mu.Lock()
	s := m.servers[id]
	if s == nil {
		m.mu.Unlock()
		return fmt.Errorf("there is no server %q in mcp.json", id)
	}
	if s.cfg.Disabled {
		s.state, s.err = StateDisconnected, ""
		m.mu.Unlock()
		if manual {
			return fmt.Errorf("server %q is switched off in mcp.json (\"disabled\": true)", id)
		}
		return nil
	}
	if s.paused && !manual {
		m.mu.Unlock()
		return nil
	}
	if s.state == StateConnected && s.client != nil {
		m.mu.Unlock()
		return nil
	}
	if s.cfg.Type == TypeStdio && s.source != SourceLegacy && !m.trust.Trusted(id, Fingerprint(s.cfg)) {
		s.state, s.err = StateConfigured, ""
		target := Target(s.cfg)
		m.mu.Unlock()
		if manual {
			return fmt.Errorf("server %q runs a program on this computer and is not approved yet:\n  %s\napprove it with: relay mcp trust %s", id, target, id)
		}
		return nil
	}
	if s.state == StateConnecting {
		m.mu.Unlock()
		if manual {
			return fmt.Errorf("server %q is already connecting", id)
		}
		return nil
	}
	s.paused = false
	s.gen++
	gen := s.gen
	s.state, s.err = StateConnecting, ""
	cfg := s.cfg
	m.mu.Unlock()
	m.changed()

	cl, err := m.dial(id, cfg)

	m.mu.Lock()
	if m.servers[id] != s || s.gen != gen {
		m.mu.Unlock() // removed, changed or stopped while we were connecting
		if cl != nil {
			cl.Close()
		}
		return errors.New("the configuration changed while connecting")
	}
	var auth *AuthError
	switch {
	case err == nil:
		s.client, s.state, s.err = cl, StateConnected, ""
		go m.watchClient(s, cl)
	case errors.As(err, &auth):
		s.state, s.err, s.authMeta = StateAuthRequired, "sign-in required", auth.ResourceMetadata
	default:
		s.state, s.err = StateError, err.Error()
	}
	m.mu.Unlock()
	m.changed()
	return err
}

// watchClient notices when a connection is lost (the program exited, the session ended).
func (m *Manager) watchClient(s *server, cl *Client) {
	<-cl.Done()
	m.mu.Lock()
	if s.client != cl {
		m.mu.Unlock() // we closed it ourselves
		return
	}
	s.client = nil
	s.state = StateDisconnected
	s.err = cl.Reason()
	m.mu.Unlock()
	m.changed()
}

// Connect starts a server and waits for the result.
func (m *Manager) Connect(id string) error { return m.connect(id, true) }

// Disconnect stops a server and keeps it stopped until Connect.
func (m *Manager) Disconnect(id string) error {
	m.mu.Lock()
	s := m.servers[id]
	if s == nil {
		m.mu.Unlock()
		return fmt.Errorf("there is no server %q in mcp.json", id)
	}
	s.gen++
	s.paused = true
	old := s.client
	s.client = nil
	s.state, s.err = StateDisconnected, ""
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}
	m.changed()
	return nil
}

func (m *Manager) Reconnect(id string) error {
	if err := m.Disconnect(id); err != nil {
		return err
	}
	return m.connect(id, true)
}

// Trust approves the current command of a stdio server and starts it.
func (m *Manager) Trust(id string) error {
	m.mu.Lock()
	s := m.servers[id]
	var cfg ServerConfig
	if s != nil {
		cfg = s.cfg
	}
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("there is no server %q in mcp.json", id)
	}
	if cfg.Type != TypeStdio {
		return fmt.Errorf("%q is a remote server: there is no local program to approve", id)
	}
	if err := m.trust.Allow(id, Fingerprint(cfg)); err != nil {
		return err
	}
	return m.connect(id, true)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Tools returns the tools of all connected servers, except the ones switched off with
// "disabled_tools".
func (m *Manager) Tools() []ToolRef {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []ToolRef
	for _, id := range ids {
		s := m.servers[id]
		if s.client == nil {
			if s.state == StateAuthRequired && m.broker != nil {
				// the server lists its tools only after the user signed in: offer the way in
				name := s.cfg.Name
				if name == "" {
					name = id
				}
				out = append(out, ToolRef{Server: id, Name: SignInTool,
					Description: "Sign the user in to " + name + ". The tools of " + name + " appear after that. Opens the browser and waits."})
			}
			continue
		}
		for _, t := range s.client.Tools() {
			if contains(s.cfg.DisabledTools, t.Name) {
				continue
			}
			out = append(out, ToolRef{Server: id, Name: t.Name, Description: t.Description, Schema: t.InputSchema})
		}
	}
	return out
}

// CallTool runs a tool of a server. A server that dropped is started again first. When the
// user has to sign in first, the error is an *AuthRequiredError: the caller opens the sign-in,
// waits, and calls again (the call was not made, so repeating it is safe).
func (m *Manager) CallTool(ctx context.Context, id, tool string, args json.RawMessage) (string, error) {
	m.mu.Lock()
	s := m.servers[id]
	var cl *Client
	var state State
	var off bool
	var cfg ServerConfig
	if s != nil {
		cl, state, off, cfg = s.client, s.state, contains(s.cfg.DisabledTools, tool), s.cfg
	}
	m.mu.Unlock()
	if s == nil {
		return "", fmt.Errorf("the MCP server %q is not in mcp.json any more", id)
	}
	if tool == SignInTool && cl == nil {
		if m.broker == nil || m.NeedsSignIn(id) {
			return "", &AuthRequiredError{Server: id, SafeToRetry: true}
		}
		if err := m.connect(id, true); err != nil { // reconnect, now with the token
			return "", err
		}
		return "Signed in. Tools now available: " + strings.Join(m.toolNames(id), ", "), nil
	}
	if off {
		return "", fmt.Errorf("the tool %q of %q is switched off in mcp.json (\"disabled_tools\")", tool, id)
	}
	if cl == nil && (state == StateDisconnected || state == StateConfigured) {
		if err := m.connect(id, false); err != nil {
			var auth *AuthError
			if errors.As(err, &auth) {
				return "", &AuthRequiredError{Server: id, SafeToRetry: true}
			}
			return "", err
		}
		m.mu.Lock()
		cl = s.client
		m.mu.Unlock()
	}
	if cl == nil {
		if state == StateAuthRequired {
			return "", &AuthRequiredError{Server: id, SafeToRetry: true}
		}
		return "", fmt.Errorf("the MCP server %q is not connected (%s); see: relay mcp list", id, m.describeState(id))
	}
	usedCredential := false
	if m.broker != nil && cfg.Type == TypeHTTP {
		static := staticOAuth(cfg)
		resource := ""
		if static != nil {
			resource = static.Resource
		}
		_, ok, err := m.broker.TokenForResource(ctx, id, static, resource, cfg.URL)
		if err != nil {
			return "", err
		}
		if cfg.OAuth != nil && !ok {
			return "", &AuthRequiredError{Server: id, SafeToRetry: true}
		}
		usedCredential = ok
	} else if cfg.OAuth != nil && m.broker != nil { // the stdio SDK reads the refreshed access-token file
		static := staticOAuth(cfg)
		target := "stdio://" + id
		_, ok, err := m.broker.TokenForResource(ctx, id, static, static.Resource, target)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", &AuthRequiredError{Server: id, SafeToRetry: true}
		}
		usedCredential = true
	}
	text, err := cl.Call(ctx, tool, args)
	var auth *AuthError
	switch {
	case errors.As(err, &auth):
		m.mu.Lock()
		if s.client == cl {
			s.state, s.err, s.authMeta = StateAuthRequired, "sign-in required", auth.ResourceMetadata
		}
		m.mu.Unlock()
		m.changed()
		return "", &AuthRequiredError{Server: id, SafeToRetry: true}
	case err != nil && cfg.OAuth != nil && cfg.Type == TypeStdio && strings.HasPrefix(err.Error(), AuthMarker) && m.broker != nil:
		_ = m.broker.Store().DropToken(id) // the service refused the token: sign in again
		return "", &AuthRequiredError{Server: id, SafeToRetry: true}
	}
	if err == nil && m.broker != nil && usedCredential {
		_ = m.broker.MarkUsed(id)
	}
	return text, err
}

// toolNames lists the tools of a connected server.
func (m *Manager) toolNames(id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	if s := m.servers[id]; s != nil && s.client != nil {
		for _, t := range s.client.Tools() {
			out = append(out, t.Name)
		}
	}
	return out
}

// ---- sign-in -----------------------------------------------------------------

// SetOAuth connects the manager to the sign-in broker. redirect returns the address of the
// callback served by the daemon.
func (m *Manager) SetOAuth(b *oauth.Broker, redirect func() string) {
	m.broker, m.redirect = b, redirect
	b.OnSignedIn = m.afterSignIn
}

// afterSignIn connects a server that was waiting for the user to sign in.
func (m *Manager) afterSignIn(id string) {
	m.mu.Lock()
	s := m.servers[id]
	waiting := s != nil && s.client == nil
	m.mu.Unlock()
	if waiting {
		_ = m.connect(id, true)
	}
	m.changed()
}

// staticOAuth is the sign-in configuration written in mcp.json (nil if there is none).
func staticOAuth(c ServerConfig) *oauth.Config {
	o := c.OAuth
	if o == nil {
		return nil
	}
	resource := o.Resource
	sendResource := o.SendResource
	if c.Type == TypeHTTP {
		if resource == "" {
			resource = c.URL
		}
		sendResource = true
	}
	cfg := oauth.Config{
		Provider:          o.Provider,
		Issuer:            o.Issuer,
		PKCES256Supported: o.PKCES256Supported,
		RequireIssuer:     o.RequireIssuer,
		AuthorizeURL:      o.AuthorizeURL, TokenURL: o.TokenURL, Registration: o.RegistrationURL, CIMDURL: o.CIMDURL, CIMDSupported: o.CIMDURL != "", RevocationURL: o.RevocationURL, UserInfoURL: o.UserInfoURL,
		Resource: resource, SendResource: sendResource, TargetURL: c.URL, Scopes: o.Scopes, Params: o.Params,
		ClientID: os.ExpandEnv(o.ClientID), ClientSecret: os.ExpandEnv(o.ClientSecret), TokenAuthMethod: o.TokenAuthMethod,
	}
	if o.Provider != "" {
		if resolved, err := oauth.ApplyProvider(cfg, o.Provider); err == nil {
			cfg = resolved
		}
	}
	return &cfg
}

func authorizationFingerprint(cfg ServerConfig, meta string) string {
	return Fingerprint(cfg) + "\x00" + meta
}

// AuthorizationDescription reports the provider, resource and scopes before any OAuth
// registration or browser flow is started. Discovery is read-only; the user must still approve.
func (m *Manager) AuthorizationDescription(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	s := m.servers[id]
	var cfg ServerConfig
	var meta string
	if s != nil {
		cfg, meta = s.cfg, s.authMeta
	}
	m.mu.Unlock()
	if s == nil {
		return "", fmt.Errorf("there is no server %q", id)
	}
	var oc *oauth.Config
	if cfg.OAuth != nil {
		oc = staticOAuth(cfg)
		if oc.Resource == "" && cfg.Type == TypeHTTP {
			oc.Resource = cfg.URL
		}
	} else if cfg.Type == TypeHTTP {
		var err error
		oc, err = oauth.Discover(ctx, nil, cfg.URL, meta)
		if err != nil {
			return "", err
		}
		oc.TargetURL = cfg.URL
	} else {
		return "", fmt.Errorf("%q has no OAuth configuration", id)
	}
	if cfg.Type == TypeStdio {
		oc.TargetURL = "stdio://" + id
	}
	m.applyStoredClient(id, oc)
	if oc.ClientID != "" {
		if err := oc.ValidateClientAuth(); err != nil {
			return "", err
		}
	}
	// Reuse exactly the metadata/configuration shown to the user after consent; do not
	// rediscover endpoints or scopes between the approval prompt and authorization.
	m.mu.Lock()
	current := m.servers[id]
	if current == nil || authorizationFingerprint(current.cfg, current.authMeta) != authorizationFingerprint(cfg, meta) {
		m.mu.Unlock()
		return "", errors.New("MCP configuration changed while preparing OAuth consent")
	}
	m.prepared[id] = preparedAuthorization{cfg: *oc, fingerprint: authorizationFingerprint(cfg, meta), created: time.Now()}
	m.mu.Unlock()
	providerName := "OAuth provider"
	if cfg.OAuth != nil && cfg.OAuth.Provider != "" {
		if p, ok := oauth.Provider(cfg.OAuth.Provider); ok {
			providerName = p.Name
		}
	}
	if oc.Issuer != "" {
		providerName = oc.Issuer
	}
	scopes := "not explicitly specified; provider defaults may apply"
	if len(oc.Scopes) > 0 {
		scopes = strings.Join(oc.Scopes, ", ")
	}
	if len(oc.SupportedScopes) > 0 {
		scopes += "\nProvider-supported scopes (not all requested): " + strings.Join(oc.SupportedScopes, ", ")
	}
	resource := oc.Resource
	if resource == "" {
		resource = "not specified"
	}
	return fmt.Sprintf("Connect %s for MCP server %q?\nResource: %s\nRequested scopes: %s\nRelay opens the provider's official page; your password stays with the provider. This grants account access only, not permission for tool writes or destructive actions.", providerName, id, resource, scopes), nil
}

// Authorize starts the sign-in of a server. It returns the address to open in the browser and
// a function that waits until the user has finished (or the context ends).
func (m *Manager) Authorize(ctx context.Context, id string) (string, func(context.Context) error, error) {
	if m.broker == nil || m.redirect == nil {
		return "", nil, errors.New("signing in is not available in this process")
	}
	m.mu.Lock()
	s := m.servers[id]
	var cfg ServerConfig
	var meta string
	if s != nil {
		cfg, meta = s.cfg, s.authMeta
	}
	prepared, hasPrepared := m.prepared[id]
	delete(m.prepared, id)
	m.mu.Unlock()
	if s == nil {
		return "", nil, fmt.Errorf("there is no server %q in mcp.json", id)
	}
	if !hasPrepared {
		return "", nil, errors.New("OAuth consent description must be fetched and approved before authorization can start")
	}
	if time.Since(prepared.created) > 5*time.Minute || prepared.fingerprint != authorizationFingerprint(cfg, meta) {
		return "", nil, errors.New("OAuth requirements changed or consent expired; review the authorization prompt again")
	}
	return m.broker.Begin(ctx, id, prepared.cfg, cfg.OAuth == nil, m.redirect())
}

// applyStoredClient overlays credentials entered in the Connections UI. The encrypted
// OAuth vault is authoritative; credentials in mcp.json remain a backwards-compatible fallback.
func (m *Manager) applyStoredClient(id string, cfg *oauth.Config) {
	if m.broker == nil || cfg == nil {
		return
	}
	rec, err := m.broker.Store().Load(id)
	if err != nil || rec.Config == nil {
		return
	}
	if len(rec.Config.UserScopes) > 0 {
		cfg.UserScopes = append([]string(nil), rec.Config.UserScopes...)
		cfg.Scopes = append([]string(nil), rec.Config.UserScopes...)
	}
	if rec.Config.ClientID != "" {
		cfg.ClientID = rec.Config.ClientID
		cfg.ClientSecret = rec.Config.ClientSecret
		if rec.Config.TokenAuthMethod != "" {
			cfg.TokenAuthMethod = rec.Config.TokenAuthMethod
		}
	}
}

// SaveClientCredentials stores OAuth client credentials in Relay's encrypted vault.
func (m *Manager) SaveClientCredentials(id, clientID, clientSecret, authMethod string) error {
	m.mu.Lock()
	s := m.servers[id]
	if s == nil || (s.cfg.OAuth == nil && s.cfg.Type != TypeHTTP) {
		m.mu.Unlock()
		return fmt.Errorf("%q is not an OAuth-capable MCP server", id)
	}
	var cfg *oauth.Config
	if s.cfg.OAuth != nil {
		cfg = staticOAuth(s.cfg)
	}
	m.mu.Unlock()
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	if clientID == "" {
		return errors.New("client_id is required")
	}
	if authMethod == "" && cfg != nil {
		authMethod = cfg.TokenAuthMethod
	}
	if authMethod == "" {
		authMethod = "none"
	}
	if authMethod != "none" && authMethod != "client_secret_basic" && authMethod != "client_secret_post" {
		return errors.New("token auth method must be none, client_secret_basic or client_secret_post")
	}
	if authMethod != "none" && clientSecret == "" {
		return errors.New("client_secret is required for the selected token auth method")
	}
	if authMethod == "none" {
		clientSecret = ""
	}
	rec, err := m.broker.Store().Load(id)
	if err != nil {
		return err
	}
	if rec.Config == nil {
		if cfg != nil {
			rec.Config = cfg
		} else {
			rec.Config = &oauth.Config{}
		}
	}
	rec.Config.ClientID = clientID
	rec.Config.ClientSecret = clientSecret
	rec.Config.TokenAuthMethod = authMethod
	return m.broker.Store().Save(id, rec)
}

// SaveClientScopes stores the scopes chosen in Connections in the encrypted vault. They replace
// the scopes of mcp.json for the next sign-in; an existing token keeps its old scopes until then.
func (m *Manager) SaveClientScopes(id string, scopes []string) error {
	m.mu.Lock()
	s := m.servers[id]
	if s == nil || (s.cfg.OAuth == nil && s.cfg.Type != TypeHTTP) {
		m.mu.Unlock()
		return fmt.Errorf("%q is not an OAuth-capable MCP server", id)
	}
	var cfg *oauth.Config
	if s.cfg.OAuth != nil {
		cfg = staticOAuth(s.cfg)
	}
	m.mu.Unlock()
	clean := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, sc := range scopes {
		sc = strings.TrimSpace(sc)
		if sc == "" || seen[sc] {
			continue
		}
		if len(sc) > 200 || strings.ContainsAny(sc, " \t\r\n\"\\") {
			return fmt.Errorf("invalid scope %q", sc)
		}
		seen[sc] = true
		clean = append(clean, sc)
	}
	rec, err := m.broker.Store().Load(id)
	if err != nil {
		return err
	}
	if rec.Config == nil {
		if cfg != nil {
			rec.Config = cfg
		} else {
			rec.Config = &oauth.Config{}
		}
	}
	rec.Config.UserScopes = clean
	return m.broker.Store().Save(id, rec)
}

// NeedsSignIn reports whether the server cannot be used before the user signs in.
func (m *Manager) NeedsSignIn(id string) bool {
	m.mu.Lock()
	s := m.servers[id]
	var cfg ServerConfig
	var state State
	if s != nil {
		cfg, state = s.cfg, s.state
	}
	m.mu.Unlock()
	if s == nil || m.broker == nil {
		return false
	}
	if cfg.OAuth != nil {
		static := staticOAuth(cfg)
		resource, target := static.Resource, cfg.URL
		if cfg.Type == TypeStdio {
			target = "stdio://" + id
		}
		if cfg.Type == TypeHTTP && resource == "" {
			resource = cfg.URL
		}
		return !m.broker.HasBoundCredential(id, resource, target)
	}
	if state == StateAuthRequired {
		return true
	}
	if cfg.Type == TypeHTTP && m.broker.Store().SignedIn(id) {
		return !m.broker.HasBoundCredential(id, "", cfg.URL)
	}
	return false
}

// Forget removes what Relay remembers about a server that was taken out of mcp.json: the
// approval of its command, and the token and the registration of its sign-in.
func (m *Manager) Forget(id string) {
	_ = m.trust.Forget(id)
	if m.broker != nil {
		_ = m.broker.Store().Delete(id)
	}
}

// dial opens a connection with what the server needs from Relay: the token file for a
// program with OAuth settings, the token itself for a remote server.
func (m *Manager) dial(id string, cfg ServerConfig) (*Client, error) {
	var token func(context.Context) (string, error)
	if m.broker != nil {
		static := staticOAuth(cfg)
		switch cfg.Type {
		case TypeHTTP:
			token = func(ctx context.Context) (string, error) {
				resource := ""
				if static != nil {
					resource = static.Resource
					if resource == "" {
						resource = cfg.URL
					}
				}
				t, ok, err := m.broker.TokenForResource(ctx, id, static, resource, cfg.URL)
				if err != nil || !ok {
					return "", err
				}
				return t, nil
			}
		case TypeStdio:
			if cfg.OAuth != nil {
				env := make(map[string]string, len(cfg.Env)+1)
				for k, v := range cfg.Env {
					env[k] = v
				}
				env["RELAY_OAUTH_TOKEN_FILE"] = m.broker.Store().TokenFile(id)
				cfg.Env = env
			}
		}
	}
	return DialWith(cfg, token)
}

func (m *Manager) describeState(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.servers[id]
	if s == nil {
		return "unknown"
	}
	if s.err != "" {
		return string(s.state) + ": " + s.err
	}
	return string(s.state)
}

// Status lists all servers, sorted by id.
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Status, 0, len(ids))
	for _, id := range ids {
		s := m.servers[id]
		st := StatusOf(id, s.cfg, s.source, m.trust)
		st.State, st.Error = s.state, s.err
		if s.cfg.OAuth != nil {
			oc := staticOAuth(s.cfg)
			clientConfigured := oc.ClientID != ""
			if !clientConfigured && m.broker != nil {
				if rec, err := m.broker.Store().Load(id); err == nil && rec.Config != nil {
					clientConfigured = rec.Config.ClientID != ""
				}
			}
			if !clientConfigured && oc.Registration == "" && oc.CIMDURL == "" && (st.State != StateDisconnected && st.State != StateConnecting) {
				st.State = StateNeedsSetup
			}
		}
		if m.broker != nil && (s.cfg.OAuth != nil || s.state == StateAuthRequired || s.cfg.Type == TypeHTTP) {
			st.SignedIn = m.broker.Store().SignedIn(id)
		}
		if s.client != nil {
			for _, t := range s.client.Tools() {
				st.Tools = append(st.Tools, ToolStatus{Name: t.Name, Description: t.Description, Disabled: contains(s.cfg.DisabledTools, t.Name)})
			}
		}
		out = append(out, st)
	}
	return out
}

// Dial connects to a server described by cfg and performs the handshake. The caller owns
// the client and must Close it. It does not check approvals: callers do that.
func Dial(cfg ServerConfig) (*Client, error) { return DialWith(cfg, nil) }

// DialWith is Dial for a remote server that needs a bearer token: token is asked before each
// request (nil: no token).
func DialWith(cfg ServerConfig, token func(context.Context) (string, error)) (*Client, error) {
	timeout := 30 * time.Second
	var t Transport
	var st *stdioTransport
	switch cfg.Type {
	case TypeStdio:
		timeout = 90 * time.Second // the first run of an npx package downloads it
		var err error
		if st, err = newStdio(cfg); err != nil {
			return nil, err
		}
		t = st
	case TypeHTTP:
		h := newHTTP(cfg)
		h.token = token
		t = h
	default:
		return nil, fmt.Errorf("type %q is not supported", cfg.Type)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, err := Connect(ctx, t)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("no answer within %s", timeout)
			if st != nil {
				if line := st.errs.last(); line != "" {
					err = fmt.Errorf("no answer within %s (the program says: %s)", timeout, line)
				}
			}
		}
		_ = t.Close()
		return nil, err
	}
	return c, nil
}

var reNotID = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// LegacyServer converts an [[mcp]] entry of config.toml. Such servers keep working as
// before, including the full environment of Relay.
func LegacyServer(name, command string, args, env []string) (string, ServerConfig, bool) {
	id := strings.Trim(reNotID.ReplaceAllString(name, "-"), "-_")
	if len(id) > 32 {
		id = id[:32]
	}
	if id == "" || command == "" {
		return "", ServerConfig{}, false
	}
	c := ServerConfig{Name: name, Type: TypeStdio, Command: command, Args: args, InheritEnv: true}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			if c.Env == nil {
				c.Env = map[string]string{}
			}
			c.Env[k] = v
		}
	}
	return id, c, ValidID(id)
}

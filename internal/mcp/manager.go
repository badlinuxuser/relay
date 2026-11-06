package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"relay/internal/watch"
)

// State of one server.
type State string

const (
	StateConfigured   State = "configured"    // in mcp.json, not started (also: waiting for approval)
	StateConnecting   State = "connecting"    // starting or handshaking
	StateConnected    State = "connected"     // tools are available
	StateAuthRequired State = "auth_required" // the server wants the user to sign in
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
		ID: id, Name: name, Type: c.Type, Target: Target(c), Source: source,
		State: StateConfigured, Warning: Warning(c), Disabled: c.Disabled,
		DisabledTools: c.DisabledTools,
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
}

// Manager keeps the servers of mcp.json (and the old [[mcp]] entries) running. It watches
// mcp.json and applies changes while Relay runs: a new entry is started, a removed one
// stopped, a changed one restarted, an unchanged one left alone. A file with an error is
// ignored as a whole and the last good configuration stays in force.
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

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager makes a manager for mcp.json at path. legacy are the servers of the old
// [[mcp]] section of config.toml (may be nil).
func NewManager(path, trustPath string, legacy map[string]ServerConfig) *Manager {
	return &Manager{
		path: path, trustPath: trustPath, trust: OpenTrust(trustPath),
		legacy: legacy, servers: map[string]*server{},
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
		sig := func() string { return watch.FileSig(m.path) + "|" + watch.FileSig(m.trustPath) }
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
		out[id] = entry{c, SourceFile}
	}
	return out, nil
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
		if s.state == StateConfigured && !s.paused {
			start = append(start, id)
		}
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
	if s.state == StateConnecting {
		m.mu.Unlock()
		if manual {
			return fmt.Errorf("server %q is already connecting", id)
		}
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
	s.paused = false
	s.gen++
	gen := s.gen
	s.state, s.err = StateConnecting, ""
	cfg := s.cfg
	m.mu.Unlock()
	m.changed()

	cl, err := Dial(cfg)

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
		s.state, s.err = StateAuthRequired, "sign-in required"
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

// CallTool runs a tool of a server. A server that dropped is started again first.
func (m *Manager) CallTool(ctx context.Context, id, tool string, args json.RawMessage) (string, error) {
	m.mu.Lock()
	s := m.servers[id]
	var cl *Client
	var state State
	var off bool
	if s != nil {
		cl, state, off = s.client, s.state, contains(s.cfg.DisabledTools, tool)
	}
	m.mu.Unlock()
	if s == nil {
		return "", fmt.Errorf("the MCP server %q is not in mcp.json any more", id)
	}
	if off {
		return "", fmt.Errorf("the tool %q of %q is switched off in mcp.json (\"disabled_tools\")", tool, id)
	}
	if cl == nil && (state == StateDisconnected || state == StateConfigured) {
		if err := m.connect(id, false); err != nil {
			return "", err
		}
		m.mu.Lock()
		cl = s.client
		m.mu.Unlock()
	}
	if cl == nil {
		return "", fmt.Errorf("the MCP server %q is not connected (%s); see: relay mcp list", id, m.describeState(id))
	}
	text, err := cl.Call(ctx, tool, args)
	var auth *AuthError
	if errors.As(err, &auth) {
		m.mu.Lock()
		if s.client == cl {
			s.state, s.err = StateAuthRequired, "sign-in required"
		}
		m.mu.Unlock()
		m.changed()
		return "", fmt.Errorf("the MCP server %q needs you to sign in: relay mcp connect %s", id, id)
	}
	return text, err
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
func Dial(cfg ServerConfig) (*Client, error) {
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
		t = newHTTP(cfg)
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

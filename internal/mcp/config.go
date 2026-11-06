package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Transports.
const (
	TypeStdio = "stdio" // a local program speaking MCP on stdin/stdout
	TypeHTTP  = "http"  // Streamable HTTP, a remote server
)

// ServerConfig is one entry of mcp.json.
type ServerConfig struct {
	Name          string            `json:"name,omitempty"`
	Type          string            `json:"type,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	DisabledTools []string          `json:"disabled_tools,omitempty"`

	// InheritEnv passes the whole environment of Relay to the server. Only servers from the
	// old [[mcp]] section of config.toml use it (they always did); mcp.json cannot set it.
	InheritEnv bool `json:"-"`
}

// File is mcp.json: {"servers": {"<id>": {...}}}. The "mcpServers" key of Claude Desktop and
// Cursor is read too, so a config can be copied between tools; it is never written back.
type File struct {
	Servers    map[string]ServerConfig `json:"servers"`
	McpServers map[string]ServerConfig `json:"mcpServers,omitempty"`
}

var reID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)

// ValidID reports whether id can be used as a server id (it becomes part of tool names).
func ValidID(id string) bool { return reID.MatchString(id) }

// Normalize fills in what can be guessed: the type from command/url.
func Normalize(c ServerConfig) ServerConfig {
	c.Name = strings.TrimSpace(c.Name)
	c.Command = strings.TrimSpace(c.Command)
	c.URL = strings.TrimSpace(c.URL)
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "":
		switch {
		case c.URL != "" && c.Command == "":
			c.Type = TypeHTTP
		case c.Command != "":
			c.Type = TypeStdio
		}
	case "http", "streamable-http", "streamable_http", "streamablehttp":
		c.Type = TypeHTTP
	case "stdio":
		c.Type = TypeStdio
	default:
		c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	}
	return c
}

// Validate checks one entry (already normalized).
func Validate(id string, c ServerConfig) error {
	if !ValidID(id) {
		return fmt.Errorf("server %q: the id must be 1-32 letters, digits, '-' or '_', and start with a letter or digit", id)
	}
	switch c.Type {
	case TypeStdio:
		if c.Command == "" {
			return fmt.Errorf("server %q: \"command\" is required for type stdio", id)
		}
		if strings.ContainsAny(c.Command, "\r\n") {
			return fmt.Errorf("server %q: \"command\" must be one line", id)
		}
	case TypeHTTP:
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("server %q: \"url\" must be a full http:// or https:// address", id)
		}
		if u.User != nil {
			return fmt.Errorf("server %q: put credentials in \"headers\", not in the url", id)
		}
	case "":
		return fmt.Errorf("server %q: set \"command\" (a local program) or \"url\" (a remote server)", id)
	default:
		return fmt.Errorf("server %q: type %q is not supported (use \"stdio\" or \"http\")", id, c.Type)
	}
	for k := range c.Env {
		if k == "" || strings.Contains(k, "=") {
			return fmt.Errorf("server %q: bad environment variable name %q", id, k)
		}
	}
	for k, v := range c.Headers {
		if k == "" || strings.ContainsAny(k+v, "\r\n") {
			return fmt.Errorf("server %q: bad header %q", id, k)
		}
	}
	for _, t := range c.DisabledTools {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("server %q: \"disabled_tools\" has an empty name", id)
		}
	}
	return nil
}

// Warning returns a non-fatal remark about an entry ("" if none).
func Warning(c ServerConfig) string {
	if c.Type != TypeHTTP {
		return ""
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "http" {
		return ""
	}
	if isLoopback(u.Hostname()) {
		return ""
	}
	return "plain http: traffic and headers are not encrypted"
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Parse reads mcp.json content. Any error in any entry rejects the whole file, so that the
// caller keeps the last good configuration.
func Parse(data []byte) (*File, error) {
	f := &File{Servers: map[string]ServerConfig{}}
	if strings.TrimSpace(string(data)) == "" {
		return f, nil
	}
	var raw File
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("mcp.json is not valid JSON: %w", err)
	}
	for id, c := range raw.McpServers {
		f.Servers[id] = c
	}
	for id, c := range raw.Servers { // "servers" wins over "mcpServers"
		f.Servers[id] = c
	}
	var errs []error
	for _, id := range f.IDs() {
		c := Normalize(f.Servers[id])
		f.Servers[id] = c
		if err := Validate(id, c); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return f, nil
}

// LoadFile reads and checks mcp.json. A missing file is an empty configuration.
func LoadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Servers: map[string]ServerConfig{}}, nil
		}
		return nil, err
	}
	return Parse(data)
}

// IDs returns the server ids in alphabetical order.
func (f *File) IDs() []string {
	ids := make([]string, 0, len(f.Servers))
	for id := range f.Servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Save writes mcp.json atomically.
func Save(path string, f *File) error {
	out := File{Servers: f.Servers}
	if out.Servers == nil {
		out.Servers = map[string]ServerConfig{}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}

func writeAtomic(path string, data []byte) error {
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

// Fingerprint identifies what a server runs or connects to. Changing the command, the
// arguments, the environment, the url or the headers changes it; the name and the list of
// disabled tools do not. A stdio server must be approved for its current fingerprint.
func Fingerprint(c ServerConfig) string {
	v := struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}{c.Type, c.Command, c.Args, c.Env, c.URL, c.Headers}
	b, _ := json.Marshal(v) // map keys are sorted by encoding/json
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Target is a short description of what the server runs or connects to.
func Target(c ServerConfig) string {
	if c.Type == TypeHTTP {
		return c.URL
	}
	parts := append([]string{c.Command}, c.Args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t\"") {
			parts[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(parts, " ")
}

// baseEnv are the variables a server may inherit from Relay: enough to find programs,
// a home folder, a proxy and certificates, but not the API keys that Relay may have.
var baseEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TERM",
	"TMPDIR", "TMP", "TEMP", "TZ",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
	"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "APPDATA",
	"LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "HOMEDRIVE", "HOMEPATH",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
}

// buildEnv makes the environment of a stdio server: the safe base variables, then the
// "env" of the entry. Values may refer to Relay's own environment as ${NAME}, so a secret can
// stay out of mcp.json: "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}.
func buildEnv(c ServerConfig) []string {
	if c.InheritEnv {
		env := os.Environ()
		for k, v := range c.Env {
			env = append(env, k+"="+os.ExpandEnv(v))
		}
		return env
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		for _, b := range baseEnv {
			if strings.EqualFold(k, b) {
				env = append(env, kv)
				break
			}
		}
	}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+os.ExpandEnv(c.Env[k]))
	}
	return env
}

// expandHeaders applies ${NAME} expansion to header values.
func expandHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = os.ExpandEnv(v)
	}
	return out
}

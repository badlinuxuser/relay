package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Transport carries JSON-RPC 2.0 messages to one MCP server.
type Transport interface {
	// Call sends a request and waits for its response (the "result" part).
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	// Notify sends a message that has no response.
	Notify(ctx context.Context, method string, params any) error
	// Close stops the server or ends the session. It is safe to call twice.
	Close() error
	// Done is closed when the connection is gone (the program exited, the session ended).
	Done() <-chan struct{}
	// Reason says why the connection is gone ("" while it is alive).
	Reason() string
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

// rpcMsg is any JSON-RPC message: request, notification, response.
type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (m rpcMsg) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }
func (m rpcMsg) isRequest() bool  { return m.Method != "" && len(m.ID) > 0 }

// numericID returns the id of a response to one of our requests (we only send numbers).
func (m rpcMsg) numericID() (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(string(m.ID)), 10, 64)
	return n, err == nil
}

func newRequest(id int64, method string, params any) (rpcMsg, error) {
	msg := rpcMsg{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return msg, err
		}
		msg.Params = b
	}
	return msg, nil
}

func newNotification(method string, params any) (rpcMsg, error) {
	msg := rpcMsg{JSONRPC: "2.0", Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return msg, err
		}
		msg.Params = b
	}
	return msg, nil
}

// replyToServer answers a request that the server sent to us. Relay offers no client
// features (no roots, no sampling), so only ping succeeds.
func replyToServer(req rpcMsg) rpcMsg {
	out := rpcMsg{JSONRPC: "2.0", ID: req.ID}
	if req.Method == "ping" {
		out.Result = json.RawMessage(`{}`)
		return out
	}
	out.Error = &rpcError{Code: -32601, Message: "method not supported by this client"}
	return out
}

// tail keeps the last lines a program wrote to stderr, to explain why it failed.
type tail struct {
	mu    sync.Mutex
	lines []string
}

func (t *tail) add(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	t.mu.Lock()
	t.lines = append(t.lines, s)
	if len(t.lines) > 8 {
		t.lines = t.lines[len(t.lines)-8:]
	}
	t.mu.Unlock()
}

// last returns the most recent line, cut to a sensible length.
func (t *tail) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return ""
	}
	s := t.lines[len(t.lines)-1]
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// AuthError means the server wants the user to sign in (HTTP 401).
type AuthError struct {
	// ResourceMetadata is the address of the OAuth protected-resource metadata, from the
	// WWW-Authenticate header ("" if the server did not say).
	ResourceMetadata string
	Header           string
}

func (e *AuthError) Error() string { return "authorization required" }

// parseResourceMetadata extracts resource_metadata="..." from a WWW-Authenticate header.
func parseResourceMetadata(h string) string {
	const key = "resource_metadata="
	i := strings.Index(h, key)
	if i < 0 {
		return ""
	}
	rest := h[i+len(key):]
	if strings.HasPrefix(rest, `"`) {
		rest = rest[1:]
		if j := strings.Index(rest, `"`); j >= 0 {
			return rest[:j]
		}
		return ""
	}
	if j := strings.IndexAny(rest, ", "); j >= 0 {
		return rest[:j]
	}
	return rest
}

func exitError(reason string) error {
	if reason == "" {
		reason = "the connection was closed"
	}
	return fmt.Errorf("%s", reason)
}

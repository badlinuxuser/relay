package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// httpTransport speaks "Streamable HTTP": every message is a POST to one address; the
// answer is either one JSON document or a stream of server-sent events that ends with it.
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	// Token, when set, returns a bearer token for the Authorization header (OAuth).
	token func(ctx context.Context) (string, error)

	next int64 // atomic

	mu        sync.Mutex
	session   string
	version   string
	closed    bool
	done      chan struct{}
	closeOnce sync.Once
}

func newHTTP(cfg ServerConfig) *httpTransport {
	return &httpTransport{
		url:     cfg.URL,
		headers: expandHeaders(cfg.Headers),
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: 60 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				MaxIdleConns:          4,
				IdleConnTimeout:       60 * time.Second,
			},
			// Never follow a redirect: the headers (and later the token) belong to the
			// address the user wrote, not to wherever a server sends us.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		done: make(chan struct{}),
	}
}

func (t *httpTransport) Done() <-chan struct{} { return t.done }
func (t *httpTransport) Reason() string {
	select {
	case <-t.done:
		return "the session ended"
	default:
		return ""
	}
}

// SetProtocolVersion is called after initialize: later requests name the agreed version.
func (t *httpTransport) SetProtocolVersion(v string) {
	t.mu.Lock()
	t.version = v
	t.mu.Unlock()
}

func (t *httpTransport) post(ctx context.Context, msg rpcMsg) (*http.Response, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}
	if t.version != "" {
		req.Header.Set("MCP-Protocol-Version", t.version)
	}
	t.mu.Unlock()
	if t.token != nil {
		tok, err := t.token(ctx)
		if err != nil {
			return nil, err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	return t.client.Do(req)
}

// check turns a failed status into an error; the body is closed when it fails.
func (t *httpTransport) check(resp *http.Response) error {
	if resp.StatusCode < 400 {
		if resp.StatusCode >= 300 {
			resp.Body.Close()
			return fmt.Errorf("the server redirected to %s; use that address in \"url\"", resp.Header.Get("Location"))
		}
		return nil
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		h := resp.Header.Get("WWW-Authenticate")
		return &AuthError{ResourceMetadata: parseResourceMetadata(h), Header: h}
	case resp.StatusCode == http.StatusNotFound && t.hasSession():
		t.end()
		return errors.New("the session expired")
	}
	msg := strings.TrimSpace(string(snippet))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
}

func (t *httpTransport) hasSession() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.session != ""
}

func (t *httpTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if t.isClosed() {
		return nil, errors.New("the connection is closed")
	}
	id := atomic.AddInt64(&t.next, 1)
	req, err := newRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	resp, err := t.post(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := t.check(resp); err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" && method == "initialize" {
		t.mu.Lock()
		t.session = s
		t.mu.Unlock()
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "text/event-stream") {
		return t.readEvents(ctx, resp.Body, id)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return nil, err
	}
	var msg rpcMsg
	if err := json.Unmarshal(bytes.TrimSpace(data), &msg); err != nil {
		return nil, fmt.Errorf("the server did not answer with JSON-RPC (%s)", ct)
	}
	if msg.Error != nil {
		return nil, msg.Error
	}
	return msg.Result, nil
}

// readEvents reads a server-sent-event stream until the answer to request id arrives.
func (t *httpTransport) readEvents(ctx context.Context, r io.Reader, id int64) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var data []string
	flush := func() (json.RawMessage, bool, error) {
		if len(data) == 0 {
			return nil, false, nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		var msg rpcMsg
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			return nil, false, nil // an event that is not JSON-RPC: ignore
		}
		switch {
		case msg.isResponse():
			if n, ok := msg.numericID(); ok && n == id {
				if msg.Error != nil {
					return nil, true, msg.Error
				}
				return msg.Result, true, nil
			}
		case msg.isRequest():
			go func(reply rpcMsg) {
				c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if resp, err := t.post(c, reply); err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}(replyToServer(msg))
		}
		return nil, false, nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if res, ok, err := flush(); ok {
				return res, err
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if res, ok, err := flush(); ok {
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("the stream ended without an answer")
}

func (t *httpTransport) Notify(ctx context.Context, method string, params any) error {
	n, err := newNotification(method, params)
	if err != nil {
		return err
	}
	resp, err := t.post(ctx, n)
	if err != nil {
		return err
	}
	if err := t.check(resp); err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	return nil
}

func (t *httpTransport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// end marks the session as over (without telling the server).
func (t *httpTransport) end() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.closeOnce.Do(func() { close(t.done) })
}

func (t *httpTransport) Close() error {
	t.mu.Lock()
	session, was := t.session, t.closed
	t.mu.Unlock()
	t.end()
	if session != "" && !was {
		// ask the server to forget the session; no matter if it refuses
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil); err == nil {
			for k, v := range t.headers {
				req.Header.Set(k, v)
			}
			req.Header.Set("Mcp-Session-Id", session)
			if t.token != nil {
				if tok, err := t.token(ctx); err == nil && tok != "" {
					req.Header.Set("Authorization", "Bearer "+tok)
				}
			}
			if resp, err := t.client.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	t.client.CloseIdleConnections()
	return nil
}

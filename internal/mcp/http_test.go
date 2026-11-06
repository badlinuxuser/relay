package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeHTTPServer is a small MCP server over Streamable HTTP. tools/list answers as a stream.
func fakeHTTPServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			seen = append(seen, "DELETE:"+r.Header.Get("Mcp-Session-Id"))
			mu.Unlock()
			return
		}
		var msg rpcMsg
		_ = json.NewDecoder(r.Body).Decode(&msg)
		mu.Lock()
		seen = append(seen, msg.Method+":"+r.Header.Get("Mcp-Session-Id")+":"+r.Header.Get("MCP-Protocol-Version"))
		mu.Unlock()
		switch msg.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "s1")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake-http"}}}`, msg.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"tools\":[{\"name\":\"echo\",\"description\":\"d\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", msg.ID)
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"pong"}]}}`, msg.ID)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func TestHTTPHandshakeStreamAndSession(t *testing.T) {
	srv, seen := fakeHTTPServer(t)
	cl, err := Dial(Normalize(ServerConfig{URL: srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if cl.Name != "fake-http" || len(cl.Tools()) != 1 || cl.Tools()[0].Name != "echo" {
		t.Fatalf("name=%q tools=%v", cl.Name, cl.Tools())
	}
	out, err := cl.Call(context.Background(), "echo", json.RawMessage(`{"a":1}`))
	if err != nil || out != "pong" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	cl.Close()
	want := []string{
		"initialize::",
		"notifications/initialized:s1:2025-06-18",
		"tools/list:s1:2025-06-18",
		"tools/call:s1:2025-06-18",
		"DELETE:s1",
	}
	got := seen()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("requests:\n got %v\nwant %v", got, want)
	}
}

func TestHTTPUnauthorizedIsAnAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://auth.example/meta"`)
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := Dial(Normalize(ServerConfig{URL: srv.URL}))
	ae, ok := err.(*AuthError)
	if !ok || ae.ResourceMetadata != "https://auth.example/meta" {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPDoesNotFollowRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect target must not be contacted")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := Dial(Normalize(ServerConfig{URL: srv.URL})); err == nil {
		t.Fatal("a redirect must be an error")
	}
}

func TestHTTPSendsConfiguredHeaders(t *testing.T) {
	t.Setenv("RELAY_TEST_KEY", "k-123")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Api-Key")
		http.Error(w, "stop", http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, _ = Dial(Normalize(ServerConfig{URL: srv.URL, Headers: map[string]string{"X-Api-Key": "${RELAY_TEST_KEY}"}}))
	if got != "k-123" {
		t.Fatalf("header=%q", got)
	}
}

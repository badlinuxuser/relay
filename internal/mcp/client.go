// Package mcp это минимальный клиент Model Context Protocol (транспорт stdio,
// JSON-RPC 2.0 с разделителем строк): initialize, tools/list, tools/call.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type Client struct {
	Name string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan rpcResp
	tools   []ToolInfo
}

// Start запускает MCP-сервер и выполняет рукопожатие.
func Start(name, command string, args, env []string) (*Client, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: %w", name, err)
	}
	c := &Client{Name: name, cmd: cmd, stdin: stdin, pending: map[int64]chan rpcResp{}}
	go c.readLoop(stdout)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "relay", "version": "0.1.0"},
	})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp %s: initialize: %w", name, err)
	}
	_ = c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	res, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp %s: tools/list: %w", name, err)
	}
	var lr struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(res, &lr); err != nil {
		c.Close()
		return nil, err
	}
	c.tools = lr.Tools
	return c, nil
}

func (c *Client) Tools() []ToolInfo { return c.tools }

func (c *Client) Close() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
}

func (c *Client) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan rpcResp, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.send(req); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var msg rpcResp
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.ID != nil && msg.Method == "" {
			c.mu.Lock()
			ch := c.pending[*msg.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if msg.ID != nil && msg.Method != "" { // запрос от сервера (например ping)
			_ = c.send(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": map[string]any{}})
		}
	}
	// сервер завершился: будим всех ожидающих
	c.mu.Lock()
	for _, ch := range c.pending {
		select {
		case ch <- rpcResp{Error: &rpcError{Message: "mcp server exited"}}:
		default:
		}
	}
	c.mu.Unlock()
}

// Call вызывает инструмент и возвращает текстовый результат.
func (c *Client) Call(ctx context.Context, tool string, args json.RawMessage) (string, error) {
	var arguments any = map[string]any{}
	if len(args) > 0 {
		var v any
		if json.Unmarshal(args, &v) == nil {
			arguments = v
		}
	}
	res, err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	var parts []string
	for _, p := range out.Content {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if out.IsError {
		return text, errors.New(text)
	}
	return text, nil
}

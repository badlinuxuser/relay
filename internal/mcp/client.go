// Package mcp is a client for the Model Context Protocol: a Manager that keeps the servers
// of mcp.json running (local programs over stdio, remote ones over Streamable HTTP) and
// offers their tools to the agent.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ClientVersion is reported to servers in the handshake; the daemon sets it.
var ClientVersion = "dev"

// The protocol versions this client can speak, newest first.
const protocolVersion = "2025-06-18"

var supportedVersions = map[string]bool{
	"2025-06-18": true,
	"2025-03-26": true,
	"2024-11-05": true,
}

type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client is one initialized session with one server.
type Client struct {
	// Name is how the server calls itself (serverInfo.name); may be empty.
	Name string

	t     Transport
	tools []ToolInfo
}

// Connect performs the handshake (initialize, initialized) and reads the tool list.
func Connect(ctx context.Context, t Transport) (*Client, error) {
	res, err := t.Call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "relay", "version": ClientVersion},
	})
	if err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(res, &init); err != nil {
		return nil, fmt.Errorf("unexpected answer to initialize: %w", err)
	}
	if !supportedVersions[init.ProtocolVersion] {
		return nil, fmt.Errorf("the server speaks MCP version %q, which this client does not support", init.ProtocolVersion)
	}
	if p, ok := t.(interface{ SetProtocolVersion(string) }); ok {
		p.SetProtocolVersion(init.ProtocolVersion)
	}
	if err := t.Notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	c := &Client{Name: init.ServerInfo.Name, t: t}
	if len(init.Capabilities.Tools) > 0 && string(init.Capabilities.Tools) != "null" {
		if err := c.refreshTools(ctx); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
	}
	return c, nil
}

func (c *Client) refreshTools(ctx context.Context) error {
	var all []ToolInfo
	cursor := ""
	for page := 0; page < 50; page++ {
		var params any = map[string]any{}
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		res, err := c.t.Call(ctx, "tools/list", params)
		if err != nil {
			return err
		}
		var lr struct {
			Tools      []ToolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(res, &lr); err != nil {
			return err
		}
		all = append(all, lr.Tools...)
		if lr.NextCursor == "" {
			break
		}
		cursor = lr.NextCursor
	}
	c.tools = all
	return nil
}

func (c *Client) Tools() []ToolInfo     { return c.tools }
func (c *Client) Done() <-chan struct{} { return c.t.Done() }
func (c *Client) Reason() string        { return c.t.Reason() }
func (c *Client) Close()                { _ = c.t.Close() }

// Call runs a tool and returns its result as text. A result that the server marks as an
// error is returned as an error (with the text in it).
func (c *Client) Call(ctx context.Context, tool string, args json.RawMessage) (string, error) {
	var arguments any = map[string]any{}
	if len(args) > 0 {
		var v any
		if json.Unmarshal(args, &v) == nil && v != nil {
			arguments = v
		}
	}
	res, err := c.t.Call(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Data     string `json:"data"`
			URI      string `json:"uri"`
			Resource *struct {
				URI      string `json:"uri"`
				Text     string `json:"text"`
				MimeType string `json:"mimeType"`
			} `json:"resource"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
		IsError    bool            `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	var parts []string
	for _, p := range out.Content {
		switch p.Type {
		case "text":
			parts = append(parts, p.Text)
		case "image", "audio":
			parts = append(parts, fmt.Sprintf("[%s result (%s, %d bytes) is not shown]", p.Type, p.MimeType, len(p.Data)*3/4))
		case "resource":
			if p.Resource != nil && p.Resource.Text != "" {
				parts = append(parts, p.Resource.Text)
			} else if p.Resource != nil {
				parts = append(parts, "[resource "+p.Resource.URI+"]")
			}
		case "resource_link":
			parts = append(parts, "[resource "+p.URI+"]")
		}
	}
	if len(parts) == 0 && len(out.Structured) > 0 && string(out.Structured) != "null" {
		parts = append(parts, string(out.Structured))
	}
	text := strings.Join(parts, "\n")
	if out.IsError {
		if text == "" {
			text = "the tool reported an error"
		}
		return text, errors.New(text)
	}
	return text, nil
}

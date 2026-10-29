package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// OpenAI работает с любым сервером, совместимым с /chat/completions.
type OpenAI struct {
	name    string
	baseURL string
	keys    *Keys
	headers map[string]string
	http    *http.Client
}

func NewOpenAI(name, baseURL string, keys *Keys, headers map[string]string) *OpenAI {
	return &OpenAI{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		keys:    keys,
		headers: headers,
		http:    &http.Client{},
	}
}

func (c *OpenAI) Name() string { return c.name }

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMsg struct {
	Role       string       `json:"role"`
	Content    any          `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *OpenAI) buildBody(req Request) ([]byte, error) {
	msgs := make([]oaMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		om := oaMsg{Role: m.Role, ToolCallID: m.ToolCallID}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			if m.Content != "" {
				om.Content = m.Content
			}
			for _, tc := range m.ToolCalls {
				var o oaToolCall
				o.ID = tc.ID
				o.Type = "function"
				o.Function.Name = tc.Name
				o.Function.Arguments = tc.Args
				if o.Function.Arguments == "" {
					o.Function.Arguments = "{}"
				}
				om.ToolCalls = append(om.ToolCalls, o)
			}
		} else {
			om.Content = m.Content
		}
		msgs = append(msgs, om)
	}
	body := map[string]any{
		"model":          req.Model,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Schema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  schema,
				},
			})
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

func (c *OpenAI) Stream(ctx context.Context, req Request, emit func(Event) error) error {
	body, err := c.buildBody(req)
	if err != nil {
		return err
	}
	return DoWithKeys(c.keys, c.name, emit, func(key string) error {
		return c.once(ctx, key, body, emit)
	})
}

func (c *OpenAI) once(ctx context.Context, key string, body []byte, emit func(Event) error) error {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if key != "" {
		hreq.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range c.headers {
		hreq.Header.Set(k, v)
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Provider: c.name, Msg: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError(c.name, resp)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	calls := map[int]*ToolCall{}
	var order []int
	var usage Usage
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var ch oaChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue
		}
		if ch.Error != nil && ch.Error.Message != "" {
			return &Error{Provider: c.name, Msg: ch.Error.Message}
		}
		if ch.Usage != nil {
			usage = Usage{In: ch.Usage.PromptTokens, Out: ch.Usage.CompletionTokens}
		}
		for _, choice := range ch.Choices {
			if choice.Delta.Content != "" {
				if err := emit(Event{Kind: EvText, Text: choice.Delta.Content}); err != nil {
					return err
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				cur, ok := calls[tc.Index]
				if !ok {
					cur = &ToolCall{}
					calls[tc.Index] = cur
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				if tc.Function.Name != "" {
					cur.Name = tc.Function.Name
				}
				cur.Args += tc.Function.Arguments
			}
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Provider: c.name, Msg: err.Error()}
	}

	sort.Ints(order)
	for _, idx := range order {
		tc := calls[idx]
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d", idx)
		}
		if tc.Args == "" {
			tc.Args = "{}"
		}
		if err := emit(Event{Kind: EvToolCall, Call: tc}); err != nil {
			return err
		}
	}
	if usage.In > 0 || usage.Out > 0 {
		return emit(Event{Kind: EvUsage, Usage: usage})
	}
	return nil
}

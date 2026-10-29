package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Anthropic работает с Messages API (потоковый режим).
type Anthropic struct {
	name    string
	baseURL string
	keys    *Keys
	headers map[string]string
	http    *http.Client
}

func NewAnthropic(name, baseURL string, keys *Keys, headers map[string]string) *Anthropic {
	return &Anthropic{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		keys:    keys,
		headers: headers,
		http:    &http.Client{},
	}
}

func (c *Anthropic) Name() string { return c.name }

type anBlock map[string]any

type anMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// toAnthropic переводит внутренний формат: system выносится отдельно,
// результаты инструментов склеиваются в одно user-сообщение.
func toAnthropic(msgs []Message) (string, []anMsg) {
	var sys []string
	var out []anMsg
	for _, m := range msgs {
		switch m.Role {
		case "system":
			sys = append(sys, m.Content)
		case "user":
			text := m.Content
			if text == "" {
				text = " "
			}
			out = append(out, anMsg{Role: "user", Content: text})
		case "assistant":
			var blocks []anBlock
			if m.Content != "" {
				blocks = append(blocks, anBlock{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any = map[string]any{}
				if tc.Args != "" {
					var v any
					if json.Unmarshal([]byte(tc.Args), &v) == nil {
						input = v
					}
				}
				blocks = append(blocks, anBlock{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anBlock{"type": "text", "text": " "})
			}
			out = append(out, anMsg{Role: "assistant", Content: blocks})
		case "tool":
			blk := anBlock{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content}
			if n := len(out); n > 0 && out[n-1].Role == "user" {
				if prev, ok := out[n-1].Content.([]anBlock); ok && len(prev) > 0 && prev[0]["type"] == "tool_result" {
					out[n-1].Content = append(prev, blk)
					continue
				}
			}
			out = append(out, anMsg{Role: "user", Content: []anBlock{blk}})
		}
	}
	return strings.Join(sys, "\n\n"), out
}

func (c *Anthropic) buildBody(req Request) ([]byte, error) {
	system, msgs := toAnthropic(req.Messages)
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens,
		"stream":     true,
		"messages":   msgs,
	}
	if system != "" {
		body["system"] = system
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
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

func (c *Anthropic) Stream(ctx context.Context, req Request, emit func(Event) error) error {
	body, err := c.buildBody(req)
	if err != nil {
		return err
	}
	return DoWithKeys(c.keys, c.name, emit, func(key string) error {
		return c.once(ctx, key, body, emit)
	})
}

func (c *Anthropic) once(ctx context.Context, key string, body []byte, emit func(Event) error) error {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("anthropic-version", "2023-06-01")
	if key != "" {
		hreq.Header.Set("x-api-key", key)
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
	tools := map[int]*ToolCall{}
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
		var ev anEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			usage.In = ev.Message.Usage.InputTokens
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				tools[ev.Index] = &ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					if err := emit(Event{Kind: EvText, Text: ev.Delta.Text}); err != nil {
						return err
					}
				}
			case "input_json_delta":
				if tc := tools[ev.Index]; tc != nil {
					tc.Args += ev.Delta.PartialJSON
				}
			}
		case "content_block_stop":
			if tc := tools[ev.Index]; tc != nil {
				if tc.Args == "" {
					tc.Args = "{}"
				}
				if err := emit(Event{Kind: EvToolCall, Call: tc}); err != nil {
					return err
				}
				delete(tools, ev.Index)
			}
		case "message_delta":
			if ev.Usage.OutputTokens > 0 {
				usage.Out = ev.Usage.OutputTokens
			}
		case "error":
			low := strings.ToLower(ev.Error.Type + " " + ev.Error.Message)
			return &Error{Provider: c.name, Msg: ev.Error.Message,
				RateLimited: strings.Contains(low, "rate_limit") || strings.Contains(low, "quota"),
				Retryable:   strings.Contains(low, "overloaded") || strings.Contains(low, "rate_limit")}
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Provider: c.name, Msg: err.Error()}
	}
	if usage.In > 0 || usage.Out > 0 {
		return emit(Event{Kind: EvUsage, Usage: usage})
	}
	return nil
}

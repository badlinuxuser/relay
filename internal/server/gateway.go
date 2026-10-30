package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"relay/internal/config"
	"relay/internal/provider"
	"relay/internal/router"
)

// OpenAI-совместимый шлюз: любая программа может использовать Relay как
// обычный OpenAI endpoint (base_url = http://<listen>/v1, ключ = токен из daemon.json).
//
// model: relay-small | relay-medium | relay-hard | relay-maximum | auto, либо id
// модели из конфигурации. Инструменты (tools) в шлюзе пока не поддерживаются.

type gwMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type gwRequest struct {
	Model       string      `json:"model"`
	Messages    []gwMessage `json:"messages"`
	Stream      bool        `json:"stream"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature *float64    `json:"temperature"`
}

// contentText достаёт текст из content: строка или массив частей [{type:text,text}].
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return ""
}

func (s *Server) handleGatewayModels(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	var data []item
	for _, m := range config.ValidModes {
		if _, ok := s.cfg.Modes[m]; ok {
			data = append(data, item{ID: "relay-" + m, Object: "model", OwnedBy: "relay"})
		}
	}
	data = append(data, item{ID: "auto", Object: "model", OwnedBy: "relay"})
	seen := map[string]bool{}
	for _, m := range s.cfg.Models {
		seen[m.ID] = true
		data = append(data, item{ID: m.ID, Object: "model", OwnedBy: m.Provider})
	}
	// models the providers report (local Ollama, Ollama cloud, Gemini, ...)
	for _, m := range s.cat.Get(r.Context(), false).Models {
		if !seen[m.ID] {
			seen[m.ID] = true
			data = append(data, item{ID: m.ID, Object: "model", OwnedBy: m.Provider})
		}
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleGatewayChat(w http.ResponseWriter, r *http.Request) {
	var req gwRequest
	if err := readJSON(r, &req); err != nil {
		httpErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		httpErr(w, 400, "messages is required")
		return
	}

	mode, model := s.cfg.DefaultMode, ""
	switch {
	case req.Model == "" || req.Model == "auto" || req.Model == "relay":
		mode = "auto"
	case strings.HasPrefix(req.Model, "relay-"):
		mode = strings.TrimPrefix(req.Model, "relay-")
	default:
		if _, ok := s.cfg.ModelByID(req.Model); ok {
			model = req.Model
		} else {
			mode = "auto"
		}
	}

	var msgs []provider.Message
	chars := 0
	for _, m := range req.Messages {
		role := m.Role
		switch role {
		case "developer":
			role = "system"
		case "system", "user", "assistant":
		default:
			role = "user"
		}
		text := contentText(m.Content)
		chars += len(text)
		msgs = append(msgs, provider.Message{Role: role, Content: text})
	}

	chain, _, err := s.router.Resolve(mode, model, false, routerHint(chars, msgs))
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	preq := provider.Request{Messages: msgs, MaxTokens: req.MaxTokens, Temperature: req.Temperature}

	id := "chatcmpl-relay-" + randHex(4)
	created := time.Now().Unix()
	usedModel := ""
	started := false
	var full strings.Builder
	var in, out int

	flusher, _ := w.(http.Flusher)
	chunk := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": usedModel,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	_, err = s.router.Stream(r.Context(), chain, preq, func(ev provider.Event) error {
		switch ev.Kind {
		case provider.EvModel:
			usedModel = ev.Text
		case provider.EvText:
			full.WriteString(ev.Text)
			if req.Stream {
				if !started {
					started = true
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.WriteHeader(200)
					chunk(map[string]any{"role": "assistant", "content": ev.Text}, nil)
				} else {
					chunk(map[string]any{"content": ev.Text}, nil)
				}
			}
		case provider.EvUsage:
			in, out = ev.Usage.In, ev.Usage.Out
		}
		return r.Context().Err()
	})

	if err != nil {
		status := 502
		if pe, ok := err.(*provider.Error); ok && pe.RateLimited {
			status = 429
		}
		if started {
			b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": err.Error()}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			return
		}
		httpErr(w, status, err.Error())
		return
	}

	if req.Stream {
		if !started { // пустой ответ
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			chunk(map[string]any{"role": "assistant", "content": ""}, nil)
		}
		chunk(map[string]any{}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	writeJSON(w, 200, map[string]any{
		"id": id, "object": "chat.completion", "created": created, "model": usedModel,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": full.String()},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out},
	})
}

func routerHint(chars int, msgs []provider.Message) router.Hint {
	code := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "```") {
			code = true
			break
		}
	}
	return router.Hint{Chars: chars, Code: code}
}

// Package provider содержит единый интерфейс к LLM-бэкендам и два адаптера:
// openai (все OpenAI-совместимые серверы) и anthropic.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolCall это вызов инструмента, запрошенный моделью. Args это JSON-строка.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// Message это сообщение диалога во внутреннем формате.
// Role: system | user | assistant | tool.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolSpec
	MaxTokens   int
	Temperature *float64
}

type Usage struct {
	In  int
	Out int
}

type EventKind int

const (
	EvText     EventKind = iota // Text
	EvToolCall                  // Call
	EvUsage                     // Usage
	EvModel                     // Text = id модели, начавшей ответ
	EvNotice                    // Text, Tag
)

type Event struct {
	Kind  EventKind
	Text  string
	Call  *ToolCall
	Usage Usage
	Cost  float64
	Tag   string // для EvNotice: fallback | rate_limit | key_switch
}

// Error это ошибка провайдера с классификацией для роутера.
type Error struct {
	Provider    string
	Status      int
	Msg         string
	RateLimited bool // 429, исчерпана квота или баланс
	Retryable   bool // имеет смысл попробовать другой ключ или модель
}

func (e *Error) Error() string {
	if e.Provider == "" {
		return e.Msg
	}
	return fmt.Sprintf("%s: %s", e.Provider, e.Msg)
}

// Client это один бэкенд. Stream блокируется до конца ответа и вызывает emit
// для каждого события; если emit вернул ошибку, поток прерывается.
type Client interface {
	Name() string
	Stream(ctx context.Context, req Request, emit func(Event) error) error
}

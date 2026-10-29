// Package proto описывает протокол между бэкендом (relayd) и интерфейсами.
// Транспорт: HTTP + JSON, потоковые ответы через Server-Sent Events.
package proto

import (
	"time"

	"relay/internal/provider"
)

const Version = "0.1.0"

// Event это одно событие потока выполнения (SSE: "data: <json>").
//
// Type:
//
//	model        начал отвечать модель Model
//	text         фрагмент ответа в Text
//	notice       служебное сообщение (Kind: fallback | rate_limit | key_switch | info)
//	tool_call    агент вызывает инструмент Tool (Args, Desc)
//	approval     нужно подтверждение действия; CallID это id для POST /api/approvals/{id}
//	tool_result  результат инструмента в Result (IsError)
//	usage        расход токенов и стоимость (In, Out, Cost)
//	error        ошибка в Text (Kind: rate_limit | provider | internal)
//	done         выполнение завершено
type Event struct {
	Type    string  `json:"type"`
	Text    string  `json:"text,omitempty"`
	Model   string  `json:"model,omitempty"`
	Tool    string  `json:"tool,omitempty"`
	CallID  string  `json:"call_id,omitempty"`
	Args    string  `json:"args,omitempty"`
	Desc    string  `json:"desc,omitempty"`
	Result  string  `json:"result,omitempty"`
	IsError bool    `json:"is_error,omitempty"`
	In      int     `json:"in,omitempty"`
	Out     int     `json:"out,omitempty"`
	Cost    float64 `json:"cost,omitempty"`
	Kind    string  `json:"kind,omitempty"`
	Slot    int     `json:"slot,omitempty"` // режим сравнения: номер модели, начиная с 1
	Step    int     `json:"step,omitempty"`
}

// DaemonInfo лежит в <home>/daemon.json, по нему интерфейсы находят бэкенд.
type DaemonInfo struct {
	Addr  string `json:"addr"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

type SessionInfo struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Mode      string             `json:"mode"`
	Model     string             `json:"model"`
	Preset    string             `json:"preset"`
	Agent     bool               `json:"agent"`
	Workspace string             `json:"workspace"`
	Updated   time.Time          `json:"updated"`
	Messages  []provider.Message `json:"messages,omitempty"`
}

type CreateSessionReq struct {
	Mode      string `json:"mode"`
	Workspace string `json:"workspace"`
}

// SettingsReq: nil-поля не меняются.
type SettingsReq struct {
	Mode   *string `json:"mode,omitempty"`
	Model  *string `json:"model,omitempty"`
	Preset *string `json:"preset,omitempty"`
	Agent  *bool   `json:"agent,omitempty"`
}

type SendReq struct {
	Text string `json:"text"`
}

type CompareReq struct {
	Text   string   `json:"text"`
	Models []string `json:"models"`
	Preset string   `json:"preset"`
}

type ApprovalReq struct {
	Decision string `json:"decision"` // allow | always | deny
}

type ModelInfo struct {
	ID       string  `json:"id"`
	Provider string  `json:"provider"`
	Name     string  `json:"name"`
	Tier     string  `json:"tier"`
	Tools    bool    `json:"tools"`
	PriceIn  float64 `json:"price_in"`
	PriceOut float64 `json:"price_out"`
}

type ProviderInfo struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Keys          int    `json:"keys"`
	KeysAvailable int    `json:"keys_available"`
}

type ModeInfo struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

type ProcInfo struct {
	Name  string   `json:"name"`
	State string   `json:"state"` // stopped | starting | ready | failed
	Error string   `json:"error,omitempty"`
	PID   int      `json:"pid,omitempty"`
	Log   []string `json:"log,omitempty"`
}

type UsageEntry struct {
	In       int     `json:"in"`
	Out      int     `json:"out"`
	Requests int     `json:"requests"`
	Cost     float64 `json:"cost"`
}

type StatusResp struct {
	Version     string                `json:"version"`
	Workspace   string                `json:"workspace"`
	DefaultMode string                `json:"default_mode"`
	Modes       []ModeInfo            `json:"modes"`
	Models      []ModelInfo           `json:"models"`
	Providers   []ProviderInfo        `json:"providers"`
	Processes   []ProcInfo            `json:"processes"`
	Usage       map[string]UsageEntry `json:"usage"`
	Presets     []string              `json:"presets"`
	MCP         []string              `json:"mcp"`
}

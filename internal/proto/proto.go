// Package proto описывает протокол между бэкендом (процесс `relay daemon`) и интерфейсами.
// Транспорт: HTTP + JSON, потоковые ответы через Server-Sent Events.
package proto

import (
	"time"

	"relay/internal/provider"
	"relay/internal/settings"
)

const Version = "0.8.0"

// Event это одно событие потока выполнения (SSE: "data: <json>").
//
// Type:
//
//	model        начал отвечать модель Model
//	text         фрагмент ответа в Text
//	thinking     фрагмент рассуждений модели в Text (не часть ответа; приходит, только если включено в настройках)
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
	// Spent in this session.
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	Cost      float64 `json:"cost"`
	// Context is the prompt size of the newest request: what the conversation weighs now.
	// TokensIn adds up the prompts of all requests, and every request sends the whole history again.
	Context int `json:"context"`
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
	// Workspace: the folder the agent works in (a resumed session moves to the current one).
	Workspace *string `json:"workspace,omitempty"`
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
	// Kind: local (runs on this computer) | cloud. Listed: the model was found in the
	// provider's own model list (otherwise it comes from config.toml).
	Kind   string `json:"kind,omitempty"`
	Listed bool   `json:"listed,omitempty"`
}

// ModelsProvider tells how fetching the model list of one provider went.
type ModelsProvider struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Local   bool   `json:"local,omitempty"`   // a server on this computer
	Skipped string `json:"skipped,omitempty"` // "no key" | "disabled"
	Error   string `json:"error,omitempty"`
}

// ModelsResp is the answer of GET /api/models: every model the user can pick.
type ModelsResp struct {
	Models    []ModelInfo      `json:"models"`
	Providers []ModelsProvider `json:"providers"`
}

type ProviderInfo struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	NeedsKey      bool   `json:"needs_key"`
	Keys          int    `json:"keys"`
	KeysAvailable int    `json:"keys_available"`
	// KeyFrom: this provider uses the keys of another one (ollama-cloud uses ollama's).
	KeyFrom string `json:"key_from,omitempty"`
}

// KeyEntry это сохранённый ключ в безопасном виде (последние 4 символа).
type KeyEntry struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Hint  string `json:"hint"`
}

type EnvKey struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type ProviderKeys struct {
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	NeedsKey  bool       `json:"needs_key"`
	Stored    []KeyEntry `json:"stored"`
	Env       []EnvKey   `json:"env,omitempty"`
	Total     int        `json:"total"`
	Available int        `json:"available"`
	// KeyOptional: the provider works without a key, but a key unlocks more (Ollama: cloud models).
	KeyOptional bool `json:"key_optional,omitempty"`
	// Group: official | external | local (the keys page lists them in this order).
	Group string `json:"group,omitempty"`
}

type KeysResp struct {
	Providers []ProviderKeys `json:"providers"`
}

type AddKeyReq struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
	Label    string `json:"label"`
}

type ModeInfo struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
	// Effective is the chain that is really used now: Models without the ones that
	// have no key or are not installed, plus installed models in their place.
	Effective []string `json:"effective,omitempty"`
}

// ProcessReq is the optional body of POST /api/processes/{name}/start.
type ProcessReq struct {
	// Args are added to the arguments from config.toml (for llama.cpp: ["-m", "model.gguf"]).
	Args []string `json:"args,omitempty"`
}

// ProcessResp is the answer of POST /api/processes/{name}/{start|stop}.
type ProcessResp struct {
	Processes []ProcInfo `json:"processes"`
	// Already: nothing was done because the server was already running (start).
	Already bool `json:"already,omitempty"`
}

type ProcInfo struct {
	Name  string `json:"name"`
	State string `json:"state"` // stopped | starting | ready | failed
	// External: the process is running but was not started by Relay (for example a
	// system-wide Ollama service). Relay uses it as is and never stops it.
	External bool     `json:"external,omitempty"`
	Error    string   `json:"error,omitempty"`
	PID      int      `json:"pid,omitempty"`
	Log      []string `json:"log,omitempty"`
}

// ServerInfo is a local model server (Ollama, llama.cpp, LM Studio, ...) that answers right now.
type ServerInfo struct {
	Name   string `json:"name"`
	Models int    `json:"models"`
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
	Servers     []ServerInfo          `json:"servers"` // local servers that are running now
	Settings    settings.Settings     `json:"settings"`
	Usage       map[string]UsageEntry `json:"usage"`
	Presets     []string              `json:"presets"`
	MCP         []string              `json:"mcp"` // names of the connected MCP servers
	// The extensions (see GET /api/mcp and /api/skills for the full lists, with tools).
	MCPServers []MCPServerInfo `json:"mcp_servers"`
	MCPError   string          `json:"mcp_error,omitempty"` // a problem in mcp.json that is being ignored
	Skills     []SkillInfo     `json:"skills"`
}

type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// MCPServerInfo describes one MCP server of mcp.json. State is one of configured,
// connecting, connected, auth_required, disconnected, error.
type MCPServerInfo struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Type      string        `json:"type"` // stdio | http
	Target    string        `json:"target"` // the command line or the url
	Source    string        `json:"source"` // mcp.json | config.toml
	State     string        `json:"state"`
	Error     string        `json:"error,omitempty"`
	Warning   string        `json:"warning,omitempty"`
	Trusted   bool          `json:"trusted"` // stdio: the user approved this command
	Disabled  bool          `json:"disabled,omitempty"`
	ToolCount int           `json:"tool_count"`
	Tools     []MCPToolInfo `json:"tools,omitempty"`
}

// MCPResp is the answer of GET /api/mcp and POST /api/mcp/{id}/{action}.
type MCPResp struct {
	Servers []MCPServerInfo `json:"servers"`
	Error   string          `json:"error,omitempty"`
	Path    string          `json:"path"`
}

type SkillInfo struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	License       string            `json:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Path          string            `json:"path"`
	Enabled       bool              `json:"enabled"`
}

type SkillProblem struct {
	Dir   string `json:"dir"`
	Error string `json:"error"`
}

// SkillsResp is the answer of GET /api/skills and POST /api/skills/{name}/{enable|disable}.
type SkillsResp struct {
	Skills   []SkillInfo    `json:"skills"`
	Problems []SkillProblem `json:"problems,omitempty"`
	Dir      string         `json:"dir"`
}

// SessionsResp is the answer of GET /api/sessions/manage: sessions with their size, for the /settings page.
type SessionRow struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Mode      string    `json:"mode"`
	Updated   time.Time `json:"updated"`
	Messages  int       `json:"messages"`
	TokensIn  int       `json:"tokens_in"`
	TokensOut int       `json:"tokens_out"`
	Cost      float64   `json:"cost"`
}

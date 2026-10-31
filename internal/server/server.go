// Package server поднимает HTTP API бэкенда: сессии, потоковые запросы (SSE),
// подтверждения, сравнение моделей, процессы и OpenAI-совместимый шлюз.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"relay/internal/agent"
	"relay/internal/catalog"
	"relay/internal/config"
	"relay/internal/keystore"
	"relay/internal/mcp"
	"relay/internal/proto"
	"relay/internal/provider"
	"relay/internal/router"
	"relay/internal/runner"
	"relay/internal/session"
	"relay/internal/tools"
	"relay/internal/usage"
)

type Server struct {
	cfg    *config.Config
	token  string
	keys   *keystore.Store
	router *router.Router
	cat    *catalog.Catalog
	usage  *usage.Tracker
	store  *session.Store
	procs  *runner.Manager
	agent  *agent.Agent

	mcpMu      sync.Mutex
	mcpClients []*mcp.Client

	mu        sync.Mutex
	approvals map[string]chan string

	loopback bool // слушаем только loopback: включена защита от DNS rebinding

	Done chan struct{}
	once sync.Once
}

func New(cfg *config.Config, home, token string) *Server {
	tr := usage.Load(filepath.Join(home, "usage.json"))
	ks := keystore.Open(filepath.Join(home, "keys.json"))
	rt := router.New(cfg, tr, ks)
	cat := catalog.New(cfg, rt.Keys)
	rt.SetCatalog(cat)
	s := &Server{
		cfg:       cfg,
		token:     token,
		keys:      ks,
		loopback:  isLoopback(cfg.Listen),
		router:    rt,
		cat:       cat,
		usage:     tr,
		store:     session.NewStore(filepath.Join(home, "sessions")),
		procs:     runner.NewManager(cfg.Processes),
		approvals: map[string]chan string{},
		Done:      make(chan struct{}),
	}
	s.agent = &agent.Agent{Cfg: cfg, Router: rt, Extra: s.mcpTools}
	cat.Warm() // start looking for models right away: Ollama, Gemini, ...
	return s
}

// Procs даёт доступ к менеджеру процессов (автозапуск и остановка из main).
func (s *Server) Procs() *runner.Manager { return s.procs }

// StartMCP запускает MCP-серверы из конфигурации; ошибки только логируются.
func (s *Server) StartMCP() {
	for _, m := range s.cfg.MCP {
		c, err := mcp.Start(m.Name, m.Command, m.Args, m.Env)
		if err != nil {
			log.Printf("mcp %s: %v", m.Name, err)
			continue
		}
		log.Printf("mcp %s: %d tools", m.Name, len(c.Tools()))
		s.mcpMu.Lock()
		s.mcpClients = append(s.mcpClients, c)
		s.mcpMu.Unlock()
	}
}

func (s *Server) mcpTools() []tools.Tool {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	var out []tools.Tool
	for _, c := range s.mcpClients {
		out = append(out, tools.MCPTools(c)...)
	}
	return out
}

// Close останавливает MCP-серверы и управляемые процессы.
func (s *Server) Close() {
	s.mcpMu.Lock()
	for _, c := range s.mcpClients {
		c.Close()
	}
	s.mcpClients = nil
	s.mcpMu.Unlock()
	s.procs.StopAll()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": proto.Version})
	})
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("POST /api/sessions/{id}/settings", s.handleSettings)
	mux.HandleFunc("POST /api/sessions/{id}/messages", s.handleSend)
	mux.HandleFunc("POST /api/approvals/{id}", s.handleApproval)
	mux.HandleFunc("POST /api/compare", s.handleCompare)
	mux.HandleFunc("POST /api/processes/{name}/{action}", s.handleProcess)
	mux.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
		s.once.Do(func() { close(s.Done) })
	})
	mux.HandleFunc("GET /api/keys", s.handleKeys)
	mux.HandleFunc("POST /api/keys", s.handleAddKey)
	mux.HandleFunc("DELETE /api/keys/{provider}/{id}", s.handleDeleteKey)
	mux.HandleFunc("GET /ui/keys", s.handleKeysPage)
	mux.HandleFunc("GET /ui/help", s.handleHelpPage)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/keys", http.StatusFound)
	})
	mux.HandleFunc("GET /v1/models", s.handleGatewayModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleGatewayChat)
	return s.auth(mux)
}

func isLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

func hostAllowed(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	switch strings.Trim(host, "[]") {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// защита от DNS rebinding: страница и API отвечают только на loopback-имена
		if s.loopback && !hostAllowed(r.Host) {
			httpErr(w, 403, "forbidden host")
			return
		}
		p := r.URL.Path
		if p == "/api/health" || p == "/" || strings.HasPrefix(p, "/ui/") {
			next.ServeHTTP(w, r)
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			httpErr(w, 401, "unauthorized: pass the token from daemon.json as a Bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- вспомогательные ---------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": msg}})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<20)).Decode(v)
}

type sse struct {
	w  http.ResponseWriter
	f  http.Flusher
	mu sync.Mutex
}

func newSSE(w http.ResponseWriter) (*sse, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	f.Flush()
	return &sse{w: w, f: f}, true
}

func (s *sse) send(ev proto.Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.mu.Lock()
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	s.f.Flush()
	s.mu.Unlock()
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sessionInfo(sess *session.Session, withMessages bool) proto.SessionInfo {
	si := proto.SessionInfo{
		ID: sess.ID, Title: sess.Title, Mode: sess.Mode, Model: sess.Model,
		Preset: sess.Preset, Agent: sess.Agent, Workspace: sess.Workspace, Updated: sess.Updated,
	}
	if withMessages {
		si.Messages = sess.Messages
	}
	return si
}

// ---- обработчики -------------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := proto.StatusResp{
		Version:     proto.Version,
		Workspace:   s.workspace(""),
		DefaultMode: s.cfg.DefaultMode,
		Processes:   s.procs.Status(),
		Usage:       map[string]proto.UsageEntry{},
	}
	for _, name := range config.ValidModes {
		if md, ok := s.cfg.Modes[name]; ok {
			mi := proto.ModeInfo{Name: name, Models: md.Models}
			if chain, _, err := s.router.Resolve(name, "", false, router.Hint{}); err == nil {
				mi.Effective = chain
			}
			st.Modes = append(st.Modes, mi)
		}
	}
	for _, m := range s.cfg.Models {
		st.Models = append(st.Models, proto.ModelInfo{
			ID: m.ID, Provider: m.Provider, Name: m.Name, Tier: m.Tier,
			Tools: m.Tools, PriceIn: m.PriceIn, PriceOut: m.PriceOut,
		})
	}
	for _, p := range s.cfg.OrderedProviders() {
		total, avail, needs := s.router.KeyStats(p.Name)
		st.Providers = append(st.Providers, proto.ProviderInfo{
			Name: p.Name, Kind: p.Kind, NeedsKey: needs, Keys: total, KeysAvailable: avail, KeyFrom: p.KeyFrom,
		})
	}
	for k, v := range s.usage.Snapshot() {
		st.Usage[k] = proto.UsageEntry{In: v.In, Out: v.Out, Requests: v.Requests, Cost: v.Cost}
	}
	for _, p := range s.cfg.Presets {
		st.Presets = append(st.Presets, p.Name)
	}
	s.mcpMu.Lock()
	for _, c := range s.mcpClients {
		st.MCP = append(st.MCP, c.Name)
	}
	s.mcpMu.Unlock()
	writeJSON(w, 200, st)
}

func (s *Server) workspace(requested string) string {
	if requested != "" {
		return requested
	}
	if s.cfg.Workspace != "" {
		return s.cfg.Workspace
	}
	wd, _ := os.Getwd()
	return wd
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	out := []proto.SessionInfo{}
	for _, sm := range s.store.List() {
		out = append(out, proto.SessionInfo{ID: sm.ID, Title: sm.Title, Mode: sm.Mode, Updated: sm.Updated})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req proto.CreateSessionReq
	_ = readJSON(r, &req)
	mode := req.Mode
	if mode == "" {
		mode = s.cfg.DefaultMode
	}
	if _, ok := s.cfg.Modes[mode]; !ok {
		httpErr(w, 400, fmt.Sprintf("mode %q is not configured", mode))
		return
	}
	sess := s.store.Create(mode, s.workspace(req.Workspace))
	writeJSON(w, 200, sessionInfo(sess, false))
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, sessionInfo(sess, true))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	var req proto.SettingsReq
	if err := readJSON(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if req.Mode != nil {
		if _, ok := s.cfg.Modes[*req.Mode]; !ok {
			httpErr(w, 400, fmt.Sprintf("mode %q is not configured", *req.Mode))
			return
		}
		sess.Mode = *req.Mode
	}
	if req.Model != nil {
		if *req.Model != "" && *req.Model != "auto" {
			if _, ok := s.cfg.ModelByID(*req.Model); !ok {
				httpErr(w, 400, fmt.Sprintf("unknown model %q", *req.Model))
				return
			}
		}
		sess.Model = *req.Model
	}
	if req.Preset != nil {
		if *req.Preset != "" && s.cfg.PresetPrompt(*req.Preset) == "" {
			httpErr(w, 400, fmt.Sprintf("unknown preset %q", *req.Preset))
			return
		}
		sess.Preset = *req.Preset
	}
	if req.Agent != nil {
		sess.Agent = *req.Agent
	}
	_ = s.store.Save(sess)
	writeJSON(w, 200, sessionInfo(sess, false))
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	var req proto.SendReq
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Text) == "" {
		httpErr(w, 400, "text is required")
		return
	}
	if !sess.Run.TryLock() {
		httpErr(w, 409, "this session is already running a request")
		return
	}
	defer sess.Run.Unlock()

	out, ok := newSSE(w)
	if !ok {
		httpErr(w, 500, "streaming is not supported")
		return
	}
	approve := func(ctx context.Context, ev proto.Event) string {
		return s.waitApproval(ctx, out, ev)
	}
	_ = s.agent.Run(r.Context(), sess, req.Text, out.send, approve)
	_ = s.store.Save(sess)
	out.send(proto.Event{Type: "done"})
}

func (s *Server) waitApproval(ctx context.Context, out *sse, ev proto.Event) string {
	id := randHex(6)
	ev.CallID = id
	ch := make(chan string, 1)
	s.mu.Lock()
	s.approvals[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.approvals, id)
		s.mu.Unlock()
	}()
	out.send(ev)
	select {
	case d := <-ch:
		return d
	case <-ctx.Done():
		return "deny"
	}
}

func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) {
	var req proto.ApprovalReq
	if err := readJSON(r, &req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	s.mu.Lock()
	ch := s.approvals[r.PathValue("id")]
	s.mu.Unlock()
	if ch == nil {
		httpErr(w, 404, "no such pending approval")
		return
	}
	select {
	case ch <- req.Decision:
	default:
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	var req proto.CompareReq
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Text) == "" {
		httpErr(w, 400, "text is required")
		return
	}
	if len(req.Models) < 1 || len(req.Models) > 4 {
		httpErr(w, 400, "pass from 1 to 4 models")
		return
	}
	for _, id := range req.Models {
		if _, ok := s.cfg.ModelByID(id); !ok {
			httpErr(w, 400, fmt.Sprintf("unknown model %q", id))
			return
		}
	}
	out, ok := newSSE(w)
	if !ok {
		httpErr(w, 500, "streaming is not supported")
		return
	}
	var msgs []provider.Message
	if p := s.cfg.PresetPrompt(req.Preset); p != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: p})
	}
	msgs = append(msgs, provider.Message{Role: "user", Content: req.Text})

	var wg sync.WaitGroup
	for i, id := range req.Models {
		wg.Add(1)
		go func(slot int, id string) {
			defer wg.Done()
			_, err := s.router.Stream(r.Context(), []string{id}, provider.Request{Messages: msgs},
				func(ev provider.Event) error {
					switch ev.Kind {
					case provider.EvModel:
						out.send(proto.Event{Type: "model", Model: ev.Text, Slot: slot})
					case provider.EvText:
						out.send(proto.Event{Type: "text", Text: ev.Text, Slot: slot})
					case provider.EvNotice:
						out.send(proto.Event{Type: "notice", Kind: ev.Tag, Text: ev.Text, Slot: slot})
					case provider.EvUsage:
						out.send(proto.Event{Type: "usage", Model: ev.Text, In: ev.Usage.In, Out: ev.Usage.Out, Cost: ev.Cost, Slot: slot})
					}
					return r.Context().Err()
				})
			if err != nil && r.Context().Err() == nil {
				out.send(proto.Event{Type: "error", Kind: errKind(err), Text: err.Error(), Slot: slot})
			}
		}(i+1, id)
	}
	wg.Wait()
	out.send(proto.Event{Type: "done"})
}

func errKind(err error) string {
	if pe, ok := err.(*provider.Error); ok {
		switch {
		case pe.RateLimited:
			return "rate_limit"
		case pe.NoKey:
			return "no_key"
		}
	}
	return "provider"
}

func (s *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	var err error
	switch action {
	case "start":
		err = s.procs.Start(name)
	case "stop":
		err = s.procs.Stop(name)
	default:
		httpErr(w, 400, "action must be start or stop")
		return
	}
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.procs.Status())
}

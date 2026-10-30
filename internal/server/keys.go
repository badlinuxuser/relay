package server

import (
	_ "embed"
	"net/http"
	"os"
	"strings"

	"relay/internal/proto"
)

// Страница управления ключами: статический HTML без секретов. Токен для API
// страница берёт из фрагмента URL (#token=...), который не уходит на сервер.
//
//go:embed ui/keys.html
var keysHTML []byte

func (s *Server) handleKeysPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; connect-src 'self'")
	_, _ = w.Write(keysHTML)
}

func (s *Server) providerKeys() []proto.ProviderKeys {
	out := make([]proto.ProviderKeys, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.Providers {
		total, avail, needs := s.router.KeyStats(p.Name)
		pk := proto.ProviderKeys{
			Name: p.Name, Kind: p.Kind, NeedsKey: needs, Total: total, Available: avail,
			Stored: []proto.KeyEntry{},
		}
		for _, m := range s.keys.List(p.Name) {
			pk.Stored = append(pk.Stored, proto.KeyEntry{ID: m.ID, Label: m.Label, Hint: m.Hint})
		}
		for _, e := range p.APIKeyEnv {
			pk.Env = append(pk.Env, proto.EnvKey{Name: e, Set: envSet(e)})
		}
		out = append(out, pk)
	}
	return out
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, proto.KeysResp{Providers: s.providerKeys()})
}

func (s *Server) knownProvider(name string) bool {
	for _, p := range s.cfg.Providers {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) handleAddKey(w http.ResponseWriter, r *http.Request) {
	var req proto.AddKeyReq
	if err := readJSON(r, &req); err != nil {
		httpErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	if !s.knownProvider(req.Provider) {
		httpErr(w, 400, "unknown provider "+req.Provider)
		return
	}
	m, err := s.keys.Add(req.Provider, req.Label, req.Key)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, proto.KeyEntry{ID: m.ID, Label: m.Label, Hint: m.Hint})
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	if !s.keys.Delete(r.PathValue("provider"), r.PathValue("id")) {
		httpErr(w, 404, "no such key")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func envSet(name string) bool { return strings.TrimSpace(os.Getenv(name)) != "" }

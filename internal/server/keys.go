package server

import (
	_ "embed"
	"net/http"
	"sort"
	"strings"

	"relay/internal/config"
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

// keyGroup sorts the cards of the keys page: the model makers' own APIs first, then other
// services (OpenRouter, Ollama cloud, ...), then servers on this computer.
func keyGroup(p config.Provider) (rank int, name string) {
	switch {
	case p.IsOfficial():
		return 0, "official"
	case p.IsLocal():
		return 2, "local"
	}
	return 1, "external"
}

func (s *Server) providerKeys() []proto.ProviderKeys {
	list := make([]config.Provider, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.OrderedProviders() {
		if p.KeyFrom == "" { // a provider that uses the keys of another one has no card of its own
			list = append(list, p)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		ri, _ := keyGroup(list[i])
		rj, _ := keyGroup(list[j])
		return ri < rj
	})
	out := make([]proto.ProviderKeys, 0, len(list))
	for _, p := range list {
		total, avail, needs := s.router.KeyStats(p.Name)
		_, group := keyGroup(p)
		pk := proto.ProviderKeys{
			Name: p.Name, Kind: p.Kind, NeedsKey: needs, Total: total, Available: avail,
			Stored: []proto.KeyEntry{}, Group: group,
		}
		for _, q := range s.cfg.Providers {
			if q.KeyFrom == p.Name {
				pk.KeyOptional = true // e.g. Ollama: no key for local models, a key for the cloud ones
			}
		}
		for _, m := range s.keys.List(p.Name) {
			pk.Stored = append(pk.Stored, proto.KeyEntry{ID: m.ID, Hint: m.Hint})
		}
		out = append(out, pk)
	}
	return out
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, proto.KeysResp{Providers: s.providerKeys()})
}

// knownProvider: keys can be saved for configured providers that keep their own keys.
func (s *Server) knownProvider(name string) bool {
	p, ok := s.cfg.ProviderByName(name)
	return ok && p.KeyFrom == ""
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
	m, err := s.keys.Add(req.Provider, "", req.Key)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	s.cat.Invalidate() // a new key can unlock more models
	writeJSON(w, 200, proto.KeyEntry{ID: m.ID, Hint: m.Hint})
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	if !s.keys.Delete(r.PathValue("provider"), r.PathValue("id")) {
		httpErr(w, 404, "no such key")
		return
	}
	s.cat.Invalidate()
	writeJSON(w, 200, map[string]any{"ok": true})
}

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
	list = append(list, s.cfg.OrderedProviders()...)
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
		// Ollama cloud keeps its keys under the name "ollama" (key_from), so old keys keep working.
		// The card of the local Ollama then has no keys: it needs none.
		if s.keysBelongToAnother(p.Name) {
			pk.NeedsKey, pk.Total, pk.Available = false, 0, 0
		} else {
			for _, m := range s.keys.List(p.KeyName()) {
				pk.Stored = append(pk.Stored, proto.KeyEntry{ID: m.ID, Hint: m.Hint})
			}
		}
		out = append(out, pk)
	}
	return out
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, proto.KeysResp{Providers: s.providerKeys()})
}

// keysBelongToAnother: another provider (Ollama cloud) stores its keys under this provider's name.
func (s *Server) keysBelongToAnother(name string) bool {
	for _, q := range s.cfg.Providers {
		if q.KeyFrom == name && q.Name != name {
			return true
		}
	}
	return false
}

// keyStoreName maps a card of the keys page to the name its keys are stored under.
// Saving under "ollama" still works for old scripts: the keys are the same ones.
func (s *Server) keyStoreName(name string) (string, bool) {
	p, ok := s.cfg.ProviderByName(name)
	if !ok {
		return "", false
	}
	return p.KeyName(), true
}

// knownProvider: keys can be saved for configured providers that have a card for them.
func (s *Server) knownProvider(name string) bool {
	_, ok := s.keyStoreName(name)
	return ok
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
	store, _ := s.keyStoreName(req.Provider)
	m, err := s.keys.Add(store, "", req.Key)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	s.cat.Invalidate() // a new key can unlock more models
	writeJSON(w, 200, proto.KeyEntry{ID: m.ID, Hint: m.Hint})
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	store, ok := s.keyStoreName(r.PathValue("provider"))
	if !ok || !s.keys.Delete(store, r.PathValue("id")) {
		httpErr(w, 404, "no such key")
		return
	}
	s.cat.Invalidate()
	writeJSON(w, 200, map[string]any{"ok": true})
}

package server

import (
	"net/http"

	"relay/internal/catalog"
	"relay/internal/proto"
)

// handleModels returns every model the user can pick: what the providers report
// (local Ollama, Ollama cloud, Gemini, ...) and the models from config.toml of the
// providers whose list could not be fetched. ?refresh=1 forces a new look.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	snap := s.cat.Get(r.Context(), r.URL.Query().Get("refresh") != "")
	writeJSON(w, 200, s.modelList(snap))
}

func (s *Server) modelList(snap *catalog.Snapshot) proto.ModelsResp {
	resp := proto.ModelsResp{Models: []proto.ModelInfo{}, Providers: []proto.ModelsProvider{}}
	for _, p := range s.cfg.Providers {
		res, ok := snap.Result(p.Name)
		if ok {
			resp.Providers = append(resp.Providers, proto.ModelsProvider{
				Name: p.Name, Count: res.Count, Skipped: res.Skipped, Error: res.Error,
			})
		}
		if ok && res.Listed {
			for _, m := range snap.Pick(p.Name, 1<<30) {
				resp.Models = append(resp.Models, proto.ModelInfo{
					ID: m.ID, Provider: m.Provider, Name: m.Name, Tools: true, Kind: m.Kind, Listed: true,
				})
			}
			continue
		}
		// No list from this provider: offer the models written in config.toml, but only
		// if the provider can be used at all (a remote one that has a key).
		if !p.NeedsKey() || res.Skipped != "" {
			continue
		}
		if total, _, _ := s.router.KeyStats(p.Name); total == 0 {
			continue
		}
		for _, m := range s.cfg.Models {
			if m.Provider != p.Name {
				continue
			}
			resp.Models = append(resp.Models, proto.ModelInfo{
				ID: m.ID, Provider: m.Provider, Name: m.Name, Tier: m.Tier, Tools: m.Tools,
				PriceIn: m.PriceIn, PriceOut: m.PriceOut, Kind: "cloud",
			})
		}
	}
	return resp
}

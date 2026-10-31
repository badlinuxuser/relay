package server

import (
	"net/http"

	"relay/internal/catalog"
	"relay/internal/proto"
)

// handleModels returns every model the user can pick: what the providers report
// (local Ollama, Ollama cloud, Gemini, ...), official providers first. Providers
// that refuse or cannot be reached contribute nothing. ?refresh=1 forces a new look.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	snap := s.cat.Get(r.Context(), r.URL.Query().Get("refresh") != "")
	writeJSON(w, 200, s.modelList(snap))
}

func (s *Server) modelList(snap *catalog.Snapshot) proto.ModelsResp {
	resp := proto.ModelsResp{Models: []proto.ModelInfo{}, Providers: []proto.ModelsProvider{}}
	for _, p := range s.cfg.OrderedProviders() {
		res, ok := snap.Result(p.Name)
		if ok {
			resp.Providers = append(resp.Providers, proto.ModelsProvider{
				Name: p.Name, Count: res.Count, Local: res.Local, Skipped: res.Skipped, Error: res.Error,
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
		// A provider whose list was refused or is unreachable offers nothing (no access).
		// Only a provider with the model search switched off (discover = "none") offers
		// the models written in config.toml, if it has a key or needs none.
		if res.Skipped != "disabled" {
			continue
		}
		if total, _, _ := s.router.KeyStats(p.Name); p.NeedsKey() && total == 0 {
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

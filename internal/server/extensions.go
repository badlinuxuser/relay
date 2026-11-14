package server

import (
	"context"
	"log"
	"net/http"
	"strings"

	"relay/internal/mcp"
	"relay/internal/proto"
	"relay/internal/tools"
)

func mcpInfo(st mcp.Status) proto.MCPServerInfo {
	info := proto.MCPServerInfo{
		ID: st.ID, Name: st.Name, Type: st.Type, Target: st.Target, Source: st.Source,
		State: string(st.State), Error: st.Error, Warning: st.Warning,
		Kind: st.Kind, OAuth: st.OAuth, SignedIn: st.SignedIn,
		Trusted: st.Trusted, Disabled: st.Disabled, ToolCount: len(st.Tools),
	}
	for _, t := range st.Tools {
		info.Tools = append(info.Tools, proto.MCPToolInfo{Name: t.Name, Description: t.Description, Disabled: t.Disabled})
	}
	return info
}

func (s *Server) mcpResp() proto.MCPResp {
	resp := proto.MCPResp{Servers: []proto.MCPServerInfo{}, Error: s.mcp.LoadError(), Path: s.mcp.Path()}
	for _, st := range s.mcp.Status() {
		resp.Servers = append(resp.Servers, mcpInfo(st))
	}
	return resp
}

func (s *Server) handleMCPList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.mcpResp())
}

// handleMCPAction: POST /api/mcp/{id}/{connect|disconnect|reconnect|trust}. It waits for the
// result, so a failed connection comes back as the error of the request.
func (s *Server) handleMCPAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "connect":
		err = s.mcp.Connect(id)
	case "disconnect":
		err = s.mcp.Disconnect(id)
	case "reconnect":
		err = s.mcp.Reconnect(id)
	case "trust":
		err = s.mcp.Trust(id)
	default:
		httpErr(w, 400, "unknown action (use connect, disconnect, reconnect or trust)")
		return
	}
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.mcpResp())
}

func (s *Server) skillInfos() []proto.SkillInfo {
	out := []proto.SkillInfo{}
	for _, sk := range s.skills.List() {
		out = append(out, proto.SkillInfo{
			Name: sk.Name, Description: sk.Description, License: sk.License,
			Compatibility: sk.Compatibility, Metadata: sk.Metadata, Path: sk.Dir, Enabled: sk.Enabled, Builtin: sk.Builtin,
		})
	}
	return out
}

func (s *Server) skillsResp() proto.SkillsResp {
	resp := proto.SkillsResp{Skills: s.skillInfos(), Dir: s.skills.Dir()}
	for _, p := range s.skills.Problems() {
		resp.Problems = append(resp.Problems, proto.SkillProblem{Dir: p.Dir, Error: p.Error})
	}
	return resp
}

func (s *Server) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.skillsResp())
}

// handleSkillAction: POST /api/skills/{name}/{enable|disable}.
func (s *Server) handleSkillAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var err error
	switch r.PathValue("action") {
	case "enable":
		err = s.skills.SetEnabled(name, true)
	case "disable":
		err = s.skills.SetEnabled(name, false)
	default:
		httpErr(w, 400, "unknown action (use enable or disable)")
		return
	}
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.skillsResp())
}

// handleMCPAuthorize starts the sign-in of a connector and answers with the address to open in
// the browser. The flow goes on after this request ends: the callback completes it.
func (s *Server) handleMCPAuthorize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	url, wait, err := s.mcp.Authorize(context.Background(), id)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	go func() {
		if err := wait(context.Background()); err != nil {
			log.Printf("sign-in to %s: %v", id, err)
		}
	}()
	writeJSON(w, 200, proto.AuthResp{URL: url})
}

// firstSentence shortens a tool description for the list.
func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	if r := []rune(s); len(r) > 110 {
		s = string(r[:109]) + "…"
	}
	return s
}

// handleTools lists what the user has, in the groups of /tools and /connectors.
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	reg := tools.NewRegistry("")
	for _, t := range s.builder.Tools() {
		reg.Add(t)
	}
	if s.set.Get().SuggestTools {
		reg.Add(s.builder.Propose())
	}
	for _, t := range tools.SkillTools(s.skills) {
		reg.Add(t)
	}
	resp := proto.ToolsResp{Builtin: []proto.ToolInfo{}, Local: []proto.MCPServerInfo{}, Connectors: []proto.MCPServerInfo{}, Callback: s.redirectURI()}
	for _, sp := range reg.Specs() {
		t, _ := reg.Get(sp.Name)
		resp.Builtin = append(resp.Builtin, proto.ToolInfo{Name: sp.Name, Description: firstSentence(sp.Description), Approval: t.NeedsApproval()})
	}
	for _, st := range s.mcp.Status() {
		if st.Kind == mcp.KindConnector {
			resp.Connectors = append(resp.Connectors, mcpInfo(st))
		} else {
			resp.Local = append(resp.Local, mcpInfo(st))
		}
	}
	writeJSON(w, 200, resp)
}

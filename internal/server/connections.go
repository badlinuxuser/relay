package server

import (
	_ "embed"
	"net/http"
)

//go:embed ui/connections.html
var connectionsHTML []byte

func (s *Server) handleConnectionsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
	_, _ = w.Write(connectionsHTML)
}

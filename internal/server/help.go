package server

import (
	_ "embed"
	"net/http"
)

// The help page: static HTML, opened by /help in the interface.
//
//go:embed ui/help.html
var helpHTML []byte

func (s *Server) handleHelpPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	_, _ = w.Write(helpHTML)
}

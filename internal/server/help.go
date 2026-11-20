package server

import (
	_ "embed"
	"net/http"
	"strings"
)

// The help page: static HTML, opened by /help in the interface. It exists in English and
// Russian; the language is chosen by ?lang=ru|en (remembered in a cookie), else by the
// cookie, else by the browser's Accept-Language.
//
//go:embed ui/help.html
var helpHTML []byte

//go:embed ui/help.ru.html
var helpRuHTML []byte

// pageLang picks the language of a web page: "ru" or "en".
func pageLang(r *http.Request) (lang string, fromQuery bool) {
	if l := r.URL.Query().Get("lang"); l == "ru" || l == "en" {
		return l, true
	}
	if c, err := r.Cookie("relay_lang"); err == nil && (c.Value == "ru" || c.Value == "en") {
		return c.Value, false
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "ru") {
		return "ru", false
	}
	return "en", false
}

func (s *Server) handleHelpPage(w http.ResponseWriter, r *http.Request) {
	lang, fromQuery := pageLang(r)
	if fromQuery { // only a language preference, no secrets
		http.SetCookie(w, &http.Cookie{Name: "relay_lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteStrictMode})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Accept-Language, Cookie")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	if lang == "ru" {
		_, _ = w.Write(helpRuHTML)
		return
	}
	_, _ = w.Write(helpHTML)
}

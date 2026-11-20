package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func helpGet(target, accept, cookie string) (body, setCookie string) {
	req := httptest.NewRequest("GET", target, nil)
	if accept != "" {
		req.Header.Set("Accept-Language", accept)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "relay_lang", Value: cookie})
	}
	rec := httptest.NewRecorder()
	(&Server{}).handleHelpPage(rec, req)
	return rec.Body.String(), rec.Header().Get("Set-Cookie")
}

func TestHelpPageLanguage(t *testing.T) {
	if b, _ := helpGet("/ui/help", "", ""); !strings.Contains(b, "Quick start") {
		t.Error("English is the default")
	}
	b, c := helpGet("/ui/help?lang=ru", "", "")
	if !strings.Contains(b, "Быстрый старт") || !strings.Contains(c, "relay_lang=ru") {
		t.Errorf("?lang=ru must give the Russian page and remember it (cookie %q)", c)
	}
	if b, _ := helpGet("/ui/help", "ru-RU,ru;q=0.9", ""); !strings.Contains(b, "Быстрый старт") {
		t.Error("Accept-Language ru gives the Russian page")
	}
	if b, _ := helpGet("/ui/help", "ru-RU", "en"); !strings.Contains(b, "Quick start") {
		t.Error("the cookie wins over Accept-Language")
	}
}

// The Russian help is a full translation: it must keep the same sections as the English one.
func TestRussianHelpHasTheSameSections(t *testing.T) {
	ids := regexp.MustCompile(`<section id="([a-z-]+)"`)
	collect := func(b []byte) string {
		var out []string
		for _, m := range ids.FindAllSubmatch(b, -1) {
			out = append(out, string(m[1]))
		}
		return strings.Join(out, ",")
	}
	if en, ru := collect(helpHTML), collect(helpRuHTML); en != ru {
		t.Errorf("sections differ:\nen: %s\nru: %s", en, ru)
	}
}

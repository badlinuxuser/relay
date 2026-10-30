package tui

import (
	"strings"
	"testing"
	"time"

	"relay/internal/client"
	"relay/internal/proto"
)

func testModel() model {
	m := New(false, false).(model)
	m.cl = &client.Client{}
	m.sess = &proto.SessionInfo{ID: "s1", Mode: "small"}
	m.width, m.height = 120, 30
	m.models = &proto.ModelsResp{Models: []proto.ModelInfo{
		{ID: "ollama/dolphin-phi:latest", Provider: "ollama", Name: "dolphin-phi:latest", Kind: "local", Listed: true},
		{ID: "ollama/granite4.1:3b", Provider: "ollama", Name: "granite4.1:3b", Kind: "local", Listed: true},
		{ID: "ollama-cloud/gpt-oss:120b", Provider: "ollama-cloud", Name: "gpt-oss:120b", Kind: "cloud", Listed: true},
		{ID: "gemini/gemini-2.5-flash", Provider: "gemini", Name: "gemini-2.5-flash", Kind: "cloud", Listed: true},
	}}
	m.catAt = time.Now()
	return m
}

func ids(items []pickItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.label())
	}
	return strings.Join(out, " ")
}

func TestPickerQuery(t *testing.T) {
	for in, want := range map[string]struct {
		filter string
		ok     bool
	}{
		"/model":         {"", true},
		"/models":        {"", true},
		"/models gem":    {"gem", true},
		"/models  gem ":  {"gem", true},
		"/model ollama":  {"ollama", true},
		"/mode":          {"", false},
		"/mode small":    {"", false},
		"/modelsx":       {"", false},
		"hello /models":  {"", false},
		"":               {"", false},
		"/compare a b c": {"", false},
	} {
		f, ok := pickerQuery(in)
		if f != want.filter || ok != want.ok {
			t.Errorf("pickerQuery(%q) = %q, %v; want %q, %v", in, f, ok, want.filter, want.ok)
		}
	}
}

func TestPickerOpensOnlyForTheModelsCommand(t *testing.T) {
	m := testModel()
	for in, want := range map[string]bool{"/model": true, "/models": true, "/models gem": true, "/mode small": false, "hi": false, "": false} {
		m.input.SetValue(in)
		if got := m.pickerOpen(); got != want {
			t.Errorf("pickerOpen(%q) = %v, want %v", in, got, want)
		}
	}
	m.input.SetValue("/models")
	m.approval = &proto.Event{}
	if m.pickerOpen() {
		t.Error("picker must stay closed while an approval is pending")
	}
	m.approval = nil
	m.cl = nil
	if m.pickerOpen() {
		t.Error("picker needs a connection")
	}
}

func TestPickerFilter(t *testing.T) {
	m := testModel()
	for in, want := range map[string]string{
		"/models":              "auto ollama/dolphin-phi:latest ollama/granite4.1:3b ollama-cloud/gpt-oss:120b gemini/gemini-2.5-flash",
		"/models gemini":       "gemini/gemini-2.5-flash",
		"/models OLLAMA":       "ollama/dolphin-phi:latest ollama/granite4.1:3b ollama-cloud/gpt-oss:120b",
		"/models cloud gpt":    "ollama-cloud/gpt-oss:120b",
		"/models ollama cloud": "ollama-cloud/gpt-oss:120b", // the provider is shown as "ollama cloud"
		"/models auto":         "auto",
		"/models zzz":          "",
	} {
		m.input.SetValue(in)
		if got := ids(m.pickerItems()); got != want {
			t.Errorf("%q:\n got  %s\n want %s", in, got, want)
		}
	}
}

func TestPickerKeys(t *testing.T) {
	m := testModel()
	m.input.SetValue("/models")
	n := len(m.pickerItems())

	m.pickerKey("up") // wraps to the last item
	if m.pickSel != n-1 {
		t.Fatalf("up from the first item: sel=%d, want %d", m.pickSel, n-1)
	}
	m.pickerKey("down")
	if m.pickSel != 0 {
		t.Fatalf("down from the last item: sel=%d", m.pickSel)
	}
	m.pickerKey("down")
	m.pickerKey("down") // now on "ollama/granite4.1:3b"

	// tab copies the highlighted model to the input and keeps the picker open
	if handled, _ := m.pickerKey("tab"); !handled || m.input.Value() != "/models ollama/granite4.1:3b" {
		t.Fatalf("tab: input = %q", m.input.Value())
	}
	if got := ids(m.pickerItems()); got != "ollama/granite4.1:3b" {
		t.Fatalf("after tab the filter is the full id: %q", got)
	}

	// enter uses it
	handled, cmd := m.pickerKey("enter")
	if !handled || cmd == nil || m.input.Value() != "" {
		t.Fatalf("enter: handled=%v cmd=%v input=%q", handled, cmd != nil, m.input.Value())
	}
	if last := m.entries[len(m.entries)-1]; last.text != "Model: ollama/granite4.1:3b" {
		t.Errorf("notice: %q", last.text)
	}
}

func TestPickerEnterOnAutoAndOnUnknownId(t *testing.T) {
	m := testModel()
	m.input.SetValue("/models")
	if handled, cmd := m.pickerKey("enter"); !handled || cmd == nil {
		t.Fatal("enter on auto")
	}
	if last := m.entries[len(m.entries)-1]; !strings.HasPrefix(last.text, "Model: auto") {
		t.Errorf("notice: %q", last.text)
	}

	// an id that is not in the list goes on to the normal command handling
	m.input.SetValue("/models ollama/brand-new:1b")
	if handled, _ := m.pickerKey("enter"); handled {
		t.Fatal("enter with no matches must fall through to /models <id>")
	}
	next, cmd := m.command("/models ollama/brand-new:1b")
	if cmd == nil {
		t.Fatal("/models <id> must send the setting")
	}
	if last := next.(model).entries[len(next.(model).entries)-1]; last.text != "Model: ollama/brand-new:1b" {
		t.Errorf("notice: %q", last.text)
	}
}

func TestPickerEscClosesUntilInputChanges(t *testing.T) {
	m := testModel()
	m.input.SetValue("/models g")
	m.pickerKey("esc")
	if m.pickerOpen() {
		t.Fatal("esc must close the picker")
	}
	prev := m.input.Value()
	m.input.SetValue("/models ge")
	m.syncPicker(prev)
	if !m.pickerOpen() {
		t.Fatal("typing must open it again")
	}
}

func TestPickerStartsAtCurrentModel(t *testing.T) {
	m := testModel()
	m.sess.Model = "gemini/gemini-2.5-flash"
	m.input.SetValue("/models")
	m.syncPicker("/model")
	items := m.pickerItems()
	if items[m.pickSelected(items)].id != "gemini/gemini-2.5-flash" {
		t.Errorf("selected %q", items[m.pickSelected(items)].id)
	}
}

func TestPickerAsksForTheListWhenItIsStale(t *testing.T) {
	m := testModel()
	m.catAt = time.Now().Add(-time.Minute)
	m.input.SetValue("/models")
	if cmd := m.syncPicker("/model"); cmd == nil || !m.catBusy {
		t.Fatal("a stale list must be fetched when the picker opens")
	}
	m.input.SetValue("/models g")
	if cmd := m.syncPicker("/models"); cmd != nil {
		t.Fatal("no second request while one is running")
	}
}

func TestModelCommandIsRenamed(t *testing.T) {
	m := testModel()
	next, _ := m.command("/model ollama/granite4.1:3b")
	e := next.(model).entries
	if !strings.Contains(e[len(e)-1].text, "Unknown command /model") {
		t.Errorf("/model must be gone, got %q", e[len(e)-1].text)
	}
	if !strings.Contains(helpText, "/models") || strings.Contains(helpText, "/model <id>") {
		t.Error("help must describe /models")
	}
}

func TestIsModelID(t *testing.T) {
	m := testModel()
	m.status = &proto.StatusResp{Providers: []proto.ProviderInfo{{Name: "ollama"}, {Name: "gemini"}}}
	for id, want := range map[string]bool{
		"ollama/granite4.1:3b": true, "ollama/not-listed:1b": true, "gemini/x": true,
		"nope/x": false, "hello": false, "and/or": false, "ollama/": false,
	} {
		if got := m.isModelID(id); got != want {
			t.Errorf("isModelID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestViewDrawsPickerOverFeed(t *testing.T) {
	m := testModel()
	m.input.SetValue("/models")
	out := m.View()
	for _, want := range []string{"models 1/5", "↑↓ select", "tab fill", "enter use", "esc close", "❯ auto", "the mode's own model chain",
		"ollama/dolphin-phi:latest", "ollama · dolphin-phi:latest", "ollama cloud · gpt-oss:120b", "gemini · gemini-2.5-flash"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if lines := strings.Split(out, "\n"); len(lines) != m.height {
		t.Errorf("the screen must keep its height: %d lines, want %d", len(lines), m.height)
	}
	m.input.SetValue("hello")
	if strings.Contains(m.View(), "models 1/") {
		t.Error("picker must be closed for other input")
	}
}

func TestPickerShowsProviderProblems(t *testing.T) {
	m := testModel()
	m.models.Providers = []proto.ModelsProvider{
		{Name: "gemini", Error: "HTTP 400: API key not valid. Please pass a valid API key."},
		{Name: "ollama-cloud", Skipped: "no key"},
	}
	m.input.SetValue("/models")
	out := m.View()
	if !strings.Contains(out, "gemini: HTTP 400: API key not valid") {
		t.Errorf("a failing provider must be visible in the picker:\n%s", out)
	}
	if strings.Contains(out, "ollama-cloud:") {
		t.Error("a provider without a key is not an error")
	}
}

func TestMissingKeysUsesOwnerOfSharedKeys(t *testing.T) {
	st := &proto.StatusResp{
		Models: []proto.ModelInfo{{ID: "x", Provider: "ollama-cloud"}, {ID: "y", Provider: "gemini"}},
		Providers: []proto.ProviderInfo{
			{Name: "ollama-cloud", NeedsKey: true, KeyFrom: "ollama"},
			{Name: "gemini", NeedsKey: true},
		},
	}
	if got := strings.Join(missingKeys(st), ","); got != "ollama,gemini" {
		t.Errorf("missingKeys = %q", got)
	}
}

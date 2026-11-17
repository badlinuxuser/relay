package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"relay/internal/config"
	"relay/internal/proto"
	"relay/internal/provider"
	"relay/internal/router"
	"relay/internal/session"
	"relay/internal/skills"
	"relay/internal/tools"
	"relay/internal/usage"
)

type keysrc map[string][]string

func (k keysrc) Values(name string) []string { return k[name] }

func newAgent(t *testing.T, maxSteps int, h func(w http.ResponseWriter, body string, n int)) (*Agent, *session.Session) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		h(w, string(b), int(atomic.AddInt32(&n, 1)))
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{
		Providers: []config.Provider{{Name: "p", Kind: "openai", BaseURL: srv.URL + "/v1"}},
		Models:    []config.Model{{ID: "m", Provider: "p", Name: "m", Tools: true}},
		Modes:     map[string]config.Mode{"small": {Models: []string{"m"}, MaxSteps: maxSteps}},
	}
	rt := router.New(cfg, usage.Load(filepath.Join(t.TempDir(), "usage.json")), keysrc{})
	return &Agent{Cfg: cfg, Router: rt},
		&session.Session{Mode: "small", Workspace: t.TempDir(), AutoTools: map[string]bool{}}
}

func chunk(w http.ResponseWriter, delta, finish string) {
	f := "null"
	if finish != "" {
		f = `"` + finish + `"`
	}
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, f)
}

func done(w http.ResponseWriter) { fmt.Fprint(w, "data: [DONE]\n\n") }

func run(t *testing.T, a *Agent, s *session.Session, agentMode bool) ([]proto.Event, error) {
	t.Helper()
	s.Agent = agentMode
	var evs []proto.Event
	err := a.Run(context.Background(), s, "go", true, func(ev proto.Event) { evs = append(evs, ev) },
		func(context.Context, proto.Event) string { return "allow" })
	return evs, err
}

func texts(evs []proto.Event) string {
	var b strings.Builder
	for _, e := range evs {
		if e.Type == "text" {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func notices(evs []proto.Event) string {
	var b strings.Builder
	for _, e := range evs {
		if e.Type == "notice" {
			b.WriteString(e.Text + "\n")
		}
	}
	return b.String()
}

func TestAnAnswerCutByTheOutputLimitGoesOn(t *testing.T) {
	var second string
	a, s := newAgent(t, 1, func(w http.ResponseWriter, body string, n int) {
		if n == 1 {
			chunk(w, `{"content":"Hello wor"}`, "")
			chunk(w, `{}`, "length")
		} else {
			second = body
			chunk(w, `{"content":"ld"}`, "stop")
		}
		done(w)
	})
	evs, err := run(t, a, s, false)
	if err != nil {
		t.Fatal(err)
	}
	if texts(evs) != "Hello world" {
		t.Fatalf("the answer must go on without a gap: %q", texts(evs))
	}
	if !strings.Contains(second, "Continue exactly where you stopped") || !strings.Contains(second, "Hello wor") {
		t.Fatalf("the model must be asked to go on, with its own words before it: %s", second)
	}
	if len(s.Messages) != 4 || s.Messages[2].Role != "user" || !strings.HasPrefix(s.Messages[2].Content, provider.InternalPrefix) {
		t.Fatalf("history: %+v", s.Messages)
	}
	if !strings.Contains(notices(evs), "output limit") {
		t.Error("the user is told that the answer goes on")
	}
}

func TestAnAnswerThatIsAlwaysCutStopsPolitely(t *testing.T) {
	var calls int32
	a, s := newAgent(t, 1, func(w http.ResponseWriter, body string, n int) {
		atomic.AddInt32(&calls, 1)
		chunk(w, `{"content":"more "}`, "")
		chunk(w, `{}`, "length")
		done(w)
	})
	evs, err := run(t, a, s, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != maxContinues+1 {
		t.Fatalf("requests: %d", got)
	}
	if !strings.Contains(notices(evs), `Say "continue"`) {
		t.Errorf("the user must be told how to get the rest: %q", notices(evs))
	}
}

func TestAToolCallCutByTheLimitIsNotRunAndIsAskedAgain(t *testing.T) {
	var second string
	a, s := newAgent(t, 4, func(w http.ResponseWriter, body string, n int) {
		if n == 1 {
			chunk(w, `{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"a.txt\",\"content\":\"half of a very long"}}]}`, "length")
		} else {
			second = body
			chunk(w, `{"content":"ok, smaller"}`, "stop")
		}
		done(w)
	})
	evs, err := run(t, a, s, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == "tool_call" || e.Type == "tool_result" {
			t.Fatalf("a cut call must not run: %+v", e)
		}
	}
	for _, m := range s.Messages {
		if len(m.ToolCalls) > 0 || m.Role == "tool" {
			t.Fatalf("a tool call without its result would break the next request: %+v", m)
		}
	}
	if !strings.Contains(second, "NOT run") || !strings.Contains(second, "write_file") {
		t.Fatalf("the model must be told which call was lost: %s", second)
	}
	if texts(evs) != "ok, smaller" {
		t.Fatalf("text: %q", texts(evs))
	}
}

func TestALongTaskGoesPastTheSoftLimitAndEndsWithASummary(t *testing.T) {
	var last string
	var steps int32
	a, s := newAgent(t, 2, func(w http.ResponseWriter, body string, n int) {
		atomic.AddInt32(&steps, 1)
		if strings.Contains(body, `"tools"`) {
			// a different call each time, so it is not taken for a loop
			chunk(w, fmt.Sprintf(`{"tool_calls":[{"index":0,"id":"c%d","type":"function","function":{"name":"no_such_tool","arguments":"{\"n\":%d}"}}]}`, n, n), "tool_calls")
		} else {
			last = body
			chunk(w, `{"content":"Summary: what is done and what is left."}`, "stop")
		}
		done(w)
	})
	evs, err := run(t, a, s, true)
	if err != nil {
		t.Fatalf("the budget must not end the work with an error: %v", err)
	}
	if got := atomic.LoadInt32(&steps); got != 9 { // 8 steps with tools (4 x the budget of 2) and the summary
		t.Fatalf("requests: %d", got)
	}
	if strings.Contains(last, `"tools"`) || !strings.Contains(last, "budget of steps") {
		t.Fatalf("the last request must offer no tools and ask for a summary: %s", last)
	}
	if !strings.Contains(texts(evs), "Summary") {
		t.Errorf("the summary must be shown: %q", texts(evs))
	}
	n := notices(evs)
	if !strings.Contains(n, "going on past 2 steps") || !strings.Contains(n, "Safety limit of 8 steps") {
		t.Errorf("notices: %q", n)
	}
	// every call of the assistant has its result: the next request is valid
	pending := map[string]bool{}
	for _, m := range s.Messages {
		for _, c := range m.ToolCalls {
			pending[c.ID] = true
		}
		if m.Role == "tool" {
			delete(pending, m.ToolCallID)
		}
	}
	if len(pending) != 0 {
		t.Errorf("calls without a result: %v", pending)
	}
}

func TestAnAgentThatRepeatsItselfIsStopped(t *testing.T) {
	var steps int32
	a, s := newAgent(t, 12, func(w http.ResponseWriter, body string, n int) {
		atomic.AddInt32(&steps, 1)
		chunk(w, `{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"no_such_tool","arguments":"{}"}}]}`, "tool_calls")
		done(w)
	})
	evs, err := run(t, a, s, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&steps); got != 4 {
		t.Fatalf("requests: %d", got)
	}
	if !strings.Contains(notices(evs), "same call again and again") {
		t.Errorf("notices: %q", notices(evs))
	}
}

func bigConversation(turns int) []provider.Message {
	msgs := []provider.Message{{Role: "system", Content: "rules"}}
	for i := 0; i < turns; i++ {
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs,
			provider.Message{Role: "user", Content: fmt.Sprintf("question %d", i)},
			provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file", Args: `{"path":"x"}`}}},
			provider.Message{Role: "tool", ToolCallID: id, Content: strings.Repeat("line of output\n", 400)},
			provider.Message{Role: "assistant", Content: fmt.Sprintf("answer %d", i)},
		)
	}
	return msgs
}

func TestAConversationThatFitsIsLeftAlone(t *testing.T) {
	msgs := bigConversation(2)
	got, cut := fitContext(msgs, 1_000_000)
	if cut || len(got) != len(msgs) {
		t.Fatal("nothing to cut")
	}
}

func TestOldToolResultsAreShortenedFirst(t *testing.T) {
	msgs := bigConversation(12)
	before := estimateTokens(msgs)
	got, cut := fitContext(msgs, before-3000)
	if !cut || len(got) != len(msgs) {
		t.Fatalf("shortening is enough here: cut=%v len=%d/%d", cut, len(got), len(msgs))
	}
	if !strings.Contains(got[3].Content, "shortened") {
		t.Error("an old result is shortened")
	}
	if got[len(got)-2].Content != msgs[len(msgs)-2].Content {
		t.Error("the newest messages stay whole")
	}
	if msgs[3].Content != strings.Repeat("line of output\n", 400) {
		t.Error("the stored history must not be changed")
	}
}

func TestTheOldestTurnsAreLeftOutWithoutBreakingToolCalls(t *testing.T) {
	msgs := bigConversation(40)
	budget := 2500
	got, cut := fitContext(msgs, budget)
	if !cut {
		t.Fatal("must cut")
	}
	if estimateTokens(got) > budget {
		t.Fatalf("still too big: %d > %d", estimateTokens(got), budget)
	}
	if got[0].Role != "system" || !strings.HasPrefix(got[1].Content, provider.InternalPrefix) || got[2].Role != "user" {
		t.Fatalf("the system message stays, a note follows, then a whole turn from its question: %+v", got[:3])
	}
	// every tool message has the assistant message with its call before it
	open := map[string]bool{}
	for _, m := range got {
		for _, c := range m.ToolCalls {
			open[c.ID] = true
		}
		if m.Role == "tool" && !open[m.ToolCallID] {
			t.Fatalf("a result without its call: %s", m.ToolCallID)
		}
	}
	if got[len(got)-1].Content != "answer 39" {
		t.Error("the newest answer is kept")
	}
	// even when the newest turn alone is too big, it is kept (the user's question must reach the model)
	tiny, _ := fitContext(msgs, 10)
	if tiny[len(tiny)-1].Content != "answer 39" || tiny[0].Role != "system" {
		t.Errorf("tiny budget: %+v", tiny)
	}
}

func TestCallsSignatureTellsRepeatsApart(t *testing.T) {
	a := []provider.ToolCall{{Name: "x", Args: "{}"}}
	b := []provider.ToolCall{{Name: "x", Args: `{"n":1}`}}
	if callsSignature(a) != callsSignature(a) || callsSignature(a) == callsSignature(b) {
		t.Fatal("signature")
	}
}

// ---- consent, order and "always" for the tools that build ----------------------

type fakeTool struct {
	name     string
	approval bool
	runs     *int32
}

func (f *fakeTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: f.name, Description: "a test tool", Schema: []byte(`{"type":"object","properties":{}}`)}
}
func (f *fakeTool) NeedsApproval() bool             { return f.approval }
func (f *fakeTool) Describe(json.RawMessage) string { return f.name }
func (f *fakeTool) Run(ctx context.Context, a json.RawMessage) (string, error) {
	atomic.AddInt32(f.runs, 1)
	return "ran " + f.name, nil
}

type plainTool struct{ fakeTool }

// offerTool is the offer to build (like propose_extension).
type offerTool struct{ fakeTool }

func (offerTool) IsBuildOffer() bool    { return true }
func (offerTool) CancelsRequest() bool  { return true }
func (offerTool) OnceOnly() bool        { return true }
func (offerTool) DeniedMessage() string { return "REFUSED: the request is cancelled" }

// buildTool is a tool that builds (like create_mcp_server).
type buildTool struct {
	fakeTool
	read []string
}

func (buildTool) NeedsOffer() bool            { return true }
func (buildTool) CancelsRequest() bool        { return true }
func (b buildTool) NeedsRead([]byte) []string { return b.read }

type onceTool struct{ fakeTool }

func (onceTool) OnceOnly() bool { return true }

// script answers request n with the tool calls of step n, and with a text after the last step.
func script(steps ...[][2]string) func(http.ResponseWriter, string, int) {
	return func(w http.ResponseWriter, body string, n int) {
		if n > len(steps) {
			chunk(w, `{"content":"all done"}`, "stop")
			done(w)
			return
		}
		var calls []string
		for i, c := range steps[n-1] {
			calls = append(calls, fmt.Sprintf(`{"index":%d,"id":"c%d_%d","type":"function","function":{"name":%q,"arguments":%q}}`, i, n, i, c[0], c[1]))
		}
		chunk(w, `{"tool_calls":[`+strings.Join(calls, ",")+`]}`, "tool_calls")
		done(w)
	}
}

func runWith(t *testing.T, a *Agent, s *session.Session, approve Approve) ([]proto.Event, error) {
	t.Helper()
	s.Agent = true
	var evs []proto.Event
	err := a.Run(context.Background(), s, "go", true, func(ev proto.Event) { evs = append(evs, ev) }, approve)
	return evs, err
}

func toolResults(s *session.Session) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestRefusingToBuildCancelsTheWholeRequest(t *testing.T) {
	var requests int32
	var ran int32
	a, s := newAgent(t, 6, func(w http.ResponseWriter, body string, n int) {
		atomic.AddInt32(&requests, 1)
		script([][2]string{{"propose_thing", "{}"}, {"other_tool", "{}"}})(w, body, n)
	})
	a.Extra = func() []tools.Tool {
		return []tools.Tool{&offerTool{fakeTool{"propose_thing", true, &ran}}, &plainTool{fakeTool{"other_tool", false, &ran}}}
	}
	evs, err := runWith(t, a, s, func(context.Context, proto.Event) string { return "deny" })
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("after a refusal the model must not be asked again: %d requests", got)
	}
	if atomic.LoadInt32(&ran) != 0 {
		t.Fatal("neither the refused tool nor the rest of the step may run")
	}
	res := toolResults(s)
	if len(res) != 2 || !strings.Contains(res[0], "REFUSED") || !strings.Contains(res[1], "Skipped") {
		t.Fatalf("every call has an answer (the history must stay valid): %q", res)
	}
	if !strings.Contains(notices(evs), "Cancelled") {
		t.Errorf("the user is told that the request was cancelled: %q", notices(evs))
	}
}

func TestBuildingStartsWithTheOfferWhenOffersAreOn(t *testing.T) {
	var ran, offered, built int32
	asked := []string{}
	a, s := newAgent(t, 6, script(
		[][2]string{{"make_thing", "{}"}},    // too early: no offer yet
		[][2]string{{"propose_thing", "{}"}}, // the offer
		[][2]string{{"make_thing", "{}"}},    // now it may
	))
	a.Suggest = func() bool { return true }
	a.Extra = func() []tools.Tool {
		return []tools.Tool{&offerTool{fakeTool{"propose_thing", true, &offered}}, &buildTool{fakeTool: fakeTool{"make_thing", true, &built}}}
	}
	_ = ran
	_, err := runWith(t, a, s, func(_ context.Context, ev proto.Event) string { asked = append(asked, ev.Tool); return "allow" })
	if err != nil {
		t.Fatal(err)
	}
	res := toolResults(s)
	if len(res) != 3 || !strings.Contains(res[0], "propose_extension") || res[1] != "ran propose_thing" || res[2] != "ran make_thing" {
		t.Fatalf("results: %q", res)
	}
	if strings.Join(asked, ",") != "propose_thing,make_thing" {
		t.Fatalf("the user is asked about the offer and then about the build, but not about the refused early call: %v", asked)
	}
}

func TestWithOffersOffBuildingNeedsOnlyItsOwnApproval(t *testing.T) {
	var built int32
	a, s := newAgent(t, 4, script([][2]string{{"make_thing", "{}"}}))
	a.Suggest = func() bool { return false }
	a.Extra = func() []tools.Tool { return []tools.Tool{&buildTool{fakeTool: fakeTool{"make_thing", true, &built}}} }
	if _, err := runWith(t, a, s, func(context.Context, proto.Event) string { return "allow" }); err != nil {
		t.Fatal(err)
	}
	if res := toolResults(s); len(res) != 1 || res[0] != "ran make_thing" {
		t.Fatalf("results: %q", res)
	}
}

func TestTheModelMustReadTheGuideAndTheSDKBeforeBuilding(t *testing.T) {
	var built int32
	a, s := newAgent(t, 10, script(
		[][2]string{{"make_thing", "{}"}},
		[][2]string{{"load_skill", `{"name":"relay-builder"}`}},
		[][2]string{{"make_thing", "{}"}},
		[][2]string{{"read_skill_file", `{"name":"relay-builder","path":"references/sdk.md"}`}},
		[][2]string{{"make_thing", "{}"}},
	))
	sk := skills.NewManager(t.TempDir(), filepath.Join(t.TempDir(), "skills.json"))
	sk.Reload()
	a.Skills = sk
	a.Suggest = func() bool { return false }
	a.Extra = func() []tools.Tool {
		return []tools.Tool{&buildTool{fakeTool: fakeTool{"make_thing", false, &built}, read: []string{"guide", "sdk"}}}
	}
	if _, err := runWith(t, a, s, func(context.Context, proto.Event) string { return "allow" }); err != nil {
		t.Fatal(err)
	}
	res := toolResults(s)
	if len(res) != 5 {
		t.Fatalf("results: %d", len(res))
	}
	if !strings.Contains(res[0], `load_skill with name "relay-builder"`) {
		t.Errorf("first the guide: %s", res[0])
	}
	if !strings.Contains(res[2], `read_skill_file`) || strings.Contains(res[2], "load_skill") {
		t.Errorf("then the SDK, and the guide is already read: %s", res[2])
	}
	if res[4] != "ran make_thing" || atomic.LoadInt32(&built) != 1 {
		t.Errorf("only now it builds: %q", res[4])
	}
}

func TestAlwaysAllowWorksForBuildingButNotForTheOffer(t *testing.T) {
	var built, offered int32
	prompts := map[string]int{}
	a, s := newAgent(t, 8, script(
		[][2]string{{"propose_thing", "{}"}},
		[][2]string{{"make_thing", "{}"}},
		[][2]string{{"make_thing", "{}"}},
		[][2]string{{"propose_thing", "{}"}},
	))
	a.Suggest = func() bool { return true }
	a.Extra = func() []tools.Tool {
		return []tools.Tool{&offerTool{fakeTool{"propose_thing", true, &offered}}, &buildTool{fakeTool: fakeTool{"make_thing", true, &built}}}
	}
	_, err := runWith(t, a, s, func(_ context.Context, ev proto.Event) string { prompts[ev.Tool]++; return "always" })
	if err != nil {
		t.Fatal(err)
	}
	if prompts["make_thing"] != 1 || atomic.LoadInt32(&built) != 2 {
		t.Errorf("\"always allow in this session\" must work for building: asked %d times, built %d", prompts["make_thing"], built)
	}
	if prompts["propose_thing"] != 2 {
		t.Errorf("the offer is asked every time: %d", prompts["propose_thing"])
	}
}

func TestOnceOnlyToolCannotBeBypassedBySessionAutoApproval(t *testing.T) {
	var ran int32
	a, s := newAgent(t, 3, script(
		[][2]string{{"once_capability", "{}"}},
	))
	s.AutoTools["once_capability"] = true
	a.Extra = func() []tools.Tool {
		return []tools.Tool{&onceTool{fakeTool{"once_capability", true, &ran}}}
	}
	asked := 0
	if _, err := runWith(t, a, s, func(context.Context, proto.Event) string { asked++; return "allow" }); err != nil {
		t.Fatal(err)
	}
	if asked != 1 || atomic.LoadInt32(&ran) != 1 {
		t.Fatalf("once-only approval: asked=%d ran=%d", asked, ran)
	}
}

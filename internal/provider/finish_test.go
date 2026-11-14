package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func collect(t *testing.T, c interface {
	Stream(context.Context, Request, func(Event) error) error
}) []Event {
	t.Helper()
	var evs []Event
	err := c.Stream(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}},
		func(ev Event) error { evs = append(evs, ev); return nil })
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func kinds(evs []Event) string {
	var out []string
	for _, e := range evs {
		switch e.Kind {
		case EvText:
			out = append(out, "text")
		case EvToolCall:
			out = append(out, "call")
		case EvFinish:
			out = append(out, "finish:"+e.Text)
		}
	}
	return strings.Join(out, ",")
}

func TestOpenAIReportsAnAnswerCutByTheLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"abc\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := NewOpenAI("p", srv.URL, NewKeys("p", nil, fixedKeys{}, false), nil)
	if got := kinds(collect(t, c)); got != "text,finish:length" {
		t.Fatalf("events: %s", got)
	}
}

func TestAnthropicReportsMaxTokensAndKeepsTheCutToolCall(t *testing.T) {
	sse := func(w http.ResponseWriter, event, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "message_start", `{"type":"message_start","message":{"usage":{"input_tokens":5}}}`)
		sse(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"writing"}}`)
		sse(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sse(w, "content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"write_file"}}`)
		sse(w, "content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\",\"content\":\"half"}}`)
		// the limit hits here: no content_block_stop for the tool call
		sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":16384}}`)
		sse(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer srv.Close()
	c := NewAnthropic("a", srv.URL, NewKeys("a", nil, fixedKeys{"k"}, true), nil)
	evs := collect(t, c)
	if got := kinds(evs); got != "text,call,finish:length" {
		t.Fatalf("a call that was cut must not be lost, and the limit must be reported: %s", got)
	}
	for _, e := range evs {
		if e.Kind == EvToolCall && (e.Call.Name != "write_file" || !strings.HasPrefix(e.Call.Args, `{"path"`)) {
			t.Fatalf("the cut call: %+v", e.Call)
		}
	}
}

func TestAnthropicNormalEndHasNoFinishEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
	}))
	defer srv.Close()
	c := NewAnthropic("a", srv.URL, NewKeys("a", nil, fixedKeys{"k"}, true), nil)
	if got := kinds(collect(t, c)); got != "text" {
		t.Fatalf("events: %s", got)
	}
}

// Package agent реализует цикл «модель → инструмент → результат → модель».
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"relay/internal/config"
	"relay/internal/proto"
	"relay/internal/provider"
	"relay/internal/router"
	"relay/internal/session"
	"relay/internal/tools"
)

const maxToolOutput = 20000

type Agent struct {
	Cfg    *config.Config
	Router *router.Router
	// Extra возвращает дополнительные инструменты (MCP).
	Extra func() []tools.Tool
}

// Approve запрашивает у пользователя разрешение; возвращает allow | always | deny.
type Approve func(ctx context.Context, ev proto.Event) string

const agentPrompt = `You are Relay's coding agent working inside the user's project.
Workspace: %s
Use the available tools to inspect and change files and to run commands. Read before you edit.
Make small, correct changes, verify them (build/tests) when possible, and finish with a short summary of what you did.`

func buildSystem(cfg *config.Config, sess *session.Session, agentMode bool) string {
	var parts []string
	if agentMode {
		parts = append(parts, fmt.Sprintf(agentPrompt, sess.Workspace))
	}
	if sess.Preset != "" {
		if p := cfg.PresetPrompt(sess.Preset); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

func providerErrKind(err error) string {
	var pe *provider.Error
	if errors.As(err, &pe) {
		switch {
		case pe.RateLimited:
			return "rate_limit"
		case pe.NoKey:
			return "no_key"
		}
		return "provider"
	}
	return "internal"
}

// Run выполняет один пользовательский запрос (в режиме агента это несколько шагов).
func (a *Agent) Run(ctx context.Context, sess *session.Session, text string, emit func(proto.Event), approve Approve) error {
	sess.Messages = append(sess.Messages, provider.Message{Role: "user", Content: text})
	if sess.Title == "" || sess.Title == "New session" {
		t := strings.Join(strings.Fields(text), " ")
		if len(t) > 48 {
			t = t[:48] + "…"
		}
		sess.Title = t
	}

	reg := tools.NewRegistry(sess.Workspace)
	if a.Extra != nil {
		for _, t := range a.Extra() {
			reg.Add(t)
		}
	}

	md := a.Cfg.Modes[sess.Mode]
	maxSteps := 1
	if sess.Agent {
		maxSteps = md.MaxSteps
		if maxSteps <= 0 {
			maxSteps = 12
		}
	}

	for step := 1; step <= maxSteps; step++ {
		msgs := make([]provider.Message, 0, len(sess.Messages)+1)
		if sys := buildSystem(a.Cfg, sess, sess.Agent); sys != "" {
			msgs = append(msgs, provider.Message{Role: "system", Content: sys})
		}
		msgs = append(msgs, sess.Messages...)

		req := provider.Request{Messages: msgs}
		if sess.Agent {
			req.Tools = reg.Specs()
		}
		chain, _, err := a.Router.Resolve(sess.Mode, sess.Model, sess.Agent, router.Hint{
			Chars: len(text), Agent: sess.Agent, Code: strings.Contains(text, "```"),
		})
		if err != nil {
			emit(proto.Event{Type: "error", Kind: "internal", Text: err.Error()})
			return err
		}

		var content strings.Builder
		var calls []provider.ToolCall
		used, err := a.Router.Stream(ctx, chain, req, func(ev provider.Event) error {
			switch ev.Kind {
			case provider.EvModel:
				emit(proto.Event{Type: "model", Model: ev.Text, Step: step})
			case provider.EvText:
				content.WriteString(ev.Text)
				emit(proto.Event{Type: "text", Text: ev.Text})
			case provider.EvToolCall:
				calls = append(calls, *ev.Call)
			case provider.EvNotice:
				emit(proto.Event{Type: "notice", Kind: ev.Tag, Text: ev.Text})
			case provider.EvUsage:
				emit(proto.Event{Type: "usage", Model: ev.Text, In: ev.Usage.In, Out: ev.Usage.Out, Cost: ev.Cost})
			}
			return ctx.Err()
		})
		_ = used
		if err != nil {
			if ctx.Err() == nil {
				emit(proto.Event{Type: "error", Kind: providerErrKind(err), Text: err.Error()})
			}
			return err
		}

		sess.Messages = append(sess.Messages, provider.Message{
			Role: "assistant", Content: content.String(), ToolCalls: calls,
		})
		if len(calls) == 0 {
			return nil
		}

		for _, call := range calls {
			sess.Messages = append(sess.Messages, a.runTool(ctx, sess, reg, call, step, emit, approve))
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	emit(proto.Event{Type: "notice", Kind: "info",
		Text: fmt.Sprintf("step limit reached (%d). Send another message to continue.", maxSteps)})
	return nil
}

func (a *Agent) runTool(ctx context.Context, sess *session.Session, reg *tools.Registry,
	call provider.ToolCall, step int, emit func(proto.Event), approve Approve) provider.Message {

	result := func(text string, isErr bool) provider.Message {
		text = truncateOutput(text)
		emit(proto.Event{Type: "tool_result", Tool: call.Name, CallID: call.ID, Result: text, IsError: isErr, Step: step})
		return provider.Message{Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: text}
	}

	tool, ok := reg.Get(call.Name)
	if !ok {
		emit(proto.Event{Type: "tool_call", Tool: call.Name, CallID: call.ID, Args: call.Args, Step: step})
		return result("unknown tool: "+call.Name, true)
	}
	args := []byte(call.Args)
	desc := tool.Describe(args)
	emit(proto.Event{Type: "tool_call", Tool: call.Name, CallID: call.ID, Args: call.Args, Desc: desc, Step: step})

	if tool.NeedsApproval() && !sess.AutoTools[call.Name] {
		decision := approve(ctx, proto.Event{Type: "approval", Tool: call.Name, CallID: call.ID, Args: call.Args, Desc: desc, Step: step})
		switch decision {
		case "always":
			sess.AutoTools[call.Name] = true
		case "allow":
		default:
			return result("The user denied this action. Do not retry it; ask what to do instead.", true)
		}
	}

	tctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	out, err := tool.Run(tctx, args)
	if err != nil {
		if strings.TrimSpace(out) != "" {
			out += "\n"
		}
		return result(out+"error: "+err.Error(), true)
	}
	return result(out, false)
}

func truncateOutput(s string) string {
	if len(s) <= maxToolOutput {
		return s
	}
	return s[:maxToolOutput] + fmt.Sprintf("\n… [truncated, %d more bytes]", len(s)-maxToolOutput)
}

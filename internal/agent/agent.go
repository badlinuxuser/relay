// Package agent реализует цикл «модель → инструмент → результат → модель».
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"relay/internal/config"
	"relay/internal/mcp"
	"relay/internal/proto"
	"relay/internal/provider"
	"relay/internal/router"
	"relay/internal/session"
	"relay/internal/skills"
	"relay/internal/tools"
)

const maxToolOutput = 20000

// suggestPrompt makes the agent offer to build what it lacks instead of saying that it cannot.
const suggestPrompt = `## You can build what you lack
You are not limited to the tools you have now: you can build new tools, connectors to outside services, and skills (see propose_extension and the skill relay-builder).
NEVER answer "I cannot do that", "I have no tool for that" or "I do not have access" before you have thought about whether something you could build would do the job. If the task needs an ability that none of your tools or skills gives you, call propose_extension (say in one sentence what is missing). This holds for direct requests ("make a tool that ...") and for indirect ones, where the user just asks for the result:
- send, read or reply in a messenger or by mail (Telegram, Slack, Discord, e-mail);
- anything in the user's accounts or services (calendar, tasks, notes, GitHub, a CRM, a shop, a bank);
- a database, an internal API, a smart-home device, a program that must be controlled;
- something the user wants to repeat or do regularly in a fixed way (a skill).
Building ALWAYS starts with propose_extension: the user must agree first, and you cannot create anything before that. If the user refuses, the whole request is cancelled: do not look for another way.
If a tool that was built for you (an MCP tool, its name starts with mcp__) fails because of a defect in its own code or setup (an error in the code, a missing module, the wrong approach, a wrong assumption about this computer), do NOT give up and do not say that you cannot do it: call propose_extension with kind "fix" and say what is broken. If the user agrees, read its code (read_server), repair it, and use it again.
Before you build, read how Relay works: load the skill relay-builder and its references/sdk.md; the build tools refuse to work until you have.
Do not offer for what your tools already cover (files, the shell, the web).`

type Agent struct {
	Cfg    *config.Config
	Router *router.Router
	// Extra возвращает дополнительные инструменты (MCP).
	Extra func() []tools.Tool
	// Skills (may be nil) supplies the installed skills in agent mode: their list goes into the
	// system prompt and the agent loads one when it needs it.
	Skills *skills.Manager
	// Auth (may be nil) signs the user in when a tool needs it: the agent pauses, the interface
	// opens the sign-in page, and the tool is called again when the user is done.
	Auth Authorizer
	// Suggest (may be nil) says whether the agent may offer to build a tool or a skill when it lacks one.
	Suggest func() bool
}

// Authorizer starts the sign-in to a connector (the MCP manager).
type Authorizer interface {
	// Authorize returns the address to open in the browser and a function that waits until
	// the user has finished or the context ends.
	Authorize(ctx context.Context, server string) (string, func(context.Context) error, error)
}

// registry makes the tools of one step. It is made again for every step, so that a server or a
// skill that the model has just created is available at once.
func (a *Agent) registry(sess *session.Session) *tools.Registry {
	reg := tools.NewRegistry(sess.Workspace)
	if a.Extra != nil {
		for _, t := range a.Extra() {
			reg.Add(t)
		}
	}
	if a.Skills != nil && sess.Agent {
		for _, t := range tools.SkillTools(a.Skills) {
			reg.Add(t)
		}
	}
	return reg
}

// Approve запрашивает у пользователя разрешение; возвращает allow | always | deny.
type Approve func(ctx context.Context, ev proto.Event) string

const agentPrompt = `You are Relay's coding agent working inside the user's project.
Workspace: %s
Use the available tools to inspect and change files and to run commands. Read before you edit.
Make small, correct changes, verify them (build/tests) when possible, and finish with a short summary of what you did.`

func buildSystem(cfg *config.Config, sess *session.Session, agentMode bool, skillsPrompt string, suggest bool) string {
	var parts []string
	if agentMode {
		parts = append(parts, fmt.Sprintf(agentPrompt, sess.Workspace))
		parts = append(parts, environmentPrompt())
		if suggest {
			parts = append(parts, suggestPrompt) // before the skills: it must not be overlooked
		}
		if skillsPrompt != "" {
			parts = append(parts, skillsPrompt)
		}
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
func (a *Agent) Run(ctx context.Context, sess *session.Session, text string, showThoughts bool, emit func(proto.Event), approve Approve) error {
	sess.Messages = append(sess.Messages, provider.Message{Role: "user", Content: text})
	sess.BuildOK, sess.Cancelled, sess.Read = false, false, map[string]bool{}
	if sess.Title == "" || sess.Title == "New session" {
		t := strings.Join(strings.Fields(text), " ")
		if len(t) > 48 {
			t = t[:48] + "…"
		}
		sess.Title = t
	}

	md := a.Cfg.Modes[sess.Mode]
	maxSteps := 1
	if sess.Agent {
		maxSteps = md.MaxSteps
		if maxSteps <= 0 {
			maxSteps = 12
		}
	}

	pinned := "" // the model the user agreed to switch to during this request

	// The step budget is soft: past maxSteps a long task simply goes on (the agent stops by itself
	// when it repeats one call again and again). At the hard limit the model gets one more turn
	// without tools to say what is done and what is left, so the answer never ends in the middle.
	soft, hard := maxSteps, maxSteps
	if sess.Agent {
		hard = min(max(maxSteps*4, maxSteps), 120)
	}
	continues := 0 // how many times the output limit cut an answer in this request
	notedTrim := false
	lastSig, sameSig := "", 0

	for step := 1; step <= hard+1; step++ {
		final := step > hard
		if sess.Agent && step == soft+1 && !final {
			emit(proto.Event{Type: "notice", Kind: "info",
				Text: fmt.Sprintf("This is a long task: going on past %d steps (esc stops it).", soft)})
		}
		reg := a.registry(sess)
		msgs := make([]provider.Message, 0, len(sess.Messages)+2)
		skillsPrompt := ""
		if a.Skills != nil {
			skillsPrompt = a.Skills.Prompt() // read again each step: a skill added meanwhile shows up
		}
		suggest := a.Suggest != nil && a.Suggest()
		if sys := buildSystem(a.Cfg, sess, sess.Agent, skillsPrompt, suggest); sys != "" {
			msgs = append(msgs, provider.Message{Role: "system", Content: sys})
		}
		msgs = append(msgs, sess.Messages...)
		if final {
			msgs = append(msgs, provider.Message{Role: "user", Content: provider.InternalPrefix +
				"The budget of steps for this request is used up. Do not call any tool. Say briefly what you have done and what is still left, so that the user can ask you to go on."})
		}

		req := provider.Request{Messages: msgs}
		if sess.Agent && !final {
			req.Tools = reg.Specs()
		}
		model := sess.Model
		if pinned != "" {
			model = pinned // the next steps start where the last one succeeded, not on the model that failed
		}
		chain, _, err := a.Router.Resolve(sess.Mode, model, sess.Agent, router.Hint{
			Chars: len(text), Agent: sess.Agent, Code: strings.Contains(text, "```"),
		})
		if err != nil {
			emit(proto.Event{Type: "error", Kind: "internal", Text: err.Error()})
			return err
		}
		var trimmed bool
		req.Messages, trimmed = fitContext(req.Messages, a.contextBudget(chain[0]))
		if trimmed && !notedTrim {
			notedTrim = true
			emit(proto.Event{Type: "notice", Kind: "info",
				Text: "The conversation is long: older tool results were shortened (and the oldest messages left out if needed) to fit the model's context."})
		}

		var content strings.Builder
		var calls []provider.ToolCall
		finish := ""
		ask := func(failed, next, why string) string {
			if sess.AutoTools["switch_model"] {
				return "allow"
			}
			d := approve(ctx, proto.Event{Type: "approval", Tool: "switch_model", CallID: "switch-" + failed, Step: step,
				Desc: fmt.Sprintf("%s failed (%s). Try %s, another model of the same provider?", failed, why, next)})
			if d == "always" {
				sess.AutoTools["switch_model"] = true
			}
			return d
		}
		used, err := a.Router.StreamAsk(ctx, chain, req, func(ev provider.Event) error {
			switch ev.Kind {
			case provider.EvModel:
				emit(proto.Event{Type: "model", Model: ev.Text, Step: step})
			case provider.EvText:
				content.WriteString(ev.Text)
				emit(proto.Event{Type: "text", Text: ev.Text})
			case provider.EvThinking:
				if showThoughts {
					emit(proto.Event{Type: "thinking", Text: ev.Text})
				}
			case provider.EvToolCall:
				calls = append(calls, *ev.Call)
			case provider.EvFinish:
				finish = ev.Text
			case provider.EvNotice:
				emit(proto.Event{Type: "notice", Kind: ev.Tag, Text: ev.Text})
			case provider.EvUsage:
				sess.TokensIn += ev.Usage.In
				sess.LastIn = ev.Usage.In
				sess.TokensOut += ev.Usage.Out
				sess.Cost += ev.Cost
				emit(proto.Event{Type: "usage", Model: ev.Text, In: ev.Usage.In, Out: ev.Usage.Out, Cost: ev.Cost})
			}
			return ctx.Err()
		}, ask)
		if err == nil && used != "" && used != chain[0] {
			// the switch happened: stay on the new model for the rest of this request, and
			// (when the model was chosen explicitly) for the session
			pinned = used
			if sess.Model != "" && sess.Model != "auto" {
				sess.Model = used
				emit(proto.Event{Type: "notice", Kind: "info", Text: "Model of this session is now " + used})
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				emit(proto.Event{Type: "error", Kind: providerErrKind(err), Text: err.Error()})
			}
			return err
		}

		// The output limit cut the answer. A tool call that was cut has invalid arguments and
		// must not run; it is dropped and the model is asked to write it again, smaller.
		cut := finish == "length"
		var dropped []string
		if cut {
			valid := make([]provider.ToolCall, 0, len(calls))
			for _, c := range calls {
				if json.Valid([]byte(c.Args)) {
					valid = append(valid, c)
				} else {
					dropped = append(dropped, c.Name)
				}
			}
			calls = valid
		}
		if final {
			calls = nil // no tools were offered: whatever came back is ignored
		}

		if content.Len() > 0 || len(calls) > 0 { // an empty assistant message would break some providers
			sess.Messages = append(sess.Messages, provider.Message{
				Role: "assistant", Content: content.String(), ToolCalls: calls,
			})
		}
		if final {
			emit(proto.Event{Type: "notice", Kind: "info",
				Text: fmt.Sprintf("Safety limit of %d steps reached. Say \"continue\" to go on.", hard)})
			return nil
		}
		if cut && len(calls) == 0 {
			continues++
			if continues > maxContinues {
				emit(proto.Event{Type: "notice", Kind: "info", Text: "The answer is very long. Say \"continue\" to get the rest."})
				return nil
			}
			note := "Continue exactly where you stopped. Do not repeat what you already wrote."
			if len(dropped) > 0 {
				note = cutCallNote(dropped)
			}
			sess.Messages = append(sess.Messages, provider.Message{Role: "user", Content: provider.InternalPrefix + note})
			emit(proto.Event{Type: "notice", Kind: "info", Text: "The answer reached the output limit of the model. Going on…"})
			step-- // a continuation is not a new step
			continue
		}
		if len(calls) == 0 {
			return nil
		}

		for _, call := range calls {
			if sess.Cancelled { // an earlier call of this step was refused: the rest does not run, but has an answer
				sess.Messages = append(sess.Messages, provider.Message{Role: "tool", ToolCallID: call.ID, Name: call.Name,
					Content: "Skipped: the user cancelled the request."})
				continue
			}
			sess.Messages = append(sess.Messages, a.runTool(ctx, sess, reg, call, step, emit, approve))
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		if sess.Cancelled {
			emit(proto.Event{Type: "notice", Kind: "info", Text: "Cancelled: you did not allow it, so the request was stopped."})
			return nil
		}
		if len(dropped) > 0 {
			sess.Messages = append(sess.Messages, provider.Message{Role: "user", Content: provider.InternalPrefix + cutCallNote(dropped)})
		}

		if sig := callsSignature(calls); sig == lastSig {
			if sameSig++; sameSig >= 4 {
				emit(proto.Event{Type: "notice", Kind: "info",
					Text: "The agent kept making the same call again and again, so it was stopped. Tell it what to do differently."})
				return nil
			}
		} else {
			lastSig, sameSig = sig, 1
		}
	}
	return nil
}

// maxContinues is how many times in one request the agent asks the model to go on after the
// output limit cut its answer.
const maxContinues = 6

func cutCallNote(names []string) string {
	return fmt.Sprintf("Your last reply reached the output limit in the middle of the tool call %s, so that call was NOT run. "+
		"Write it again, but smaller: put less in one call (split big files into several steps).", strings.Join(names, ", "))
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

	if no, ok := tool.(tools.NeedsOffer); ok && no.NeedsOffer() && !sess.BuildOK && a.Suggest != nil && a.Suggest() {
		// building always starts with a question to the user
		return result("Not allowed yet: before you build anything you must ask the user. Call propose_extension first (say what it is for and which kind it is) and wait for the answer. Then build.", true)
	}
	if nr, ok := tool.(tools.NeedsRead); ok {
		if need := missingReading(sess, nr.NeedsRead([]byte(call.Args))); len(need) > 0 {
			return result("Not yet: you have not read how Relay works in this request, and what you write would be guesswork. First: "+
				strings.Join(need, " ")+" Then build.", true)
		}
	}
	if tool.NeedsApproval() && !sess.AutoTools[call.Name] {
		decision := approve(ctx, proto.Event{Type: "approval", Tool: call.Name, CallID: call.ID, Args: call.Args, Desc: desc, Step: step})
		switch decision {
		case "always":
			if oo, ok := tool.(tools.OnceOnly); !ok || !oo.OnceOnly() {
				sess.AutoTools[call.Name] = true
			}
		case "allow":
		default:
			if c, ok := tool.(tools.CancelOnDeny); ok && c.CancelsRequest() {
				sess.Cancelled = true // the whole request ends, see Run
			}
			if d, ok := tool.(tools.Denier); ok {
				return result(d.DeniedMessage(), true)
			}
			return result("The user denied this action. Do not retry it; ask what to do instead.", true)
		}
	}

	tctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	tctx = tools.WithProgress(tctx, func(label string, stage, stages int) {
		emit(proto.Event{Type: "progress", Tool: call.Name, Text: label, Stage: stage, Stages: stages})
	})
	out, err := tool.Run(tctx, args)
	var need *mcp.AuthRequiredError
	if errors.As(err, &need) && a.Auth != nil {
		// the user has not signed in: pause, open the sign-in page, wait, and call again
		if serr := a.signIn(tctx, need.Server, emit); serr != nil {
			return result("error: "+serr.Error(), true)
		}
		out, err = tool.Run(tctx, args)
	}
	if err != nil {
		if strings.TrimSpace(out) != "" {
			out += "\n"
		}
		return result(out+"error: "+err.Error(), true)
	}
	if bo, ok := tool.(tools.BuildOffer); ok && bo.IsBuildOffer() {
		sess.BuildOK = true // the user agreed: building is allowed in this request
	}
	noteReading(sess, call)
	return result(out, false)
}

// missingReading says what the model still has to read.
func missingReading(sess *session.Session, need []string) []string {
	var out []string
	for _, n := range need {
		if sess.Read[n] {
			continue
		}
		switch n {
		case "guide":
			out = append(out, `Call load_skill with name "relay-builder".`)
		case "sdk":
			out = append(out, `Call read_skill_file with name "relay-builder" and path "references/sdk.md".`)
		}
	}
	return out
}

// noteReading remembers that the model has read the guide of the builder or its SDK reference.
func noteReading(sess *session.Session, call provider.ToolCall) {
	var v struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(call.Args), &v) != nil || v.Name != "relay-builder" {
		return
	}
	switch call.Name {
	case "load_skill":
		sess.Read["guide"] = true
	case "read_skill_file":
		if strings.HasSuffix(strings.ReplaceAll(v.Path, `\`, "/"), "references/sdk.md") {
			sess.Read["sdk"] = true
		}
	}
}

// signIn pauses the work until the user has signed in to a connector. The event "auth" tells
// the interface to open the page; the wait ends when the user is done, cancels (esc) or the
// time is up.
func (a *Agent) signIn(ctx context.Context, server string, emit func(proto.Event)) error {
	url, wait, err := a.Auth.Authorize(ctx, server)
	if err != nil {
		return fmt.Errorf("cannot start the sign-in to %q: %v", server, err)
	}
	emit(proto.Event{Type: "auth", Tool: server, Text: url,
		Desc: fmt.Sprintf("Sign in to %s in your browser. Waiting (esc cancels)…", server)})
	if err := wait(ctx); err != nil {
		return fmt.Errorf("the sign-in to %q did not finish: %v", server, err)
	}
	emit(proto.Event{Type: "notice", Kind: "info", Text: "Signed in to " + server + ". Continuing."})
	return nil
}

func truncateOutput(s string) string {
	if len(s) <= maxToolOutput {
		return s
	}
	return s[:maxToolOutput] + fmt.Sprintf("\n… [truncated, %d more bytes]", len(s)-maxToolOutput)
}

// environmentPrompt tells the model where it runs. Models tend to believe that they are in a remote
// sandbox and refuse to touch "the user's computer"; Relay runs on that very computer.
func environmentPrompt() string {
	desk := "no graphical session was found (a remote terminal?)"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		desk = "a graphical desktop is available"
	}
	return fmt.Sprintf("## Where you run\nRelay is a program on the user's own computer (%s; %s). You are NOT in a sandbox or in a remote service: "+
		"the commands you run and the tools you build run on THIS computer, with the user's rights, and can open the user's browser and other "+
		"applications, show notifications and use their files. Never tell the user that you cannot reach their computer; if something is missing, build it.", runtime.GOOS, desk)
}

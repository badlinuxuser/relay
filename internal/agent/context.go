package agent

import (
	"strings"

	"relay/internal/provider"
)

// defaultContext is the size of the context window (in tokens) assumed for a model whose
// "context" is not set in config.toml. A model with a smaller window needs it set.
const defaultContext = 128000

// keepRecent is the number of newest messages whose tool results are never shortened.
const keepRecent = 8

func sizeOf(m provider.Message) int {
	n := 4 + len(m.Content)/4
	for _, c := range m.ToolCalls {
		n += 4 + (len(c.Name)+len(c.Args))/4
	}
	return n
}

// estimateTokens is a rough count (a token is about 4 bytes of text).
func estimateTokens(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += sizeOf(m)
	}
	return n
}

// fitContext makes a long conversation fit the budget (tokens) without touching the stored
// history: old tool results are shortened first; if that is not enough, the oldest turns are left
// out (always from a user message, so a tool call never loses its result). It reports whether
// anything was cut.
func fitContext(msgs []provider.Message, budget int) ([]provider.Message, bool) {
	if budget <= 0 || estimateTokens(msgs) <= budget {
		return msgs, false
	}
	out := make([]provider.Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < len(out)-keepRecent; i++ {
		if out[i].Role == "tool" && len(out[i].Content) > 1500 {
			out[i].Content = out[i].Content[:1200] + "\n[… old output shortened to fit the context]"
		}
	}
	if estimateTokens(out) <= budget {
		return out, true
	}

	sys := 0
	for sys < len(out) && out[sys].Role == "system" {
		sys++
	}
	head := estimateTokens(out[:sys])
	suffix := make([]int, len(out)+1)
	for i := len(out) - 1; i >= sys; i-- {
		suffix[i] = suffix[i+1] + sizeOf(out[i])
	}
	cut := -1
	for p := sys; p < len(out); p++ {
		if out[p].Role == "user" && head+suffix[p]+40 <= budget {
			cut = p
			break
		}
	}
	if cut < 0 { // even the newest turn is too big: keep it alone
		for p := len(out) - 1; p >= sys; p-- {
			if out[p].Role == "user" {
				cut = p
				break
			}
		}
		if cut < 0 {
			return out, true
		}
	}
	kept := append([]provider.Message{}, out[:sys]...)
	if cut > sys {
		kept = append(kept, provider.Message{Role: "user", Content: provider.InternalPrefix +
			"(The earlier part of this conversation was left out to fit the model's context window.)"})
	}
	return append(kept, out[cut:]...), true
}

// contextBudget is how many tokens of conversation a model gets: 70% of its window, the rest is
// for the tool list and the answer.
func (a *Agent) contextBudget(id string) int {
	tokens := defaultContext
	if m, ok := a.Cfg.ModelByID(id); ok && m.Context > 0 {
		tokens = m.Context
	}
	return tokens * 70 / 100
}

// callsSignature identifies a set of tool calls, to notice an agent that repeats itself.
func callsSignature(calls []provider.ToolCall) string {
	parts := make([]string, len(calls))
	for i, c := range calls {
		parts[i] = c.Name + "\x00" + c.Args
	}
	return strings.Join(parts, "\x01")
}

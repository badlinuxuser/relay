package tools

import "context"

type progressKey struct{}

// WithProgress gives a tool a way to report what it is doing: the agent passes the function in the
// context, and the interface shows it as a progress bar. stage counts from 1 to stages.
func WithProgress(ctx context.Context, f func(label string, stage, stages int)) context.Context {
	return context.WithValue(ctx, progressKey{}, f)
}

// Progress reports a stage of a long job (a no-op when nobody listens).
func Progress(ctx context.Context, label string, stage, stages int) {
	if f, ok := ctx.Value(progressKey{}).(func(string, int, int)); ok {
		f(label, stage, stages)
	}
}

// Denier is implemented by a tool that has its own answer for the model when the user says no
// (the default asks the model to find out what the user wants instead).
type Denier interface{ DeniedMessage() string }

// BuildOffer marks the tool with which the agent offers to build a missing tool or skill. Once the
// user agreed to it, building is allowed in the same request (see NeedsOffer).
type BuildOffer interface{ IsBuildOffer() bool }

// NeedsOffer marks a tool that builds something (a server, a skill): it may be used only after the
// user agreed to the offer of a BuildOffer tool in the same request. Without it the tool is refused
// and the model is told to ask first.
type NeedsOffer interface{ NeedsOffer() bool }

// CancelOnDeny marks a tool whose refusal by the user cancels the whole request, not only the call:
// the agent stops, and does not look for another way.
type CancelOnDeny interface{ CancelsRequest() bool }

// MCPToolName is the name under which the agent sees the tool of an MCP server.
func MCPToolName(server, tool string) string {
	return mcpToolName(server, tool, map[string]bool{})
}

// NeedsRead is implemented by a tool that may be used only after the model has read something in the
// same request: "guide" (the skill relay-builder) and "sdk" (its references/sdk.md). It gets the
// arguments of the call, because a remote server needs no SDK.
type NeedsRead interface{ NeedsRead(args []byte) []string }

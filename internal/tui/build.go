package tui

import (
	"fmt"
	"strings"
	"time"

	"relay/internal/proto"
)

// While the agent builds a tool or a skill the model thinks for a long time and writes a lot of
// code, and makes many calls that are of no interest to the user. By default one line replaces all
// that, right under the question: a moving label of what is going on, the time, and the stage. The
// reasoning, the calls of the model and what it says on the way are not shown (the setting "While
// the agent builds": progress / thoughts); only the final answer is.

type buildState struct {
	active  bool
	phase   int     // 1..buildPhases
	frac    float64 // how far inside the phase, 0..1
	label   string  // what a tool reports now; "" means the model works: a phrase of the phase is shown
	startAt time.Time
}

const buildPhases = 5

// buildIdle are the lines shown, one after the other, while the model works in a phase.
var buildIdle = map[int][]string{
	1: {"Reading the builder guide", "Looking at the examples"},
	2: {"Designing the tools", "Choosing the arguments", "Thinking about what can go wrong", "Writing the code"},
	3: {"Building the server", "Checking the code", "Starting the server"},
	4: {"Writing the skill", "Describing when to use the tools"},
	5: {"Trying the new tools", "Checking the result"},
}

// buildProgressOn: the bar is shown instead of the reasoning.
func (m model) buildProgressOn() bool {
	return m.build.active && m.appSettings().BuilderView != "thoughts"
}

func (m *model) startBuild(phase int, label string) {
	if !m.build.active {
		m.build = buildState{active: true, startAt: time.Now()}
		m.dropInterim()
	}
	m.build.advance(phase, label)
}

// dropInterim takes away what the model said before the build began ("let me first load the guide"):
// it was not an answer.
func (m *model) dropInterim() {
	m.pend, m.hold = nil, ""
	for n := len(m.entries); n > 0; n-- {
		if k := m.entries[n-1].kind; k != eAssistant && k != eThought {
			break
		}
		m.entries = m.entries[:n-1]
	}
	if m.openAsst >= len(m.entries) {
		m.openAsst = -1
	}
	if m.openThink >= len(m.entries) {
		m.openThink = -1
	}
}

// builderTools are the tools with which the agent builds; their calls are not shown while the
// progress line is used.
var builderTools = map[string]bool{
	"create_mcp_server": true, "create_skill": true, "list_extensions": true,
	"authorize_connector": true, "remove_extension": true, "propose_extension": true, "read_server": true,
}

func isBuilderEntry(e entry) bool {
	if builderTools[e.tool] {
		return true
	}
	return (e.tool == "load_skill" || e.tool == "read_skill_file") && strings.Contains(e.text, "relay-builder")
}

// hideEntry: a call of a tool that belongs to a build is not drawn (unless the user chose to see
// the thoughts and the work of the model).
func (m model) hideEntry(e entry) bool {
	if e.kind != eTool || m.appSettings().BuilderView == "thoughts" {
		return false
	}
	return e.hidden || isBuilderEntry(e)
}

// advance moves to a later phase (never back), and sets what is shown.
func (b *buildState) advance(phase int, label string) {
	if phase > b.phase {
		b.phase, b.frac = phase, 0
	}
	b.label = label
}

// trackBuild follows the events of a request and moves the bar.
func (m *model) trackBuild(ev proto.Event) {
	b := &m.build
	switch ev.Type {
	case "tool_call":
		switch {
		case ev.Tool == "propose_extension":
			m.startBuild(1, "Asking whether to build it")
		case ev.Tool == "load_skill" && strings.Contains(ev.Args, "relay-builder"):
			m.startBuild(1, "Reading the builder guide")
		case ev.Tool == "create_mcp_server":
			m.startBuild(3, "Building the server")
		case ev.Tool == "create_skill":
			m.startBuild(4, "Writing the skill")
		case !b.active:
		case ev.Tool == "read_skill_file":
			b.advance(1, "Reading the examples")
		case ev.Tool == "list_extensions":
			b.advance(1, "Looking at what is installed")
		case ev.Tool == "read_server":
			b.advance(1, "Reading the current code")
		case ev.Tool == "authorize_connector":
			b.label = "Signing in"
		case strings.HasPrefix(ev.Tool, "mcp__") && b.phase >= 3:
			b.advance(5, "Trying the new tools")
		}
	case "progress":
		if ev.Tool == "create_mcp_server" || ev.Tool == "create_skill" {
			m.startBuild(3, ev.Text)
		}
		switch {
		case ev.Tool == "create_mcp_server" && ev.Text == "Writing the skill":
			b.advance(4, ev.Text)
			b.frac = 0.5
		case ev.Tool == "create_mcp_server":
			b.advance(3, ev.Text)
			b.frac = float64(ev.Stage-1) / float64(max(1, ev.Stages-1))
		case ev.Tool == "create_skill":
			b.advance(4, ev.Text)
			b.frac = float64(ev.Stage) / float64(max(1, ev.Stages))
		}
	case "tool_result":
		if !b.active {
			return
		}
		switch {
		case ev.Tool == "create_mcp_server" && ev.IsError:
			b.phase, b.frac, b.label = 2, 0, "Fixing the problem"
		case ev.Tool == "create_mcp_server":
			b.advance(5, "Trying the new tools")
		case ev.Tool == "create_skill":
			b.advance(5, "Finishing up")
		case ev.Tool == "load_skill" || ev.Tool == "read_skill_file" || ev.Tool == "list_extensions" || ev.Tool == "propose_extension" || ev.Tool == "read_server":
			b.advance(2, "") // the model designs now
		case strings.HasPrefix(ev.Tool, "mcp__"):
			b.label = "Checking the result"
		default:
			b.label = ""
		}
	case "auth":
		b.label = "Waiting for you to sign in"
	case "approval":
		if b.active && ev.Tool != "switch_model" {
			b.label = "Waiting for your approval"
		}
	}
}

// shimmer draws the label with a bright spot that runs along it.
func shimmer(label string, frame int, st styles) string {
	r := []rune(label)
	pos := (frame / 2) % (len(r) + 8) // a short pause before the spot starts again
	var b strings.Builder
	for i, c := range r {
		if d := pos - i; d >= 0 && d < 3 {
			b.WriteString(st.accent.Render(string(c)))
		} else {
			b.WriteString(st.text.Render(string(c)))
		}
	}
	return b.String()
}

func clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 60 {
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

// buildPanel is the progress line: a moving label, the time, the stage. It is the last part of the
// feed, so it stands right under the question of the user.
func (m model) buildPanel(w int, st styles) []string {
	if !m.buildProgressOn() || !m.streaming || m.approval != nil {
		return nil
	}
	b := m.build
	phase := min(max(b.phase, 1), buildPhases)
	label := b.label
	if label == "" {
		phrases := buildIdle[phase]
		label = phrases[(m.frame/25)%len(phrases)] + "…" // a new line about every three seconds
	}
	// Keep the animated activity label and elapsed time; the phase name/count is
	// implementation detail and adds noise (especially while writing a skill).
	line := "  " + st.accent.Render(spin[m.frame%len(spin)]) + " " + shimmer(label, m.frame, st) +
		st.dim.Render(fmt.Sprintf("  %s", clock(time.Since(b.startAt))))
	return []string{"", line}
}

// withBuildLine puts the progress line after the last entry.
func (m model) withBuildLine(parts [][]string, w int, st styles) [][]string {
	if p := m.buildPanel(w, st); len(p) > 0 {
		return append(parts, p)
	}
	return parts
}
